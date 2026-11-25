package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Long polling of dividend sync jobs: the same contract as the price jobs (see
// jobwait_test.go), through the shared awaitChange loop.

type dividendPoll struct {
	code int
	job  dividendJob
	body string
}

func pollDividend(ctx context.Context, handler http.Handler, cookies []*http.Cookie, key, query string) dividendPoll {
	r := httptest.NewRequest("GET", "/api/dividend-sync-jobs/"+key+query, nil).WithContext(ctx)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	res := dividendPoll{code: w.Code, body: w.Body.String()}
	if w.Code == 200 {
		json.Unmarshal(w.Body.Bytes(), &res.job)
	}
	return res
}

func pollDividendAsync(ctx context.Context, handler http.Handler, cookies []*http.Cookie, key, query string) <-chan dividendPoll {
	out := make(chan dividendPoll, 1)
	go func() { out <- pollDividend(ctx, handler, cookies, key, query) }()
	return out
}

func divAnswerWithin(t *testing.T, ch <-chan dividendPoll, d time.Duration, what string) dividendPoll {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(d):
		t.Fatalf("%s: no answer within %v", what, d)
		return dividendPoll{}
	}
}

func divStillWaiting(t *testing.T, ch <-chan dividendPoll, d time.Duration) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("answered although nothing changed: %d %+v", r.code, r.job)
	case <-time.After(d):
	}
}

// putDividendJob registers a RUNNING job of user with n PENDING items.
func putDividendJob(h *Handler, user, key string, n int) *dividendJob {
	job := &dividendJob{Version: 1, Status: "RUNNING", Total: n, user: user, created: h.dividends.now(), Items: make([]dividendJobItem, n)}
	for i := range job.Items {
		job.Items[i] = dividendJobItem{InstrumentID: int64(i + 1), Status: "PENDING"}
	}
	h.dividends.jobs.mu.Lock()
	h.dividends.jobs.entries[key] = job
	h.dividends.jobs.mu.Unlock()
	return job
}

// dividendChange is what a runner does for one finished item.
func dividendChange(h *Handler, job *dividendJob, i int, status string) {
	d := h.dividends
	d.jobs.mu.Lock()
	job.Items[i].Status = status
	job.Done++
	d.jobs.touch(job)
	d.jobs.mu.Unlock()
}

func dividendFinish(h *Handler, job *dividendJob) {
	d := h.dividends
	d.jobs.mu.Lock()
	job.Status = "DONE"
	d.jobs.touch(job)
	d.jobs.mu.Unlock()
}

// awaitDividendParked returns once a request is parked on the job: a waiter creates
// the job's wake-up channel under the jobs lock just before it blocks.
func awaitDividendParked(t *testing.T, h *Handler, job *dividendJob) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.dividends.jobs.mu.Lock()
		parked := job.changed.ch != nil
		h.dividends.jobs.mu.Unlock()
		if parked {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no request ever parked on the job")
}

func TestDividendJobAnswersWithoutSinceImmediately(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	putDividendJob(h, pollUser, "j1", 2)
	start := time.Now()
	r := pollDividend(context.Background(), handler, cookies, "j1", "")
	if r.code != 200 || r.job.Version != 1 || r.job.Status != "RUNNING" || len(r.job.Items) != 2 || r.job.Total != 2 || time.Since(start) > time.Second {
		t.Fatalf("%+v after %v", r, time.Since(start))
	}
	// The version is an integer in the JSON, next to the unchanged fields.
	var raw map[string]any
	if err := json.Unmarshal([]byte(r.body), &raw); err != nil {
		t.Fatal(err)
	}
	if v, ok := raw["version"].(float64); !ok || v != 1 {
		t.Fatalf("version in the answer: %s", r.body)
	}
	for _, field := range []string{"status", "done", "total", "items"} {
		if _, ok := raw[field]; !ok {
			t.Fatalf("%q missing from the answer: %s", field, r.body)
		}
	}
}

func TestDividendJobSinceBlocksUntilAnItemChanges(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	job := putDividendJob(h, pollUser, "j1", 2)

	// The job is already newer than since=0: no waiting.
	if r := divAnswerWithin(t, pollDividendAsync(context.Background(), handler, cookies, "j1", "?since=0"), time.Second, "since=0"); r.job.Version != 1 {
		t.Fatalf("%+v", r)
	}

	waiting := pollDividendAsync(context.Background(), handler, cookies, "j1", "?since=1")
	divStillWaiting(t, waiting, 100*time.Millisecond)
	awaitDividendParked(t, h, job)
	// No lock is held while a request waits: runners and other requests proceed.
	if !h.dividends.jobs.mu.TryLock() {
		t.Fatal("the jobs lock is held while a request waits")
	}
	h.dividends.jobs.mu.Unlock()

	start := time.Now()
	dividendChange(h, job, 1, "OK")
	r := divAnswerWithin(t, waiting, 2*time.Second, "waiter after a change")
	if d := time.Since(start); r.code != 200 || r.job.Version != 2 || r.job.Done != 1 || r.job.Items[1].Status != "OK" || r.job.Items[0].Status != "PENDING" || d > time.Second {
		t.Fatalf("%+v after %v", r, d)
	}

	// since equal to the current version waits again; DONE ends the wait.
	waiting = pollDividendAsync(context.Background(), handler, cookies, "j1", "?since=2")
	divStillWaiting(t, waiting, 50*time.Millisecond)
	dividendFinish(h, job)
	if r = divAnswerWithin(t, waiting, 2*time.Second, "waiter at DONE"); r.job.Status != "DONE" || r.job.Version != 3 {
		t.Fatalf("%+v", r)
	}
}

// A job that is DONE has nothing left to wait for, whatever since says.
func TestDividendJobDoneAnswersAtOnce(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	job := putDividendJob(h, pollUser, "j1", 1)
	dividendChange(h, job, 0, "OK")
	dividendFinish(h, job)
	for _, q := range []string{"?since=3", "?since=99"} {
		start := time.Now()
		r := pollDividend(context.Background(), handler, cookies, "j1", q)
		if r.code != 200 || r.job.Status != "DONE" || time.Since(start) > time.Second {
			t.Fatalf("%s: %+v after %v", q, r, time.Since(start))
		}
	}
}

// After jobWait the answer is the unchanged state, with the same shape and version.
func TestDividendJobTimeoutAnswersWithTheCurrentState(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = 120 * time.Millisecond
	putDividendJob(h, pollUser, "j1", 1)
	start := time.Now()
	r := pollDividend(context.Background(), handler, cookies, "j1", "?since=1")
	d := time.Since(start)
	if r.code != 200 || r.job.Version != 1 || r.job.Status != "RUNNING" || d < 100*time.Millisecond || d > 2*time.Second {
		t.Fatalf("%+v after %v", r, d)
	}
	if jobWaitMax != 10*time.Second || jobWaitMax >= 15*time.Second {
		t.Fatalf("jobWaitMax %v must stay well under the 15 s WriteTimeout", jobWaitMax)
	}
}

func TestDividendJobBadSinceIsRejected(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute // a wrongly accepted request would hang
	putDividendJob(h, pollUser, "j1", 1)
	for _, q := range []string{"?since=abc", "?since=-1", "?since=1.5", "?since=", "?since=99999999999999999999"} {
		start := time.Now()
		r := pollDividend(context.Background(), handler, cookies, "j1", q)
		if r.code != 400 || !strings.Contains(r.body, `"VALIDATION_ERROR"`) || time.Since(start) > time.Second {
			t.Fatalf("%s: %d %s", q, r.code, r.body)
		}
	}
	// A bad since on a job that does not exist is still a validation error: the
	// request is checked before the lookup, as for price jobs.
	if r := pollDividend(context.Background(), handler, cookies, "nope", "?since=abc"); r.code != 400 {
		t.Fatalf("%d %s", r.code, r.body)
	}
}

func TestDividendJobNotFoundRulesApplyToLongPollsToo(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	putDividendJob(h, "someone-else", "theirs", 1)
	expired := putDividendJob(h, pollUser, "old", 1)
	h.dividends.jobs.mu.Lock()
	expired.Status, expired.created = "DONE", h.dividends.now().Add(-2*time.Hour)
	h.dividends.jobs.mu.Unlock()
	h.jobWait = time.Minute // a wrongly accepted request would hang
	for _, key := range []string{"theirs", "old", "unknown"} {
		for _, q := range []string{"", "?since=1"} {
			start := time.Now()
			if r := pollDividend(context.Background(), handler, cookies, key, q); r.code != 404 || time.Since(start) > time.Second {
				t.Fatalf("%s%s: %d after %v", key, q, r.code, time.Since(start))
			}
		}
	}
	if w := featureRequest(handler, "GET", "/api/dividend-sync-jobs/theirs?since=1", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

// A client that goes away releases its handler at once, and nothing is left behind.
func TestDividendJobClientCancelReturnsPromptlyWithoutLeakingGoroutines(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	job := putDividendJob(h, pollUser, "j1", 1)
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	waiting := pollDividendAsync(ctx, handler, cookies, "j1", "?since=1")
	awaitDividendParked(t, h, job) // authenticated, checked the version, and is waiting
	divStillWaiting(t, waiting, 20*time.Millisecond)
	start := time.Now()
	cancel()
	r := divAnswerWithin(t, waiting, time.Second, "cancelled request")
	if d := time.Since(start); r.code != 200 || d > 500*time.Millisecond || r.job.Version != 1 || len(r.job.Items) != 1 || r.job.Status != "RUNNING" {
		t.Fatalf("%d after %v: %s", r.code, d, r.body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for runtime.NumGoroutine() > before && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if n := runtime.NumGoroutine(); n > before {
		t.Fatalf("%d goroutines before, %d after the request ended", before, n)
	}
}

// Handler.Close releases every waiter with the current state.
func TestHandlerCloseReleasesDividendLongPollWaiters(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	job := putDividendJob(h, pollUser, "j1", 1)
	var waiting []<-chan dividendPoll
	for range 4 {
		waiting = append(waiting, pollDividendAsync(context.Background(), handler, cookies, "j1", "?since=1"))
	}
	awaitDividendParked(t, h, job)
	for _, w := range waiting {
		divStillWaiting(t, w, 20*time.Millisecond)
	}
	start := time.Now()
	go h.Close()
	for _, w := range waiting {
		if r := divAnswerWithin(t, w, 2*time.Second, "waiter after Close"); r.code != 200 || r.job.Version != 1 {
			t.Fatalf("%+v", r)
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waiters were released after %v", d)
	}
}

// Drain (the first step of a server shutdown) releases the waiters too, so
// Server.Shutdown does not wait for a held poll.
func TestHandlerDrainReleasesDividendLongPollWaiters(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	job := putDividendJob(h, pollUser, "j1", 1)
	waiting := pollDividendAsync(context.Background(), handler, cookies, "j1", "?since=1")
	awaitDividendParked(t, h, job)
	start := time.Now()
	h.Drain()
	if r := divAnswerWithin(t, waiting, 2*time.Second, "waiter after Drain"); r.code != 200 || r.job.Version != 1 || time.Since(start) > time.Second {
		t.Fatalf("%+v", r)
	}
}

// One change wakes every waiter. The waiters call awaitChange directly, and the test
// counts the ones that have taken the job's wake-up channel (under the jobs lock,
// exactly as the handler does), so the change comes only after all of them wait.
func TestDividendJobOneChangeWakesEveryRegisteredWaiter(t *testing.T) {
	h, _, _ := pollSetup(t)
	h.jobWait = time.Minute
	job := putDividendJob(h, pollUser, "j1", 1)
	d := h.dividends
	const waiters = 20
	var registered atomic.Int32
	versions := make(chan int64, waiters)
	for range waiters {
		go func() {
			var seen int64
			h.awaitChange(context.Background(), &d.jobs.mu, true, func(long bool) (found, ready bool, wait <-chan struct{}) {
				if !long || job.Version > 1 {
					seen = job.Version
					return true, true, nil
				}
				registered.Add(1)
				return true, false, job.changed.wait()
			})
			versions <- seen
		}()
	}
	deadline := time.Now().Add(5 * time.Second)
	for registered.Load() < waiters {
		if time.Now().After(deadline) {
			t.Fatalf("%d of %d waiters registered", registered.Load(), waiters)
		}
		time.Sleep(time.Millisecond)
	}
	select {
	case v := <-versions:
		t.Fatalf("a waiter was answered with version %d before any change", v)
	default:
	}
	dividendChange(h, job, 0, "OK")
	for range waiters {
		select {
		case v := <-versions:
			if v != 2 {
				t.Fatalf("a waiter saw version %d", v)
			}
		case <-time.After(2 * time.Second):
			t.Fatal("a waiter was not woken by the change")
		}
	}
}

// A change that lands between a client's version check and its wait is never
// lost: every poll that starts at the latest version is answered by the next change,
// however the two interleave.
func TestDividendJobHasNoLostWakeups(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	const rounds = 150
	job := putDividendJob(h, pollUser, "j1", rounds)
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		since := int64(i + 1)
		waiting := pollDividendAsync(context.Background(), handler, cookies, "j1", "?since="+itoa(since))
		wg.Add(1)
		go func() { defer wg.Done(); dividendChange(h, job, i, "OK") }() // races the poll's check and wait
		r := divAnswerWithin(t, waiting, 3*time.Second, "round "+itoa(since))
		if r.job.Version <= since {
			t.Fatalf("round %d answered with version %d", i, r.job.Version)
		}
		wg.Wait()
	}
}
