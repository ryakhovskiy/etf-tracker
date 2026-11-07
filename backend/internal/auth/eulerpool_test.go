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

// recordingEP wraps the fixture mock and remembers every request.
type recordingEP struct {
	mu    sync.Mutex
	calls []string
	fail  int // when non-zero, answer with this status
}

func (r *recordingEP) Do(ctx context.Context, method, path string, body []byte) (int, http.Header, []byte, error) {
	r.mu.Lock()
	r.calls = append(r.calls, method+" "+path)
	fail := r.fail
	r.mu.Unlock()
	if fail != 0 {
		return fail, http.Header{}, []byte(`{"error":"x"}`), nil
	}
	return marketdata.MockEP{}.Do(ctx, method, path, body)
}
func (r *recordingEP) count(prefix string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, c := range r.calls {
		if strings.Contains(c, prefix) {
			n++
		}
	}
	return n
}
func (r *recordingEP) total() int { r.mu.Lock(); defer r.mu.Unlock(); return len(r.calls) }

// epSetup returns a Eulerpool-primary handler whose Alpha Vantage transport fails the test when used.
func epSetup(t *testing.T) (*Handler, http.Handler, []*http.Cookie, *recordingEP, *int32) {
	t.Helper()
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	h.primary = marketdata.ProviderEulerpool
	rec := &recordingEP{}
	h.ep.Transport = rec
	var avCalls int32
	h.av.Transport = avTestTransport(func(context.Context, string, string) ([]byte, error) {
		avCalls++
		t.Error("Alpha Vantage was called without an explicit backup request")
		return nil, marketdata.ErrUpstream
	})
	h.fx.Provider = &fxTestProvider{}
	return h, handler, cookies, rec, &avCalls
}

type onlineResult struct {
	Provider string   `json:"provider"`
	Warnings []string `json:"warnings"`
	Results  []struct {
		Source, ISIN, Name, Type string
		ExistingInstrumentID     int64
		InPortfolio              bool
	} `json:"results"`
}

func searchOnline(t *testing.T, handler http.Handler, cookies []*http.Cookie, q string) onlineResult {
	t.Helper()
	w := featureRequest(handler, "GET", "/api/instruments/search/online?q="+strings.ReplaceAll(q, " ", "%20"), "", cookies...)
	if w.Code != 200 {
		t.Fatalf("%s: %d %s", q, w.Code, w.Body.String())
	}
	var out onlineResult
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestOnlineSearchChain(t *testing.T) {
	h, handler, cookies, rec, avCalls := epSetup(t)

	// Eulerpool hit: no OpenFIGI, rows carry the source and merge by ISIN.
	h.figi.Provider = failingFigi{t}
	out := searchOnline(t, handler, cookies, "HSBC MSCI World")
	if out.Provider != "EULERPOOL" || len(out.Results) == 0 || len(out.Results) > 10 || out.Results[0].Source != "EULERPOOL" || out.Results[0].Type != "ETF" || len(out.Warnings) != 0 {
		t.Fatalf("%+v", out)
	}
	seen := map[string]bool{}
	for _, r := range out.Results {
		if seen[r.ISIN] {
			t.Fatal("duplicate ISIN", r.ISIN)
		}
		seen[r.ISIN] = true
	}
	// Existing instruments are matched by ISIN (HSBC is seeded as id 17).
	out = searchOnline(t, handler, cookies, "IE00B4X9L533")
	if len(out.Results) != 1 || out.Results[0].ExistingInstrumentID != 17 {
		t.Fatalf("%+v", out)
	}

	// WKN queries never touch Eulerpool.
	h.figi.Provider = marketdata.Mock{}
	before := rec.total()
	out = searchOnline(t, handler, cookies, "A1C9KK")
	if rec.total() != before || out.Provider != "OPENFIGI" {
		t.Fatalf("WKN went to Eulerpool: %+v", out)
	}

	// Eulerpool empty -> OpenFIGI automatically, no warning, never AV.
	out = searchOnline(t, handler, cookies, "zzzznothing")
	if out.Provider != "OPENFIGI" || len(out.Warnings) != 0 {
		t.Fatalf("%+v", out)
	}

	// Eulerpool failing -> OpenFIGI plus a warning.
	rec.fail = 500
	out = searchOnline(t, handler, cookies, "HSBC MSCI World")
	if out.Provider != "OPENFIGI" || len(out.Warnings) != 1 || out.Warnings[0] != "EULERPOOL_UNAVAILABLE" {
		t.Fatalf("%+v", out)
	}
	// A rejected key disables Eulerpool and says so.
	rec.fail = 401
	out = searchOnline(t, handler, cookies, "HSBC MSCI World")
	rec.fail = 0
	out = searchOnline(t, handler, cookies, "HSBC MSCI World")
	if out.Provider != "OPENFIGI" || out.Warnings[0] != "EULERPOOL_AUTH_ERROR" {
		t.Fatalf("auth failure not sticky: %+v", out)
	}
	if *avCalls != 0 {
		t.Fatal("AV called")
	}
}

type failingFigi struct{ t *testing.T }

func (f failingFigi) Search(context.Context, marketdata.SearchRequest) (marketdata.Response, error) {
	f.t.Error("OpenFIGI called although Eulerpool had results")
	return marketdata.Response{}, marketdata.ErrUpstream
}
func (f failingFigi) Map(context.Context, []marketdata.MappingJob) ([]marketdata.Response, error) {
	f.t.Error("OpenFIGI called although Eulerpool had results")
	return nil, marketdata.ErrUpstream
}

// validISIN completes base (11 chars) with its check digit.
func validISIN(t *testing.T, base string) string {
	t.Helper()
	for d := 0; d < 10; d++ {
		if c := fmt.Sprintf("%s%d", base, d); marketdata.ValidISIN(c) {
			return c
		}
	}
	t.Fatal("no check digit")
	return ""
}

func TestPickingEulerpoolRowCreatesInstrumentAndFetchesProfile(t *testing.T) {
	h, handler, cookies, rec, _ := epSetup(t)
	isin := validISIN(t, "IE00NEWETF1")
	body := fmt.Sprintf(`{"source":"EULERPOOL","isin":%q,"ticker":"NEW.L","name":"New ETF","type":"ETF","currency":"USD","figi":"","exchCode":"","inPortfolio":false}`, isin)
	w := featureRequest(handler, "POST", "/api/instruments", body, cookies...)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var created database.Instrument
	_ = json.Unmarshal(w.Body.Bytes(), &created)
	h.WaitBackground()
	if created.ID == 0 || created.ISIN != isin || rec.count("/etf/profile/"+isin) != 1 {
		t.Fatalf("%+v %v", created, rec.calls)
	}
	// Mock Eulerpool has no profile for it: 404, and nothing is stored.
	if w = featureRequest(handler, "GET", fmt.Sprintf("/api/instruments/%d/etf-profile", created.ID), "", cookies...); w.Code != 404 {
		t.Fatal(w.Code)
	}
	if source, err := h.store.InstrumentSource(context.Background(), created.ID); err != nil || source != "EULERPOOL" {
		t.Fatal(source, err)
	}
	// Re-picking an existing instrument only fills empty fields.
	w = featureRequest(handler, "POST", "/api/instruments", `{"source":"EULERPOOL","isin":"US0378331005","ticker":"X","name":"Renamed","type":"STOCK","figi":"","exchCode":"","inPortfolio":false}`, cookies...)
	var apple database.Instrument
	_ = json.Unmarshal(w.Body.Bytes(), &apple)
	if w.Code != 200 || apple.ID != 1 || apple.Name != "Apple Inc." || apple.Ticker != "AAPL" {
		t.Fatalf("%d %+v", w.Code, apple)
	}
	// A stock never triggers a profile request.
	if rec.count("/etf/profile/US0378331005") != 0 {
		t.Fatal("profile for a stock")
	}
	// Invalid rows are refused.
	if w = featureRequest(handler, "POST", "/api/instruments", `{"source":"EULERPOOL","isin":"BAD","ticker":"X","name":"X","type":"STOCK","figi":"","exchCode":"","inPortfolio":false}`, cookies...); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestETFProfileRefreshCacheAndAUMConversion(t *testing.T) {
	h, handler, cookies, rec, _ := epSetup(t)
	if w := featureRequest(handler, "GET", "/api/instruments/17/etf-profile", "", cookies...); w.Code != 404 {
		t.Fatal("profile before fetch", w.Code)
	}
	w := featureRequest(handler, "POST", "/api/instruments/17/etf-profile/refresh", "", cookies...)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body.String())
	}
	var p map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &p)
	if p["etfCompany"] != "HSBC" || p["expenseRatio"] != "0.15" || p["currency"] != "GBP" || p["trackingIndex"] != "MSCI World Index" || p["inceptionDate"] != "2010-12-08" || p["stale"] != false {
		t.Fatalf("%v", p)
	}
	// 14,335,960,472 GBP at the test rate 0.9231.
	if p["aumEur"] != database.AUMToEUR("14335960472", 92310000) || p["aumEur"] == "" {
		t.Fatal(p["aumEur"])
	}
	if w = featureRequest(handler, "GET", "/api/instruments/17/etf-profile", "", cookies...); w.Code != 200 {
		t.Fatal(w.Code)
	}
	// A background fetch within the TTL is served from the cache.
	h.profileInBackground(17)
	h.WaitBackground()
	if rec.count("/etf/profile/IE00B4X9L533") != 1 {
		t.Fatal("cache ignored", rec.calls)
	}
	// Stale profiles are refetched; refresh always costs one request.
	h.profileTTL = time.Nanosecond
	h.profileInBackground(17)
	h.WaitBackground()
	featureRequest(handler, "POST", "/api/instruments/17/etf-profile/refresh", "", cookies...)
	if rec.count("/etf/profile/IE00B4X9L533") != 3 {
		t.Fatal(rec.calls)
	}
	// Stocks have no profile; unknown ETFs are 404 and ledger rows stay uncounted.
	if w = featureRequest(handler, "POST", "/api/instruments/1/etf-profile/refresh", "", cookies...); w.Code != 400 {
		t.Fatal("stock refresh", w.Code)
	}
	if w = featureRequest(handler, "POST", "/api/instruments/13/etf-profile/refresh", "", cookies...); w.Code != 404 {
		t.Fatal("unknown ETF", w.Code)
	}
	if w = featureRequest(handler, "GET", "/api/instruments/17/etf-profile", ""); w.Code != 401 {
		t.Fatal("profile without session", w.Code)
	}
}

func TestEulerpoolPriceJobAndEstimate(t *testing.T) {
	h, handler, cookies, rec, avCalls := epSetup(t)
	ctx := context.Background()
	user := "google-user-1"
	p, err := h.store.SavePortfolio(ctx, user, 0, "Prices")
	if err != nil {
		t.Fatal(err)
	}
	// 1 AAPL (USD stock), 2 MSFT (unknown to Eulerpool), 7 SAP (EUR stock), 10 iShares ETF (no price series), 17 HSBC ETF.
	if _, err = h.store.AddItems(ctx, user, p.ID, []int64{1, 2, 7, 10, 17}); err != nil {
		t.Fatal(err)
	}
	h.profileInBackground(10) // what the add-items route does for ETFs
	h.profileInBackground(17)
	h.WaitBackground()
	path := fmt.Sprintf("/api/portfolios/%d", p.ID)
	date := marketdata.BerlinDay(time.Now())
	ids := "1,2,7,10,17"
	// Profiles already cached by the background fetch: 1 bulk + 2 ETF series = 3 requests.
	w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date="+date+"&instrumentIds="+ids, "", cookies...)
	var est struct {
		Eulerpool    struct{ Requests, Remaining int }
		Alphavantage struct{ Requests, Remaining int }
	}
	_ = json.Unmarshal(w.Body.Bytes(), &est)
	if w.Code != 200 || est.Eulerpool.Requests != 3 || est.Eulerpool.Remaining <= 0 {
		t.Fatal(w.Code, w.Body.String())
	}
	before := rec.total()
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[%s]}`, date, ids), cookies), cookies)
	for i, want := range []struct{ status, reason string }{{"OK", ""}, {"NEEDS_BACKUP", "NOT_FOUND"}, {"OK", ""}, {"NEEDS_BACKUP", "NOT_FOUND"}, {"OK", ""}} {
		if job.Items[i].Status != want.status || job.Items[i].Reason != want.reason {
			t.Fatalf("item %d: %+v", i, job.Items[i])
		}
	}
	if got := rec.total() - before; got != est.Eulerpool.Requests || rec.count("/market/quotes/bulk") != 1 {
		t.Fatalf("estimate %d != actual %d: %v", est.Eulerpool.Requests, got, rec.calls)
	}
	aapl, sap, hsbc := job.Items[0], job.Items[2], job.Items[4]
	// FX uses the quote's own currency: USD for AAPL, EUR untouched, GBP for the ETF.
	if aapl.Currency != "USD" || aapl.OrigPrice != "349.5100" || aapl.Price != "322.6327" || aapl.Provider != "EULERPOOL" || aapl.FXRateDate == "" {
		t.Fatalf("%+v", aapl.Quote)
	}
	if sap.Currency != "EUR" || sap.Price != "206.0500" || sap.FXRate != "" {
		t.Fatalf("%+v", sap.Quote)
	}
	if hsbc.Currency != "GBP" || hsbc.OrigPrice != "36.6575" || hsbc.Price != "33.8385" || hsbc.QuoteDate != "2026-11-04" {
		t.Fatalf("%+v", hsbc.Quote)
	}
	// The second run reuses fresh quotes: only the two failed items are asked again.
	before = rec.total()
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[%s]}`, date, ids), cookies), cookies)
	for i, want := range []string{"FRESH", "NEEDS_BACKUP", "FRESH", "NEEDS_BACKUP", "FRESH"} {
		if job.Items[i].Status != want {
			t.Fatalf("rerun item %d: %+v", i, job.Items[i])
		}
	}
	if got := rec.total() - before; got != 2 {
		t.Fatalf("rerun sent %d requests: %v", got, rec.calls)
	}
	// Eulerpool quotes are labelled with the provider in the saving plan.
	plan, err := h.store.SavingPlan(ctx, user, p.ID)
	if err != nil || !strings.Contains(plan[0].PriceSource, "Eulerpool") {
		t.Fatalf("%+v %v", plan[0], err)
	}
	// The AV backup is a separate request and never fires on its own.
	if *avCalls != 0 {
		t.Fatal("AV called")
	}
	b, _ := h.store.AVBudget(ctx, user, time.Now(), h.av.Limits)
	if b.UserRemaining != h.av.Limits.User {
		t.Fatal("AV budget spent", b)
	}
	// Explicit backup for an item without an AV symbol reports NO_SYMBOL and costs nothing.
	job = waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[2],"provider":"ALPHAVANTAGE"}`, date), cookies), cookies)
	if job.Items[0].Status != "NO_SYMBOL" {
		t.Fatalf("%+v", job.Items[0])
	}
	if w = featureRequest(handler, "POST", path+"/price-fetch-jobs", fmt.Sprintf(`{"date":%q,"instrumentIds":[2],"provider":"OTHER"}`, date), cookies...); w.Code != 400 {
		t.Fatal(w.Code)
	}
}

func TestEulerpoolLimitEndsAsNeedsBackup(t *testing.T) {
	h, handler, cookies, rec, avCalls := epSetup(t)
	ctx := context.Background()
	user := "google-user-1"
	p, _ := h.store.SavePortfolio(ctx, user, 0, "Limited")
	if _, err := h.store.AddItems(ctx, user, p.ID, []int64{1, 17}); err != nil {
		t.Fatal(err)
	}
	h.profileInBackground(17)
	h.WaitBackground()
	path := fmt.Sprintf("/api/portfolios/%d", p.ID)
	date := marketdata.BerlinDay(time.Now())
	h.ep.SoftDaily = 2 // the profile fetch used one request, one is left
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[1,17]}`, date), cookies), cookies)
	if job.Items[0].Status != "OK" || job.Items[1].Status != "NEEDS_BACKUP" || job.Items[1].Reason != "EULERPOOL_LIMIT_REACHED" {
		t.Fatalf("%+v", job.Items)
	}
	if rec.count("/etf/quotes") != 0 || *avCalls != 0 {
		t.Fatal("call past the budget", rec.calls)
	}
}

func TestEulerpoolBulkBatchesAbove50(t *testing.T) {
	t.Setenv("MAX_ITEMS_PER_PORTFOLIO", "80")
	h, handler, cookies, rec, _ := epSetup(t)
	ctx := context.Background()
	user := "google-user-1"
	p, _ := h.store.SavePortfolio(ctx, user, 0, "Many")
	var ids []int64
	var list []string
	for i := 0; i < 51; i++ {
		inst, err := h.store.SaveEulerpoolInstrument(ctx, marketdata.Result{ISIN: validISIN(t, fmt.Sprintf("DE00%07d", i)), Ticker: fmt.Sprintf("T%d", i), Name: fmt.Sprintf("Stock %d", i), Type: "STOCK"})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, inst.ID)
		list = append(list, fmt.Sprint(inst.ID))
	}
	if _, err := h.store.AddItems(ctx, user, p.ID, ids); err != nil {
		t.Fatal(err)
	}
	path := fmt.Sprintf("/api/portfolios/%d", p.ID)
	date := marketdata.BerlinDay(time.Now())
	body := fmt.Sprintf(`{"date":%q,"instrumentIds":[%s]}`, date, strings.Join(list, ","))
	w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date="+date+"&instrumentIds="+strings.Join(list, ","), "", cookies...)
	if !strings.Contains(w.Body.String(), `"requests":2`) {
		t.Fatal(w.Body.String())
	}
	job := waitPriceJob(t, handler, startPriceJob(t, handler, path, body, cookies), cookies)
	if rec.count("/market/quotes/bulk") != 2 || len(job.Items) != 51 || job.Items[50].Status != "NEEDS_BACKUP" {
		t.Fatalf("batches=%d", rec.count("/market/quotes/bulk"))
	}
}

func TestLinkingISINViaEulerpool(t *testing.T) {
	h, handler, cookies, rec, avCalls := epSetup(t)
	ctx := context.Background()
	inst, err := h.store.SaveOnlineInstrument(ctx, marketdata.Result{FIGI: "BBG000BLNNH6", ExchCode: "GR", Ticker: "SAP", Name: "SAP SE", Type: "STOCK", Currency: "EUR"})
	if err != nil || inst.ISIN != "" {
		t.Fatalf("%+v %v", inst, err)
	}
	path := fmt.Sprintf("/api/instruments/%d", inst.ID)
	w := featureRequest(handler, "GET", path+"/isin/search", "", cookies...)
	var found onlineResult
	_ = json.Unmarshal(w.Body.Bytes(), &found)
	if w.Code != 200 || found.Provider != "EULERPOOL" || len(found.Results) != 1 || found.Results[0].ISIN != "DE0007164600" || found.Results[0].ExistingInstrumentID != 7 || rec.count("/equity/search") != 1 {
		t.Fatal(w.Code, w.Body.String())
	}
	// An ISIN owned by another instrument cannot be taken.
	if w = featureRequest(handler, "PUT", path+"/isin", `{"isin":"DE0007164600"}`, cookies...); w.Code != 409 || !strings.Contains(w.Body.String(), "ISIN_IN_USE") {
		t.Fatal(w.Code, w.Body.String())
	}
	if w = featureRequest(handler, "PUT", path+"/isin", `{"isin":"nope"}`, cookies...); w.Code != 400 {
		t.Fatal(w.Code)
	}
	isin := validISIN(t, "DE00FREE001")
	if w = featureRequest(handler, "PUT", path+"/isin", fmt.Sprintf(`{"isin":%q}`, isin), cookies...); w.Code != 204 {
		t.Fatal(w.Code, w.Body.String())
	}
	if ref, _ := h.store.InstrumentRef(ctx, inst.ID); ref.ISIN != isin {
		t.Fatal(ref)
	}
	if w = featureRequest(handler, "PUT", path+"/isin", fmt.Sprintf(`{"isin":%q}`, validISIN(t, "DE00FREE002")), cookies...); w.Code != 409 {
		t.Fatal("relink allowed", w.Code)
	}
	if w = featureRequest(handler, "GET", path+"/isin/search?q=x", "", cookies...); w.Code != 400 {
		t.Fatal(w.Code)
	}
	if w = featureRequest(handler, "GET", path+"/isin/search", ""); w.Code != 401 {
		t.Fatal(w.Code)
	}
	if *avCalls != 0 {
		t.Fatal("AV called")
	}
}
