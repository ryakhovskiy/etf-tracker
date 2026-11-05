package marketdata_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"holdings/backend/internal/database"
	md "holdings/backend/internal/marketdata"
)

type avFunc func(context.Context, string, string) ([]byte, error)

func (f avFunc) Call(c context.Context, a, b string) ([]byte, error) { return f(c, a, b) }

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func store(t *testing.T) *database.Store {
	t.Helper()
	s, e := database.Open(filepath.Join(t.TempDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { s.Close() })
	for _, u := range []string{"alice", "bob", "carol"} {
		if _, e = s.UpsertGoogleUser(context.Background(), u, u+"@example.com", u, ""); e != nil {
			t.Fatal(e)
		}
	}
	return s
}
func fakeGateway(s *database.Store, t md.AVTransport, now time.Time) *md.AVGateway {
	g := md.NewAVGateway(s, t, md.NewCache(50, time.Hour), md.AVLimits{})
	g.Now = func() time.Time { return now }
	g.Sleep = func(ctx context.Context, d time.Duration) error { now = now.Add(d); return nil }
	return g
}
func TestAVAtomicLimitsAndRollingWindow(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	limits := md.AVLimits{}.Defaults()
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := s.ReserveAV(ctx, "alice", "SYMBOL_SEARCH", now, limits)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, md.ErrAVUserLimit) {
				t.Errorf("reservation: %v", err)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 10 {
		t.Fatal(accepted.Load())
	}
	for _, u := range []string{"bob", "carol"} {
		for i := 0; i < 10; i++ {
			e := s.ReserveAV(ctx, u, "GLOBAL_QUOTE", now, limits)
			if e != nil && !errors.Is(e, md.ErrAVLimit) {
				t.Fatal(e)
			}
		}
	}
	b, e := s.AVBudget(ctx, "alice", now, limits)
	if e != nil || b.GlobalRemaining != 0 || b.UserRemaining != 0 {
		t.Fatalf("%+v %v", b, e)
	}
	if e = s.ReserveAV(ctx, "alice", "GLOBAL_QUOTE", now.Add(24*time.Hour), limits); e != nil {
		t.Fatal("rolling boundary", e)
	}
}
func TestAVWorkerCacheSpacingRestartAndBlock(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC)
	var calls []time.Time
	body := []byte(`{"bestMatches":[{"1. symbol":"SAP","2. name":"SAP","3. type":"Equity","8. currency":"USD","9. matchScore":"1"}]}`)
	var g *md.AVGateway
	g = fakeGateway(s, avFunc(func(context.Context, string, string) ([]byte, error) {
		calls = append(calls, g.Now())
		return body, nil
	}), now)
	// The injected clock advances only through the worker's wait.

	for i := 0; i < 6; i++ {
		if _, e := g.Search(ctx, "alice", fmt.Sprint(i)); e != nil {
			t.Fatal(e)
		}
	}
	for i := 1; i < len(calls); i++ {
		if calls[i].Sub(calls[i-1]) < 12*time.Second {
			t.Fatal(calls)
		}
	}
	if _, e := g.Search(ctx, "bob", "0"); e != nil || len(calls) != 6 {
		t.Fatalf("LRU cache %d %v", len(calls), e)
	}
	b, e := s.AVBudget(ctx, "alice", g.Now(), g.Limits)
	if e != nil || b.UserRemaining != 4 {
		t.Fatalf("%+v %v", b, e)
	}
	restarted := fakeGateway(s, md.MockAV{}, g.Now())
	b, e = restarted.Store.AVBudget(ctx, "alice", g.Now(), g.Limits)
	if e != nil || b.UserRemaining != 4 {
		t.Fatal("restart lost ledger", b, e)
	}
	g.Transport = avFunc(func(context.Context, string, string) ([]byte, error) {
		return []byte(`{"Information":"API rate limit reached"}`), nil
	})
	if _, e = g.Search(ctx, "alice", "new"); !errors.Is(e, md.ErrAVLimit) {
		t.Fatal(e)
	}
	if _, e = g.Search(ctx, "bob", "other"); !errors.Is(e, md.ErrAVLimit) {
		t.Fatal(e)
	}
	if _, e = g.Search(ctx, "bob", "0"); e != nil {
		t.Fatal("cache must precede blocked state", e)
	}
}

func TestAVQuoteReuseConversionAndErrors(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	if e := s.LinkSymbol(ctx, "alice", 1, "SAP", "USD", "US"); e != nil {
		t.Fatal(e)
	}
	calls := 0
	g := fakeGateway(s, avFunc(func(c context.Context, f, q string) ([]byte, error) { calls++; return md.MockAV{}.Call(c, f, q) }), time.Now())
	fx := &md.FXRate{Date: "2026-11-04", Rate: 92310000}
	q, fresh, e := g.Quote(ctx, "alice", "SAP", "USD", 1, fx)
	if e != nil || fresh || q.Price != "113.9629" || q.ID == 0 {
		t.Fatalf("%+v %v %v", q, fresh, e)
	}
	// An exhausted/blocked other user still reuses the shared quote.
	if e = s.BlockAV(ctx, g.Now().Add(time.Hour)); e != nil {
		t.Fatal(e)
	}
	q, fresh, e = g.Quote(ctx, "bob", "SAP", "USD", 1, nil)
	if e != nil || !fresh || calls != 1 {
		t.Fatalf("%+v %v %v calls=%d", q, fresh, e, calls)
	}
	for _, raw := range []string{`{"Error Message":"bad symbol"}`, `{}`, `{"Global Quote":{"05. price":"NaN"}}`} {
		t.Run(raw, func(t *testing.T) {
			other := store(t)
			gateway := fakeGateway(other, avFunc(func(context.Context, string, string) ([]byte, error) { return []byte(raw), nil }), time.Now())
			if _, _, e = gateway.Quote(ctx, "alice", "x", "EUR", 2, nil); !errors.Is(e, md.ErrUpstream) {
				t.Fatal(e)
			}
		})
	}
	for _, tc := range []struct{ p, r, d, w int64 }{{1, 50000000, 1, 1}, {1234567, 92310000, 1, 1139629}, {1000000, 118340000, 100, 11834}} {
		got, e := md.Convert(tc.p, tc.r, tc.d)
		if e != nil || got != tc.w {
			t.Fatalf("%+v got=%d %v", tc, got, e)
		}
	}
}
func TestAllowedDatesBerlin(t *testing.T) {
	for _, tc := range []struct {
		now, date string
		ok        bool
	}{{"2026-11-09T10:00:00Z", "2026-11-06", true}, {"2026-11-08T10:00:00Z", "2026-11-06", true}, {"2026-11-07T10:00:00Z", "2026-11-07", true}, {"2026-11-09T10:00:00Z", "2026-11-08", false}, {"2026-11-05T23:30:00Z", "2026-11-06", true}} {
		now, _ := time.Parse(time.RFC3339, tc.now)
		if md.AllowedDate(tc.date, now) != tc.ok {
			t.Fatal(tc)
		}
	}
}
func response(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body))}
}
func TestFrankfurterURLStorageCacheAndErrors(t *testing.T) {
	ctx := context.Background()
	s := store(t)
	calls := 0
	var urls []string
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		calls++
		urls = append(urls, r.URL.String())
		if strings.HasSuffix(r.URL.Path, "currencies") {
			return response(200, `[{"iso_code":"USD"},{"iso_code":"GBP"}]`), nil
		}
		return response(200, `[{"date":"2026-11-03","base":"USD","quote":"USD","rate":1},{"date":"2026-11-03","base":"USD","quote":"EUR","rate":0.92},{"date":"2026-11-04","base":"USD","quote":"EUR","rate":0.9231}]`), nil
	})}
	provider := &md.Frankfurter{HTTP: client, BaseURL: "https://mock.invalid/v2", Provider: "ECB", Cache: md.NewCache(1, 24*time.Hour)}
	fx := &md.FXService{Store: s, Provider: provider, ProviderKey: "ECB", Now: func() time.Time { return time.Date(2026, 11, 5, 12, 0, 0, 0, time.UTC) }}
	if r, e := fx.Rate(ctx, "EUR", "2026-11-05"); e != nil || r != nil || calls != 0 {
		t.Fatal(r, e, calls)
	}
	r, e := fx.Rate(ctx, "USD", "2026-11-05")
	if e != nil || r.Date != "2026-11-04" || r.Rate != 92310000 {
		t.Fatalf("%+v %v", r, e)
	}
	if !strings.Contains(urls[1], "/v2/providers/ECB/rates?") || !strings.Contains(urls[1], "from=2026-10-26") || !strings.Contains(urls[1], "quotes=EUR") {
		t.Fatal(urls)
	}
	if r, e = fx.Rate(ctx, "USD", "2026-11-04"); e != nil || r.Rate != 92310000 || calls != 2 {
		t.Fatal("cached rate refetched", calls, e)
	}
	earlier, e := s.CachedFX(ctx, "USD", "2026-11-03", "ECB")
	if e != nil || earlier.Rate != 92000000 {
		t.Fatal(earlier, e)
	}
	if _, e = fx.Rate(ctx, "BTC", "2026-11-05"); !errors.Is(e, md.ErrFXUnsupported) {
		t.Fatal(e)
	}
	budget, e := s.AVBudget(ctx, "alice", time.Now(), md.AVLimits{}.Defaults())
	if e != nil || budget.GlobalRemaining != 25 {
		t.Fatal("FX spent AV budget", budget, e)
	}
	for _, status := range []int{404, 422, 429, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			n := 0
			delay := time.Duration(0)
			f := &md.Frankfurter{HTTP: &http.Client{Transport: roundTrip(func(*http.Request) (*http.Response, error) {
				n++
				res := response(status, `[]`)
				if status == 503 {
					res.Header.Set("Retry-After", "45")
				}
				return res, nil
			})}, BaseURL: "https://mock.invalid", Provider: "ECB", Sleep: func(_ context.Context, d time.Duration) error { delay = d; return nil }}
			_, err := f.Rates(ctx, "USD", "2026-11-01", "2026-11-05")
			want := md.ErrUpstream
			if status == 404 || status == 422 {
				want = md.ErrFXUnavailable
			}
			if !errors.Is(err, want) {
				t.Fatal(err)
			}
			if status == 503 {
				if n != 2 || delay != 30*time.Second {
					t.Fatal(n, delay)
				}
			} else if n != 1 {
				t.Fatal(n)
			}
		})
	}
}

type fxFunc struct {
	rates func(context.Context, string, string, string) ([]md.FXRate, error)
}

func (f fxFunc) Currencies(context.Context) (map[string]bool, error) {
	return map[string]bool{"USD": true, "GBP": true, "CHF": true}, nil
}
func (f fxFunc) Rates(ctx context.Context, c, from, to string) ([]md.FXRate, error) {
	return f.rates(ctx, c, from, to)
}
func TestFXPublicationDatesAndRefresh(t *testing.T) {
	s := store(t)
	ctx := context.Background()
	berlin, _ := time.LoadLocation("Europe/Berlin")
	now := time.Date(2026, 11, 5, 13, 0, 0, 0, berlin) // Thursday, before the ECB publication
	// ECB working-day series; a row is published at 16:00 Berlin on its day.
	published := map[string]int64{"2025-12-22": 104000000, "2025-12-23": 104100000, "2025-12-24": 104200000, "2026-11-04": 118340000, "2026-11-05": 118400000, "2026-11-06": 118500000}
	var calls []string
	provider := fxFunc{rates: func(_ context.Context, c, from, to string) ([]md.FXRate, error) {
		calls = append(calls, c+" "+from+".."+to)
		out := []md.FXRate{{Date: from, Base: c, Quote: c, Rate: 100000000}} // identity record is skipped
		for d := from; d <= to; d = mustDay(t, d).AddDate(0, 0, 1).Format("2006-01-02") {
			if rate, ok := published[d]; ok && now.After(time.Date(mustDay(t, d).Year(), mustDay(t, d).Month(), mustDay(t, d).Day(), 16, 0, 0, 0, berlin)) {
				out = append(out, md.FXRate{Date: d, Base: c, Quote: "EUR", Rate: rate})
			}
		}
		return out, nil
	}}
	fx := &md.FXService{Store: s, Provider: provider, ProviderKey: "ECB", Now: func() time.Time { return now }}
	expect := func(ccy, date, want string, wantCalls int) {
		t.Helper()
		r, err := fx.Rate(ctx, ccy, date)
		if err != nil || r.Date != want || len(calls) != wantCalls {
			t.Fatalf("%s %s: rate=%+v err=%v calls=%v", ccy, date, r, err, calls)
		}
	}
	// Today before 16:00 uses the previous publication; pence use GBP.
	expect("GBp", "2026-11-05", "2026-11-04", 1)
	if calls[0] != "GBP 2026-10-26..2026-11-05" {
		t.Fatal(calls)
	}
	// After publication the same day, today's rate is fetched, continuing after the newest cached date.
	now = time.Date(2026, 11, 5, 17, 30, 0, 0, berlin)
	expect("GBX", "2026-11-05", "2026-11-05", 2)
	if calls[1] != "GBP 2026-11-05..2026-11-05" {
		t.Fatal(calls)
	}
	// A weekend uses Friday's publication once it exists, then is served from cache.
	now = time.Date(2026, 11, 7, 11, 0, 0, 0, berlin)
	expect("GBP", "2026-11-07", "2026-11-06", 3)
	if calls[2] != "GBP 2026-11-06..2026-11-07" {
		t.Fatal(calls)
	}
	expect("GBP", "2026-11-08", "2026-11-06", 3)
	expect("GBP", "2026-11-04", "2026-11-04", 3) // older rows are final
	// Holidays (25./26.12.) use the last publication before them, with fx_rate_date set to it.
	now = time.Date(2025, 12, 29, 10, 0, 0, 0, berlin)
	expect("CHF", "2025-12-26", "2025-12-24", 4)
	expect("CHF", "2025-12-25", "2025-12-24", 4)
	q, err := fx.Rate(ctx, "CHF", "2025-12-26")
	if err != nil || q.Rate != 104200000 {
		t.Fatal(q, err)
	}
	// More than seven days without a publication is unavailable.
	now = time.Date(2026, 11, 16, 10, 0, 0, 0, berlin)
	if _, err = fx.Rate(ctx, "GBP", "2026-11-14"); !errors.Is(err, md.ErrFXUnavailable) || calls[len(calls)-1] != "GBP 2026-11-07..2026-11-14" {
		t.Fatal("stale rate accepted", err, calls)
	}
	// A row published today may be re-fetched; the replacement is stored.
	now = time.Date(2026, 11, 5, 18, 0, 0, 0, berlin)
	if err = s.SaveFX(ctx, []md.FXRate{{Date: "2026-11-05", Base: "USD", Quote: "EUR", Rate: 92000000}}, "ECB", now); err != nil {
		t.Fatal(err)
	}
	published["2026-11-05"] = 92310000
	r, err := fx.Rate(ctx, "USD", "2026-11-05")
	if err != nil || r.Rate != 92310000 || calls[len(calls)-1] != "USD 2026-11-05..2026-11-05" {
		t.Fatal(r, err, calls)
	}
}

func mustDay(t *testing.T, s string) time.Time {
	t.Helper()
	d, err := time.Parse("2006-01-02", s)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestAVHTTPKeyIsNeverReturned(t *testing.T) {
	secret := "private-key-never-log"
	client := &md.AVHTTP{BaseURL: "https://mock.invalid/query", Key: secret, HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.Query().Get("apikey") != secret || r.URL.Query().Get("function") != "GLOBAL_QUOTE" {
			t.Fatal(r.URL.Path)
		}
		return nil, fmt.Errorf("transport failed: %s", r.URL)
	})}}
	_, err := client.Call(context.Background(), "GLOBAL_QUOTE", "SAP")
	if !errors.Is(err, md.ErrUpstream) || strings.Contains(err.Error(), secret) {
		t.Fatal("key leaked", err)
	}
}
