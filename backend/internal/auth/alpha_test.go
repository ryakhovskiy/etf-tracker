package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/marketdata"
)

type avTestTransport func(context.Context, string, string) ([]byte, error)

func (f avTestTransport) Call(ctx context.Context, fn, q string) ([]byte, error) {
	return f(ctx, fn, q)
}

type fxTestProvider struct {
	calls   atomic.Int32
	missing bool
}

func (f *fxTestProvider) Currencies(context.Context) (map[string]bool, error) {
	return map[string]bool{"USD": true, "GBP": true}, nil
}
func (f *fxTestProvider) Rates(ctx context.Context, c, from, to string) ([]marketdata.FXRate, error) {
	f.calls.Add(1)
	if f.missing {
		return nil, marketdata.ErrFXUnavailable
	}
	d, _ := time.Parse("2006-01-02", to)
	return []marketdata.FXRate{{Date: d.AddDate(0, 0, -1).Format("2006-01-02"), Base: c, Quote: "EUR", Rate: 92310000}}, nil
}
func waitPriceJob(t *testing.T, h http.Handler, key string, cookies []*http.Cookie) priceJob {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		w := featureRequest(h, "GET", "/api/price-fetch-jobs/"+key, "", cookies...)
		if w.Code != 200 {
			t.Fatalf("poll: %d %s", w.Code, w.Body.String())
		}
		var job priceJob
		if err := json.Unmarshal(w.Body.Bytes(), &job); err != nil {
			t.Fatal(err)
		}
		if job.Status == "DONE" {
			return job
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("job did not finish")
	return priceJob{}
}
func startPriceJob(t *testing.T, h http.Handler, path, body string, cookies []*http.Cookie) string {
	t.Helper()
	w := featureRequest(h, "POST", path+"/price-fetch-jobs", body, cookies...)
	if w.Code != 202 {
		t.Fatalf("start: %d %s", w.Code, w.Body.String())
	}
	var result struct {
		ID string `json:"jobId"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.ID
}
func TestAlphaJobsEstimateFXAndOwnership(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	ctx := context.Background()
	user := "google-user-1"
	h.av.Limits.Minute = 1000000
	p, err := h.store.SavePortfolio(ctx, user, 0, "Prices")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.AddItems(ctx, user, p.ID, []int64{1, 2, 3, 4, 5}); err != nil {
		t.Fatal(err)
	}
	for _, r := range []struct {
		id   int64
		s, c string
	}{{1, "APPLE", "USD"}, {2, "MICROSOFT", "USD"}, {3, "SAP.DEX", "EUR"}, {4, "BAD", "BTC"}} {
		if err = h.store.LinkSymbol(ctx, user, r.id, r.s, r.c, "Germany"); err != nil {
			t.Fatal(err)
		}
	}
	fx := &fxTestProvider{}
	h.fx.Provider = fx
	path := fmt.Sprintf("/api/portfolios/%d", p.ID)
	date := marketdata.BerlinDay(time.Now())
	body := fmt.Sprintf(`{"date":%q,"instrumentIds":[1,2,3,4,5]}`, date)
	w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date="+date+"&instrumentIds=1,2,3,4,5", "", cookies...)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"requestsNeeded":4`) {
		t.Fatal(w.Code, w.Body.String())
	}
	b, _ := h.store.AVBudget(ctx, user, time.Now(), h.av.Limits)
	if b.UserRemaining != 10 {
		t.Fatal("estimate made AV calls", b)
	}
	key := startPriceJob(t, handler, path, body, cookies)
	job := waitPriceJob(t, handler, key, cookies)
	for i, want := range []string{"OK", "OK", "OK", "FX_UNSUPPORTED_CURRENCY", "NO_SYMBOL"} {
		if job.Items[i].Status != want {
			t.Fatalf("item %d: %+v", i, job.Items[i])
		}
	}
	if fx.calls.Load() != 1 {
		t.Fatal("FX not grouped", fx.calls.Load())
	}
	if job.Items[0].FXRateDate == "" || job.Items[0].ID == 0 {
		t.Fatalf("missing quote provenance %+v", job.Items[0])
	}
	b, _ = h.store.AVBudget(ctx, user, time.Now(), h.av.Limits)
	if b.UserRemaining != 7 {
		t.Fatal(b)
	}
	other, err := h.store.UpsertGoogleUser(ctx, "other", "other@example.com", "", "")
	if err != nil {
		t.Fatal(err)
	}
	rec := httptest.NewRecorder()
	if err = h.tokens.issue(ctx, rec, other.ID); err != nil {
		t.Fatal(err)
	}
	if w = featureRequest(handler, "GET", "/api/price-fetch-jobs/"+key, "", rec.Result().Cookies()...); w.Code != 404 {
		t.Fatal("foreign job exposed", w.Code)
	}
	// A new in-memory registry represents restart while all successful prices stay in SQLite.
	h.jobs.mu.Lock()
	h.jobs.entries = map[string]*priceJob{}
	h.jobs.mu.Unlock()
	if w = featureRequest(handler, "GET", "/api/price-fetch-jobs/"+key, "", cookies...); w.Code != 404 {
		t.Fatal(w.Code)
	}
	key = startPriceJob(t, handler, path, body, cookies)
	job = waitPriceJob(t, handler, key, cookies)
	for i := 0; i < 3; i++ {
		if job.Items[i].Status != "FRESH" {
			t.Fatal(job.Items[i])
		}
	}
	b, _ = h.store.AVBudget(ctx, user, time.Now(), h.av.Limits)
	if b.UserRemaining != 7 {
		t.Fatal("reuse spent budget", b)
	}
	if w = featureRequest(handler, "POST", path+"/price-fetch-jobs", `{"date":"1989-12-31","instrumentIds":[1]}`, cookies...); w.Code != 400 {
		t.Fatal("date before 1990 allowed", w.Code)
	}
}
func TestAlphaJobRunningImmediatePersistenceAndBudgetExhaustion(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	ctx := context.Background()
	user := "google-user-1"
	h.av.Limits.Minute = 1000000
	h.av.Limits.User = 1
	p, _ := h.store.SavePortfolio(ctx, user, 0, "Budget")
	h.store.AddItems(ctx, user, p.ID, []int64{1, 2, 3})
	for i := int64(1); i <= 3; i++ {
		if err := h.store.LinkSymbol(ctx, user, i, fmt.Sprintf("S%d", i), "EUR", ""); err != nil {
			t.Fatal(err)
		}
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	h.av.Transport = avTestTransport(func(c context.Context, fn, q string) ([]byte, error) {
		close(entered)
		select {
		case <-release:
		case <-c.Done():
			return nil, c.Err()
		}
		return marketdata.MockAV{}.Call(c, fn, q)
	})
	path := fmt.Sprintf("/api/portfolios/%d", p.ID)
	body := fmt.Sprintf(`{"date":%q,"instrumentIds":[1,2,3]}`, marketdata.BerlinDay(time.Now()))
	key := startPriceJob(t, handler, path, body, cookies)
	<-entered
	w := featureRequest(handler, "POST", path+"/price-fetch-jobs", body, cookies...)
	if w.Code != 409 || !strings.Contains(w.Body.String(), "JOB_RUNNING") {
		t.Fatal(w.Code, w.Body.String())
	}
	close(release)
	job := waitPriceJob(t, handler, key, cookies)
	for i, want := range []string{"OK", "RATE_LIMITED", "RATE_LIMITED"} {
		if job.Items[i].Status != want {
			t.Fatal(job.Items)
		}
	}
	quote, err := h.store.FreshQuote(ctx, 1, time.Now())
	if err != nil || quote == nil {
		t.Fatal("quote not saved", quote, err)
	}
}
func TestAlphaMissingFXNeverSpendsAndSymbolRoutes(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	ctx := context.Background()
	p, _ := h.store.SavePortfolio(ctx, "google-user-1", 0, "FX")
	h.store.AddItems(ctx, "google-user-1", p.ID, []int64{1})
	h.fx.Provider = &fxTestProvider{missing: true}
	request := func(method, path, body string, status int) {
		t.Helper()
		w := featureRequest(handler, method, path, body, cookies...)
		if w.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
	}
	request("PUT", "/api/instruments/1/av-symbol", `{"avSymbol":"AAPL","currency":"USD","region":"US"}`, 204)
	request("PUT", "/api/instruments/1/av-symbol", `{"avSymbol":"AAPL","currency":"USD","region":"US"}`, 409)
	request("PUT", "/api/instruments/2/av-symbol", `{"avSymbol":"AAPL","currency":"USD","region":"US"}`, 409)
	path := fmt.Sprintf("/api/portfolios/%d", p.ID)
	key := startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[1]}`, marketdata.BerlinDay(time.Now())), cookies)
	job := waitPriceJob(t, handler, key, cookies)
	if job.Items[0].Status != "FX_RATE_UNAVAILABLE" {
		t.Fatal(job.Items)
	}
	b, _ := h.store.AVBudget(ctx, "google-user-1", time.Now(), h.av.Limits)
	if b.UserRemaining != 10 {
		t.Fatal("missing FX spent AV budget", b)
	}
	request("DELETE", "/api/instruments/1/av-symbol", "", 204)
	request("GET", "/api/instruments/search/online?q=US0378331005&provider=av", "", 400)
	request("GET", "/api/instruments/search/online?q=apple&provider=av", "", 200)
	w := featureRequest(handler, "GET", "/api/instruments/search/online?q=apple&provider=av", "", cookies...)
	if !strings.Contains(w.Body.String(), `"source":"ALPHAVANTAGE"`) {
		t.Fatal(w.Body.String())
	}
	var results marketdata.Results
	if err := json.Unmarshal(w.Body.Bytes(), &results); err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(results.Results[0])
	request("POST", "/api/instruments", string(payload), 200)
	b, _ = h.store.AVBudget(ctx, "google-user-1", time.Now(), h.av.Limits)
	if b.UserRemaining != 9 {
		t.Fatal("search cache spent twice", b)
	}
}
func TestAlphaRoutesRequireAuth(t *testing.T) {
	_, h := auditHandler(t, ":memory:")
	for _, r := range []struct{ method, path string }{{"GET", "/api/market-data/budget"}, {"GET", "/api/portfolios/1/price-fetch/estimate"}, {"POST", "/api/portfolios/1/price-fetch-jobs"}, {"GET", "/api/price-fetch-jobs/job"}, {"GET", "/api/instruments/1/av-symbol/search"}, {"PUT", "/api/instruments/1/av-symbol"}, {"DELETE", "/api/instruments/1/av-symbol"}} {
		if w := featureRequest(h, r.method, r.path, ""); w.Code != 401 {
			t.Fatal(r, w.Code)
		}
	}
}

func TestAlphaReusedQuoteUsesFXOfTransactionDate(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	ctx := context.Background()
	user := "google-user-1"
	p, err := h.store.SavePortfolio(ctx, user, 0, "FX dates")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.AddItems(ctx, user, p.ID, []int64{1}); err != nil {
		t.Fatal(err)
	}
	if err = h.store.LinkSymbol(ctx, user, 1, "APPLE", "USD", "US"); err != nil {
		t.Fatal(err)
	}
	// Today's shared quote was converted at another publication (e.g. for another user's date).
	shared := &marketdata.Quote{InstrumentID: 1, Price: "9.0000", OrigPrice: "10.0000", Currency: "USD", FXRate: "0.90000000", FXRateDate: "2026-01-02", QuoteDate: "2026-01-02", FetchedAt: time.Now()}
	if err = h.store.SaveQuote(ctx, shared, "APPLE"); err != nil {
		t.Fatal(err)
	}
	h.fx.Provider = &fxTestProvider{} // publishes 0.9231 dated the day before the transaction date
	date := marketdata.BerlinDay(time.Now())
	d, _ := time.Parse("2006-01-02", date)
	fxDate := d.AddDate(0, 0, -1).Format("2006-01-02")
	path := fmt.Sprintf("/api/portfolios/%d", p.ID)
	run := func() priceItem {
		t.Helper()
		key := startPriceJob(t, handler, path, fmt.Sprintf(`{"date":%q,"instrumentIds":[1]}`, date), cookies)
		item := waitPriceJob(t, handler, key, cookies).Items[0]
		if item.Status != "FRESH" || item.Quote == nil {
			t.Fatalf("%+v", item)
		}
		return item
	}
	item := run()
	if item.FXRateDate != fxDate || item.FXRate != "0.92310000" || item.Price != "9.2310" || item.OrigPrice != "10.0000" || item.ID == shared.ID {
		t.Fatalf("reused quote kept another date's FX: %+v", item.Quote)
	}
	if again := run(); again.ID != item.ID {
		t.Fatal("converted quote was not reused", again.Quote)
	}
	b, err := h.store.AVBudget(ctx, user, time.Now(), h.av.Limits)
	if err != nil || b.UserRemaining != h.av.Limits.User || b.GlobalRemaining != h.av.Limits.Day {
		t.Fatal("re-conversion spent AV budget", b, err)
	}
	if _, err = h.store.CreateTransaction(ctx, user, p.ID, database.TransactionInput{Date: date, Lines: []database.TransactionLine{{InstrumentID: 1, Amount: "92.31", Price: item.Price, Quantity: "10.000000", QuoteID: item.ID}}}); err != nil {
		t.Fatal(err)
	}
	lines, err := h.store.Transactions(ctx, user, p.ID, 1, 50)
	if err != nil || lines.Lines[0].OrigPrice != "10.0000" || lines.Lines[0].FXRate != "0.92310000" || lines.Lines[0].FXRateDate != fxDate {
		t.Fatal("committed line lost the transaction date's FX", lines, err)
	}
}

func TestHoldingsPriorityUsesLimitedBudgetAndReusesSharedQuotes(t *testing.T) {
	for _, scope := range []string{"user", "global"} {
		t.Run(scope, func(t *testing.T) {
			h, handler := auditHandler(t, ":memory:")
			cookies := auditLogin(t, h, handler)
			ctx := context.Background()
			user := "google-user-1"
			h.av.Limits.Minute = 1000000
			if scope == "user" {
				h.av.Limits.User = 1
			} else {
				h.av.Limits.Day = 1
			}
			p, err := h.store.SavePortfolio(ctx, user, 0, "Priority")
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.store.AddItems(ctx, user, p.ID, []int64{1, 2, 3, 4, 5}); err != nil {
				t.Fatal(err)
			}
			for _, id := range []int64{1, 2, 3, 5} {
				if err = h.store.LinkSymbol(ctx, user, id, fmt.Sprintf("S%d", id), "EUR", ""); err != nil {
					t.Fatal(err)
				}
			}
			if _, err = h.store.CreateTransaction(ctx, user, p.ID, database.TransactionInput{Date: "2025-01-01", Lines: []database.TransactionLine{
				{InstrumentID: 1, Amount: "10", Price: "10", Quantity: "1"},
				{InstrumentID: 2, Amount: "1000", Price: "10", Quantity: "100"},
				{InstrumentID: 3, Amount: "500", Price: "10", Quantity: "50"},
				{InstrumentID: 4, Amount: "50", Price: "10", Quantity: "5"},
			}}); err != nil {
				t.Fatal(err)
			}
			if err = h.store.SaveQuote(ctx, &marketdata.Quote{InstrumentID: 3, Price: "10.0000", OrigPrice: "10.0000", Currency: "EUR", QuoteDate: "2026-11-03", FetchedAt: time.Now()}, "S3"); err != nil {
				t.Fatal(err)
			}
			var calls atomic.Int32
			h.av.Transport = avTestTransport(func(ctx context.Context, fn, symbol string) ([]byte, error) {
				calls.Add(1)
				if symbol != "S2" {
					t.Errorf("budget spent on %s, expected highest holding S2", symbol)
				}
				return marketdata.MockAV{}.Call(ctx, fn, symbol)
			})
			path := fmt.Sprintf("/api/portfolios/%d", p.ID)
			date := marketdata.BerlinDay(time.Now())
			body := fmt.Sprintf(`{"date":%q,"instrumentIds":[1,3,4,2,5],"priority":"HOLDINGS_VALUE"}`, date)
			job := waitPriceJob(t, handler, startPriceJob(t, handler, path, body, cookies), cookies)
			if len(job.Items) != 4 {
				t.Fatal("unheld item was included", job.Items)
			}
			for i, want := range []struct {
				id     int64
				status string
			}{{2, "OK"}, {3, "FRESH"}, {4, "NO_SYMBOL"}, {1, "RATE_LIMITED"}} {
				if job.Items[i].InstrumentID != want.id || job.Items[i].Status != want.status {
					t.Fatalf("%d: %+v", i, job.Items[i])
				}
			}
			perf, err := h.store.Performance(ctx, user, p.ID)
			if err != nil || perf.Current == nil || !perf.Current.Partial {
				t.Fatal(perf.Current, err)
			}
			for _, item := range perf.Current.Items {
				if item.InstrumentID == 1 && (item.Source != "PURCHASE" || item.Reason != "NO_QUOTE") {
					t.Fatal(item)
				}
			}
			budget, err := h.store.AVBudget(ctx, user, time.Now(), h.av.Limits)
			if err != nil || budget.NextSlotAt.Before(time.Now().Add(23*time.Hour)) {
				t.Fatal("missing daily retry time", budget, err)
			}
			w := featureRequest(handler, "GET", path+"/price-fetch/estimate?date="+date+"&instrumentIds=2,3", "", cookies...)
			if w.Code != 200 || !strings.Contains(w.Body.String(), `"requestsNeeded":0`) {
				t.Fatal(w.Code, w.Body.String())
			}
			free := fmt.Sprintf(`{"date":%q,"instrumentIds":[2,3],"priority":"HOLDINGS_VALUE"}`, date)
			again := waitPriceJob(t, handler, startPriceJob(t, handler, path, free, cookies), cookies)
			if calls.Load() != 1 || again.Items[0].Status != "FRESH" || again.Items[1].Status != "FRESH" {
				t.Fatal("fresh quotes used budget", calls.Load(), again.Items)
			}
			w = featureRequest(handler, "POST", path+"/price-fetch-jobs", fmt.Sprintf(`{"date":%q,"instrumentIds":[1],"priority":"UNKNOWN"}`, date), cookies...)
			if w.Code != 400 {
				t.Fatal(w.Code, w.Body.String())
			}
		})
	}
}

func TestAlphaSymbolSearchTerm(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	ctx := context.Background()
	user := "google-user-1"
	h.av.Limits.Minute = 1000000 // spacing is covered by the gateway tests
	var sent []string
	h.av.Transport = avTestTransport(func(ctx context.Context, fn, q string) ([]byte, error) {
		sent = append(sent, q)
		return marketdata.MockAV{}.Call(ctx, fn, q)
	})
	// Seeded instrument 19 has its ISIN as ticker; 21 has a Yahoo-style ".DE" ticker.
	p, err := h.store.SavePortfolio(ctx, user, 0, "ETFs")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.AddItems(ctx, user, p.ID, []int64{19, 21}); err != nil {
		t.Fatal(err)
	}
	plan, err := h.store.SavingPlan(ctx, user, p.ID)
	if err != nil || plan[0].AVSearchTerm != "Vanguard FTSE Emerging Markets" || plan[1].AVSearchTerm != "XD5E" {
		t.Fatalf("%+v %v", plan, err)
	}
	search := func(path string, status int) marketdata.Results {
		t.Helper()
		w := featureRequest(handler, "GET", path, "", cookies...)
		var out marketdata.Results
		if w.Code != status || (status == 200 && json.Unmarshal(w.Body.Bytes(), &out) != nil) {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return out
	}
	if r := search("/api/instruments/19/av-symbol/search", 200); len(r.Results) != 0 {
		t.Fatal("unrelated mock results", r.Results)
	}
	search("/api/instruments/19/av-symbol/search?q=IE00B3VVMM84", 400)
	search("/api/instruments/19/av-symbol/search?q="+strings.Repeat("x", 101), 400)
	if r := search("/api/instruments/21/av-symbol/search", 200); len(r.Results) != 1 || r.Results[0].AVSymbol != "XD5E.DEX" {
		t.Fatal(r.Results)
	}
	if r := search("/api/instruments/19/av-symbol/search?q=%20sap%20", 200); len(r.Results) != 2 || r.Results[0].AVSymbol != "SAP.DEX" {
		t.Fatal(r.Results)
	}
	if strings.Join(sent, "|") != "Vanguard FTSE Emerging Markets|XD5E|sap" {
		t.Fatal("AV keywords", sent)
	}
	b, err := h.store.AVBudget(ctx, user, time.Now(), h.av.Limits)
	if err != nil || b.UserRemaining != h.av.Limits.User-3 {
		t.Fatal("rejected searches must not spend budget", b, err)
	}
}

func TestAlphaRejectedSearchExplainsAPIKey(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	notice, err := os.ReadFile("../marketdata/testdata/marketdata/alpha-demo-information.json")
	if err != nil {
		t.Fatal(err)
	}
	h.av.Transport = avTestTransport(func(context.Context, string, string) ([]byte, error) { return notice, nil })
	w := featureRequest(handler, "GET", "/api/instruments/19/av-symbol/search?q=vanguard", "", cookies...)
	if w.Code != 502 || !strings.Contains(w.Body.String(), `"code":"UPSTREAM_ERROR"`) || !strings.Contains(w.Body.String(), "ALPHAVANTAGE_API_KEY") {
		t.Fatal(w.Code, w.Body.String())
	}
}
