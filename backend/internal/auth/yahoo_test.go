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

	"holdings/backend/internal/marketdata"
)

// recordingYahoo wraps the fixture mock and remembers every request.
type recordingYahoo struct {
	mu    sync.Mutex
	calls []string
	fail  int
}

func (r *recordingYahoo) Get(ctx context.Context, rawURL string) (int, http.Header, []byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, rawURL)
	fail := r.fail
	r.mu.Unlock()
	if fail != 0 {
		return fail, http.Header{}, []byte("Edge: Too Many Requests"), nil
	}
	return marketdata.MockYahoo{}.Get(ctx, rawURL)
}
func (r *recordingYahoo) count(part string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if strings.Contains(c, part) {
			n++
		}
	}
	return n
}
func (r *recordingYahoo) total() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.calls) }

func yahooSetup(t *testing.T) (*Handler, http.Handler, []*http.Cookie, *recordingYahoo, *recordingEP) {
	t.Helper()
	h, handler, cookies, ep, _ := epSetup(t)
	y := &recordingYahoo{}
	h.yahoo.Transport = y
	h.yahoo.MinInterval = 0
	return h, handler, cookies, y, ep
}

func yahooPortfolio(t *testing.T, h *Handler, ids ...int64) string {
	t.Helper()
	p, err := h.store.SavePortfolio(context.Background(), "google-user-1", 0, "Yahoo")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.AddItems(context.Background(), "google-user-1", p.ID, ids); err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("/api/portfolios/%d", p.ID)
}

func resetJobs(h *Handler) {
	h.jobs.mu.Lock()
	h.jobs.entries = map[string]*priceJob{}
	h.jobs.mu.Unlock()
}

func TestYahooCurrentPricesBatchedWithEstimateAndNoAutoCalls(t *testing.T) {
	h, handler, cookies, y, ep := yahooSetup(t)
	// 7 SAP (EUR), 18 Vanguard ETF (VHYD.L, USD), 2 MSFT (unknown to both mocks).
	path := yahooPortfolio(t, h, 2, 7, 18)
	date := marketdata.BerlinDay(time.Now())
	w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date="+date+"&instrumentIds=2,7,18&provider=YAHOO", "", cookies...)
	var est struct {
		Provider string
		Yahoo    struct {
			Requests, Remaining int
			Historical          bool
		}
	}
	_ = json.Unmarshal(w.Body.Bytes(), &est)
	// Three symbol searches plus one batched quote request.
	if w.Code != 200 || est.Provider != "YAHOO" || est.Yahoo.Requests != 4 || est.Yahoo.Historical || est.Yahoo.Remaining <= 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	if y.total() != 0 {
		t.Fatal("an estimate must not call Yahoo")
	}

	// A Eulerpool job with failing items never falls back to Yahoo by itself.
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[2]}`, date), cookies), cookies)
	if job.Items[0].Status != "NEEDS_BACKUP" || y.total() != 0 || ep.total() == 0 {
		t.Fatalf("%+v yahoo=%d", job.Items[0], y.total())
	}

	resetJobs(h)
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[2,7,18],"provider":"YAHOO"}`, date), cookies), cookies)
	if job.Items[0].Status != "NO_SYMBOL" || job.Items[1].Status != "OK" || job.Items[2].Status != "OK" {
		t.Fatalf("%+v", job.Items)
	}
	sap, vhyd := job.Items[1], job.Items[2]
	if sap.Provider != "YAHOO" || sap.Currency != "EUR" || sap.Price != "210.7500" || sap.FXRate != "" {
		t.Fatalf("%+v", sap.Quote)
	}
	// FX uses the quote's own currency (USD for this London listing).
	if vhyd.Currency != "USD" || vhyd.OrigPrice != "91.7850" || vhyd.Price != "84.7267" || vhyd.FXRateDate == "" {
		t.Fatalf("%+v", vhyd.Quote)
	}
	if y.count("/v7/finance/quote") != 1 || y.count("/v1/finance/search") != 3 || y.count("getcrumb") != 1 {
		t.Fatalf("%v", y.calls)
	}
	// The crumb is attached to the quote request.
	if !strings.Contains(strings.Join(y.calls, " "), "crumb=mockcrumb1") {
		t.Fatal("quote sent without crumb")
	}
	// Symbols are cached, including "Yahoo does not know MSFT": no further searches.
	before := y.count("/v1/finance/search")
	if ref, _ := h.store.InstrumentRef(context.Background(), 18); ref.YahooSymbol != "VHYD.L" {
		t.Fatalf("%+v", ref)
	}
	resetJobs(h)
	waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[2,18],"provider":"YAHOO"}`, date), cookies), cookies)
	if y.count("/v1/finance/search") != before {
		t.Fatal("symbols were searched again")
	}
	var portfolioID int64
	if _, err := fmt.Sscanf(path, "/api/portfolios/%d", &portfolioID); err != nil {
		t.Fatal(err)
	}
	plan, err := h.store.SavingPlan(context.Background(), "google-user-1", portfolioID)
	if err != nil || len(plan) != 3 || !strings.Contains(plan[2].PriceSource, "Yahoo Finance") {
		t.Fatalf("%+v %v", plan, err)
	}
}

func TestYahooHistoricalDateUsesLastCloseAndIsCached(t *testing.T) {
	h, handler, cookies, y, _ := yahooSetup(t)
	path := yahooPortfolio(t, h, 7)
	fetch := func(date string) priceItem {
		resetJobs(h)
		job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[7],"provider":"YAHOO"}`, date), cookies), cookies)
		return job.Items[0]
	}
	// Tuesday 2026-11-03 has its own close.
	item := fetch("2026-11-03")
	if item.Status != "OK" || item.OrigPrice != "206.6500" || item.QuoteDate != "2026-11-03" || item.Provider != "YAHOO" {
		t.Fatalf("%+v %+v", item, item.Quote)
	}
	if y.count("/v8/finance/chart/SAP.DE") != 1 || y.count("/v7/finance/quote") != 0 {
		t.Fatalf("historical dates use the chart endpoint: %v", y.calls)
	}
	last := y.calls[len(y.calls)-1]
	if !strings.Contains(last, "interval=1d") || !strings.Contains(last, "period1=") || !strings.Contains(last, "crumb=") {
		t.Fatal(last)
	}
	// The same date again is served from stored history.
	item = fetch("2026-11-03")
	if item.Status != "OK" || y.count("/v8/finance/chart/SAP.DE") != 1 {
		t.Fatalf("cache miss: %+v %v", item, y.calls)
	}
	// A Sunday resolves to Friday's close.
	item = fetch("2026-11-01")
	if item.Status != "OK" || item.QuoteDate != "2026-10-30" || item.OrigPrice != "211.2500" {
		t.Fatalf("%+v %+v", item, item.Quote)
	}
	// A date before the series starts has no close.
	if item = fetch("2026-01-02"); item.Status != "NOT_FOUND" {
		t.Fatalf("%+v", item)
	}
	// A historical close is not mistaken for a current price afterwards.
	if q, _ := h.store.FreshQuote(context.Background(), 7, time.Now()); q != nil {
		t.Fatalf("historical quote reused as fresh: %+v", q)
	}
}

func TestYahooDateRulesAndRateLimit(t *testing.T) {
	h, handler, cookies, y, _ := yahooSetup(t)
	path := yahooPortfolio(t, h, 7, 18)
	for _, date := range []string{"1989-12-31", "2999-01-01", "nope"} {
		w := featureRequest(handler, "POST", path+"/price-fetch-jobs", fmt.Sprintf(`{"date":%q,"instrumentIds":[7],"provider":"YAHOO"}`, date), cookies...)
		if w.Code != 400 {
			t.Fatal(date, w.Code)
		}
	}
	// Other providers still refuse historical dates.
	if w := featureRequest(handler, "POST", path+"/price-fetch-jobs", `{"date":"2026-11-03","instrumentIds":[7]}`, cookies...); w.Code != 400 {
		t.Fatal(w.Code)
	}
	w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date=2026-11-03&instrumentIds=7,18&provider=YAHOO", "", cookies...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"historical":true`) || !strings.Contains(w.Body.String(), `"requests":4`) {
		t.Fatal(w.Code, w.Body.String())
	}
	// A 429 stops the whole job: remaining items are RATE_LIMITED and no more calls are made.
	y.fail = 429
	date := marketdata.BerlinDay(time.Now())
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[7,18],"provider":"YAHOO"}`, date), cookies), cookies)
	if job.Items[0].Status != "RATE_LIMITED" || job.Items[1].Status != "RATE_LIMITED" {
		t.Fatalf("%+v", job.Items)
	}
	calls := y.total()
	y.fail = 0
	resetJobs(h)
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[7],"provider":"YAHOO"}`, date), cookies), cookies)
	if y.total() != calls || job.Items[0].Status != "RATE_LIMITED" {
		t.Fatalf("calls during cooldown: %d -> %d %+v", calls, y.total(), job.Items[0])
	}
	w = featureRequest(handler, "GET", "/api/market-data/budget", "", cookies...)
	if !strings.Contains(w.Body.String(), `"yahoo":{"remaining":0,"blockedUntil"`) {
		t.Fatal(w.Body.String())
	}
}

func TestYahooPriceHistoryEndpoint(t *testing.T) {
	h, handler, cookies, y, _ := yahooSetup(t)
	w := featureRequest(handler, "GET", "/api/instruments/18/price-history?from=2026-10-26&to=2026-11-06", "", cookies...)
	var got struct {
		Provider, Symbol, Currency string
		Prices                     []struct{ Date, Close, Currency string }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	// The mock serves one fixture series for VHYD.L (USD).
	if w.Code != 200 || got.Provider != "YAHOO" || got.Symbol != "VHYD.L" || got.Currency != "USD" || len(got.Prices) < 2 || got.Prices[0].Date >= got.Prices[len(got.Prices)-1].Date {
		t.Fatal(w.Code, w.Body.String())
	}
	// Stored for later date-based fetches; the symbol search happened once.
	if p, _ := h.store.HistoryDay(context.Background(), 18, marketdata.ProviderYahoo, got.Prices[0].Date); p == nil || p.Currency != "USD" {
		t.Fatal("history not stored")
	}
	if y.count("/v1/finance/search") != 1 || y.count("/v8/finance/chart/VHYD.L") != 1 {
		t.Fatal(y.calls)
	}
	for _, q := range []string{"from=2026-11-06&to=2026-10-26", "from=1980-01-01", "to=2999-01-01", "from=abc"} {
		if w = featureRequest(handler, "GET", "/api/instruments/18/price-history?"+q, "", cookies...); w.Code != 400 {
			t.Fatal(q, w.Code)
		}
	}
	if w = featureRequest(handler, "GET", "/api/instruments/1/price-history", "", cookies...); w.Code != 404 {
		t.Fatal("instrument unknown to Yahoo:", w.Code)
	}
	if w = featureRequest(handler, "GET", "/api/instruments/9999/price-history", "", cookies...); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w = featureRequest(handler, "GET", "/api/instruments/18/price-history", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	y.fail = 429
	if w = featureRequest(handler, "GET", "/api/instruments/7/price-history", "", cookies...); w.Code != 429 || !strings.Contains(w.Body.String(), "YAHOO_LIMIT_REACHED") {
		t.Fatal(w.Code, w.Body.String())
	}
}
