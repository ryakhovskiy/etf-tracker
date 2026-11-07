package marketdata

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func yfixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/marketdata/yahoo/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestYahooParsersReadRecordedFixtures(t *testing.T) {
	hits, err := ParseYahooSearch(yfixture(t, "search-isin-etf"))
	if err != nil || len(hits) != 1 || hits[0].Symbol != "VHYD.L" || hits[0].Type != "ETF" || !strings.Contains(hits[0].Name, "Vanguard FTSE All-World High Dividend") {
		t.Fatalf("%+v %v", hits, err)
	}
	if hits, _ = ParseYahooSearch(yfixture(t, "search-empty")); len(hits) != 0 {
		t.Fatal(hits)
	}
	// Only equities and ETFs are usable.
	hits, _ = ParseYahooSearch([]byte(`{"quotes":[{"symbol":"BTC-USD","quoteType":"CRYPTOCURRENCY"},{"symbol":"SAP.DE","quoteType":"EQUITY","shortname":"SAP  SE"}]}`))
	if len(hits) != 1 || hits[0].Symbol != "SAP.DE" || hits[0].Name != "SAP SE" {
		t.Fatal(hits)
	}

	quotes, err := ParseYahooQuotes(yfixture(t, "quote-multi"))
	if err != nil || len(quotes) != 3 || quotes["VHYD.L"].Currency != "USD" || quotes["VHYD.L"].Price != "91.7850" || quotes["SAP.DE"].At.Unix() != 1793983149 {
		t.Fatalf("%+v %v", quotes, err)
	}
	if _, err = ParseYahooQuotes([]byte(`{"quoteResponse":{"result":null,"error":{"code":"x"}}}`)); !errors.Is(err, ErrUpstream) {
		t.Fatal(err)
	}

	s, err := ParseYahooChart(yfixture(t, "chart-sap"))
	if err != nil || s.Symbol != "SAP.DE" || s.Currency != "EUR" || len(s.Points) != 10 || s.Points[0] != (YahooPoint{"2026-10-26", "212.2500"}) || s.Points[9] != (YahooPoint{"2026-11-06", "210.7500"}) {
		t.Fatalf("%+v %v", s, err)
	}
	if s.Points[2].Close != "211.2000" { // 211.1999969482422 rounds half-up at 4 places
		t.Fatal(s.Points[2])
	}
	if p, ok := s.LastCloseOnOrBefore("2026-11-01"); !ok || p.Day != "2026-10-30" {
		t.Fatalf("weekend must resolve to Friday: %+v %v", p, ok)
	}
	if _, ok := s.LastCloseOnOrBefore("2026-01-01"); ok {
		t.Fatal("no close before the series starts")
	}
	if _, err = ParseYahooChart(yfixture(t, "chart-not-found")); !errors.Is(err, ErrYahooNotFound) {
		t.Fatal(err)
	}
	// Missing bars (null closes) are skipped, and days follow the exchange time zone.
	s, err = ParseYahooChart([]byte(`{"chart":{"result":[{"meta":{"symbol":"7203.T","currency":"JPY","exchangeTimezoneName":"Asia/Tokyo"},"timestamp":[1793970000,1794056400],"indicators":{"quote":[{"close":[null,2500.5]}]}}],"error":null}}`))
	if err != nil || len(s.Points) != 1 || s.Points[0] != (YahooPoint{"2026-11-07", "2500.5000"}) {
		t.Fatalf("%+v %v", s, err)
	}
}

// fakeYahoo is a small Yahoo imitation that enforces the real rules: a
// User-Agent, the A3 cookie for the crumb, and the crumb for quote requests.
type fakeYahoo struct {
	mu          sync.Mutex
	crumb       string
	crumbSeq    int
	cookies     int
	crumbCalls  int
	quoteCalls  int
	tooManyBody bool // answer 429 to everything
	badFirst    bool // first getcrumb answers 200 with an error text
	*httptest.Server
}

func newFakeYahoo(t *testing.T) *fakeYahoo {
	f := &fakeYahoo{}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		if r.UserAgent() == "" || strings.HasPrefix(r.UserAgent(), "Go-http-client") {
			w.WriteHeader(429)
			w.Write([]byte("Edge: Too Many Requests"))
			return
		}
		if f.tooManyBody {
			w.WriteHeader(429)
			w.Write([]byte("Edge: Too Many Requests"))
			return
		}
		_, cookieErr := r.Cookie("A3")
		switch r.URL.Path {
		case "/":
			f.cookies++
			http.SetCookie(w, &http.Cookie{Name: "A3", Value: "session", Path: "/", Secure: false})
			w.WriteHeader(404)
		case "/v1/test/getcrumb":
			f.crumbCalls++
			if cookieErr != nil {
				w.WriteHeader(401)
				w.Write([]byte(`{"finance":{"error":{"description":"Invalid Cookie"}}}`))
				return
			}
			if f.badFirst && f.crumbCalls == 1 {
				w.Write([]byte("Too Many Requests"))
				return
			}
			f.crumbSeq++
			f.crumb = "crumb." + string(rune('A'+f.crumbSeq)) + "-123"
			w.Write([]byte(f.crumb))
		case "/v7/finance/quote":
			f.quoteCalls++
			if cookieErr != nil || r.URL.Query().Get("crumb") != f.crumb || f.crumb == "" {
				w.WriteHeader(401)
				w.Write([]byte(`{"finance":{"result":null,"error":{"code":"Unauthorized"}}}`))
				return
			}
			w.Write(yfixture(t, "quote-multi"))
		case "/v1/finance/search":
			w.Write(yfixture(t, "search-isin-etf"))
		default:
			if strings.HasPrefix(r.URL.Path, "/v8/finance/chart/") {
				if r.URL.Query().Get("period1") == "" || r.URL.Query().Get("interval") != "1d" {
					w.WriteHeader(400)
					return
				}
				w.Write(yfixture(t, "chart-sap"))
				return
			}
			w.WriteHeader(404)
		}
	}))
	t.Cleanup(f.Close)
	return f
}

func yahooGateway(t *testing.T, f *fakeYahoo, logs *bytes.Buffer) (*YahooGateway, *memStore) {
	store := &memStore{state: map[string]string{}}
	var log *slog.Logger
	if logs != nil {
		log = slog.New(slog.NewTextHandler(logs, nil))
	}
	g := NewYahooGateway(store, NewYahooHTTP(5*time.Second, log, ""), 1000, 0, time.Hour)
	g.CookieURL, g.QueryURL, g.FallbackURL = f.URL, f.URL, f.URL
	g.Log = log
	return g, store
}

func TestYahooSessionGetsCookieThenCrumbOnceAndReusesIt(t *testing.T) {
	f := newFakeYahoo(t)
	var logs bytes.Buffer
	g, _ := yahooGateway(t, f, &logs)
	ctx := context.Background()
	for range 3 {
		quotes, err := g.Quotes(ctx, "u", []string{"SAP.DE", "VHYD.L"})
		if err != nil || len(quotes) != 3 {
			t.Fatal(quotes, err)
		}
	}
	if f.cookies != 1 || f.crumbCalls != 1 || f.quoteCalls != 3 {
		t.Fatalf("session built %d/%d times, quote calls %d", f.cookies, f.crumbCalls, f.quoteCalls)
	}
	// The crumb never reaches the log in clear text.
	if strings.Contains(logs.String(), "crumb.B-123") || !strings.Contains(logs.String(), "crumb=***") {
		t.Fatalf("crumb handling in log:\n%s", logs.String())
	}
}

func TestYahooRebuildsSessionWhenCrumbIsRejected(t *testing.T) {
	f := newFakeYahoo(t)
	g, store := yahooGateway(t, f, nil)
	ctx := context.Background()
	if _, err := g.Quotes(ctx, "u", []string{"SAP.DE"}); err != nil {
		t.Fatal(err)
	}
	// Yahoo rotates the crumb server-side: the next quote is 401 until a new crumb is fetched.
	f.mu.Lock()
	f.crumb = "rotated-elsewhere"
	f.mu.Unlock()
	quotes, err := g.Quotes(ctx, "u", []string{"SAP.DE"})
	if err != nil || len(quotes) == 0 {
		t.Fatalf("no recovery: %v %v", quotes, err)
	}
	if f.cookies != 2 || f.crumbCalls != 2 || f.quoteCalls != 3 {
		t.Fatalf("cookies=%d crumbs=%d quotes=%d", f.cookies, f.crumbCalls, f.quoteCalls)
	}
	// Every exchange, including cookie and crumb requests, is in the ledger.
	if len(store.rows) != 7 { // cookie, crumb, quote, quote(401), cookie, crumb, quote
		t.Fatalf("ledger rows: %d", len(store.rows))
	}
	// A persistent 401 stops after one rebuild instead of looping.
	f.mu.Lock()
	f.crumb = ""
	f.mu.Unlock()
	g.crumb = "stale"
	before := f.quoteCalls
	f.Server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v7/finance/quote" {
			w.WriteHeader(401)
			return
		}
		if r.URL.Path == "/" {
			http.SetCookie(w, &http.Cookie{Name: "A3", Value: "x", Path: "/"})
			w.WriteHeader(404)
			return
		}
		w.Write([]byte("fresh-crumb-1"))
	})
	if _, err = g.Quotes(ctx, "u", []string{"SAP.DE"}); !errors.Is(err, ErrYahooAuth) {
		t.Fatal(err)
	}
	if f.quoteCalls != before {
		t.Fatal("handler swap should have bypassed the counter")
	}
}

func TestYahooCrumbRejectsErrorTextAndFallsBackToSecondHost(t *testing.T) {
	f := newFakeYahoo(t)
	f.badFirst = true // first getcrumb answers 200 "Too Many Requests"
	g, _ := yahooGateway(t, f, nil)
	if _, err := g.Quotes(context.Background(), "u", []string{"SAP.DE"}); err != nil {
		t.Fatal("did not fall back to the second host:", err)
	}
	if f.crumbCalls != 2 || strings.Contains(g.crumb, " ") {
		t.Fatalf("crumbCalls=%d crumb=%q", f.crumbCalls, g.crumb)
	}
}

func TestYahooNeedsCookieForCrumbAndBrowserUserAgent(t *testing.T) {
	f := newFakeYahoo(t)
	// Cookie host that sets no cookie: the crumb request is 401 and the session fails cleanly.
	g, _ := yahooGateway(t, f, nil)
	g.CookieURL = f.URL + "/nocookie"
	if _, err := g.Quotes(context.Background(), "u", []string{"SAP.DE"}); !errors.Is(err, ErrYahooAuth) {
		t.Fatal(err)
	}
	// The live transport always sends a browser User-Agent.
	var seen string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { seen = r.UserAgent() }))
	defer srv.Close()
	if _, _, _, err := NewYahooHTTP(time.Second, nil, "").Get(context.Background(), srv.URL); err != nil || !strings.Contains(seen, "Mozilla/5.0") {
		t.Fatal(seen, err)
	}
}

func TestYahooRateLimitStartsCooldownWithoutFurtherRequests(t *testing.T) {
	f := newFakeYahoo(t)
	f.tooManyBody = true
	g, store := yahooGateway(t, f, nil)
	ctx := context.Background()
	if _, err := g.Quotes(ctx, "u", []string{"SAP.DE"}); !errors.Is(err, ErrYahooLimit) {
		t.Fatal(err)
	}
	rows := len(store.rows)
	f.tooManyBody = false
	if _, err := g.Quotes(ctx, "u", []string{"SAP.DE"}); !errors.Is(err, ErrYahooLimit) || len(store.rows) != rows {
		t.Fatalf("request sent during cooldown: %v rows %d->%d", err, rows, len(store.rows))
	}
	if b, _ := g.Budget(ctx); b.BlockedUntil == nil || b.Remaining != 0 {
		t.Fatal(b)
	}
	g.Now = func() time.Time { return time.Now().Add(2 * time.Hour) }
	if _, err := g.Quotes(ctx, "u", []string{"SAP.DE"}); err != nil {
		t.Fatal("still blocked after the cooldown", err)
	}
}

func TestYahooSoftLimitAndOperations(t *testing.T) {
	f := newFakeYahoo(t)
	g, _ := yahooGateway(t, f, nil)
	ctx := context.Background()
	sym, err := g.ResolveSymbol(ctx, "u", "IE00B8GKDB10")
	if err != nil || sym != "VHYD.L" {
		t.Fatal(sym, err)
	}
	from := time.Date(2026, 10, 26, 0, 0, 0, 0, time.UTC)
	s, err := g.History(ctx, "u", "SAP.DE", from, from.AddDate(0, 0, 11))
	if err != nil || len(s.Points) != 10 {
		t.Fatal(s, err)
	}
	if _, err = g.Quotes(ctx, "u", nil); err == nil {
		t.Fatal("empty batch accepted")
	}
	if _, err = g.Quotes(ctx, "u", make([]string, 41)); err == nil {
		t.Fatal("oversized batch accepted")
	}
	g.SoftDaily = 1
	if _, err = g.ResolveSymbol(ctx, "u", "IE00B8GKDB10"); !errors.Is(err, ErrYahooLimit) {
		t.Fatal("soft limit ignored", err)
	}
}

func TestMockYahooEnforcesTheCrumb(t *testing.T) {
	g := NewYahooGateway(&memStore{state: map[string]string{}}, MockYahoo{}, 100, 0, time.Hour)
	ctx := context.Background()
	quotes, err := g.Quotes(ctx, "u", []string{"SAP.DE", "VHYD.L", "NOPE.XX"})
	if err != nil || len(quotes) != 2 {
		t.Fatal(quotes, err)
	}
	if _, err = g.History(ctx, "u", "NOPE.XX", time.Now(), time.Now()); !errors.Is(err, ErrYahooNotFound) {
		t.Fatal(err)
	}
	if sym, err := g.ResolveSymbol(ctx, "u", "DE0007164600"); err != nil || sym != "SAP.DE" {
		t.Fatal(sym, err)
	}
	if sym, _ := g.ResolveSymbol(ctx, "u", "US0378331005"); sym != "" {
		t.Fatal(sym)
	}
	status, _, _, _ := MockYahoo{}.Get(ctx, "https://q.test/v7/finance/quote?symbols=SAP.DE&crumb=wrong")
	if status != 401 {
		t.Fatal(status)
	}
}
