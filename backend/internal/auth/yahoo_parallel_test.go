package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

// barrier releases its waiters once n of them have arrived, which proves that n
// requests were in flight at the same time. A serial run never gets there and the
// wait times out, which the test reports through timedOut.
type barrier struct {
	n        int32
	arrived  atomic.Int32
	open     chan struct{}
	timedOut atomic.Bool
}

func newBarrier(n int) *barrier { return &barrier{n: int32(n), open: make(chan struct{})} }

func (b *barrier) wait(ctx context.Context) {
	if b.arrived.Add(1) == b.n {
		close(b.open)
	}
	select {
	case <-b.open:
	case <-ctx.Done():
	case <-time.After(2 * time.Second):
		b.timedOut.Store(true)
	}
}

// parallelYahoo serves the recorded fixtures like the mock, under extra symbols:
// alias maps a test symbol to a fixture symbol. It records every request under the
// symbol that was asked for, and lets a test hold or override requests.
type parallelYahoo struct {
	mu    sync.Mutex
	calls []string
	alias map[string]string
	// hold runs before the request is answered; respond may replace the answer.
	hold    func(ctx context.Context, rawURL string)
	respond func(rawURL string) (status int, body []byte, ok bool)
}

func (p *parallelYahoo) Get(ctx context.Context, rawURL string) (int, http.Header, []byte, error) {
	p.mu.Lock()
	p.calls = append(p.calls, rawURL)
	hold, respond := p.hold, p.respond
	p.mu.Unlock()
	if hold != nil {
		hold(ctx, rawURL)
	}
	if err := ctx.Err(); err != nil { // like net/http
		return 0, nil, nil, err
	}
	if respond != nil {
		if status, body, ok := respond(rawURL); ok {
			return status, http.Header{}, body, nil
		}
	}
	for from, to := range p.alias {
		rawURL = strings.Replace(rawURL, "/v8/finance/chart/"+from+"?", "/v8/finance/chart/"+to+"?", 1)
	}
	return marketdata.MockYahoo{}.Get(ctx, rawURL)
}

func (p *parallelYahoo) count(part string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := 0
	for _, c := range p.calls {
		if strings.Contains(c, part) {
			n++
		}
	}
	return n
}

func (p *parallelYahoo) total() int { p.mu.Lock(); defer p.mu.Unlock(); return len(p.calls) }

// parallelSetup is yahooSetup with parallelYahoo as the transport.
func parallelSetup(t *testing.T, alias map[string]string) (*Handler, http.Handler, []*http.Cookie, *parallelYahoo) {
	t.Helper()
	h, handler, cookies, _, _ := yahooSetup(t)
	p := &parallelYahoo{alias: alias}
	h.yahoo.Transport = p
	return h, handler, cookies, p
}

// linkedInstruments stores instruments that already know their Yahoo symbol, so a
// run needs no symbol search, and returns their IDs.
func linkedInstruments(t *testing.T, h *Handler, symbols ...string) []int64 {
	t.Helper()
	var ids []int64
	for _, symbol := range symbols {
		inst, err := h.store.SaveYahooInstrument(context.Background(), marketdata.Result{YahooSymbol: symbol, Name: "Test " + symbol, Type: "STOCK"}, "")
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, inst.ID)
	}
	return ids
}

func idList(ids []int64) string {
	parts := make([]string, len(ids))
	for i, id := range ids {
		parts[i] = fmt.Sprint(id)
	}
	return strings.Join(parts, ",")
}

func isFXRequest(u string) bool { return strings.Contains(u, "=X?") }

// Real-time rows run at the same time and a currency shared by several rows costs
// one live FX request, whichever row asks first.
func TestRealtimeRowsRunConcurrentlyWithOneFXRequestPerCurrency(t *testing.T) {
	// Two USD listings (VHYD.L) and two GBp listings (ULVR.L), none in EUR.
	h, handler, cookies, p := parallelSetup(t, map[string]string{"USA1": "VHYD.L", "USA2": "VHYD.L", "GBA1": "ULVR.L", "GBA2": "ULVR.L"})
	ids := linkedInstruments(t, h, "USA1", "GBA1", "USA2", "GBA2")
	path := yahooPortfolio(t, h, ids...)
	date := marketdata.BerlinDay(time.Now())
	charts := newBarrier(4)
	p.hold = func(ctx context.Context, u string) {
		if strings.Contains(u, "range=5d") && !isFXRequest(u) {
			charts.wait(ctx) // all four chart requests must be in flight together
		}
	}
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[%s],"realtime":true}`, date, idList(ids)), cookies), cookies)
	if charts.timedOut.Load() {
		t.Fatal("the chart requests did not run at the same time")
	}
	for i, it := range job.Items {
		if it.Status != "OK" || it.InstrumentID != ids[i] {
			t.Fatalf("row %d: %+v", i, it)
		}
	}
	// USD rows 0 and 2, GBp rows 1 and 3: the same rate for the same currency.
	if job.Items[0].FXRate != "0.89560000" || job.Items[2].FXRate != job.Items[0].FXRate || job.Items[1].FXRate != "1.19490000" || job.Items[3].FXRate != job.Items[1].FXRate {
		t.Fatalf("%+v", job.Items)
	}
	if p.count("USDEUR=X") != 1 || p.count("GBPEUR=X") != 1 || p.total() != 6 {
		t.Fatalf("want 4 charts and one FX request per currency: %v", p.calls)
	}
}

// A 429 in the middle of a parallel run: the request in flight keeps its result,
// rows that have not started are RATE_LIMITED, and nothing more is sent.
func TestRealtimeParallel429KeepsInFlightResultAndSkipsTheRest(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{"RLA": "SAP.DE", "RLB": "SAP.DE", "RLC": "SAP.DE", "RLD": "SAP.DE"})
	h.yahoo.MaxConcurrency = 2
	ids := linkedInstruments(t, h, "RLA", "RLB", "RLC", "RLD")
	path := yahooPortfolio(t, h, ids...)
	date := marketdata.BerlinDay(time.Now())
	bInFlight := make(chan struct{})
	var once sync.Once
	p.hold = func(ctx context.Context, u string) {
		switch {
		case strings.Contains(u, "/RLB?"):
			once.Do(func() { close(bInFlight) })
			// B answers only after the 429 has started the cool-down.
			deadline := time.Now().Add(5 * time.Second)
			for time.Now().Before(deadline) {
				if b, err := h.yahoo.Budget(ctx); err == nil && b.BlockedUntil != nil {
					return
				}
				time.Sleep(time.Millisecond)
			}
		case strings.Contains(u, "/RLA?"):
			select { // A answers 429 once B is in flight too
			case <-bInFlight:
			case <-time.After(5 * time.Second):
			}
		}
	}
	p.respond = func(u string) (int, []byte, bool) {
		if strings.Contains(u, "/RLA?") {
			return 429, []byte("Edge: Too Many Requests"), true
		}
		return 0, nil, false
	}
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[%s],"realtime":true}`, date, idList(ids)), cookies), cookies)
	got := []string{job.Items[0].Status, job.Items[1].Status, job.Items[2].Status, job.Items[3].Status}
	if strings.Join(got, " ") != "RATE_LIMITED OK RATE_LIMITED RATE_LIMITED" {
		t.Fatalf("%v", got)
	}
	if job.Items[1].Quote == nil || job.Items[1].Price != "210.7500" {
		t.Fatalf("the request in flight lost its result: %+v", job.Items[1])
	}
	if p.total() != 2 {
		t.Fatalf("nothing may be sent after the 429: %v", p.calls)
	}
}

// Past-date fetches run one History request per instrument at the same time, and
// days that are stored send none.
func TestHistoricalFetchRunsInParallelAndSkipsCachedDays(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{"HA1": "SAP.DE", "HA2": "SAP.DE", "HA3": "SAP.DE", "HA4": "SAP.DE"})
	ids := linkedInstruments(t, h, "HA1", "HA2", "HA3", "HA4")
	path := yahooPortfolio(t, h, ids...)
	const date = "2026-11-06"
	run := func(subset []int64) priceJob {
		resetJobs(h)
		return waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[%s],"provider":"YAHOO"}`, date, idList(subset)), cookies), cookies)
	}
	history := func() int { return p.count("period1=") }

	charts := newBarrier(3)
	p.hold = func(ctx context.Context, u string) {
		if strings.Contains(u, "period1=") {
			charts.wait(ctx)
		}
	}
	job := run(ids[:3])
	if charts.timedOut.Load() {
		t.Fatal("the History requests did not run at the same time")
	}
	for i, it := range job.Items {
		if it.Status != "OK" || it.InstrumentID != ids[i] || it.QuoteDate != date || it.OrigPrice != "210.7500" {
			t.Fatalf("row %d: %+v %+v", i, it, it.Quote)
		}
	}
	if history() != 3 {
		t.Fatalf("%d History requests for 3 instruments", history())
	}

	// All three days are stored now: no request at all.
	p.hold = nil
	before := p.total()
	if job = run(ids[:3]); job.Items[0].Status != "OK" || job.Items[2].Status != "OK" || p.total() != before {
		t.Fatalf("cached days sent requests: %v", p.calls[before:])
	}

	// A fourth instrument is new: only its History request is sent.
	job = run(ids)
	for i, it := range job.Items {
		if it.Status != "OK" {
			t.Fatalf("row %d: %+v", i, it)
		}
	}
	if history() != 4 || p.total() != before+1 {
		t.Fatalf("only the uncached instrument should be requested: %v", p.calls[before:])
	}
}

// storeAudit records Yahoo ledger accesses made after the handler was closed.
type storeAudit struct {
	marketdata.EPStore
	closed atomic.Bool
	late   atomic.Int32
}

func (s *storeAudit) touch() {
	if s.closed.Load() {
		s.late.Add(1)
	}
}
func (s *storeAudit) RecordProviderRequest(ctx context.Context, p, u, f string, at time.Time, counted bool) error {
	s.touch()
	return s.EPStore.RecordProviderRequest(ctx, p, u, f, at, counted)
}
func (s *storeAudit) ProviderUsage(ctx context.Context, p string, since time.Time) (int, error) {
	s.touch()
	return s.EPStore.ProviderUsage(ctx, p, since)
}
func (s *storeAudit) ProviderState(ctx context.Context, p, k string) (string, error) {
	s.touch()
	return s.EPStore.ProviderState(ctx, p, k)
}
func (s *storeAudit) SetProviderState(ctx context.Context, p, k, v string) error {
	s.touch()
	return s.EPStore.SetProviderState(ctx, p, k, v)
}

func jobStatus(h *Handler, key string) string {
	h.jobs.mu.Lock()
	defer h.jobs.mu.Unlock()
	return h.jobs.entries[key].Status
}

// closeDuring starts a real-time job on rows that need rows' requests, holds the
// first request matching held until its context ends, closes the handler, and
// checks that Close waited for that work and nothing touched the store after it.
func closeDuring(t *testing.T, held func(rawURL string) bool, ids func(h *Handler) []int64) {
	t.Helper()
	h, handler, cookies, p := parallelSetup(t, map[string]string{"CL1": "SAP.DE"})
	audit := &storeAudit{EPStore: h.yahoo.Store}
	h.yahoo.Store = audit
	rows := ids(h)
	path := yahooPortfolio(t, h, rows...)
	entered := make(chan struct{})
	var finished atomic.Bool
	var once sync.Once
	p.hold = func(ctx context.Context, u string) {
		if !held(u) {
			return
		}
		once.Do(func() { close(entered) })
		<-ctx.Done() // only the shutdown can end this request
		time.Sleep(60 * time.Millisecond)
		finished.Store(true)
	}
	date := marketdata.BerlinDay(time.Now())
	key := startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[%s],"realtime":true}`, date, idList(rows)), cookies)
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the held request never started")
	}

	closed := make(chan struct{})
	go func() { h.Close(); audit.closed.Store(true); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the running job")
	}
	if !finished.Load() {
		t.Fatal("Close returned while a request of the job was still running")
	}
	if status := jobStatus(h, key); status != "DONE" {
		t.Fatalf("job status after Close: %s", status)
	}
	time.Sleep(150 * time.Millisecond) // a straggler would show up now
	if n := audit.late.Load(); n != 0 {
		t.Fatalf("%d store accesses after Close returned", n)
	}
	// New work is refused after Close instead of starting on a closed store.
	job := &priceJob{Status: "QUEUED", Items: []priceItem{{InstrumentID: 1, Status: "PENDING"}}}
	ran := false
	h.startYahooJob(job, func() { ran = true })
	if ran || job.Status != "DONE" || job.Items[0].Status != "ERROR" {
		t.Fatalf("a job was started after Close: ran=%v %+v", ran, job)
	}
}

// Close cancels a job blocked in a chart request and waits for it.
func TestHandlerCloseCancelsAndWaitsForARunningYahooJob(t *testing.T) {
	closeDuring(t, func(u string) bool {
		return strings.Contains(u, "/v8/finance/chart/") && strings.Contains(u, "range=5d")
	},
		func(h *Handler) []int64 { return linkedInstruments(t, h, "CL1") })
}

// Close also stops a job that is blocked building the Yahoo session. That build is
// shared and runs on its own context, so the job ending is not enough: Close must
// wait for the cookie and crumb work too, and nothing may touch the store after.
func TestHandlerCloseWaitsForYahooSessionBuild(t *testing.T) {
	closeDuring(t, func(u string) bool { return strings.HasSuffix(u, "/") },
		func(h *Handler) []int64 { return []int64{7} }) // SAP has no Yahoo symbol yet: the run searches, which needs a session
}

func TestForEachParallelRespectsLimitAndContext(t *testing.T) {
	var running, peak atomic.Int32
	var done atomic.Int32
	forEachParallel(context.Background(), 20, 3, func(int) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(5 * time.Millisecond)
		running.Add(-1)
		done.Add(1)
	}, nil)
	if peak.Load() != 3 || done.Load() != 20 {
		t.Fatalf("peak %d, done %d", peak.Load(), done.Load())
	}

	ctx, cancel := context.WithCancel(context.Background())
	var ran, skipped atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		forEachParallel(ctx, 6, 1, func(int) {
			if ran.Add(1) == 1 {
				close(started)
			}
			<-release
		}, func(int) { skipped.Add(1) })
	}()
	<-started
	cancel() // the first item is running; the dispatcher is waiting for its slot
	time.Sleep(20 * time.Millisecond)
	close(release)
	<-finished
	if ran.Load()+skipped.Load() != 6 || skipped.Load() == 0 {
		t.Fatalf("ran %d, skipped %d: items not started must be skipped", ran.Load(), skipped.Load())
	}
}

func TestFXMemoLoadsOncePerCurrency(t *testing.T) {
	var loads atomic.Int32
	m := &fxMemo{keepErr: true}
	release := make(chan struct{})
	load := func() (*marketdata.FXRate, error) {
		loads.Add(1)
		<-release
		return &marketdata.FXRate{Rate: 5}, nil
	}
	results := make(chan *marketdata.FXRate, 6)
	for range 6 {
		go func() {
			r, _ := m.get(context.Background(), "USD", load)
			results <- r
		}()
	}
	time.Sleep(30 * time.Millisecond)
	close(release)
	for range 6 {
		if r := <-results; r == nil || r.Rate != 5 {
			t.Fatal(r)
		}
	}
	if loads.Load() != 1 {
		t.Fatalf("%d loads for one currency", loads.Load())
	}

	// A failure is kept when asked (real-time: the ECB fallback answer is the result)...
	failing := func() (*marketdata.FXRate, error) { loads.Add(1); return nil, errors.New("down") }
	before := loads.Load()
	keep := &fxMemo{keepErr: true}
	keep.get(context.Background(), "GBP", failing)
	keep.get(context.Background(), "GBP", failing)
	if loads.Load() != before+1 {
		t.Fatal("kept failure was loaded again")
	}
	// ...and retried otherwise (past dates: a failed load may succeed later).
	retry := &fxMemo{}
	retry.get(context.Background(), "GBP", failing)
	retry.get(context.Background(), "GBP", failing)
	if loads.Load() != before+3 {
		t.Fatal("failed load was not retried")
	}

	// A waiter stops waiting when its own context ends.
	slow := &fxMemo{}
	hold := make(chan struct{})
	go slow.get(context.Background(), "CHF", func() (*marketdata.FXRate, error) { <-hold; return nil, nil })
	time.Sleep(20 * time.Millisecond)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	if _, err := slow.get(ctx, "CHF", failing); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	close(hold)
}
