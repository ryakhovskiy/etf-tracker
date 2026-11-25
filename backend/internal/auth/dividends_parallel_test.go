package auth

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

const rateLimitMessage = "Yahoo Finance is rate limiting requests. Try again later."

// waitStarts reads n request starts from the fake.
func waitStarts(t *testing.T, started <-chan int64, n int) []int64 {
	t.Helper()
	var ids []int64
	for range n {
		select {
		case id := <-started:
			ids = append(ids, id)
		case <-time.After(5 * time.Second):
			t.Fatalf("only %d of %d requests started: %v", len(ids), n, ids)
		}
	}
	return ids
}

// pollUntil long-polls the job, version by version, until pred holds. Every answer
// must carry a version that never goes back.
func pollUntil(t *testing.T, handler http.Handler, cookies []*http.Cookie, key string, pred func(dividendJob) bool) dividendJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	since := int64(0)
	for time.Now().Before(deadline) {
		r := pollDividend(context.Background(), handler, cookies, key, "?since="+itoa(since))
		if r.code != 200 {
			t.Fatalf("poll: %d %s", r.code, r.body)
		}
		if r.job.Version < since {
			t.Fatalf("the version went back from %d to %d", since, r.job.Version)
		}
		since = r.job.Version
		if pred(r.job) {
			return r.job
		}
	}
	t.Fatal("the job never reached the expected state")
	return dividendJob{}
}

func gates(ids ...int64) map[int64]chan struct{} {
	out := map[int64]chan struct{}{}
	for _, id := range ids {
		out[id] = make(chan struct{})
	}
	return out
}

// With a blocking transport, every item has its request in flight at once, up to the
// gateway's concurrency cap, and the cap is never exceeded.
func TestDividendSyncSendsTheRequestsConcurrently(t *testing.T) {
	h, handler, cookies, _, _, _ := dividendSetup(t)
	fake := &fakeDividends{block: make(chan struct{}), started: make(chan int64, 16)}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	h.yahoo.MaxConcurrency = 4
	path := yahooPortfolio(t, h, 7, 10, 18, 20)

	key := startDividendSync(t, handler, path, "", cookies)
	// No request has returned, yet all four have started: nothing waits for another.
	started := waitStarts(t, fake.started, 4)
	if fake.peakInflight() != 4 || len(started) != 4 {
		t.Fatalf("peak %d, started %v", fake.peakInflight(), started)
	}
	if r := pollDividend(context.Background(), handler, cookies, key, ""); r.job.Status != "RUNNING" || r.job.Done != 0 {
		t.Fatalf("%+v", r.job)
	}
	close(fake.block)
	job := pollUntil(t, handler, cookies, key, func(j dividendJob) bool { return j.Status == "DONE" })
	if job.Done != 4 || fake.peakInflight() != 4 {
		t.Fatalf("%+v peak %d", job, fake.peakInflight())
	}
}

func TestDividendSyncRespectsTheConcurrencyCap(t *testing.T) {
	h, handler, cookies, _, _, _ := dividendSetup(t)
	fake := &fakeDividends{gate: gates(7, 10, 18, 20), started: make(chan int64, 16)}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	h.yahoo.MaxConcurrency = 2
	path := yahooPortfolio(t, h, 7, 10, 18, 20)

	key := startDividendSync(t, handler, path, "", cookies)
	first := waitStarts(t, fake.started, 2)
	// The cap is reached: no third request starts while two are in flight.
	select {
	case id := <-fake.started:
		t.Fatalf("request %d started beyond the cap of 2", id)
	case <-time.After(150 * time.Millisecond):
	}
	// One finishing lets the next item start.
	close(fake.gate[first[0]])
	waitStarts(t, fake.started, 1)
	select {
	case id := <-fake.started:
		t.Fatalf("request %d started beyond the cap of 2", id)
	case <-time.After(50 * time.Millisecond):
	}
	for id, g := range fake.gate {
		if id != first[0] {
			close(g)
		}
	}
	job := pollUntil(t, handler, cookies, key, func(j dividendJob) bool { return j.Status == "DONE" })
	if job.Done != 4 || fake.peakInflight() != 2 {
		t.Fatalf("%+v peak %d", job, fake.peakInflight())
	}
}

// Items finish in any order: each status is set at its own index, Done counts the
// finished ones, and every change is a new version.
func TestDividendSyncFollowsOutOfOrderCompletion(t *testing.T) {
	h, handler, cookies, _, _, _ := dividendSetup(t)
	fake := &fakeDividends{gate: gates(7, 10, 18, 20), fail: map[int64]error{10: marketdata.ErrYahooNotFound}, started: make(chan int64, 16)}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	h.yahoo.MaxConcurrency = 4
	path := yahooPortfolio(t, h, 7, 10, 18, 20) // items follow the instrument ids
	key := startDividendSync(t, handler, path, "", cookies)
	waitStarts(t, fake.started, 4)

	first := pollDividend(context.Background(), handler, cookies, key, "")
	if first.job.Version != 1 || first.job.Total != 4 || first.job.Done != 0 {
		t.Fatalf("%+v", first.job)
	}
	statuses := func(j dividendJob) string {
		var s []string
		for _, it := range j.Items {
			s = append(s, it.Status)
		}
		return strings.Join(s, ",")
	}
	// The last item finishes first, then the second (an error), then the rest.
	steps := []struct {
		release int64
		done    int
		want    string
	}{
		{20, 1, "PENDING,PENDING,PENDING,OK"},
		{10, 2, "PENDING,ERROR,PENDING,OK"},
		{7, 3, "OK,ERROR,PENDING,OK"},
	}
	version := first.job.Version
	for _, st := range steps {
		close(fake.gate[st.release])
		job := pollUntil(t, handler, cookies, key, func(j dividendJob) bool { return j.Version > version })
		if job.Done != st.done || statuses(job) != st.want || job.Version != version+1 || job.Status != "RUNNING" {
			t.Fatalf("after %d: %+v (%s)", st.release, job, statuses(job))
		}
		version = job.Version
	}
	for i, id := range []int64{7, 10, 18, 20} {
		if h := pollDividend(context.Background(), handler, cookies, key, ""); h.job.Items[i].InstrumentID != id {
			t.Fatalf("item %d is %d", i, h.job.Items[i].InstrumentID)
		}
	}
	close(fake.gate[18])
	job := pollUntil(t, handler, cookies, key, func(j dividendJob) bool { return j.Status == "DONE" })
	// 1 + one version per item + one for DONE.
	if job.Done != 4 || statuses(job) != "OK,ERROR,OK,OK" || job.Version != 1+4+1 || job.Items[1].Error != "Yahoo Finance has no data for this symbol." {
		t.Fatalf("%+v (%s)", job, statuses(job))
	}
	// The running map is cleaned up: the next sync may start.
	if w := featureRequest(handler, "POST", path+"/dividend-sync", "", cookies...); w.Code != 202 {
		t.Fatal(w.Code, w.Body.String())
	}
}

// A 429 lets the requests in flight finish with their own results; the items that
// have not started fail with the rate-limit message and send nothing. Every failed
// item is recorded like a failed fetch.
func TestDividendSyncRateLimitFinishesInFlightAndSkipsTheRest(t *testing.T) {
	h, handler, cookies, _, _, _ := dividendSetup(t)
	fake := &fakeDividends{gate: gates(7, 10), fail: map[int64]error{7: marketdata.ErrYahooLimit}, started: make(chan int64, 16)}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	h.yahoo.MaxConcurrency = 2
	path := yahooPortfolio(t, h, 7, 10, 18, 20)

	key := startDividendSync(t, handler, path, "", cookies)
	waitStarts(t, fake.started, 2) // items 7 and 10 are in flight; 18 and 20 wait for a slot
	close(fake.gate[7])            // 7 answers 429
	// The two items that had not started fail at once, without a request.
	job := pollUntil(t, handler, cookies, key, func(j dividendJob) bool { return j.Done == 3 })
	for i, want := range []string{"ERROR", "PENDING", "ERROR", "ERROR"} {
		if job.Items[i].Status != want {
			t.Fatalf("%+v", job.Items)
		}
	}
	for _, i := range []int{0, 2, 3} {
		if job.Items[i].Error != rateLimitMessage {
			t.Fatalf("item %d: %q", i, job.Items[i].Error)
		}
	}
	// The request that was in flight is not cut short and keeps its own result.
	close(fake.gate[10])
	job = pollUntil(t, handler, cookies, key, func(j dividendJob) bool { return j.Status == "DONE" })
	if job.Done != 4 || job.Items[1].Status != "OK" || job.Items[1].Error != "" {
		t.Fatalf("%+v", job.Items)
	}
	if calls := fake.snapshot(); len(calls) != 2 || !strings.HasPrefix(calls[0], "7 ") || !strings.HasPrefix(calls[1], "10 ") {
		t.Fatalf("requests sent: %v", calls)
	}
	// Failed items keep their cached data, are reported as errors, and stay stale.
	cal := getCalendar(t, handler, path, 2026, cookies)
	got := map[int64]string{}
	for _, e := range cal.Sync.Errors {
		got[e.InstrumentID] = e.Message
	}
	if len(got) != 3 || got[7] != rateLimitMessage || got[18] != rateLimitMessage || got[20] != rateLimitMessage || got[10] != "" {
		t.Fatalf("%+v", cal.Sync.Errors)
	}
	// The next sync is not blocked by the 429 of this one.
	fake.fail, fake.gate = nil, nil
	if job = pollUntil(t, handler, cookies, startDividendSync(t, handler, path, "", cookies), func(j dividendJob) bool { return j.Status == "DONE" }); job.Items[0].Status != "OK" || job.Items[3].Status != "OK" {
		t.Fatalf("%+v", job.Items)
	}
}

// Version bumps are one per item plus one for DONE, also with a job of no items.
func TestDividendSyncVersions(t *testing.T) {
	h, handler, cookies, _, _, _ := dividendSetup(t)
	fake := &fakeDividends{}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	path := yahooPortfolio(t, h, 7)
	job := pollUntil(t, handler, cookies, startDividendSync(t, handler, path, "", cookies), func(j dividendJob) bool { return j.Status == "DONE" })
	if job.Version != 3 {
		t.Fatalf("%+v", job)
	}
	// Nothing is stale: the job is DONE at once, and its items are an empty array,
	// not null, on every answer (plain and long poll).
	emptyKey := startDividendSync(t, handler, path, `{"scope":"STALE"}`, cookies)
	for _, q := range []string{"", "?since=1", "?since=0"} {
		empty := pollDividend(context.Background(), handler, cookies, emptyKey, q)
		if empty.job.Status != "DONE" || empty.job.Total != 0 || empty.job.Version != 1 || !strings.Contains(empty.body, `"items":[]`) || strings.Contains(empty.body, `"items":null`) {
			t.Fatalf("%q: %s", q, empty.body)
		}
	}
}

// forEachParallel may hand a free slot to an item although ctx is already done. That
// item must take the interrupted path, whatever the store answers to a cancelled
// context; none may report a storage failure.
func TestDividendSyncItemsStartedAfterCancelAreInterrupted(t *testing.T) {
	h, _, _, _, _, _ := dividendSetup(t)
	fake := &fakeDividends{}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	h.yahoo.MaxConcurrency = 1
	const n = 40
	job := putDividendJob(h, pollUser, "j1", n)
	for i := range job.Items {
		job.Items[i].InstrumentID = int64([]int{7, 10, 18, 20}[i%4]) // instruments the store knows
	}
	h.dividends.jobs.running[0] = "j1"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.runDividendSync(ctx, "j1", job, "2025-01-01")
	h.dividends.jobs.mu.Lock()
	done := job.snapshot()
	h.dividends.jobs.mu.Unlock()
	if done.Status != "DONE" || done.Done != n || len(fake.snapshot()) != 0 {
		t.Fatalf("%+v requests=%v", done, fake.snapshot())
	}
	for i, it := range done.Items {
		if it.Status != "ERROR" || it.Error != "The sync was interrupted." {
			t.Fatalf("item %d: %+v", i, it)
		}
	}
	// And syncInstrument itself, as an item that got past the check would run it.
	if msg := h.syncInstrument(ctx, fake, 7, "2025-01-01"); msg != "The sync was interrupted." {
		t.Fatalf("%q", msg)
	}
}

// Close cancels the requests in flight and waits for them; the job ends DONE, and
// nothing runs, or is recorded, afterwards.
func TestHandlerCloseCancelsAndWaitsForARunningDividendSync(t *testing.T) {
	h, handler, cookies, _, _, _ := dividendSetup(t)
	fake := &fakeDividends{block: make(chan struct{}), started: make(chan int64, 16)}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	h.yahoo.MaxConcurrency = 2
	path := yahooPortfolio(t, h, 7, 10, 18, 20)
	key := startDividendSync(t, handler, path, "", cookies)
	waitStarts(t, fake.started, 2)
	waiting := pollDividendAsync(context.Background(), handler, cookies, key, "?since=1")
	divStillWaiting(t, waiting, 50*time.Millisecond)

	closed := make(chan struct{})
	go func() { h.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the running sync")
	}
	fake.mu.Lock()
	inflight, sent := fake.inflight, len(fake.calls)
	fake.mu.Unlock()
	if inflight != 0 || sent != 2 {
		t.Fatalf("Close returned with %d requests in flight; %d were sent", inflight, sent)
	}
	h.dividends.jobs.mu.Lock()
	job := h.dividends.jobs.entries[key].snapshot()
	running := len(h.dividends.jobs.running)
	h.dividends.jobs.mu.Unlock()
	if job.Status != "DONE" || job.Done != 4 || running != 0 {
		t.Fatalf("%+v running=%d", job, running)
	}
	for _, it := range job.Items {
		if it.Status != "ERROR" || it.Error != "The sync was interrupted." {
			t.Fatalf("%+v", job.Items)
		}
	}
	// A poll that was waiting was released by the shutdown, not by a timeout.
	if r := divAnswerWithin(t, waiting, 2*time.Second, "waiter"); r.code != 200 {
		t.Fatalf("%+v", r)
	}
	// A shutdown is not a provider failure: nothing was recorded for any item.
	for _, id := range []int64{7, 10, 18, 20} {
		if st, err := h.store.DividendSyncState(context.Background(), id); err != nil || st != nil {
			t.Fatalf("instrument %d: %+v %v", id, st, err)
		}
	}
}

// Close during the Yahoo session build, which the sync's items share: the items wake
// with the gateway's cancellation but find their own context done, so no failure is
// recorded for them, and Close waits for all of it.
func TestHandlerCloseDuringDividendSyncSessionBuildRecordsNoError(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{})
	entered := make(chan struct{})
	var once sync.Once
	var finished bool
	var mu sync.Mutex
	p.hold = func(ctx context.Context, u string) {
		if !strings.HasSuffix(u, "/") { // the cookie request opens the session
			return
		}
		once.Do(func() { close(entered) })
		<-ctx.Done()
		time.Sleep(60 * time.Millisecond)
		mu.Lock()
		finished = true
		mu.Unlock()
	}
	// SAP and MSFT have no Yahoo symbol yet: the sync searches, which needs a session.
	path := yahooPortfolio(t, h, 7, 2)
	key := startDividendSync(t, handler, path, "", cookies)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the session build never started")
	}
	closed := make(chan struct{})
	go func() { h.Close(); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not return")
	}
	mu.Lock()
	ok := finished
	mu.Unlock()
	if !ok {
		t.Fatal("Close returned while the session build was still running")
	}
	h.dividends.jobs.mu.Lock()
	job := h.dividends.jobs.entries[key].snapshot()
	h.dividends.jobs.mu.Unlock()
	if job.Status != "DONE" || job.Done != 2 {
		t.Fatalf("%+v", job)
	}
	for _, id := range []int64{7, 2} {
		if st, err := h.store.DividendSyncState(context.Background(), id); err != nil || st != nil {
			t.Fatalf("a shutdown was recorded as a failure for %d: %+v %v", id, st, err)
		}
	}
}

// After Close nothing starts: a sync request ends at once, without a goroutine or a
// provider request.
func TestDividendSyncAfterCloseEndsAtOnce(t *testing.T) {
	h, handler, cookies, _, _, _ := dividendSetup(t)
	fake := &fakeDividends{}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	path := yahooPortfolio(t, h, 7, 10)
	h.Close()
	key := startDividendSync(t, handler, path, "", cookies)
	r := pollDividend(context.Background(), handler, cookies, key, "")
	if r.job.Status != "DONE" || r.job.Done != 2 || r.job.Items[0].Status != "ERROR" || len(fake.snapshot()) != 0 {
		t.Fatalf("%+v", r.job)
	}
	// It does not stay "running": the portfolio is free for the next sync.
	h.dividends.jobs.mu.Lock()
	n := len(h.dividends.jobs.running)
	h.dividends.jobs.mu.Unlock()
	if n != 0 {
		t.Fatalf("%d syncs are marked running", n)
	}
}

// gateFX is an ECB time-series provider that answers a request only once `want`
// currencies are being requested, so sequential lookups can never finish.
type gateFX struct {
	inner   *seriesFX
	want    int
	mu2     sync.Mutex
	arrived map[string]bool
	all     chan struct{}
}

func (g *gateFX) Currencies(ctx context.Context) (map[string]bool, error) {
	return g.inner.Currencies(ctx)
}

func (g *gateFX) Rates(ctx context.Context, c, from, to string) ([]marketdata.FXRate, error) {
	g.mu2.Lock()
	if g.arrived == nil {
		g.arrived, g.all = map[string]bool{}, make(chan struct{})
	}
	g.arrived[c] = true
	if len(g.arrived) == g.want {
		close(g.all)
	}
	all := g.all
	g.mu2.Unlock()
	select {
	case <-all:
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(5 * time.Second):
		return nil, context.DeadlineExceeded
	}
	return g.inner.Rates(ctx, c, from, to)
}

// On a cold FX cache the calendar asks for all its currencies at once.
func TestDividendCalendarRequestsTheCurrenciesConcurrently(t *testing.T) {
	h, handler, cookies, _, _, fx := dividendSetup(t)
	ctx := context.Background()
	ulvr, err := h.store.SaveYahooInstrument(ctx, marketdata.Result{YahooSymbol: "ULVR.L", Name: "Unilever PLC", Type: "STOCK"}, "")
	if err != nil {
		t.Fatal(err)
	}
	path := yahooPortfolio(t, h, ulvr.ID, 18, 20) // GBp, USD and EUR listings
	buy(t, h, path, "2026-01-05", ulvr.ID, "100")
	buy(t, h, path, "2026-01-05", 18, "100")
	waitDividendJob(t, handler, startDividendSync(t, handler, path, "", cookies), cookies)
	if fx.count("USD") != 0 || fx.count("GBP") != 0 {
		t.Fatal("the sync must not touch FX", fx.calls)
	}
	gate := &gateFX{inner: fx, want: 2}
	h.fx.Provider = gate
	cal := getCalendar(t, handler, path, 2026, cookies)
	gate.mu2.Lock()
	arrived := len(gate.arrived)
	gate.mu2.Unlock()
	if arrived != 2 {
		t.Fatalf("%d currencies were requested", arrived)
	}
	// Both were converted: neither lookup waited for the other and timed out.
	if e := entry(t, cal, ulvr.ID, "2026-08-06"); val(e.Warning) != "–" || val(e.PerUnitEUR) != "0.4712" {
		t.Fatalf("%+v", e)
	}
	if e := entry(t, cal, 18, "2026-09-17"); val(e.Warning) != "–" || val(e.PerUnitEUR) != "0.4792" {
		t.Fatalf("%+v", e)
	}
}
