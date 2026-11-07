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

// yahooPrimary returns a handler whose primary provider is Yahoo, with recording
// Yahoo and Eulerpool mocks and an Alpha Vantage transport that fails the test.
func yahooPrimary(t *testing.T) (*Handler, http.Handler, []*http.Cookie, *recordingYahoo, *recordingEP, *int32) {
	t.Helper()
	h, handler, cookies, ep, avCalls := epSetup(t)
	y := &recordingYahoo{}
	h.yahoo.Transport = y
	h.yahoo.MinInterval = 0
	h.primary = marketdata.ProviderYahoo
	return h, handler, cookies, y, ep, avCalls
}

type yahooSearchResult struct {
	Provider string   `json:"provider"`
	Warnings []string `json:"warnings"`
	Results  []struct {
		Source, YahooSymbol, ISIN, Name, Type, ExchangeName, Sector string
		ExistingInstrumentID                                        int64
	} `json:"results"`
}

func searchYahoo(t *testing.T, handler http.Handler, cookies []*http.Cookie, q string) yahooSearchResult {
	t.Helper()
	w := featureRequest(handler, "GET", "/api/instruments/search/online?q="+strings.ReplaceAll(q, " ", "%20"), "", cookies...)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
	}
	var out yahooSearchResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSearchPrefersYahooThenEulerpoolThenOpenFIGI(t *testing.T) {
	h, handler, cookies, y, ep, avCalls := yahooPrimary(t)
	h.figi.Provider = failingFigi{t}

	// A name query: Yahoo answers, with exchange and sector, and nothing else is called.
	out := searchYahoo(t, handler, cookies, "SAP SE")
	if out.Provider != "YAHOO" || len(out.Results) != 1 || len(out.Warnings) != 0 {
		t.Fatalf("%+v", out)
	}
	row := out.Results[0]
	if row.Source != "YAHOO" || row.YahooSymbol != "SAP.DE" || row.Type != "STOCK" || row.ExchangeName != "XETRA" || row.Sector != "Technology" || row.ISIN != "" {
		t.Fatalf("%+v", row)
	}
	if ep.total() != 0 {
		t.Fatal("Eulerpool called although Yahoo had results")
	}
	// An ISIN query makes the ISIN of the row known, and an existing instrument is recognised.
	out = searchYahoo(t, handler, cookies, "IE00B8GKDB10")
	if len(out.Results) != 1 || out.Results[0].YahooSymbol != "VHYD.L" || out.Results[0].ISIN != "IE00B8GKDB10" || out.Results[0].ExistingInstrumentID != 18 || out.Results[0].Type != "ETF" {
		t.Fatalf("%+v", out)
	}

	// WKN queries cannot go to Yahoo or Eulerpool: straight to OpenFIGI.
	h.figi.Provider = marketdata.Mock{}
	before, epBefore := y.total(), ep.total()
	out = searchYahoo(t, handler, cookies, "A1C9KK")
	if out.Provider != "OPENFIGI" || y.total() != before || ep.total() != epBefore {
		t.Fatalf("WKN query reached Yahoo/Eulerpool: %+v", out)
	}

	// Yahoo empty -> Eulerpool (also empty) -> OpenFIGI, without warnings and never Alpha Vantage.
	out = searchYahoo(t, handler, cookies, "zzzz nothing")
	if out.Provider != "OPENFIGI" || len(out.Warnings) != 0 || ep.count("/equity/search") != 1 {
		t.Fatalf("%+v", out)
	}

	// Yahoo rate limited -> Eulerpool answers, with a warning. The cooldown keeps Yahoo quiet afterwards.
	y.fail = 429
	out = searchYahoo(t, handler, cookies, "HSBC MSCI World")
	if out.Provider != "EULERPOOL" || len(out.Warnings) != 1 || out.Warnings[0] != "YAHOO_LIMIT_REACHED" || len(out.Results) == 0 {
		t.Fatalf("%+v", out)
	}
	y.fail = 0
	calls := y.total()
	out = searchYahoo(t, handler, cookies, "HSBC MSCI World")
	if out.Provider != "EULERPOOL" || out.Warnings[0] != "YAHOO_LIMIT_REACHED" || y.total() != calls {
		t.Fatalf("cooldown not honoured: %+v", out)
	}
	if *avCalls != 0 {
		t.Fatal("Alpha Vantage called")
	}
}

func pickBody(symbol, name, typ string) string {
	return fmt.Sprintf(`{"source":"YAHOO","yahooSymbol":%q,"ticker":%q,"name":%q,"type":%q,"figi":"","exchCode":"","inPortfolio":false}`, symbol, symbol, name, typ)
}

func TestPickingYahooRowFindsISINAndReusesInstruments(t *testing.T) {
	h, handler, cookies, _, ep, _ := yahooPrimary(t)
	ctx := context.Background()
	post := func(body string) (int, struct {
		ID   int64
		ISIN string
		Name string
	}) {
		w := featureRequest(handler, "POST", "/api/instruments", body, cookies...)
		var inst struct {
			ID   int64
			ISIN string
			Name string
		}
		_ = json.Unmarshal(w.Body.Bytes(), &inst)
		return w.Code, inst
	}

	// SAP is already in the database (id 7): the ISIN found via Eulerpool leads to it.
	code, inst := post(pickBody("SAP.DE", "SAP SE", "STOCK"))
	if code != 200 || inst.ID != 7 || inst.ISIN != "DE0007164600" || ep.count("/equity/search") != 1 {
		t.Fatal(code, inst, ep.calls)
	}
	if ref, _ := h.store.InstrumentRef(ctx, 7); ref.YahooSymbol != "SAP.DE" {
		t.Fatalf("%+v", ref)
	}
	// Picking it again is recognised by symbol: no second lookup.
	if code, inst = post(pickBody("SAP.DE", "SAP SE", "STOCK")); code != 200 || inst.ID != 7 || ep.count("/equity/search") != 1 {
		t.Fatal(code, inst, ep.calls)
	}
	// An ETF whose name clearly matches: linked to the existing instrument and its profile is fetched.
	code, inst = post(pickBody("VHYD.L", "Vanguard FTSE All-World High Dividend Yield UCITS ETF USD Distributing", "ETF"))
	h.WaitBackground()
	if code != 200 || inst.ID != 18 || inst.ISIN != "IE00B8GKDB10" || ep.count("/etf/profile/IE00B8GKDB10") != 1 {
		t.Fatal(code, inst, ep.calls)
	}
	// Nothing found: the instrument is created without an ISIN, identified by its symbol.
	code, inst = post(pickBody("ZZZ.L", "Zzz Totally Unknown Fund", "ETF"))
	h.WaitBackground()
	if code != 200 || inst.ID < 22 || inst.ISIN != "" {
		t.Fatal(code, inst)
	}
	if src, _ := h.store.InstrumentSource(ctx, inst.ID); src != "YAHOO" {
		t.Fatal(src)
	}
	if ep.count("/etf/profile/") != 1 {
		t.Fatal("profile requested for an instrument without ISIN", ep.calls)
	}
	// A Eulerpool outage never blocks picking: the instrument just stays without an ISIN.
	ep.fail = 500
	code, inst = post(pickBody("YYY.DE", "Yyy AG", "STOCK"))
	ep.fail = 0
	if code != 200 || inst.ID == 0 || inst.ISIN != "" {
		t.Fatal(code, inst)
	}
	// Invalid rows are refused.
	if w := featureRequest(handler, "POST", "/api/instruments", pickBody("bad symbol", "X", "STOCK"), cookies...); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestLinkingYahooSymbolFromTheTransactionsPage(t *testing.T) {
	h, handler, cookies, _, ep, _ := yahooPrimary(t)
	ctx := context.Background()
	inst, err := h.store.SaveOnlineInstrument(ctx, marketdata.Result{FIGI: "BBG000BLNNH6", ExchCode: "GR", Ticker: "SAP", Name: "SAP SE", Type: "STOCK", Currency: "EUR"})
	if err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/instruments/%d/yahoo-symbol", inst.ID)
	var found yahooSearchResult
	w := featureRequest(handler, "GET", path+"/search", "", cookies...)
	_ = json.Unmarshal(w.Body.Bytes(), &found)
	if w.Code != 200 || found.Provider != "YAHOO" || len(found.Results) != 1 || found.Results[0].YahooSymbol != "SAP.DE" || found.Results[0].ExistingInstrumentID != 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	w = featureRequest(handler, "PUT", path, `{"symbol":"sap.de"}`, cookies...)
	var linked struct{ Symbol, ISIN string }
	_ = json.Unmarshal(w.Body.Bytes(), &linked)
	// The ISIN lookup finds DE0007164600, which belongs to instrument 7, so it is not taken.
	if w.Code != 200 || linked.Symbol != "SAP.DE" || linked.ISIN != "" || ep.count("/equity/search") != 1 {
		t.Fatal(w.Code, w.Body.String(), ep.calls)
	}
	if ref, _ := h.store.InstrumentRef(ctx, inst.ID); ref.YahooSymbol != "SAP.DE" {
		t.Fatalf("%+v", ref)
	}
	// The linked instrument is now recognised in search results, other instruments cannot take the symbol.
	w = featureRequest(handler, "GET", path+"/search", "", cookies...)
	_ = json.Unmarshal(w.Body.Bytes(), &found)
	if found.Results[0].ExistingInstrumentID != inst.ID {
		t.Fatalf("%+v", found)
	}
	if w = featureRequest(handler, "PUT", "/api/instruments/8/yahoo-symbol", `{"symbol":"SAP.DE"}`, cookies...); w.Code != 409 || !strings.Contains(w.Body.String(), "SYMBOL_IN_USE") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = featureRequest(handler, "PUT", path, `{"symbol":"not valid"}`, cookies...); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = featureRequest(handler, "PUT", "/api/instruments/9999/yahoo-symbol", `{"symbol":"X.DE"}`, cookies...); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if w = featureRequest(handler, "GET", path+"/search?q=x", "", cookies...); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = featureRequest(handler, "GET", path+"/search", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
}

func TestYahooIsTheDefaultProviderForCurrentAndHistoricalPrices(t *testing.T) {
	h, handler, cookies, y, ep, avCalls := yahooPrimary(t)
	path := yahooPortfolio(t, h, 7, 18)
	date := marketdata.BerlinDay(time.Now())
	w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date="+date+"&instrumentIds=7,18", "", cookies...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"provider":"YAHOO"`) {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = featureRequest(handler, "GET", "/api/market-data/budget", "", cookies...); !strings.Contains(w.Body.String(), `"primary":"YAHOO"`) {
		t.Fatal(w.Body.String())
	}
	// No provider in the request: Yahoo, today's date -> one batched quote request.
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[7,18]}`, date), cookies), cookies)
	if job.Items[0].Status != "OK" || job.Items[0].Provider != "YAHOO" || job.Items[1].Status != "OK" || y.count("/v7/finance/quote") != 1 {
		t.Fatalf("%+v %v", job.Items, y.calls)
	}
	// A past date is just as valid for the default provider: closes from the chart endpoint.
	resetJobs(h)
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, `{"date":"2026-11-03","instrumentIds":[7]}`, cookies), cookies)
	if job.Items[0].Status != "OK" || job.Items[0].OrigPrice != "206.6500" || y.count("/v8/finance/chart/SAP.DE") != 1 {
		t.Fatalf("%+v", job.Items[0])
	}
	if ep.total() != 0 || *avCalls != 0 {
		t.Fatalf("a fallback provider was called without a click: eulerpool=%d av=%d", ep.total(), *avCalls)
	}
	// Failed items never trigger a fallback by themselves.
	y.fail = 429
	resetJobs(h)
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[18]}`, "2026-11-02"), cookies), cookies)
	if job.Items[0].Status != "RATE_LIMITED" || ep.total() != 0 || *avCalls != 0 {
		t.Fatalf("%+v eulerpool=%d", job.Items[0], ep.total())
	}
}

func TestEulerpoolFallbackPricesPastDatesFromItsHistory(t *testing.T) {
	h, handler, cookies, y, ep, _ := yahooPrimary(t)
	path := yahooPortfolio(t, h, 7, 17) // SAP (stock) and HSBC MSCI World (ETF)
	body := `{"date":"2026-11-03","instrumentIds":[7,17],"provider":"EULERPOOL"}`
	w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date=2026-11-03&instrumentIds=7,17&provider=EULERPOOL", "", cookies...)
	var est struct{ Eulerpool struct{ Requests int } }
	_ = json.Unmarshal(w.Body.Bytes(), &est)
	// Per item: the history plus its currency (equity profile / ETF profile).
	if w.Code != 200 || est.Eulerpool.Requests != 4 {
		t.Fatal(w.Code, w.Body.String())
	}
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, body, cookies), cookies)
	sap, hsbc := job.Items[0], job.Items[1]
	if sap.Status != "OK" || sap.Provider != "EULERPOOL" || sap.Currency != "EUR" || sap.OrigPrice != "206.0500" || sap.QuoteDate != "2026-11-03" {
		t.Fatalf("%+v %+v", sap, sap.Quote)
	}
	if hsbc.Status != "OK" || hsbc.Currency != "GBP" || hsbc.OrigPrice != "36.2625" || hsbc.Price != "33.4739" || hsbc.QuoteDate != "2026-11-03" {
		t.Fatalf("%+v %+v", hsbc, hsbc.Quote)
	}
	if ep.total() != est.Eulerpool.Requests || ep.count("/market/quotes/bulk") != 0 {
		t.Fatalf("estimate %d vs %d: %v", est.Eulerpool.Requests, ep.total(), ep.calls)
	}
	if !strings.Contains(strings.Join(ep.calls, " "), "enddate=") || ep.count("/equity/quotes/DE0007164600") != 1 || ep.count("/etf/quotes/IE00B4X9L533") != 1 {
		t.Fatal(ep.calls)
	}
	// The stock currency is cached: a second run only fetches the two histories.
	before := ep.total()
	resetJobs(h)
	waitPriceJob(t, handler, startPriceJob(t, handler, path, body, cookies), cookies)
	if ep.total()-before != 2 || ep.count("/equity/profile/") != 1 {
		t.Fatalf("%v", ep.calls)
	}
	// A weekend resolves to the previous trading day.
	resetJobs(h)
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, `{"date":"2026-11-01","instrumentIds":[7],"provider":"EULERPOOL"}`, cookies), cookies)
	if job.Items[0].Status != "OK" || job.Items[0].QuoteDate != "2026-10-30" || job.Items[0].OrigPrice != "211.2500" {
		t.Fatalf("%+v %+v", job.Items[0], job.Items[0].Quote)
	}
	// Before the series starts: needs another source, with the reason.
	resetJobs(h)
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, `{"date":"2026-01-02","instrumentIds":[7],"provider":"EULERPOOL"}`, cookies), cookies)
	if job.Items[0].Status != "NEEDS_BACKUP" || job.Items[0].Reason != "EMPTY_SERIES" {
		t.Fatalf("%+v", job.Items[0])
	}
	if y.total() != 0 {
		t.Fatal("Yahoo called by a Eulerpool job")
	}
}

func TestAlphaVantageFallbackPricesPastDatesFromDailySeries(t *testing.T) {
	h, handler, cookies, y, _, _ := yahooPrimary(t)
	ctx := context.Background()
	user := "google-user-1"
	h.av.Transport = marketdata.MockAV{}
	h.av.Limits.Minute = 1000000
	path := yahooPortfolio(t, h, 3)
	if err := h.store.LinkSymbol(ctx, user, 3, "IBM", "USD", "US"); err != nil {
		t.Fatal(err)
	}
	// A current price first: it must not be reused for a past date.
	date := marketdata.BerlinDay(time.Now())
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[3],"provider":"ALPHAVANTAGE"}`, date), cookies), cookies)
	if job.Items[0].Status != "OK" {
		t.Fatalf("%+v", job.Items[0])
	}
	fetch := func(day string) priceItem {
		resetJobs(h)
		return waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[3],"provider":"ALPHAVANTAGE"}`, day), cookies), cookies).Items[0]
	}
	item := fetch("2026-11-03")
	if item.Status != "OK" || item.OrigPrice != "250.0400" || item.QuoteDate != "2026-11-03" || item.Currency != "USD" || item.FXRateDate == "" {
		t.Fatalf("%+v %+v", item, item.Quote)
	}
	if item = fetch("2026-11-01"); item.Status != "OK" || item.QuoteDate != "2026-10-30" || item.OrigPrice != "257.8100" {
		t.Fatalf("weekend: %+v %+v", item, item.Quote)
	}
	// Older than the compact series (about 100 trading days): clearly not found.
	if item = fetch("2026-01-02"); item.Status != "NOT_FOUND" {
		t.Fatalf("%+v", item)
	}
	// Each past-date fetch costs one AV request; estimates say so and do not reuse today's quote.
	w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date=2026-11-03&instrumentIds=3&provider=ALPHAVANTAGE", "", cookies...)
	if !strings.Contains(w.Body.String(), `"requestsNeeded":1`) {
		t.Fatal(w.Body.String())
	}
	if y.total() != 0 {
		t.Fatal("Yahoo called by an Alpha Vantage job")
	}
}
