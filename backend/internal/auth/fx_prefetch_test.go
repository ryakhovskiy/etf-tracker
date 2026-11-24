package auth

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

// seedQuote stores an old quote (not fresh) so the instrument's listing currency
// is known without a network call.
func seedQuote(t *testing.T, h *Handler, id int64, currency string) {
	t.Helper()
	q := &marketdata.Quote{InstrumentID: id, OrigPrice: "10.0000", Price: "10.0000", Currency: currency, QuoteDate: "2026-10-01", FetchedAt: time.Now().AddDate(0, 0, -30), Provider: marketdata.ProviderYahoo}
	if err := h.store.SaveQuote(context.Background(), q, ""); err != nil {
		t.Fatal(err)
	}
}

func isFX(u string) bool { return strings.Contains(u, "=X?") }
func isChart(u string) bool {
	return strings.Contains(u, "/v8/finance/chart/") && strings.Contains(u, "range=5d") && !isFX(u)
}

// signal is a one-shot event that can be waited on with a timeout.
type signal struct {
	once sync.Once
	ch   chan struct{}
}

func newSignal() *signal { return &signal{ch: make(chan struct{})} }
func (s *signal) fire()  { s.once.Do(func() { close(s.ch) }) }
func (s *signal) wait(timedOut *atomic.Bool) {
	select {
	case <-s.ch:
	case <-time.After(2 * time.Second):
		timedOut.Store(true)
	}
}

func realtimeBody(ids []int64) string {
	return fmt.Sprintf(`{"date":%q,"instrumentIds":[%s],"realtime":true}`, marketdata.BerlinDay(time.Now()), idList(ids))
}

// FX never waits for a price: both live FX requests (USD and GBP) are sent while
// every chart request is still in flight, once per currency, as soon as the job
// starts, because the currencies come from the stored quotes.
func TestRealtimeFXIsRequestedBeforeAnyPriceResponse(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{"USA1": "VHYD.L", "GBA1": "ULVR.L", "USA2": "VHYD.L"})
	ids := linkedInstruments(t, h, "USA1", "GBA1", "USA2")
	seedQuote(t, h, ids[0], "USD")
	seedQuote(t, h, ids[1], "GBp") // pence: predicted as GBP
	seedQuote(t, h, ids[2], "USD")
	path := yahooPortfolio(t, h, ids...)
	usd, gbp, chartsIn := newSignal(), newSignal(), newSignal()
	var chartWaitTimedOut, fxWaitTimedOut atomic.Bool
	var charts atomic.Int32
	// Each side waits for the other: a chart response is held until both FX requests
	// have been sent, and an FX response until every chart request is in flight. A
	// run that finishes its FX before it starts the charts, or that asks for FX only
	// after a price, cannot get through both gates.
	p.hold = func(ctx context.Context, u string) {
		switch {
		case isFX(u):
			if strings.Contains(u, "USDEUR") {
				usd.fire()
			} else {
				gbp.fire()
			}
			chartsIn.wait(&fxWaitTimedOut)
		case isChart(u):
			if charts.Add(1) == 3 {
				chartsIn.fire()
			}
			usd.wait(&chartWaitTimedOut)
			gbp.wait(&chartWaitTimedOut)
		}
	}
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, realtimeBody(ids), cookies), cookies)
	if chartWaitTimedOut.Load() {
		t.Fatal("a chart request was in flight but the FX requests were not sent: FX waited for a price")
	}
	if fxWaitTimedOut.Load() {
		t.Fatal("the FX requests finished before the chart requests were all in flight: FX and prices are not concurrent")
	}
	for i, it := range job.Items {
		if it.Status != "OK" {
			t.Fatalf("row %d: %+v", i, it)
		}
	}
	if job.Items[0].FXRate != "0.89560000" || job.Items[1].FXRate != "1.19490000" || job.Items[2].FXRate != job.Items[0].FXRate || job.Items[0].FXSource != "YAHOO" {
		t.Fatalf("%+v", job.Items)
	}
	if p.count("USDEUR=X") != 1 || p.count("GBPEUR=X") != 1 || p.total() != 5 {
		t.Fatalf("want 3 charts and one FX request per currency: %v", p.calls)
	}
}

// The prefetched live FX request itself is rate limited while the charts are
// already in flight: the charts keep their own results, the rate falls back to the
// ECB, and the rows share one fallback lookup.
func TestRealtimeFXRateLimitFallsBackToOneSharedEcbLookup(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{"USA1": "VHYD.L", "USA2": "VHYD.L"})
	ids := linkedInstruments(t, h, "USA1", "USA2")
	seedQuote(t, h, ids[0], "USD")
	seedQuote(t, h, ids[1], "USD")
	path := yahooPortfolio(t, h, ids...)
	chartsIn := newSignal()
	var charts atomic.Int32
	var timedOut atomic.Bool
	p.hold = func(ctx context.Context, u string) {
		switch {
		case isChart(u):
			if charts.Add(1) == 2 {
				chartsIn.fire()
			}
		case isFX(u):
			chartsIn.wait(&timedOut) // the 429 lands while both charts are in flight
		}
	}
	p.respond = func(u string) (int, []byte, bool) {
		if isFX(u) {
			return 429, []byte("Edge: Too Many Requests"), true
		}
		return 0, nil, false
	}
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, realtimeBody(ids), cookies), cookies)
	if timedOut.Load() {
		t.Fatal("the charts were never both in flight")
	}
	for i, it := range job.Items {
		if it.Status != "OK" || it.FXSource != "" || it.FXRate != "0.92310000" || it.Currency != "USD" {
			t.Fatalf("row %d must keep its chart result, converted at the ECB rate: %+v", i, it.Quote)
		}
	}
	if p.count("USDEUR=X") != 1 || p.count("range=5d")-p.count("USDEUR=X") != 2 {
		t.Fatalf("%v", p.calls)
	}
	if n := h.fx.Provider.(*fxTestProvider).calls.Load(); n != 1 {
		t.Fatalf("%d ECB fallback lookups for one currency shared by two rows", n)
	}
}

// logBuffer is a goroutine-safe log sink.
type logBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (l *logBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *logBuffer) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

// A currency that cannot be predicted (a title never priced) still gets its live
// rate, on demand and once, and the case is logged; predicted ones are not.
func TestRealtimeUnpredictedCurrencyIsFetchedOnDemandOnce(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{"USA1": "VHYD.L", "GBA1": "ULVR.L", "GBA2": "ULVR.L"})
	logs := &logBuffer{}
	h.logger = slog.New(slog.NewTextHandler(io.MultiWriter(logs), nil))
	ids := linkedInstruments(t, h, "USA1", "GBA1", "GBA2")
	seedQuote(t, h, ids[0], "USD") // only USD is known; the two GBp titles were never priced
	path := yahooPortfolio(t, h, ids...)
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, realtimeBody(ids), cookies), cookies)
	for i, it := range job.Items {
		if it.Status != "OK" {
			t.Fatalf("row %d: %+v", i, it)
		}
	}
	if job.Items[1].FXRate != "1.19490000" || job.Items[2].FXRate != "1.19490000" || job.Items[1].FXSource != "YAHOO" {
		t.Fatalf("%+v", job.Items)
	}
	if p.count("USDEUR=X") != 1 || p.count("GBPEUR=X") != 1 {
		t.Fatalf("one FX request per currency, two rows sharing the unpredicted one: %v", p.calls)
	}
	var onDemand []string
	for _, line := range strings.Split(logs.String(), "\n") {
		if strings.Contains(line, "not predicted") {
			onDemand = append(onDemand, line)
		}
	}
	if len(onDemand) != 1 || !strings.Contains(onDemand[0], "currency=GBP") {
		t.Fatalf("want exactly one on-demand log, for GBP: %q", onDemand)
	}
}

// A prediction that turns out wrong costs one request and is not applied. The
// unused request is held until its context ends: the job must still finish, with
// the EUR price saved unconverted, and the request must be gone when it does.
func TestRealtimeUnusedPredictionDoesNotDelayTheJob(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{"EU1": "SAP.DE"})
	ids := linkedInstruments(t, h, "EU1")
	seedQuote(t, h, ids[0], "GBP") // predicted GBP, but the chart answers EUR
	path := yahooPortfolio(t, h, ids...)
	var fxEnded, timedOut atomic.Bool
	fxIn := newSignal()
	p.hold = func(ctx context.Context, u string) {
		switch {
		case strings.Contains(u, "GBPEUR=X"):
			fxIn.fire()
			<-ctx.Done() // only cancelling the job can end this request
			fxEnded.Store(true)
		case isChart(u):
			fxIn.wait(&timedOut) // the price arrives only once the unused FX request is in flight
		}
	}
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, realtimeBody(ids), cookies), cookies)
	if timedOut.Load() {
		t.Fatal("the predicted FX request was never sent")
	}
	it := job.Items[0]
	if it.Status != "OK" || it.Currency != "EUR" || it.FXRate != "" || it.Price != it.OrigPrice || it.Price != "210.7500" {
		t.Fatalf("the EUR price must be saved without conversion: %+v", it.Quote)
	}
	if p.count("GBPEUR=X") != 1 {
		t.Fatalf("the unused prediction costs exactly one request: %v", p.calls)
	}
	if !fxEnded.Load() {
		t.Fatal("the job finished while its unused FX request was still alive")
	}
}

// The same when the run stops early: a 429 makes the rows skip, and the FX request
// that is still in flight (needed by no row) must not hold the job.
func TestRealtimeEarly429DoesNotWaitForOutstandingFX(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{"RLA": "SAP.DE"})
	ids := linkedInstruments(t, h, "RLA")
	seedQuote(t, h, ids[0], "USD")
	path := yahooPortfolio(t, h, ids...)
	fxIn := newSignal()
	var timedOut, fxEnded atomic.Bool
	p.hold = func(ctx context.Context, u string) {
		switch {
		case isFX(u):
			fxIn.fire()
			<-ctx.Done()
			fxEnded.Store(true)
		case isChart(u):
			fxIn.wait(&timedOut) // the 429 arrives while the FX request is in flight
		}
	}
	p.respond = func(u string) (int, []byte, bool) {
		if isChart(u) {
			return 429, []byte("Edge: Too Many Requests"), true
		}
		return 0, nil, false
	}
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, realtimeBody(ids), cookies), cookies)
	if timedOut.Load() {
		t.Fatal("the FX request was never sent")
	}
	if job.Items[0].Status != "RATE_LIMITED" {
		t.Fatalf("%+v", job.Items[0])
	}
	if !fxEnded.Load() {
		t.Fatal("the job finished while its FX request was still alive")
	}
}

// Close cancels a job whose FX prefetch is in flight and waits for it: the
// prefetch is a goroutine of the job, so nothing may touch the store after Close.
func TestHandlerCloseWaitsForARunningFXPrefetch(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{"CF1": "VHYD.L"})
	audit := &storeAudit{EPStore: h.yahoo.Store}
	h.yahoo.Store = audit
	ids := linkedInstruments(t, h, "CF1")
	seedQuote(t, h, ids[0], "USD") // predicts a live USD rate: a prefetch goroutine starts with the job
	path := yahooPortfolio(t, h, ids...)
	fxIn := newSignal()
	var fxFinished, timedOut atomic.Bool
	p.hold = func(ctx context.Context, u string) {
		switch {
		case isFX(u):
			fxIn.fire()
			<-ctx.Done()
			time.Sleep(60 * time.Millisecond)
			fxFinished.Store(true)
		case isChart(u):
			<-ctx.Done() // the row is stuck as well, so only Close can end the job
		}
	}
	key := startPriceJob(t, handler, path, realtimeBody(ids), cookies)
	fxIn.wait(&timedOut)
	if timedOut.Load() {
		t.Fatal("the FX prefetch never started")
	}
	closed := make(chan struct{})
	go func() { h.Close(); audit.closed.Store(true); close(closed) }()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel the job and its FX prefetch")
	}
	if !fxFinished.Load() {
		t.Fatal("Close returned while the FX prefetch was still running")
	}
	if status := jobStatus(h, key); status != "DONE" {
		t.Fatalf("job status after Close: %s", status)
	}
	time.Sleep(150 * time.Millisecond)
	if n := audit.late.Load(); n != 0 {
		t.Fatalf("%d store accesses after Close returned", n)
	}
}

// blockingECB is the ECB provider with a gate: Rates holds each request until the
// gate opens, and counts requests per currency.
type blockingECB struct {
	mu    sync.Mutex
	calls map[string]int
	gate  func(ctx context.Context, currency string)
	// fail, when set, is returned by every request after the gate.
	fail error
	// failed is fired when a failing request is about to return.
	failed *signal
}

func (b *blockingECB) Currencies(context.Context) (map[string]bool, error) {
	return map[string]bool{"USD": true, "GBP": true}, nil
}
func (b *blockingECB) Rates(ctx context.Context, c, from, to string) ([]marketdata.FXRate, error) {
	b.mu.Lock()
	b.calls[c]++
	gate := b.gate
	b.mu.Unlock()
	if gate != nil {
		gate(ctx, c)
	}
	if b.fail != nil {
		if b.failed != nil {
			b.failed.fire()
		}
		return nil, b.fail
	}
	d, _ := time.Parse("2006-01-02", to)
	return []marketdata.FXRate{{Date: d.AddDate(0, 0, -1).Format("2006-01-02"), Base: c, Quote: "EUR", Rate: 92310000}}, nil
}
func (b *blockingECB) count(c string) int { b.mu.Lock(); defer b.mu.Unlock(); return b.calls[c] }

// ecbOverlap makes an ECB-priced job prove its overlap: every ECB request is held
// until the price request has started, and the price request is held until both
// ECB lookups (USD and GBP) are in flight.
func ecbOverlap(t *testing.T, h *Handler, p *parallelYahoo, priceRequest func(string) bool) (ecb *blockingECB, ecbTimedOut, priceTimedOut *atomic.Bool) {
	t.Helper()
	ecb = &blockingECB{calls: map[string]int{}}
	h.fx.Provider = ecb
	priceStarted := newSignal()
	both := newBarrier(2)
	ecbTimedOut, priceTimedOut = &atomic.Bool{}, &atomic.Bool{}
	ecb.gate = func(ctx context.Context, _ string) {
		priceStarted.wait(ecbTimedOut) // the ECB lookup is concurrent with the price request
		both.wait(ctx)
	}
	p.hold = func(ctx context.Context, u string) {
		if priceRequest(u) {
			priceStarted.fire()
			select { // the price response waits until both ECB lookups are in flight
			case <-both.open:
			case <-time.After(2 * time.Second):
				priceTimedOut.Store(true)
			}
		}
	}
	return ecb, ecbTimedOut, priceTimedOut
}

// Today (batched quote) jobs: the ECB lookups for all expected currencies start at
// job start, concurrently with each other and with the quote request.
func TestTodayJobStartsEcbLookupsWithThePriceRequest(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, nil)
	ids := linkedInstruments(t, h, "VHYD.L", "SAP.DE")
	seedQuote(t, h, ids[0], "USD")
	seedQuote(t, h, ids[1], "GBP") // predicted GBP; the quote answers EUR, so it is unused
	path := yahooPortfolio(t, h, ids...)
	ecb, ecbTimedOut, priceTimedOut := ecbOverlap(t, h, p, func(u string) bool { return strings.Contains(u, "/v7/finance/quote") })
	body := fmt.Sprintf(`{"date":%q,"instrumentIds":[%s],"provider":"YAHOO"}`, marketdata.BerlinDay(time.Now()), idList(ids))
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, body, cookies), cookies)
	if ecbTimedOut.Load() || priceTimedOut.Load() {
		t.Fatalf("ECB lookups did not overlap with the quote request (ecb stuck %v, price stuck %v)", ecbTimedOut.Load(), priceTimedOut.Load())
	}
	if job.Items[0].Status != "OK" || job.Items[1].Status != "OK" || job.Items[0].Currency != "USD" || job.Items[0].FXRate == "" {
		t.Fatalf("%+v %+v", job.Items[0], job.Items[1])
	}
	if ecb.count("USD") != 1 || ecb.count("GBP") != 1 || p.count("/v7/finance/quote") != 1 {
		t.Fatalf("one lookup per currency and one quote request: usd=%d gbp=%d %v", ecb.count("USD"), ecb.count("GBP"), p.calls)
	}
}

// Past-date jobs: the ECB lookups for the date start at job start, alongside the
// History requests, once per currency and date; stored rates and days send nothing.
func TestPastDateJobStartsEcbLookupsWithTheHistoryRequests(t *testing.T) {
	h, handler, cookies, p := parallelSetup(t, map[string]string{"HB1": "SAP.DE"})
	ids := linkedInstruments(t, h, "VHYD.L", "HB1")
	seedQuote(t, h, ids[0], "USD")
	seedQuote(t, h, ids[1], "GBP") // predicted GBP, answered EUR: unused
	path := yahooPortfolio(t, h, ids...)
	ecb, ecbTimedOut, priceTimedOut := ecbOverlap(t, h, p, func(u string) bool { return strings.Contains(u, "period1=") })
	body := fmt.Sprintf(`{"date":"2026-11-06","instrumentIds":[%s],"provider":"YAHOO"}`, idList(ids))
	run := func() priceJob {
		resetJobs(h)
		return waitPriceJob(t, handler, startPriceJob(t, handler, path, body, cookies), cookies)
	}
	job := run()
	if ecbTimedOut.Load() || priceTimedOut.Load() {
		t.Fatalf("ECB lookups did not overlap with the History requests (ecb stuck %v, price stuck %v)", ecbTimedOut.Load(), priceTimedOut.Load())
	}
	if job.Items[0].Status != "OK" || job.Items[0].Currency != "USD" || job.Items[0].FXRate == "" || job.Items[1].Status != "OK" || job.Items[1].FXRate != "" {
		t.Fatalf("%+v %+v", job.Items[0], job.Items[1])
	}
	if ecb.count("USD") != 1 || ecb.count("GBP") != 1 || p.count("period1=") != 2 {
		t.Fatalf("usd=%d gbp=%d history=%d", ecb.count("USD"), ecb.count("GBP"), p.count("period1="))
	}
	// Everything is stored now: the cache is respected, nothing is requested again.
	p.hold, ecb.gate = nil, nil
	before := p.total()
	if job = run(); job.Items[0].Status != "OK" || p.total() != before || ecb.count("USD") != 1 || ecb.count("GBP") != 1 {
		t.Fatalf("cache ignored: usd=%d gbp=%d requests %v", ecb.count("USD"), ecb.count("GBP"), p.calls[before:])
	}
}

// An ECB prefetch that fails before its price arrives is kept for the job: the row
// that needs the rate waits on / reuses that result instead of starting the same
// lookup again after the price.
func TestFailedEcbPrefetchIsNotRepeatedAfterThePrice(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		held       func(string) bool
	}{
		{"today", fmt.Sprintf(`{"date":%q,"instrumentIds":[%%s],"provider":"YAHOO"}`, marketdata.BerlinDay(time.Now())), func(u string) bool { return strings.Contains(u, "/v7/finance/quote") }},
		{"past date", `{"date":"2026-11-06","instrumentIds":[%s],"provider":"YAHOO"}`, func(u string) bool { return strings.Contains(u, "period1=") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, handler, cookies, p := parallelSetup(t, nil)
			ids := linkedInstruments(t, h, "VHYD.L", "VDEM.L")
			seedQuote(t, h, ids[0], "USD")
			seedQuote(t, h, ids[1], "USD")
			path := yahooPortfolio(t, h, ids...)
			ecb := &blockingECB{calls: map[string]int{}, fail: marketdata.ErrUpstream, failed: newSignal()}
			h.fx.Provider = ecb
			var timedOut atomic.Bool
			p.hold = func(ctx context.Context, u string) {
				if tc.held(u) {
					ecb.failed.wait(&timedOut)
					time.Sleep(50 * time.Millisecond) // the failure is stored before the price arrives
				}
			}
			job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(tc.body, idList(ids)), cookies), cookies)
			if timedOut.Load() {
				t.Fatal("the ECB lookup never started before the price")
			}
			for i, it := range job.Items {
				if it.Status == "OK" {
					t.Fatalf("row %d priced although the ECB lookup failed: %+v", i, it.Quote)
				}
			}
			if n := ecb.count("USD"); n != 1 {
				t.Fatalf("%d ECB lookups for one currency and date: the failed prefetch was repeated after the price", n)
			}
		})
	}
}

// An unused ECB prediction is cancelled when the rows are done, in runYahooPrices
// too (today and past-date jobs): the job finishes, with the EUR price unconverted,
// and the held ECB request is gone when it does.
func TestUnusedEcbPredictionDoesNotDelayTodayOrPastDateJobs(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		price      func(string) bool
	}{
		{"today", fmt.Sprintf(`{"date":%q,"instrumentIds":[%%s],"provider":"YAHOO"}`, marketdata.BerlinDay(time.Now())), func(u string) bool { return strings.Contains(u, "/v7/finance/quote") }},
		{"past date", `{"date":"2026-11-06","instrumentIds":[%s],"provider":"YAHOO"}`, func(u string) bool { return strings.Contains(u, "period1=") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, handler, cookies, p := parallelSetup(t, nil)
			ids := linkedInstruments(t, h, "SAP.DE")
			seedQuote(t, h, ids[0], "GBP") // predicted GBP, but SAP.DE is quoted in EUR
			path := yahooPortfolio(t, h, ids...)
			ecb := &blockingECB{calls: map[string]int{}}
			h.fx.Provider = ecb
			gbpIn := newSignal()
			var ecbEnded, timedOut atomic.Bool
			ecb.gate = func(ctx context.Context, _ string) {
				gbpIn.fire()
				<-ctx.Done() // only cancelling the job can end this lookup
				ecbEnded.Store(true)
			}
			p.hold = func(ctx context.Context, u string) {
				if tc.price(u) {
					gbpIn.wait(&timedOut) // the price arrives once the unused lookup is in flight
				}
			}
			job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(tc.body, idList(ids)), cookies), cookies)
			if timedOut.Load() {
				t.Fatal("the predicted ECB lookup never started")
			}
			it := job.Items[0]
			if it.Status != "OK" || it.Currency != "EUR" || it.FXRate != "" || it.Price != it.OrigPrice {
				t.Fatalf("the EUR price must be saved without conversion: %+v", it.Quote)
			}
			if ecb.count("GBP") != 1 {
				t.Fatalf("the unused prediction costs exactly one lookup: %d", ecb.count("GBP"))
			}
			if !ecbEnded.Load() {
				t.Fatal("the job finished while its unused ECB lookup was still alive")
			}
		})
	}
}

// The prediction uses the latest stored quote first, then a stored listing currency.
func TestExpectedCurrenciesPreferTheLatestQuote(t *testing.T) {
	h, _, _, _ := parallelSetup(t, nil)
	ids := linkedInstruments(t, h, "AAA", "BBB", "CCC", "DDD")
	ctx := context.Background()
	seedQuote(t, h, ids[0], "USD")
	if err := h.store.SetEPCurrency(ctx, ids[0], "GBP"); err != nil { // loses against the quote
		t.Fatal(err)
	}
	if err := h.store.SetEPCurrency(ctx, ids[1], "CHF"); err != nil { // no quote: the listing currency
		t.Fatal(err)
	}
	seedQuote(t, h, ids[2], "GBp") // pence use their major currency
	seedQuote(t, h, ids[2], "EUR") // the latest quote wins: EUR needs no FX
	// ids[3]: nothing known.
	rows, err := h.store.SavingPlan(ctx, "google-user-1", mustPortfolio(t, h, ids))
	if err != nil {
		t.Fatal(err)
	}
	got := h.expectedCurrencies(ctx, rows)
	if strings.Join(got, ",") != "CHF,USD" {
		t.Fatalf("%v", got)
	}
}

func mustPortfolio(t *testing.T, h *Handler, ids []int64) int64 {
	t.Helper()
	p, err := h.store.SavePortfolio(context.Background(), "google-user-1", 0, "Predict")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.AddItems(context.Background(), "google-user-1", p.ID, ids); err != nil {
		t.Fatal(err)
	}
	return p.ID
}
