package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

const pollUser = "google-user-1"

// putJob registers a RUNNING job of user with n PENDING items.
func putJob(h *Handler, user, key string, n int) *priceJob {
	job := &priceJob{Version: 1, Status: "RUNNING", Kind: "PORTFOLIO", user: user, created: time.Now(), Items: make([]priceItem, n)}
	for i := range job.Items {
		job.Items[i] = priceItem{InstrumentID: int64(i + 1), Status: "PENDING"}
	}
	h.jobs.mu.Lock()
	h.jobs.entries[key] = job
	h.jobs.mu.Unlock()
	return job
}

// change is what a runner does for one finished item.
func change(h *Handler, job *priceJob, i int, status string) {
	h.jobs.mu.Lock()
	job.Items[i].Status = status
	h.jobs.touch(job)
	h.jobs.mu.Unlock()
}

func finish(h *Handler, job *priceJob) {
	h.jobs.mu.Lock()
	job.Status = "DONE"
	h.jobs.touch(job)
	h.jobs.mu.Unlock()
}

type pollResult struct {
	code int
	job  priceJob
	body string
}

// poll sends one GET and returns when it is answered.
func poll(ctx context.Context, handler http.Handler, cookies []*http.Cookie, key, query string) pollResult {
	r := httptest.NewRequest("GET", "/api/price-fetch-jobs/"+key+query, nil).WithContext(ctx)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	res := pollResult{code: w.Code, body: w.Body.String()}
	if w.Code == 200 {
		json.Unmarshal(w.Body.Bytes(), &res.job)
	}
	return res
}

// pollAsync starts a GET in the background.
func pollAsync(ctx context.Context, handler http.Handler, cookies []*http.Cookie, key, query string) <-chan pollResult {
	out := make(chan pollResult, 1)
	go func() { out <- poll(ctx, handler, cookies, key, query) }()
	return out
}

func answerWithin(t *testing.T, ch <-chan pollResult, d time.Duration, what string) pollResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(d):
		t.Fatalf("%s: no answer within %v", what, d)
		return pollResult{}
	}
}

func stillWaiting(t *testing.T, ch <-chan pollResult, d time.Duration) {
	t.Helper()
	select {
	case r := <-ch:
		t.Fatalf("answered although nothing changed: %d %+v", r.code, r.job)
	case <-time.After(d):
	}
}

// awaitRegistered returns once a request is parked on the job: a waiter creates the
// job's changed channel under h.jobs.mu just before it blocks.
func awaitRegistered(t *testing.T, h *Handler, job *priceJob) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		h.jobs.mu.Lock()
		parked := job.changed != nil
		h.jobs.mu.Unlock()
		if parked {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no request ever parked on the job")
}

func pollSetup(t *testing.T) (*Handler, http.Handler, []*http.Cookie) {
	t.Helper()
	h, handler := auditHandler(t, ":memory:")
	return h, handler, auditLogin(t, h, handler)
}

func TestJobAnswersWithoutSinceImmediately(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	putJob(h, pollUser, "j1", 2)
	start := time.Now()
	r := poll(context.Background(), handler, cookies, "j1", "")
	if r.code != 200 || r.job.Version != 1 || r.job.Status != "RUNNING" || len(r.job.Items) != 2 || time.Since(start) > time.Second {
		t.Fatalf("%+v after %v", r, time.Since(start))
	}
	// An older version is answered at once too.
	if r = poll(context.Background(), handler, cookies, "j1", "?since=0"); r.code != 200 || r.job.Version != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestJobWaitsForTheVersionAndAChangeWakesIt(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	job := putJob(h, pollUser, "j1", 3)
	waiting := pollAsync(context.Background(), handler, cookies, "j1", "?since=1")
	stillWaiting(t, waiting, 150*time.Millisecond)
	start := time.Now()
	change(h, job, 0, "OK")
	r := answerWithin(t, waiting, 500*time.Millisecond, "wake-up")
	if r.code != 200 || r.job.Version != 2 || r.job.Items[0].Status != "OK" {
		t.Fatalf("%+v", r)
	}
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("woke after %v", d)
	}
	// The next request, with the new version, waits again.
	next := pollAsync(context.Background(), handler, cookies, "j1", "?since=2")
	stillWaiting(t, next, 100*time.Millisecond)
	change(h, job, 1, "OK")
	if r = answerWithin(t, next, 500*time.Millisecond, "second wake-up"); r.job.Version != 3 {
		t.Fatalf("%+v", r.job)
	}
}

func TestJobChangesCoalesceIntoOneAnswer(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	job := putJob(h, pollUser, "j1", 3)
	waiting := pollAsync(context.Background(), handler, cookies, "j1", "?since=1")
	stillWaiting(t, waiting, 100*time.Millisecond)
	h.jobs.mu.Lock() // three changes while the lock is held: one answer with the latest state
	for i := range job.Items {
		job.Items[i].Status = "OK"
		h.jobs.touch(job)
	}
	h.jobs.mu.Unlock()
	r := answerWithin(t, waiting, 500*time.Millisecond, "coalesced answer")
	if r.job.Version != 4 || r.job.Items[2].Status != "OK" {
		t.Fatalf("%+v", r.job)
	}
}

func TestJobWaitTimesOutWithTheCurrentState(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = 120 * time.Millisecond
	putJob(h, pollUser, "j1", 1)
	start := time.Now()
	r := poll(context.Background(), handler, cookies, "j1", "?since=1")
	if d := time.Since(start); r.code != 200 || r.job.Version != 1 || r.job.Status != "RUNNING" || d < 100*time.Millisecond || d > time.Second {
		t.Fatalf("%+v after %v", r, d)
	}
}

func TestJobDefaultWaitStaysBelowTheServerWriteTimeout(t *testing.T) {
	if jobWaitMax != 10*time.Second || jobWaitMax >= 15*time.Second {
		t.Fatalf("jobWaitMax %v must stay well under the 15 s WriteTimeout", jobWaitMax)
	}
}

func TestJobDoneAnswersImmediatelyEvenWithCurrentVersion(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	job := putJob(h, pollUser, "j1", 1)
	finish(h, job)
	start := time.Now()
	r := poll(context.Background(), handler, cookies, "j1", "?since=2")
	if r.code != 200 || r.job.Status != "DONE" || r.job.Version != 2 || time.Since(start) > time.Second {
		t.Fatalf("%+v after %v", r, time.Since(start))
	}
	// A waiter is released by the change to DONE.
	job2 := putJob(h, pollUser, "j2", 1)
	waiting := pollAsync(context.Background(), handler, cookies, "j2", "?since=1")
	stillWaiting(t, waiting, 100*time.Millisecond)
	finish(h, job2)
	if r = answerWithin(t, waiting, 500*time.Millisecond, "DONE wake-up"); r.job.Status != "DONE" {
		t.Fatalf("%+v", r.job)
	}
}

func TestJobBadSinceIsRejected(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	putJob(h, pollUser, "j1", 1)
	for _, q := range []string{"?since=abc", "?since=", "?since=-1", "?since=1.5", "?since=99999999999999999999"} {
		r := poll(context.Background(), handler, cookies, "j1", q)
		if r.code != 400 || !contains(r.body, "VALIDATION_ERROR") {
			t.Fatalf("%s: %d %s", q, r.code, r.body)
		}
	}
}

func contains(s, part string) bool {
	for i := 0; i+len(part) <= len(s); i++ {
		if s[i:i+len(part)] == part {
			return true
		}
	}
	return false
}

func TestJobNotFoundRulesApplyToLongPollsToo(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	putJob(h, "someone-else", "theirs", 1)
	expired := putJob(h, pollUser, "old", 1)
	h.jobs.mu.Lock()
	expired.Status, expired.created = "DONE", time.Now().Add(-2*time.Hour)
	h.jobs.mu.Unlock()
	h.jobWait = time.Minute // a wrongly accepted request would hang
	for _, key := range []string{"theirs", "old", "unknown"} {
		for _, q := range []string{"", "?since=1"} {
			start := time.Now()
			if r := poll(context.Background(), handler, cookies, key, q); r.code != 404 || time.Since(start) > time.Second {
				t.Fatalf("%s%s: %d after %v", key, q, r.code, time.Since(start))
			}
		}
	}
	if w := featureRequest(handler, "GET", "/api/price-fetch-jobs/theirs?since=1", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

// A client that goes away releases its handler at once, and nothing is left behind.
func TestJobClientCancelReturnsPromptlyWithoutLeakingGoroutines(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	putJob(h, pollUser, "j1", 1)
	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	waiting := pollAsync(ctx, handler, cookies, "j1", "?since=1")
	stillWaiting(t, waiting, 100*time.Millisecond)
	start := time.Now()
	cancel()
	r := answerWithin(t, waiting, time.Second, "cancelled request")
	// h.avBudget is nil: the real store path runs with the cancelled context, and
	// the answer is still the documented job JSON with the current state.
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
func TestHandlerCloseReleasesLongPollWaiters(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	job := putJob(h, pollUser, "j1", 1)
	var waiting []<-chan pollResult
	for range 4 {
		waiting = append(waiting, pollAsync(context.Background(), handler, cookies, "j1", "?since=1"))
	}
	awaitRegistered(t, h, job)
	for _, w := range waiting {
		stillWaiting(t, w, 20*time.Millisecond)
	}
	start := time.Now()
	go h.Close()
	for _, w := range waiting {
		if r := answerWithin(t, w, 2*time.Second, "waiter after Close"); r.code != 200 || r.job.Version != 1 {
			t.Fatalf("%+v", r)
		}
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("waiters were released after %v", d)
	}
}

// The budget is read outside h.jobs.mu: while it is being read, the job table is
// free for runners and other requests.
func TestJobAnswerReadsTheBudgetOutsideTheJobsLock(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	job := putJob(h, pollUser, "j1", 1)
	inBudget, release := make(chan struct{}), make(chan struct{})
	h.avBudget = func(ctx context.Context, user string) (marketdata.Budget, error) {
		close(inBudget)
		<-release
		return marketdata.Budget{NextSlotAt: time.Unix(1700000000, 0)}, nil
	}
	answer := pollAsync(context.Background(), handler, cookies, "j1", "")
	select {
	case <-inBudget:
	case <-time.After(2 * time.Second):
		t.Fatal("the budget was never read")
	}
	if !h.jobs.mu.TryLock() {
		t.Fatal("h.jobs.mu is held while the budget is read")
	}
	h.jobs.mu.Unlock()
	// A runner can change the job meanwhile; the answer being encoded is the snapshot.
	change(h, job, 0, "OK")
	close(release)
	r := answerWithin(t, answer, time.Second, "answer")
	if r.code != 200 || r.job.NextRequestAt.Unix() != 1700000000 || r.job.Version != 1 || r.job.Items[0].Status != "PENDING" {
		t.Fatalf("the answer is not the consistent snapshot: %+v", r)
	}
	h.jobs.mu.Lock()
	stored := job.NextRequestAt
	h.jobs.mu.Unlock()
	if !stored.IsZero() {
		t.Fatal("the budget was written into the shared job")
	}
}

// Changes that land between a client's version check and its wait are never lost.
func TestJobNoLostWakeupUnderConcurrentChanges(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = 3 * time.Second
	job := putJob(h, pollUser, "j1", 1)
	const rounds = 150
	var wg sync.WaitGroup
	for i := 0; i < rounds; i++ {
		h.jobs.mu.Lock()
		since := job.Version
		h.jobs.mu.Unlock()
		waiting := pollAsync(context.Background(), handler, cookies, "j1", "?since="+itoa(since))
		wg.Go(func() { change(h, job, 0, "OK") }) // races with the waiter's check and wait
		start := time.Now()
		r := answerWithin(t, waiting, 2*time.Second, "round")
		if r.job.Version <= since || time.Since(start) > time.Second {
			t.Fatalf("round %d: answered version %d for since=%d after %v", i, r.job.Version, since, time.Since(start))
		}
	}
	wg.Wait()
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

// Two clients waiting for the same job are both woken by one change.
func TestJobChangeWakesEveryWaiter(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	job := putJob(h, pollUser, "j1", 1)
	a := pollAsync(context.Background(), handler, cookies, "j1", "?since=1")
	b := pollAsync(context.Background(), handler, cookies, "j1", "?since=1")
	stillWaiting(t, a, 100*time.Millisecond)
	stillWaiting(t, b, 10*time.Millisecond)
	change(h, job, 0, "OK")
	for name, ch := range map[string]<-chan pollResult{"first": a, "second": b} {
		if r := answerWithin(t, ch, 500*time.Millisecond, name+" waiter"); r.job.Version != 2 {
			t.Fatalf("%s: %+v", name, r.job)
		}
	}
}

// A held GET released by Handler.Close, with the store closed right after Close
// returns, is a 200 with the current state: the awakened handler does no store work.
func TestHeldJobRequestSurvivesCloseThenStoreClose(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	job := putJob(h, pollUser, "j1", 2)
	held := pollAsync(context.Background(), handler, cookies, "j1", "?since=1")
	awaitRegistered(t, h, job)
	h.Close()
	h.store.Close() // what main does next
	r := answerWithin(t, held, 2*time.Second, "held request after Close")
	if r.code != 200 || r.job.Version != 1 || len(r.job.Items) != 2 || r.job.Status != "RUNNING" {
		t.Fatalf("%d %s", r.code, r.body)
	}
}

// followToDone is what a client does: one GET, then GET ?since=<version> until DONE.
// It checks that every answer carries a newer version and returns the versions seen.
func followToDone(t *testing.T, handler http.Handler, cookies []*http.Cookie, key string) ([]int64, priceJob) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	var versions []int64
	query := ""
	for time.Now().Before(deadline) {
		r := poll(context.Background(), handler, cookies, key, query)
		if r.code != 200 || r.job.Version < 1 {
			t.Fatalf("%d %s", r.code, r.body)
		}
		if n := len(versions); n > 0 && r.job.Version <= versions[n-1] && r.job.Status != "DONE" {
			t.Fatalf("an answer to since=%d carried version %d", versions[n-1], r.job.Version)
		}
		versions = append(versions, r.job.Version)
		if r.job.Status == "DONE" {
			return versions, r.job
		}
		query = "?since=" + itoa(r.job.Version)
	}
	t.Fatal("job never finished")
	return nil, priceJob{}
}

// Every runner reports its changes through the job version: QUEUED(1) -> RUNNING ->
// one change per item -> DONE.
func assertVersions(t *testing.T, kind string, versions []int64, job priceJob) {
	t.Helper()
	want := int64(len(job.Items) + 3) // queued + RUNNING + each item + DONE
	if got := versions[len(versions)-1]; got != want {
		t.Fatalf("%s: final version %d, want %d for %d items (seen %v)", kind, got, want, len(job.Items), versions)
	}
	for _, it := range job.Items {
		if it.Status == "PENDING" {
			t.Fatalf("%s: item left PENDING: %+v", kind, job.Items)
		}
	}
}

func TestEveryRunnerBumpsTheJobVersion(t *testing.T) {
	t.Run("yahoo real-time", func(t *testing.T) {
		h, handler, cookies, _ := parallelSetup(t, map[string]string{"VA1": "SAP.DE", "VA2": "SAP.DE"})
		ids := linkedInstruments(t, h, "VA1", "VA2")
		key := startPriceJob(t, handler, yahooPortfolio(t, h, ids...), realtimeBody(ids), cookies)
		versions, job := followToDone(t, handler, cookies, key)
		assertVersions(t, "yahoo real-time", versions, job)
	})
	t.Run("yahoo prices", func(t *testing.T) {
		h, handler, cookies, _, _ := yahooSetup(t)
		path := yahooPortfolio(t, h, 2, 7, 18)
		body := fmt.Sprintf(`{"date":%q,"instrumentIds":[2,7,18],"provider":"YAHOO"}`, marketdata.BerlinDay(time.Now()))
		versions, job := followToDone(t, handler, cookies, startPriceJob(t, handler, path, body, cookies))
		assertVersions(t, "yahoo prices", versions, job)
	})
	t.Run("yahoo past date", func(t *testing.T) {
		h, handler, cookies, _, _ := yahooSetup(t)
		path := yahooPortfolio(t, h, 7, 18)
		versions, job := followToDone(t, handler, cookies, startPriceJob(t, handler, path, `{"date":"2026-11-03","instrumentIds":[7,18],"provider":"YAHOO"}`, cookies))
		assertVersions(t, "yahoo past date", versions, job)
	})
	t.Run("eulerpool", func(t *testing.T) {
		h, handler, cookies, _, _ := epSetup(t)
		ctx := context.Background()
		p, err := h.store.SavePortfolio(ctx, pollUser, 0, "EP")
		if err != nil {
			t.Fatal(err)
		}
		if _, err = h.store.AddItems(ctx, pollUser, p.ID, []int64{1, 7}); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"date":%q,"instrumentIds":[1,7]}`, marketdata.BerlinDay(time.Now()))
		versions, job := followToDone(t, handler, cookies, startPriceJob(t, handler, fmt.Sprintf("/api/portfolios/%d", p.ID), body, cookies))
		assertVersions(t, "eulerpool", versions, job)
	})
	t.Run("alpha vantage", func(t *testing.T) {
		h, handler := auditHandler(t, ":memory:")
		cookies := auditLogin(t, h, handler)
		ctx := context.Background()
		h.av.Limits.Minute = 1000000
		h.av.Limits.User = 10
		p, _ := h.store.SavePortfolio(ctx, pollUser, 0, "AV")
		h.store.AddItems(ctx, pollUser, p.ID, []int64{1})
		if err := h.store.LinkSymbol(ctx, pollUser, 1, "S1", "EUR", ""); err != nil {
			t.Fatal(err)
		}
		h.av.Transport = avTestTransport(func(c context.Context, fn, q string) ([]byte, error) { return marketdata.MockAV{}.Call(c, fn, q) })
		body := fmt.Sprintf(`{"date":%q,"instrumentIds":[1]}`, marketdata.BerlinDay(time.Now()))
		versions, job := followToDone(t, handler, cookies, startPriceJob(t, handler, fmt.Sprintf("/api/portfolios/%d", p.ID), body, cookies))
		assertVersions(t, "alpha vantage", versions, job)
	})
	t.Run("user real-time refresh", func(t *testing.T) {
		h, handler, cookies, _, _, _ := yahooPrimary(t)
		summaryPortfolio(t, h, pollUser, "One", []int64{7, 18}, []int64{7, 18})
		for id, symbol := range map[int64]string{7: "SAP.DE", 18: "VHYD.L"} {
			if err := h.store.LinkYahooSymbol(context.Background(), id, symbol); err != nil {
				t.Fatal(err)
			}
		}
		result := summaryRefresh(t, handler, cookies, "{}", 202)
		if result.JobID == nil {
			t.Fatalf("%+v", result)
		}
		versions, job := followToDone(t, handler, cookies, *result.JobID)
		if job.Kind != "USER_REALTIME" {
			t.Fatalf("%+v", job)
		}
		assertVersions(t, "user real-time", versions, job)
	})
}

// The production shutdown order over a real HTTP server: Shutdown (which drains the
// handler), then Handler.Close, then the store is closed. A long-poll request that
// is held when Shutdown begins gets its snapshot at once, so Shutdown ends well
// before its grace deadline and cleanup completes; without the drain it would wait
// out the whole hold.
func TestServerShutdownReleasesHeldLongPollsBeforeTheGraceDeadline(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute // far beyond the grace period below
	job := putJob(h, pollUser, "j1", 2)
	srv := httptest.NewUnstartedServer(handler)
	srv.Config.RegisterOnShutdown(h.Drain) // as newServer does in cmd/api
	srv.Start()
	defer srv.Close()

	type answer struct {
		code int
		job  priceJob
		err  error
	}
	held := make(chan answer, 1)
	go func() {
		req, _ := http.NewRequest("GET", srv.URL+"/api/price-fetch-jobs/j1?since=1", nil)
		for _, c := range cookies {
			req.AddCookie(c)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			held <- answer{err: err}
			return
		}
		defer resp.Body.Close()
		a := answer{code: resp.StatusCode}
		a.err = json.NewDecoder(resp.Body).Decode(&a.job)
		held <- a
	}()
	awaitRegistered(t, h, job) // the request has reached the server and is parked
	select {
	case a := <-held:
		t.Fatalf("answered before shutdown although nothing changed: %+v", a)
	default:
	}

	const grace = 3 * time.Second
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	if err := srv.Config.Shutdown(ctx); err != nil {
		t.Fatalf("Shutdown did not finish within %v: %v", grace, err)
	}
	h.Close()       // main's deferred Close
	h.store.Close() // then the store
	if d := time.Since(start); d > grace/2 {
		t.Fatalf("shutdown and cleanup took %v", d)
	}
	select {
	case a := <-held:
		if a.err != nil || a.code != 200 || a.job.Version != 1 || len(a.job.Items) != 2 {
			t.Fatalf("%+v", a)
		}
	case <-time.After(time.Second):
		t.Fatal("the held request was not answered")
	}
}

// A request released by Drain (the first phase of shutdown; Close has not run and
// neither the request nor h.ctx is cancelled) is answered with its captured state
// and starts no budget work.
func TestDrainedLongPollSkipsTheBudgetRead(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.jobWait = time.Minute
	job := putJob(h, pollUser, "j1", 1)
	var budgetCalls atomic.Int32
	h.avBudget = func(ctx context.Context, user string) (marketdata.Budget, error) {
		budgetCalls.Add(1)
		return marketdata.Budget{}, errors.New("budget read after drain")
	}
	held := pollAsync(context.Background(), handler, cookies, "j1", "?since=1")
	awaitRegistered(t, h, job)
	h.Drain()
	r := answerWithin(t, held, time.Second, "drained request")
	if r.code != 200 || r.job.Version != 1 || budgetCalls.Load() != 0 {
		t.Fatalf("%d %s (budget reads: %d)", r.code, r.body, budgetCalls.Load())
	}
	// Requests that arrive during the drain are answered at once, without budget work.
	if r = poll(context.Background(), handler, cookies, "j1", "?since=1"); r.code != 200 || budgetCalls.Load() != 0 {
		t.Fatalf("%d %s (budget reads: %d)", r.code, r.body, budgetCalls.Load())
	}
}

// A budget read that is already running when Drain starts is cancelled with it, and
// the request still answers 200 with its state: optional work never uses up the
// shutdown grace.
func TestDrainCancelsABudgetReadThatIsAlreadyRunning(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	putJob(h, pollUser, "j1", 1)
	inBudget := make(chan struct{})
	h.avBudget = func(ctx context.Context, user string) (marketdata.Budget, error) {
		close(inBudget)
		<-ctx.Done() // a read that only ends when its context does
		return marketdata.Budget{}, ctx.Err()
	}
	answer := pollAsync(context.Background(), handler, cookies, "j1", "")
	select {
	case <-inBudget:
	case <-time.After(2 * time.Second):
		t.Fatal("the budget was never read")
	}
	start := time.Now()
	h.Drain()
	r := answerWithin(t, answer, time.Second, "request with a running budget read")
	if d := time.Since(start); r.code != 200 || r.job.Version != 1 || len(r.job.Items) != 1 || d > 500*time.Millisecond {
		t.Fatalf("%d after %v: %s", r.code, d, r.body)
	}
}

// A queued multi-item job refused because the handler is closed never runs, and its
// failure follows the version contract: one version per item and one for DONE.
func TestJobRefusedAfterCloseBumpsTheVersionPerItem(t *testing.T) {
	h, handler, cookies := pollSetup(t)
	h.Close()
	job := putJob(h, pollUser, "j1", 3)
	job.Status = "QUEUED"
	ran := false
	h.startYahooJob(job, func() { ran = true })
	if ran {
		t.Fatal("the runner executed on a closed handler")
	}
	r := poll(context.Background(), handler, cookies, "j1", "")
	if r.code != 200 || r.job.Status != "DONE" || r.job.Version != 1+3+1 { // queued + 3 items + DONE
		t.Fatalf("%d version %d: %s", r.code, r.job.Version, r.body)
	}
	for i, it := range r.job.Items {
		if it.Status != "ERROR" {
			t.Fatalf("item %d: %+v", i, it)
		}
	}
	// A client that was told version 2 (one item failed) still sees later changes as newer.
	if again := poll(context.Background(), handler, cookies, "j1", "?since=2"); again.job.Version != 5 {
		t.Fatalf("%+v", again.job)
	}
}
