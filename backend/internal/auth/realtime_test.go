package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"holdings/backend/internal/marketdata"
)

// fxOutage answers 404 for Yahoo FX pairs and the fixtures otherwise.
type fxOutage struct{ recordingYahoo }

func (f *fxOutage) Get(ctx context.Context, rawURL string) (int, http.Header, []byte, error) {
	if strings.Contains(rawURL, "EUR=X") {
		f.mu.Lock()
		f.calls = append(f.calls, rawURL)
		f.mu.Unlock()
		return 404, http.Header{}, []byte(`{"chart":{"result":null,"error":{"code":"Not Found"}}}`), nil
	}
	return f.recordingYahoo.Get(ctx, rawURL)
}

func realtimeEstimateOf(t *testing.T, handler http.Handler, cookies []*http.Cookie, path, date, ids string) *realtimeEstimate {
	t.Helper()
	w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date="+date+"&instrumentIds="+ids, "", cookies...)
	var est struct{ Realtime *realtimeEstimate }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &est) != nil {
		t.Fatal(w.Code, w.Body.String())
	}
	return est.Realtime
}

func TestRealtimePricesBypassTheDayCacheAndUseLiveFX(t *testing.T) {
	h, handler, cookies, y, _ := yahooSetup(t)
	ctx := context.Background()
	ulvr, err := h.store.SaveYahooInstrument(ctx, marketdata.Result{YahooSymbol: "ULVR.L", Name: "Unilever PLC", Type: "STOCK"}, "")
	if err != nil {
		t.Fatal(err)
	}
	// 2 MSFT (unknown to Yahoo), 7 SAP (EUR), 18 VHYD.L (USD), Unilever (GBp, symbol already linked).
	path := yahooPortfolio(t, h, 2, 7, 18, ulvr.ID)
	ids := fmt.Sprintf("2,7,18,%d", ulvr.ID)
	date := marketdata.BerlinDay(time.Now())

	// Three symbol searches plus four chart requests; no currency is known yet.
	est := realtimeEstimateOf(t, handler, cookies, path, date, ids)
	if est == nil || est.Requests != 7 || est.Remaining <= 0 || len(est.NoSymbol) != 0 {
		t.Fatalf("%+v", est)
	}
	if y.total() != 0 {
		t.Fatal("an estimate must not call Yahoo")
	}

	run := func() priceJob {
		resetJobs(h)
		return waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[%s],"realtime":true}`, date, ids), cookies), cookies)
	}
	job := run()
	msft, sap, vhyd, gbp := job.Items[0], job.Items[1], job.Items[2], job.Items[3]
	if msft.Status != "NO_SYMBOL" || sap.Status != "OK" || vhyd.Status != "OK" || gbp.Status != "OK" {
		t.Fatalf("%+v", job.Items)
	}
	if sap.Price != "210.7500" || sap.Currency != "EUR" || sap.FXRate != "" || sap.Provider != "YAHOO" || sap.QuoteTime.IsZero() {
		t.Fatalf("%+v", sap.Quote)
	}
	// Live FX: USDEUR=X 0.8956 and GBPEUR=X 1.1949; pence are divided by 100.
	if vhyd.Currency != "USD" || vhyd.OrigPrice != "91.7850" || vhyd.Price != "82.2026" || vhyd.FXRate != "0.89560000" || vhyd.FXSource != "YAHOO" {
		t.Fatalf("%+v", vhyd.Quote)
	}
	if gbp.Currency != "GBp" || gbp.OrigPrice != "4930.0000" || gbp.Price != "58.9086" || gbp.FXRate != "1.19490000" || gbp.FXSource != "YAHOO" {
		t.Fatalf("%+v", gbp.Quote)
	}
	// Chart requests only, no v7 quote. The session was built once, for the symbol searches.
	if y.count("range=1d") != 5 || y.count("getcrumb") != 1 || y.count("/v7/finance/quote") != 0 {
		t.Fatalf("%v", y.calls)
	}
	if y.count("/v8/finance/chart/USDEUR=X") != 1 || y.count("/v8/finance/chart/GBPEUR=X") != 1 {
		t.Fatalf("one FX request per currency: %v", y.calls)
	}

	// Pressing it again fetches again: the day cache is ignored, symbols are not.
	before, searches := y.count("range=1d"), y.count("/v1/finance/search")
	if job = run(); job.Items[1].Status != "OK" || y.count("range=1d") != before+5 || y.count("/v1/finance/search") != searches {
		t.Fatalf("%+v %v", job.Items, y.calls)
	}
	// Now currencies are known from the stored quotes.
	if est = realtimeEstimateOf(t, handler, cookies, path, date, ids); est.Requests != 5 || len(est.NoSymbol) != 1 {
		t.Fatalf("%+v", est)
	}

	// The transaction defaults show the market time and the live rate.
	var portfolioID int64
	_, _ = fmt.Sscanf(path, "/api/portfolios/%d", &portfolioID)
	plan, err := h.store.SavingPlan(ctx, "google-user-1", portfolioID)
	if err != nil || len(plan) != 4 {
		t.Fatal(plan, err)
	}
	src := plan[2].PriceSource
	// Market time (Berlin) and FX source survive storage.
	if src != "Fetched 06.11.2026 18:01 · Yahoo Finance real-time · 1 USD = 0,8956 € (Yahoo live)" || plan[2].DefaultPrice != "82.2026" {
		t.Fatal(src, plan[2].DefaultPrice)
	}
}

func TestRealtimeRulesRateLimitAndFXFallback(t *testing.T) {
	h, handler, cookies, y, _ := yahooSetup(t)
	ulvr, err := h.store.SaveYahooInstrument(context.Background(), marketdata.Result{YahooSymbol: "ULVR.L", Name: "Unilever PLC", Type: "STOCK"}, "")
	if err != nil {
		t.Fatal(err)
	}
	path := yahooPortfolio(t, h, 7, 18, ulvr.ID)
	date := marketdata.BerlinDay(time.Now())

	// With the symbol linked, the chart endpoint needs no session: no cookie or crumb requests.
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[%d],"realtime":true}`, date, ulvr.ID), cookies), cookies)
	if job.Items[0].Status != "OK" || y.total() != 2 || y.count("getcrumb") != 0 || y.count("fc.yahoo") != 0 || y.count("crumb=") != 0 {
		t.Fatalf("%+v %v", job.Items, y.calls)
	}
	resetJobs(h)
	y.calls = nil

	// Today only, and only from Yahoo.
	for _, body := range []string{`{"date":"2026-11-03","instrumentIds":[7],"realtime":true}`, fmt.Sprintf(`{"date":%q,"instrumentIds":[7],"realtime":true,"provider":"EULERPOOL"}`, date)} {
		if w := featureRequest(handler, "POST", path+"/price-fetch-jobs", body, cookies...); w.Code != 400 {
			t.Fatal(body, w.Code, w.Body.String())
		}
	}
	if est := realtimeEstimateOf(t, handler, cookies, path, "2026-11-03", "7,18"); est != nil {
		t.Fatalf("past dates have no real-time estimate: %+v", est)
	}
	if w := featureRequest(handler, "POST", path+"/price-fetch-jobs", fmt.Sprintf(`{"date":%q,"instrumentIds":[7],"realtime":true}`, date)); w.Code != 401 {
		t.Fatal(w.Code)
	}

	// Without a live rate the ECB reference rate is used, and marked as such.
	f := &fxOutage{}
	h.yahoo.Transport = f
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[18],"realtime":true}`, date), cookies), cookies)
	if item := job.Items[0]; item.Status != "OK" || item.FXSource != "" || item.FXRateDate == "" || item.Price != "84.7267" || f.count("USDEUR=X") != 1 {
		t.Fatalf("%+v %v", item.Quote, f.calls)
	}

	// A 429 stops the run: the rest is RATE_LIMITED and nothing more is sent.
	h.yahoo.Transport = y
	y.fail = 429
	resetJobs(h)
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[7,18],"realtime":true}`, date), cookies), cookies)
	if job.Items[0].Status != "RATE_LIMITED" || job.Items[1].Status != "RATE_LIMITED" || y.total() != 1 {
		t.Fatalf("%+v %v", job.Items, y.calls)
	}
}
