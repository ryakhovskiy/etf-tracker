package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/marketdata"
)

// fakeClock starts on Saturday 2026-11-07 12:00 Berlin and only moves when told
// to (or when the sync job sleeps).
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func newFakeClock() *fakeClock {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	return &fakeClock{now: time.Date(2026, 11, 7, 12, 0, 0, 0, berlin)}
}
func (c *fakeClock) Now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.now }
func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}
func (c *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	c.sleeps = append(c.sleeps, d)
	c.now = c.now.Add(d)
	c.mu.Unlock()
	return ctx.Err()
}

// seriesFX answers ECB time series (one rate per weekday) and counts requests per currency.
type seriesFX struct {
	mu    sync.Mutex
	calls map[string]int
}

func (f *seriesFX) Currencies(context.Context) (map[string]bool, error) {
	return map[string]bool{"USD": true, "GBP": true}, nil
}
func (f *seriesFX) Rates(_ context.Context, c, from, to string) ([]marketdata.FXRate, error) {
	f.mu.Lock()
	if f.calls == nil {
		f.calls = map[string]int{}
	}
	f.calls[c]++
	f.mu.Unlock()
	rate := map[string]int64{"USD": 92310000, "GBP": 118340000}[c]
	out := []marketdata.FXRate{}
	start, _ := time.Parse("2006-01-02", from)
	end, _ := time.Parse("2006-01-02", to)
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			out = append(out, marketdata.FXRate{Date: d.Format("2006-01-02"), Base: c, Quote: "EUR", Rate: rate})
		}
	}
	return out, nil
}
func (f *seriesFX) count(c string) int { f.mu.Lock(); defer f.mu.Unlock(); return f.calls[c] }

func dividendSetup(t *testing.T) (*Handler, http.Handler, []*http.Cookie, *recordingYahoo, *fakeClock, *seriesFX) {
	t.Helper()
	h, handler, cookies, y, _ := yahooSetup(t)
	clock := newFakeClock()
	fx := &seriesFX{}
	h.dividends.now, h.dividends.sleep = clock.Now, clock.Sleep
	h.fx.Now, h.fx.Provider = clock.Now, fx
	return h, handler, cookies, y, clock, fx
}

func buy(t *testing.T, h *Handler, path, date string, id int64, quantity string) {
	t.Helper()
	var portfolio int64
	fmt.Sscanf(path, "/api/portfolios/%d", &portfolio)
	if _, err := h.store.CreateTransaction(context.Background(), "google-user-1", portfolio, database.TransactionInput{Date: date, Lines: []database.TransactionLine{{InstrumentID: id, Amount: quantity + ".00", Price: "1.0000", Quantity: quantity + ".000000"}}}); err != nil {
		t.Fatal(err)
	}
}

func startDividendSync(t *testing.T, handler http.Handler, path, body string, cookies []*http.Cookie) string {
	t.Helper()
	w := featureRequest(handler, "POST", path+"/dividend-sync", body, cookies...)
	var out struct{ JobID string }
	if err := json.Unmarshal(w.Body.Bytes(), &out); w.Code != 202 || err != nil || out.JobID == "" {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return out.JobID
}

func waitDividendJob(t *testing.T, handler http.Handler, key string, cookies []*http.Cookie) dividendJob {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		w := featureRequest(handler, "GET", "/api/dividend-sync-jobs/"+key, "", cookies...)
		var job dividendJob
		if err := json.Unmarshal(w.Body.Bytes(), &job); w.Code != 200 || err != nil {
			t.Fatalf("poll: %d %s", w.Code, w.Body.String())
		}
		if job.Status == "DONE" {
			return job
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("dividend sync did not finish")
	return dividendJob{}
}

func getCalendar(t *testing.T, handler http.Handler, path string, year int, cookies []*http.Cookie) database.DividendCalendar {
	t.Helper()
	w := featureRequest(handler, "GET", fmt.Sprintf("%s/dividends?year=%d", path, year), "", cookies...)
	var cal database.DividendCalendar
	if err := json.Unmarshal(w.Body.Bytes(), &cal); w.Code != 200 || err != nil {
		t.Fatalf("%d %s", w.Code, w.Body.String())
	}
	return cal
}

func entry(t *testing.T, cal database.DividendCalendar, id int64, exDate string) database.DividendEntry {
	t.Helper()
	for _, m := range cal.Months {
		for _, e := range m.Entries {
			if e.InstrumentID == id && e.ExDate == exDate {
				return e
			}
		}
	}
	t.Fatalf("no entry for %d on %s", id, exDate)
	return database.DividendEntry{}
}

func val(p *string) string {
	if p == nil {
		return "–"
	}
	return *p
}

func TestDividendCalendarSyncsFromYahooFixtures(t *testing.T) {
	h, handler, cookies, y, _, fx := dividendSetup(t)
	// 20 iShares Select Dividend (ISPA.DE, EUR, quarterly), 18 Vanguard High Dividend
	// (VHYD.L, USD, quarterly), 10 iShares Core MSCI World (IWDA.L, accumulating),
	// 7 SAP (SAP.DE, annual), 2 Microsoft (unknown to the Yahoo mock).
	path := yahooPortfolio(t, h, 20, 18, 10, 7, 2)
	buy(t, h, path, "2025-03-03", 20, "100")
	buy(t, h, path, "2026-06-18", 18, "50") // on VHYD's ex-date: not entitled to that payout
	buy(t, h, path, "2026-05-05", 7, "10")  // the day before SAP's ex-date: entitled

	// Before any sync: 12 empty months, everything stale, and nothing was fetched.
	cal := getCalendar(t, handler, path, 2026, cookies)
	if len(cal.Months) != 12 || len(cal.Sync.Stale) != 5 || cal.Sync.LastSyncedAt != nil || cal.Totals.Total != "0.00" || cal.Bounds != (database.DividendBounds{MinYear: 2025, MaxYear: 2027}) || y.total() != 0 {
		t.Fatalf("%+v calls=%d", cal, y.total())
	}

	job := waitDividendJob(t, handler, startDividendSync(t, handler, path, "", cookies), cookies)
	if job.Total != 5 || job.Done != 5 {
		t.Fatalf("%+v", job)
	}
	for _, it := range job.Items {
		want := "OK"
		if it.InstrumentID == 2 {
			want = "ERROR"
		}
		if it.Status != want {
			t.Fatalf("%+v", job.Items)
		}
	}
	// Items follow the instrument ids: Microsoft (2) comes first.
	if job.Items[0].InstrumentID != 2 || job.Items[0].Error != "Yahoo Finance has no listing for this instrument." {
		t.Fatalf("%+v", job.Items[0])
	}
	// One chart request per instrument with a listing, with dividend events.
	if y.count("events=div") != 4 || y.count("/v8/finance/chart/ISPA.DE") != 1 || y.count("/v8/finance/chart/IWDA.L") != 1 {
		t.Fatal(y.calls)
	}

	cal = getCalendar(t, handler, path, 2026, cookies)
	// ISPA: bought long before, 100 units. 0.417165 -> 0.4172 x 100 = 41.72; yield 0.417165 / 39.965 = 1.04 %.
	e := entry(t, cal, 20, "2026-10-15")
	if e.Kind != "ACTUAL" || e.Ticker != "ISPA.DE" || val(e.ISIN) != "DE000A0F5UH1" || e.Units != "100" || val(e.PerUnitEUR) != "0.4172" || val(e.Total) != "41.72" || val(e.YieldPct) != "1.04" || val(e.Close) != "39.9650" || e.Currency != "EUR" || e.FXRate != nil {
		t.Fatalf("%+v", e)
	}
	// VHYD: bought on the 2026-06-18 ex-date -> not held then; held for 2026-09-17 (USD at the ECB rate of that day).
	if e = entry(t, cal, 18, "2026-06-18"); e.Held || val(e.Total) != "0.00" || e.Units != "0" {
		t.Fatalf("%+v", e)
	}
	if e = entry(t, cal, 18, "2026-09-17"); !e.Held || e.Currency != "USD" || val(e.PerUnitEUR) != "0.4792" || val(e.Total) != "23.96" || val(e.FXRateDate) != "2026-09-17" || val(e.FXRate) != "0.92310000" {
		t.Fatalf("%+v", e)
	}
	// Forecast from 2025-12-18, at the latest ECB rate (today is Saturday: Friday's).
	if e = entry(t, cal, 18, "2026-12-18"); e.Kind != "FORECAST" || val(e.BasisExDate) != "2025-12-18" || val(e.FXRateDate) != "2026-11-06" || e.Units != "50" || val(e.CloseDate) != "2026-11-06" {
		t.Fatalf("%+v", e)
	}
	// SAP: bought the day before the ex-date. 2.50 x 10 = 25.00; yield 2.5 / 149.80 = 1.67 %.
	if e = entry(t, cal, 7, "2026-05-06"); val(e.Total) != "25.00" || val(e.YieldPct) != "1.67" {
		t.Fatalf("%+v", e)
	}
	// The accumulating ETF and the unknown stock have no entries.
	for _, m := range cal.Months {
		for _, e := range m.Entries {
			if e.InstrumentID == 10 || e.InstrumentID == 2 {
				t.Fatalf("%+v", e)
			}
		}
	}
	// FX: one time-series request for USD covers every date, also on reload.
	if fx.count("USD") != 1 || fx.count("GBP") != 0 {
		t.Fatal(fx.calls)
	}
	getCalendar(t, handler, path, 2026, cookies)
	if fx.count("USD") != 1 {
		t.Fatal("FX refetched", fx.calls)
	}
	// Sync status: the stock without a listing is the only stale item and reports its error.
	if cal.Sync.LastSyncedAt == nil || len(cal.Sync.Stale) != 1 || cal.Sync.Stale[0] != 2 || len(cal.Sync.Errors) != 1 || cal.Sync.Errors[0].Label != "MSFT" || cal.Sync.Errors[0].LastSyncedAt != nil || cal.Sync.JobID != nil || cal.Sync.Provider != "YAHOO" {
		t.Fatalf("%+v", cal.Sync)
	}
	// received = Σ actual, expected = Σ forecast.
	var received, expected int
	for _, m := range cal.Months {
		for _, e := range m.Entries {
			if e.Kind == "ACTUAL" {
				received++
			} else {
				expected++
			}
		}
	}
	if received != 4+3+1 || expected != 1 || cal.Totals.Expected == "0.00" {
		t.Fatalf("%d %d %+v", received, expected, cal.Totals)
	}

	// 2025: ISPA's January payout came before the purchase (not held); April is held.
	cal = getCalendar(t, handler, path, 2025, cookies)
	if e = entry(t, cal, 20, "2025-01-15"); e.Held || val(e.Total) != "0.00" {
		t.Fatalf("%+v", e)
	}
	if e = entry(t, cal, 20, "2025-04-15"); !e.Held || e.Units != "100" {
		t.Fatalf("%+v", e)
	}
	// 2027: forecasts only, quarterly ISPA repeated from the last 12 months.
	cal = getCalendar(t, handler, path, 2027, cookies)
	if e = entry(t, cal, 20, "2027-07-15"); e.Kind != "FORECAST" || val(e.BasisExDate) != "2026-07-15" || e.PerUnitOrig != "0.674846" {
		t.Fatalf("%+v", e)
	}
	entry(t, cal, 7, "2027-05-06")
	if cal.Totals.Received != "0.00" || cal.Totals.Total != cal.Totals.Expected {
		t.Fatalf("%+v", cal.Totals)
	}

	// Year bounds: first transaction year through next year.
	for _, q := range []string{"2024", "2028", "abc", "20261"} {
		w := featureRequest(handler, "GET", path+"/dividends?year="+q, "", cookies...)
		if w.Code != 400 || !strings.Contains(w.Body.String(), "INVALID_YEAR") {
			t.Fatal(q, w.Code, w.Body.String())
		}
	}
	// Without a year: the current one.
	w := featureRequest(handler, "GET", path+"/dividends", "", cookies...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"year":2026`) {
		t.Fatal(w.Code, w.Body.String())
	}
}

func TestDividendGBXAndUnsupportedCurrency(t *testing.T) {
	h, handler, cookies, _, _, fx := dividendSetup(t)
	ctx := context.Background()
	// A Yahoo instrument listed in pence.
	ulvr, err := h.store.SaveYahooInstrument(ctx, marketdata.Result{YahooSymbol: "ULVR.L", Name: "Unilever PLC", Type: "STOCK"}, "")
	if err != nil {
		t.Fatal(err)
	}
	path := yahooPortfolio(t, h, ulvr.ID, 20)
	buy(t, h, path, "2026-01-05", ulvr.ID, "100")
	waitDividendJob(t, handler, startDividendSync(t, handler, path, "", cookies), cookies)
	cal := getCalendar(t, handler, path, 2026, cookies)
	// 39.82 GBp = 0.3982 GBP x 1.1834 = 0.4712 EUR; x 100 = 47.12. Yield in pence: 39.82 / 4774 = 0.83 %.
	e := entry(t, cal, ulvr.ID, "2026-08-06")
	if e.Currency != "GBp" || e.PerUnitOrig != "39.820000" || val(e.PerUnitEUR) != "0.4712" || val(e.Total) != "47.12" || val(e.YieldPct) != "0.83" {
		t.Fatalf("%+v", e)
	}
	if fx.count("GBP") != 1 {
		t.Fatal(fx.calls)
	}
	// A currency ECB does not publish: "–" with a warning; the EUR listing is unaffected.
	xyz := &marketdata.DividendHistory{Currency: "XYZ", Events: []marketdata.DividendEvent{{ExDate: "2026-08-06", AmountPerShare: "39.82"}}, CloseBefore: map[string]string{}}
	if err = h.store.SaveDividends(ctx, ulvr.ID, xyz, "YAHOO", "2026-08-01", "2026-08-31", time.Now()); err != nil {
		t.Fatal(err)
	}
	cal = getCalendar(t, handler, path, 2026, cookies)
	if e = entry(t, cal, ulvr.ID, "2026-08-06"); e.PerUnitEUR != nil || e.Total != nil || val(e.Warning) != "FX_UNSUPPORTED_CURRENCY" {
		t.Fatalf("%+v", e)
	}
	if e = entry(t, cal, 20, "2026-10-15"); e.Warning != nil || val(e.PerUnitEUR) != "0.4172" {
		t.Fatalf("%+v", e)
	}
}

// fakeDividends records calls and can fail or block them.
type fakeDividends struct {
	mu      sync.Mutex
	clock   *fakeClock
	calls   []string // "id from..to@time"
	at      []time.Time
	fail    map[int64]error
	block   chan struct{}
	elapsed time.Duration // simulated request time
}

func (f *fakeDividends) Dividends(ctx context.Context, inst marketdata.Instrument, from, to time.Time) (*marketdata.DividendHistory, error) {
	f.mu.Lock()
	f.calls = append(f.calls, fmt.Sprintf("%d %s..%s", inst.ID, from.Format("2006-01-02"), to.Format("2006-01-02")))
	f.at = append(f.at, f.clock.Now())
	block, err := f.block, f.fail[inst.ID]
	f.mu.Unlock()
	if block != nil {
		select {
		case <-block:
		case <-ctx.Done(): // a failed test cancels the job on Close
			return nil, ctx.Err()
		}
	}
	f.clock.Advance(f.elapsed)
	if err != nil {
		return nil, err
	}
	return &marketdata.DividendHistory{Currency: "EUR", Events: []marketdata.DividendEvent{{ExDate: "2026-04-15", AmountPerShare: "0.5"}}, CloseBefore: map[string]string{"2026-04-15": "50"}, LatestClose: "51", LatestCloseDate: "2026-11-06"}, nil
}
func (f *fakeDividends) snapshot() ([]string, []time.Time) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.calls...), append([]time.Time(nil), f.at...)
}

func TestDividendSyncSpacingRangesAndErrors(t *testing.T) {
	h, handler, cookies, y, clock, _ := dividendSetup(t)
	fake := &fakeDividends{clock: clock, elapsed: 300 * time.Millisecond}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	path := yahooPortfolio(t, h, 20, 18, 7)

	job := waitDividendJob(t, handler, startDividendSync(t, handler, path, "", cookies), cookies)
	calls, at := fake.snapshot()
	// One instrument at a time, at least one second apart: 300 ms request + 700 ms wait.
	if job.Done != 3 || len(at) != 3 || at[1].Sub(at[0]) != time.Second || at[2].Sub(at[1]) != time.Second || len(clock.sleeps) != 2 || clock.sleeps[0] != 700*time.Millisecond {
		t.Fatalf("%+v %v %v", job, at, clock.sleeps)
	}
	// No transactions: the first sync covers 1 January of last year through today.
	if calls[0] != "7 2025-01-01..2026-11-07" || calls[2] != "20 2025-01-01..2026-11-07" || y.total() != 0 {
		t.Fatal(calls)
	}

	// Nothing is stale now: a STALE sync has nothing to do.
	job = waitDividendJob(t, handler, startDividendSync(t, handler, path, `{"scope":"STALE"}`, cookies), cookies)
	if calls, _ = fake.snapshot(); job.Total != 0 || job.Status != "DONE" || len(calls) != 3 {
		t.Fatalf("%+v", job)
	}
	// Eight days later all are stale; a later sync starts 400 days before the last one.
	clock.Advance(8 * 24 * time.Hour)
	fake.fail = map[int64]error{7: marketdata.ErrYahooLimit}
	job = waitDividendJob(t, handler, startDividendSync(t, handler, path, `{"scope":"STALE"}`, cookies), cookies)
	calls, _ = fake.snapshot()
	if job.Total != 3 || calls[4] != "18 2025-10-03..2026-11-15" {
		t.Fatalf("%+v %v", job, calls)
	}
	// A failure keeps the cached events and is reported with the last good sync.
	if job.Items[0].Status != "ERROR" || job.Items[0].Error != "Yahoo Finance is rate limiting requests. Try again later." || job.Items[1].Status != "OK" {
		t.Fatalf("%+v", job.Items)
	}
	cal := getCalendar(t, handler, path, 2026, cookies)
	if e := entry(t, cal, 7, "2026-04-15"); val(e.PerUnitEUR) != "0.5000" {
		t.Fatalf("%+v", e)
	}
	if len(cal.Sync.Errors) != 1 || cal.Sync.Errors[0].InstrumentID != 7 || cal.Sync.Errors[0].LastSyncedAt == nil || cal.Sync.Errors[0].Message != "Yahoo Finance is rate limiting requests. Try again later." {
		t.Fatalf("%+v", cal.Sync.Errors)
	}
	// SAP's last success is 8 days old: still stale, so the tab queues it again.
	if len(cal.Sync.Stale) != 1 || cal.Sync.Stale[0] != 7 {
		t.Fatal(cal.Sync.Stale)
	}
	// A transaction from 2023 needs data from 2022: every item is stale and starts there.
	fake.fail = nil
	buy(t, h, path, "2023-02-01", 20, "1")
	cal = getCalendar(t, handler, path, 2026, cookies)
	if len(cal.Sync.Stale) != 3 || cal.Bounds.MinYear != 2023 {
		t.Fatalf("%+v", cal.Sync)
	}
	waitDividendJob(t, handler, startDividendSync(t, handler, path, `{"scope":"STALE"}`, cookies), cookies)
	calls, _ = fake.snapshot()
	if strings.Join(calls[len(calls)-3:], ",") != "7 2022-01-01..2026-11-15,18 2022-01-01..2026-11-15,20 2022-01-01..2026-11-15" {
		t.Fatal(calls)
	}
}

func TestDividendSyncRunsOncePerPortfolio(t *testing.T) {
	h, handler, cookies, _, clock, _ := dividendSetup(t)
	fake := &fakeDividends{clock: clock, block: make(chan struct{})}
	h.dividends.provider = func(string) marketdata.DividendProvider { return fake }
	path := yahooPortfolio(t, h, 20, 18)
	key := startDividendSync(t, handler, path, "", cookies)
	w := featureRequest(handler, "POST", path+"/dividend-sync", "", cookies...)
	var conflict struct {
		Error struct{ Code, JobID string }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &conflict)
	if w.Code != 409 || conflict.Error.Code != "SYNC_RUNNING" || conflict.Error.JobID != key {
		t.Fatal(w.Code, w.Body.String())
	}
	// The calendar stays usable and points to the running job.
	if cal := getCalendar(t, handler, path, 2026, cookies); cal.Sync.JobID == nil || *cal.Sync.JobID != key {
		t.Fatalf("%+v", cal.Sync)
	}
	w = featureRequest(handler, "GET", "/api/dividend-sync-jobs/"+key, "", cookies...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":"RUNNING"`) || !strings.Contains(w.Body.String(), `"total":2`) {
		t.Fatal(w.Body.String())
	}
	// Another portfolio can sync at the same time.
	p, err := h.store.SavePortfolio(context.Background(), "google-user-1", 0, "Second")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.AddItems(context.Background(), "google-user-1", p.ID, []int64{7}); err != nil {
		t.Fatal(err)
	}
	otherKey := startDividendSync(t, handler, fmt.Sprintf("/api/portfolios/%d", p.ID), "", cookies)
	close(fake.block)
	waitDividendJob(t, handler, key, cookies)
	waitDividendJob(t, handler, otherKey, cookies)
	// Done: a new sync may start.
	waitDividendJob(t, handler, startDividendSync(t, handler, path, "", cookies), cookies)
	if w = featureRequest(handler, "POST", path+"/dividend-sync", `{"scope":"SOME"}`, cookies...); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestDividendRoutesAuthAndOwnership(t *testing.T) {
	h, handler, cookies, _, _, _ := dividendSetup(t)
	for _, tc := range []struct{ method, path string }{{"GET", "/api/portfolios/1/dividends"}, {"POST", "/api/portfolios/1/dividend-sync"}, {"GET", "/api/dividend-sync-jobs/x"}} {
		if w := featureRequest(handler, tc.method, tc.path, ""); w.Code != 401 {
			t.Fatal(tc, w.Code)
		}
	}
	ctx := context.Background()
	if _, err := h.store.UpsertGoogleUser(ctx, "google-user-2", "other@example.com", "Other", ""); err != nil {
		t.Fatal(err)
	}
	theirs, err := h.store.SavePortfolio(ctx, "google-user-2", 0, "Theirs")
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/portfolios/%d", theirs.ID)
	for _, tc := range []struct{ method, path string }{{"GET", path + "/dividends"}, {"GET", path + "/dividends?year=1900"}, {"POST", path + "/dividend-sync"}, {"GET", "/api/portfolios/999/dividends"}, {"GET", "/api/portfolios/abc/dividends"}} {
		if w := featureRequest(handler, tc.method, tc.path, "", cookies...); w.Code != 404 {
			t.Fatal(tc, w.Code, w.Body.String())
		}
	}
	// Someone else's job does not exist for this user.
	h.dividends.jobs.mu.Lock()
	h.dividends.jobs.entries["theirs"] = &dividendJob{Status: "RUNNING", Items: []dividendJobItem{}, user: "google-user-2", created: h.dividends.now()}
	h.dividends.jobs.mu.Unlock()
	if w := featureRequest(handler, "GET", "/api/dividend-sync-jobs/theirs", "", cookies...); w.Code != 404 {
		t.Fatal(w.Code)
	}
}
