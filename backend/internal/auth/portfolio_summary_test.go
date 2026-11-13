package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/marketdata"
)

func summaryPortfolio(t *testing.T, h *Handler, user, name string, items []int64, held []int64) int64 {
	t.Helper()
	ctx := context.Background()
	p, err := h.store.SavePortfolio(ctx, user, 0, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.AddItems(ctx, user, p.ID, items); err != nil {
		t.Fatal(err)
	}
	if len(held) > 0 {
		lines := []database.TransactionLine{}
		for _, id := range held {
			lines = append(lines, database.TransactionLine{InstrumentID: id, Amount: "100", Price: "100", Quantity: "1"})
		}
		if _, err = h.store.CreateTransaction(ctx, user, p.ID, database.TransactionInput{Date: "2026-11-02", Lines: lines}); err != nil {
			t.Fatal(err)
		}
	}
	return p.ID
}

func summaryRefresh(t *testing.T, handler http.Handler, cookies []*http.Cookie, body string, status int) refreshResult {
	t.Helper()
	w := featureRequest(handler, "POST", "/api/price-refresh-jobs", body, cookies...)
	var out refreshResult
	if w.Code != status || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("refresh: %d %s", w.Code, w.Body.String())
	}
	return out
}

func summaryInfo(t *testing.T, handler http.Handler, cookies []*http.Cookie) refreshInfo {
	t.Helper()
	w := featureRequest(handler, "GET", "/api/portfolios/summaries", "", cookies...)
	var out struct{ Refresh refreshInfo }
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil {
		t.Fatalf("summaries: %d %s", w.Code, w.Body.String())
	}
	return out.Refresh
}

func TestSummaryRoutesRequireSessionAndExcludeOtherUsers(t *testing.T) {
	h, handler, cookies, y, ep, av := yahooPrimary(t)
	for _, tc := range []struct{ method, path, body string }{{"GET", "/api/portfolios/summaries", ""}, {"POST", "/api/price-refresh-jobs", "{}"}} {
		if w := featureRequest(handler, tc.method, tc.path, tc.body); w.Code != 401 {
			t.Fatalf("%s: %d", tc.path, w.Code)
		}
	}
	a := summaryPortfolio(t, h, "google-user-1", "Mine", []int64{7}, []int64{7})
	if _, err := h.store.UpsertGoogleUser(context.Background(), "foreign", "foreign@example.invalid", "Foreign", ""); err != nil {
		t.Fatal(err)
	}
	summaryPortfolio(t, h, "foreign", "Secret", []int64{18}, []int64{18})
	w := featureRequest(handler, "GET", "/api/portfolios/summaries", "", cookies...)
	var out database.Summaries
	if w.Code != 200 || json.Unmarshal(w.Body.Bytes(), &out) != nil || len(out.Summaries) != 1 || out.Summaries[0].PortfolioID != a || out.Total.PortfolioCount != 1 {
		t.Fatalf("scope %d %s", w.Code, w.Body.String())
	}
	for _, body := range []string{`{"provider":"ALPHAVANTAGE"}`, `{"instrumentIds":[18]}`, `{"force":true,"extra":1}`} {
		if w := featureRequest(handler, "POST", "/api/price-refresh-jobs", body, cookies...); w.Code != 400 {
			t.Fatalf("unexpected fields accepted: %d %s", w.Code, w.Body.String())
		}
	}
	if y.total() != 0 || ep.total() != 0 || *av != 0 {
		t.Fatal("read/auth/invalid requests called a provider")
	}
}

func TestSummaryRefreshOneDeduplicatedYahooJobRegardlessOfPrimary(t *testing.T) {
	for _, primary := range []string{marketdata.ProviderEulerpool, marketdata.ProviderAlphaVantage} {
		t.Run(primary, func(t *testing.T) {
			h, handler, cookies, y, ep, av := yahooPrimary(t)
			h.primary = primary
			summaryPortfolio(t, h, "google-user-1", "One", []int64{7, 18}, []int64{7, 18})
			summaryPortfolio(t, h, "google-user-1", "Two", []int64{7, 2}, []int64{7})
			if _, err := h.store.UpsertGoogleUser(context.Background(), "foreign", "foreign@example.invalid", "", ""); err != nil {
				t.Fatal(err)
			}
			summaryPortfolio(t, h, "foreign", "Secret", []int64{6}, []int64{6})
			for id, symbol := range map[int64]string{7: "SAP.DE", 18: "VHYD.L"} {
				if err := h.store.LinkYahooSymbol(context.Background(), id, symbol); err != nil {
					t.Fatal(err)
				}
			}
			if info := summaryInfo(t, handler, cookies); !info.Stale || !info.AutoFetch {
				t.Fatalf("initial %+v", info)
			}
			if y.total() != 0 {
				t.Fatal("summary GET fetched prices")
			}
			result := summaryRefresh(t, handler, cookies, "{}", 202)
			if !result.Started || result.JobID == nil {
				t.Fatalf("%+v", result)
			}
			job := waitPriceJob(t, handler, *result.JobID, cookies)
			ids := []int64{}
			for _, item := range job.Items {
				ids = append(ids, item.InstrumentID)
				if item.Status != "OK" || item.Quote == nil || item.Provider != "YAHOO" || item.QuoteTime.IsZero() {
					t.Fatalf("item %+v", item)
				}
			}
			if job.Kind != "USER_REALTIME" || !reflect.DeepEqual(ids, []int64{7, 18}) {
				t.Fatalf("job %+v ids %v", job, ids)
			}
			if y.count("/v8/finance/chart/") != 3 || ep.total() != 0 || *av != 0 {
				t.Fatalf("wrong providers/count: Yahoo %v EP %d AV %d", y.calls, ep.total(), *av)
			}
			calls := y.total()
			again := summaryRefresh(t, handler, cookies, "{}", 200)
			if again.Reason != "FRESH" || again.Started || y.total() != calls {
				t.Fatalf("repeat %+v", again)
			}
		})
	}
}

func TestSummaryRefreshFreshnessAndThrottleBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name   string
		age    time.Duration
		quote  bool
		last   time.Duration
		reason string
	}{
		{"fresh at exactly15minutes", 15 * time.Minute, true, 0, "FRESH"},
		{"fresh just inside15minutes", 15*time.Minute - time.Nanosecond, true, 0, "FRESH"},
		{"stale just past15minutes", 15*time.Minute + time.Nanosecond, true, 0, ""},
		{"missing quotes start once", 0, false, 0, ""},
		{"attempt exactly15minutes is throttled", 0, false, 15 * time.Minute, "THROTTLED"},
		{"attempt just inside15minutes is throttled", 0, false, 15*time.Minute - time.Nanosecond, "THROTTLED"},
		{"attempt older than15minutes can start", 0, false, 15*time.Minute + time.Nanosecond, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, handler, cookies, y, _, _ := yahooPrimary(t)
			now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
			h.clock = func() time.Time { return now }
			h.yahoo.Now = h.clock
			summaryPortfolio(t, h, "google-user-1", "Mine", []int64{7}, []int64{7})
			if err := h.store.LinkYahooSymbol(context.Background(), 7, "SAP.DE"); err != nil {
				t.Fatal(err)
			}
			if tc.quote {
				q := &marketdata.Quote{InstrumentID: 7, Price: "210", OrigPrice: "210", Currency: "EUR", QuoteDate: "2026-11-06", FetchedAt: now.Add(-tc.age), Provider: "YAHOO"}
				if err := h.store.SaveQuote(context.Background(), q, "SAP.DE"); err != nil {
					t.Fatal(err)
				}
			}
			if tc.last > 0 {
				h.jobs.mu.Lock()
				if h.jobs.lastRefresh == nil {
					h.jobs.lastRefresh = map[string]time.Time{}
				}
				h.jobs.lastRefresh["google-user-1"] = now.Add(-tc.last)
				h.jobs.mu.Unlock()
			}
			info := summaryInfo(t, handler, cookies)
			if info.MaxAgeSeconds != 900 || info.AutoFetch != (tc.reason == "") {
				t.Fatalf("decision %+v", info)
			}
			status := 200
			if tc.reason == "" {
				status = 202
			}
			result := summaryRefresh(t, handler, cookies, "{}", status)
			if result.Reason != tc.reason {
				t.Fatalf("%+v", result)
			}
			if result.JobID != nil {
				waitPriceJob(t, handler, *result.JobID, cookies)
			} else if y.total() != 0 {
				t.Fatal("blocked decision called Yahoo")
			}
		})
	}
}

func TestSummaryRefreshUsesNewestRelevantQuoteAndIgnoresOlderMissingAndUnheld(t *testing.T) {
	h, handler, cookies, y, _, _ := yahooPrimary(t)
	now := time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)
	h.clock = func() time.Time { return now }
	h.yahoo.Now = h.clock
	summaryPortfolio(t, h, "google-user-1", "Mine", []int64{7, 18, 2}, []int64{7, 18})
	for _, q := range []*marketdata.Quote{{InstrumentID: 7, Price: "210", OrigPrice: "210", Currency: "EUR", QuoteDate: "2026-11-06", FetchedAt: now.Add(-time.Minute), Provider: "YAHOO"}, {InstrumentID: 2, Price: "100", OrigPrice: "100", Currency: "EUR", QuoteDate: "2026-11-06", FetchedAt: now.Add(time.Hour), Provider: "YAHOO"}} {
		if err := h.store.SaveQuote(context.Background(), q, ""); err != nil {
			t.Fatal(err)
		}
	}
	info := summaryInfo(t, handler, cookies)
	if info.Stale || info.AutoFetch || info.NewestFetchedAt == nil || !info.NewestFetchedAt.Equal(now.Add(-time.Minute)) {
		t.Fatalf("%+v", info)
	}
	if result := summaryRefresh(t, handler, cookies, "{}", 200); result.Reason != "FRESH" || y.total() != 0 {
		t.Fatalf("%+v", result)
	}
}

func TestSummaryRefreshReuseForceAndYahooUnavailability(t *testing.T) {
	t.Run("reuse existing portfolio job even under force", func(t *testing.T) {
		h, handler, cookies, y, ep, av := yahooPrimary(t)
		summaryPortfolio(t, h, "google-user-1", "Mine", []int64{7}, []int64{7})
		h.jobs.mu.Lock()
		h.jobs.entries["existing"] = &priceJob{Status: "RUNNING", Kind: "PORTFOLIO", user: "google-user-1", created: time.Now(), Items: []priceItem{{InstrumentID: 7, Status: "PENDING"}}}
		h.jobs.entries["secret"] = &priceJob{Status: "RUNNING", user: "foreign", created: time.Now()}
		h.jobs.mu.Unlock()
		for _, body := range []string{"{}", `{"force":true}`} {
			got := summaryRefresh(t, handler, cookies, body, 200)
			if !got.Reused || got.JobID == nil || *got.JobID != "existing" || got.Started {
				t.Fatalf("%+v", got)
			}
		}
		if w := featureRequest(handler, "GET", "/api/price-fetch-jobs/secret", "", cookies...); w.Code != 404 {
			t.Fatalf("foreign job %d", w.Code)
		}
		if w := featureRequest(handler, "GET", "/api/price-fetch-jobs/existing", ""); w.Code != 401 {
			t.Fatalf("unauthenticated job %d", w.Code)
		}
		if y.total() != 0 || ep.total() != 0 || *av != 0 {
			t.Fatal("job reuse called a provider")
		}
	})
	for _, blocked := range []bool{false, true} {
		t.Run(fmt.Sprintf("force obeys Yahoo unavailable=%v", blocked), func(t *testing.T) {
			h, handler, cookies, y, ep, av := yahooPrimary(t)
			now := time.Now().UTC()
			h.clock = func() time.Time { return now }
			h.yahoo.Now = h.clock
			summaryPortfolio(t, h, "google-user-1", "Mine", []int64{7}, []int64{7})
			h.jobs.mu.Lock()
			if h.jobs.lastRefresh == nil {
				h.jobs.lastRefresh = map[string]time.Time{}
			}
			h.jobs.lastRefresh["google-user-1"] = now
			h.jobs.mu.Unlock()
			if blocked {
				if err := h.store.SetProviderState(context.Background(), "YAHOO", "blocked_until", now.Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
					t.Fatal(err)
				}
			}
			status := 202
			if blocked {
				status = 200
			}
			got := summaryRefresh(t, handler, cookies, `{"force":true}`, status)
			if blocked {
				if got.Reason != "YAHOO_UNAVAILABLE" || y.total() != 0 {
					t.Fatalf("%+v", got)
				}
			} else {
				if got.JobID == nil {
					t.Fatal(got)
				}
				waitPriceJob(t, handler, *got.JobID, cookies)
			}
			if ep.total() != 0 || *av != 0 {
				t.Fatal("automatic fallback was spent")
			}
		})
	}
}

type summaryLateKey struct{}
type summaryGatedStore struct {
	marketdata.EPStore
	once             sync.Once
	reached, release chan struct{}
}

func (s *summaryGatedStore) ProviderState(ctx context.Context, provider, key string) (string, error) {
	if ctx.Value(summaryLateKey{}) == true {
		s.once.Do(func() {
			close(s.reached)
			select {
			case <-s.release:
			case <-ctx.Done():
			}
		})
	}
	return s.EPStore.ProviderState(ctx, provider, key)
}

func TestSummaryRefreshLateContenderRechecksThrottleAfterFirstJobCompletes(t *testing.T) {
	h, handler, cookies, y, _, _ := yahooPrimary(t)
	summaryPortfolio(t, h, "google-user-1", "Mine", []int64{7}, []int64{7})
	if err := h.store.LinkYahooSymbol(context.Background(), 7, "SAP.DE"); err != nil {
		t.Fatal(err)
	}
	gate := &summaryGatedStore{EPStore: h.yahoo.Store, reached: make(chan struct{}), release: make(chan struct{})}
	h.yahoo.Store = gate
	var release sync.Once
	unblock := func() { release.Do(func() { close(gate.release) }) }
	defer unblock()
	ctx, cancel := context.WithTimeout(context.WithValue(context.Background(), summaryLateKey{}, true), 5*time.Second)
	defer cancel()
	r := httptest.NewRequest("POST", "/api/price-refresh-jobs", strings.NewReader("{}")).WithContext(ctx)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	late := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { defer close(done); handler.ServeHTTP(late, r) }()
	select {
	case <-gate.reached:
	case <-ctx.Done():
		t.Fatal("late contender never reached the post-decision gate")
	}
	first := summaryRefresh(t, handler, cookies, "{}", 202)
	waitPriceJob(t, handler, *first.JobID, cookies)
	calls := y.total()
	unblock()
	select {
	case <-done:
	case <-ctx.Done():
		t.Fatal("late contender did not finish")
	}
	var result refreshResult
	if late.Code != 200 || json.Unmarshal(late.Body.Bytes(), &result) != nil || result.Reason != "THROTTLED" || result.Started {
		t.Fatalf("late contender %d %s", late.Code, late.Body.String())
	}
	if y.total() != calls {
		t.Fatal("late contender spent an extra Yahoo request")
	}
}

type summaryHolidayYahoo struct{ exactFX bool }

func (f summaryHolidayYahoo) Get(_ context.Context, raw string) (int, http.Header, []byte, error) {
	stock := strings.Contains(raw, "HOLIDAY.USD")
	symbol, currency, price, previous := "USDEUR=X", "EUR", "0.9", "0.8"
	previousDay := 12
	if f.exactFX {
		previousDay = 11
	}
	if stock {
		symbol, currency, price, previous, previousDay = "HOLIDAY.USD", "USD", "120", "100", 11
	}
	now := time.Date(2026, 11, 13, 16, 0, 0, 0, time.UTC).Unix()
	before := time.Date(2026, 11, previousDay, 16, 0, 0, 0, time.UTC).Unix()
	body := fmt.Sprintf(`{"chart":{"result":[{"meta":{"symbol":%q,"currency":%q,"regularMarketPrice":%s,"regularMarketTime":%d,"exchangeTimezoneName":"UTC","chartPreviousClose":999},"timestamp":[%d,%d],"indicators":{"quote":[{"close":[%s,%s]}]}}],"error":null}}`, symbol, currency, price, now, before, now, previous, price)
	return 200, http.Header{}, []byte(body), nil
}

func TestSummaryRefreshExchangeHolidayRequiresExactPreviousFXDate(t *testing.T) {
	for _, exact := range []bool{false, true} {
		t.Run(fmt.Sprintf("exact previous FX date=%v", exact), func(t *testing.T) {
			h, handler, cookies, _, ep, av := yahooPrimary(t)
			at := time.Date(2026, 11, 13, 16, 0, 0, 0, time.UTC)
			h.clock = func() time.Time { return at }
			h.yahoo.Now = h.clock
			ctx := context.Background()
			inst, err := h.store.SaveYahooInstrument(ctx, marketdata.Result{YahooSymbol: "HOLIDAY.USD", Name: "Holiday instrument", Type: "STOCK"}, "")
			if err != nil {
				t.Fatal(err)
			}
			summaryPortfolio(t, h, "google-user-1", "Holiday", []int64{inst.ID}, []int64{inst.ID})
			h.yahoo.Transport = summaryHolidayYahoo{exactFX: exact}
			result := summaryRefresh(t, handler, cookies, "{}", 202)
			job := waitPriceJob(t, handler, *result.JobID, cookies)
			if len(job.Items) != 1 || job.Items[0].Status != "OK" {
				t.Fatalf("%+v", job)
			}
			// Previous-close provenance is intentionally internal (json:"-"). Check
			// its persisted round-trip rather than expecting it in the job poll JSON.
			q, err := h.store.FreshQuote(ctx, inst.ID, h.now())
			if err != nil || q == nil {
				t.Fatalf("stored quote: %+v %v", q, err)
			}
			wantRate, wantChange := "", "18.00"
			wantReasons := []string{"FX_PREVIOUS_UNAVAILABLE"}
			if exact {
				wantRate, wantChange, wantReasons = "0.80000000", "28.00", []string{}
			}
			if q.PrevClose != "100.0000" || q.PrevCloseDate != "2026-11-11" || q.PrevFXRate != wantRate || q.FXRate != "0.90000000" {
				t.Fatalf("neighbouring FX close was treated as exact: %+v", q)
			}
			summaries, err := h.store.Summaries(ctx, "google-user-1")
			if err != nil {
				t.Fatal(err)
			}
			got := summaries.Total
			if got.Change == nil || got.Change.Amount != wantChange || got.ChangePartial == exact || !reflect.DeepEqual(got.Reasons, wantReasons) {
				t.Fatalf("holiday approximation %+v", got)
			}
			if ep.total() != 0 || *av != 0 {
				t.Fatal("foreign-currency fallback spent another price provider")
			}
		})
	}
}

func TestSummaryRefreshEmptyBudgetFailureAndForeignJobRules(t *testing.T) {
	t.Run("unheld selected items spend nothing", func(t *testing.T) {
		h, handler, cookies, y, ep, av := yahooPrimary(t)
		summaryPortfolio(t, h, "google-user-1", "Unheld", []int64{7}, nil)
		info := summaryInfo(t, handler, cookies)
		if info.Stale || info.AutoFetch || info.Estimate != nil {
			t.Fatalf("empty refresh %+v", info)
		}
		if got := summaryRefresh(t, handler, cookies, "{}", 200); got.Reason != "NOTHING_TO_PRICE" {
			t.Fatal(got)
		}
		if y.total() != 0 || ep.total() != 0 || *av != 0 {
			t.Fatal("unheld items spent a provider")
		}
	})
	t.Run("zero Yahoo budget wins over force", func(t *testing.T) {
		h, handler, cookies, y, ep, av := yahooPrimary(t)
		summaryPortfolio(t, h, "google-user-1", "Held", []int64{7}, []int64{7})
		h.yahoo.SoftDaily = 1
		if err := h.store.RecordProviderRequest(context.Background(), "YAHOO", "google-user-1", "test", time.Now(), true); err != nil {
			t.Fatal(err)
		}
		info := summaryInfo(t, handler, cookies)
		if info.AutoFetch || info.Yahoo.Available {
			t.Fatalf("budget %+v", info)
		}
		if got := summaryRefresh(t, handler, cookies, `{"force":true}`, 200); got.Reason != "YAHOO_UNAVAILABLE" {
			t.Fatal(got)
		}
		if y.total() != 0 || ep.total() != 0 || *av != 0 {
			t.Fatal("budget gate spent a provider")
		}
	})
	t.Run("failed job is attempt-throttled with no automatic fallback", func(t *testing.T) {
		h, handler, cookies, y, ep, av := yahooPrimary(t)
		summaryPortfolio(t, h, "google-user-1", "Held", []int64{7}, []int64{7})
		y.fail = 500
		first := summaryRefresh(t, handler, cookies, "{}", 202)
		job := waitPriceJob(t, handler, *first.JobID, cookies)
		if job.Items[0].Status != "ERROR" {
			t.Fatalf("%+v", job)
		}
		calls := y.total()
		info := summaryInfo(t, handler, cookies)
		if info.AutoFetch || info.LastAttemptAt == nil {
			t.Fatalf("failed attempt %+v", info)
		}
		if got := summaryRefresh(t, handler, cookies, "{}", 200); got.Reason != "THROTTLED" {
			t.Fatal(got)
		}
		if y.total() != calls || ep.total() != 0 || *av != 0 {
			t.Fatal("failed automatic refresh retried/spent a fallback")
		}
	})
	t.Run("another users running job is not reused", func(t *testing.T) {
		h, handler, cookies, _, _, _ := yahooPrimary(t)
		summaryPortfolio(t, h, "google-user-1", "Held", []int64{7}, []int64{7})
		h.jobs.mu.Lock()
		h.jobs.entries["foreign"] = &priceJob{Status: "RUNNING", user: "other", created: time.Now()}
		h.jobs.mu.Unlock()
		got := summaryRefresh(t, handler, cookies, "{}", 202)
		if got.Reused || got.JobID == nil || *got.JobID == "foreign" {
			t.Fatal(got)
		}
		waitPriceJob(t, handler, *got.JobID, cookies)
	})
}

func TestSummaryRefreshECBRateFallbackIsMarkedApproximate(t *testing.T) {
	h, handler, cookies, _, ep, av := yahooPrimary(t)
	ctx := context.Background()
	summaryPortfolio(t, h, "google-user-1", "FX fallback", []int64{18}, []int64{18})
	if err := h.store.LinkYahooSymbol(ctx, 18, "VHYD.L"); err != nil {
		t.Fatal(err)
	}
	h.yahoo.Transport = &fxOutage{}
	fx := &fxTestProvider{}
	h.fx.Provider = fx
	created := summaryRefresh(t, handler, cookies, "{}", 202)
	job := waitPriceJob(t, handler, *created.JobID, cookies)
	if len(job.Items) != 1 || job.Items[0].Status != "OK" || job.Items[0].FXSource != "" || job.Items[0].FXRate != "0.92310000" {
		t.Fatalf("fallback job %+v", job)
	}
	got, err := h.store.Summaries(ctx, "google-user-1")
	if err != nil || got.Total.Partial || !got.Total.ChangePartial || !reflect.DeepEqual(got.Total.Reasons, []string{"FX_PREVIOUS_UNAVAILABLE"}) {
		t.Fatalf("fallback summary %+v %v", got, err)
	}
	if got.Total.Change == nil || got.Total.Change.Amount != "-1.11" {
		t.Fatalf("fallback change %+v", got.Total.Change)
	}
	if fx.calls.Load() == 0 || ep.total() != 0 || *av != 0 {
		t.Fatal("expected ECB FX only, never AV/Eulerpool")
	}
}
