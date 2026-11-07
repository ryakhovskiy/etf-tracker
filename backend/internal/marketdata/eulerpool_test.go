package marketdata

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile("../../../testdata/marketdata/eulerpool/" + name + ".json")
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEPParsersReadRecordedFixtures(t *testing.T) {
	hits, err := ParseEPSearch(fixture(t, "equity-search-keyword"), 10)
	if err != nil || len(hits) == 0 || len(hits) > 10 || hits[0].ISIN != "IE00B4X9L533" || hits[0].Type != "ETF" || hits[0].Currency != "USD" {
		t.Fatalf("%+v %v", hits, err)
	}
	// One row per listing is merged by ISIN; null currency stays empty.
	hits, err = ParseEPSearch(fixture(t, "equity-search-isin"), 10)
	if err != nil || len(hits) != 1 || hits[0].Ticker != "H4ZJ.DE" || hits[0].Currency != "" {
		t.Fatalf("%+v %v", hits, err)
	}
	for _, name := range []string{"equity-search-empty", "equity-search-wkn"} {
		if hits, err = ParseEPSearch(fixture(t, name), 10); err != nil || len(hits) != 0 {
			t.Fatal(name, hits, err)
		}
	}
	// Types other than stock/etf are dropped.
	hits, _ = ParseEPSearch([]byte(`{"results":[{"name":"X","isin":"DE0007164600","ticker":"X","type":"crypto"},{"name":"SAP","isin":"DE0007164600","ticker":"SAP.DE","type":"stock","currency":"EUR"}]}`), 10)
	if len(hits) != 1 || hits[0].Type != "STOCK" || hits[0].Currency != "EUR" {
		t.Fatal(hits)
	}
	if _, err = ParseEPSearch([]byte(`nope`), 10); !errors.Is(err, ErrUpstream) {
		t.Fatal(err)
	}

	quotes, failures, err := ParseEPBulk(fixture(t, "quotes-bulk"))
	sap := quotes["DE0007164600"]
	if err != nil || sap.Price != "206.0500" || sap.Currency != "EUR" || sap.At.Unix() != 1793692800 || failures["IE00B4X9L533"] != "ISIN not found" || len(quotes) != 1 {
		t.Fatalf("%+v %v %v", quotes, failures, err)
	}
	if quotes, failures, err = ParseEPBulk(fixture(t, "quotes-bulk-unknown-isin")); err != nil || len(quotes) != 0 || len(failures) != 2 {
		t.Fatal(quotes, failures, err)
	}
	if quotes, _, err = ParseEPBulk(fixture(t, "quotes-bulk-mixed")); err != nil || quotes["US0378331005"].Currency != "USD" {
		t.Fatal(quotes, err)
	}

	price, at, ok, err := ParseEPSeries(fixture(t, "etf-quotes"))
	if err != nil || !ok || price != "36.6575" || at.UnixMilli() != 1793779200000 {
		t.Fatal(price, at, ok, err)
	}
	if _, _, ok, err = ParseEPSeries([]byte(`[]`)); err != nil || ok {
		t.Fatal("empty series", ok, err)
	}
	if _, _, _, err = ParseEPSeries(fixture(t, "etf-profile-unknown")); !errors.Is(err, ErrUpstream) {
		t.Fatal("error body parsed as series", err)
	}

	p, err := ParseEPProfile(fixture(t, "etf-profile"))
	if err != nil || p.ETFCompany != "HSBC" || p.ExpenseRatio != "0.15" || p.AUM != "14335960472" || p.Currency != "GBP" || p.InceptionDate != "2010-12-08" || p.DividendYield != "" || p.TrackingIndex != "MSCI World Index" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err = ParseEPProfile(fixture(t, "etf-profile-unknown")); !errors.Is(err, ErrUpstream) {
		t.Fatal("error body parsed as profile", err)
	}
}

type memStore struct {
	rows  []bool
	state map[string]string
}

func (m *memStore) RecordProviderRequest(_ context.Context, provider, _, _ string, _ time.Time, counted bool) error {
	m.rows = append(m.rows, counted)
	return nil
}
func (m *memStore) ProviderUsage(context.Context, string, time.Time) (int, error) {
	n := 0
	for _, c := range m.rows {
		if c {
			n++
		}
	}
	return n, nil
}
func (m *memStore) ProviderState(_ context.Context, _, k string) (string, error) {
	return m.state[k], nil
}
func (m *memStore) SetProviderState(_ context.Context, _, k, v string) error {
	m.state[k] = v
	return nil
}

type scripted struct {
	status int
	header http.Header
	body   []byte
	calls  int
}

func (s *scripted) Do(context.Context, string, string, []byte) (int, http.Header, []byte, error) {
	s.calls++
	return s.status, s.header, s.body, nil
}

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Set(kv[i], kv[i+1])
	}
	return h
}

func TestEPGatewayLedgerHeadersAndSoftLimit(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	store := &memStore{state: map[string]string{}}
	tr := &scripted{status: 200, body: fixture(t, "equity-search-empty"), header: hdr("X-RateLimit-Limit", "100000", "X-RateLimit-Remaining", "3", "X-RateLimit-Reset", strconv.FormatInt(now.Add(time.Hour).Unix(), 10), "X-RateLimit-Window", "month")}
	g := NewEPGateway(store, tr, 500)
	g.Now = func() time.Time { return now }
	if _, err := g.Search(ctx, "u", "hsbc"); err != nil {
		t.Fatal(err)
	}
	if store.state["remaining"] != "3" || store.state["window"] != "month" || store.state["limit"] != "100000" || len(store.rows) != 1 || !store.rows[0] {
		t.Fatalf("%v %v", store.state, store.rows)
	}
	b, _ := g.Budget(ctx)
	if b.Remaining != 3 {
		t.Fatalf("header must win over the soft limit: %+v", b)
	}
	// A lower soft limit wins over the header.
	g.SoftDaily = 1
	if b, _ = g.Budget(ctx); b.Remaining != 0 {
		t.Fatalf("soft limit ignored: %+v", b)
	}
	if _, err := g.Search(ctx, "u", "hsbc"); !errors.Is(err, ErrEPLimit) || tr.calls != 1 {
		t.Fatalf("called at zero budget: %v calls=%d", err, tr.calls)
	}
	// Once the header window has reset it no longer applies.
	g.SoftDaily = 500
	g.Now = func() time.Time { return now.Add(2 * time.Hour) }
	if b, _ = g.Budget(ctx); b.Remaining != 499 {
		t.Fatalf("expired header still applied: %+v", b)
	}
}

func TestEPGatewayStatuses(t *testing.T) {
	ctx := context.Background()
	now := time.Unix(1_800_000_000, 0)
	reset := now.Add(30 * time.Minute)
	store := &memStore{state: map[string]string{}}
	tr := &scripted{status: 429, header: hdr("X-RateLimit-Reset", strconv.FormatInt(reset.Unix(), 10)), body: []byte(`{}`)}
	g := NewEPGateway(store, tr, 500)
	g.Now = func() time.Time { return now }
	if _, err := g.Search(ctx, "u", "hsbc"); !errors.Is(err, ErrEPLimit) {
		t.Fatal(err)
	}
	tr.status = 200
	if _, err := g.Search(ctx, "u", "hsbc"); !errors.Is(err, ErrEPLimit) || tr.calls != 1 {
		t.Fatalf("429 did not block until reset: %v calls=%d", err, tr.calls)
	}
	g.Now = func() time.Time { return reset.Add(time.Second) }
	tr.body = fixture(t, "equity-search-empty")
	if _, err := g.Search(ctx, "u", "hsbc"); err != nil {
		t.Fatal("still blocked after reset", err)
	}

	// 404 carries no limit headers: recorded, not counted, and reported as not found.
	store = &memStore{state: map[string]string{}}
	tr = &scripted{status: 404, header: http.Header{}, body: fixture(t, "etf-profile-unknown")}
	g = NewEPGateway(store, tr, 500)
	if _, err := g.ETFProfile(ctx, "u", "XX0000000000"); !errors.Is(err, ErrEPNotFound) || len(store.rows) != 1 || store.rows[0] {
		t.Fatalf("%v %v", err, store.rows)
	}

	// 401 disables Eulerpool until restart and logs once.
	var logs bytes.Buffer
	tr = &scripted{status: 401, header: http.Header{}, body: []byte(`{"error":"Invalid API key"}`)}
	g = NewEPGateway(&memStore{state: map[string]string{}}, tr, 500)
	g.Log = slog.New(slog.NewTextHandler(&logs, nil))
	for range 3 {
		if _, err := g.Search(ctx, "u", "hsbc"); !errors.Is(err, ErrEPAuth) {
			t.Fatal(err)
		}
	}
	logged := strings.Count(logs.String(), "API key rejected")
	if tr.calls != 1 || logged != 1 {
		t.Fatalf("calls=%d logged=%d", tr.calls, logged)
	}
	if b, _ := g.Budget(ctx); !b.Disabled || b.Remaining != 0 {
		t.Fatal(b)
	}
}

func TestEPSendsBearerAndNoTokenInURL(t *testing.T) {
	var gotAuth, gotURL string
	srvClient := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		gotAuth, gotURL = r.Header.Get("Authorization"), r.URL.String()
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: nopCloser{strings.NewReader(`{}`)}}, nil
	})}
	c := &EPHTTP{HTTP: srvClient, BaseURL: "https://example.test/api/1", Key: "SECRET"}
	if _, _, _, err := c.Do(context.Background(), "GET", "/equity/search?q=x", nil); err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer SECRET" || strings.Contains(gotURL, "SECRET") || gotURL != "https://example.test/api/1/equity/search?q=x" {
		t.Fatal(gotAuth, gotURL)
	}
}

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

type nopCloser struct{ *strings.Reader }

func (nopCloser) Close() error { return nil }

func TestMockEPServesFixtures(t *testing.T) {
	g := NewEPGateway(&memStore{state: map[string]string{}}, MockEP{}, 500)
	ctx := context.Background()
	hits, err := g.Search(ctx, "u", "HSBC MSCI World")
	if err != nil || len(hits) == 0 {
		t.Fatal(hits, err)
	}
	quotes, failures, err := g.Bulk(ctx, "u", []string{"IE00B4X9L533", "DE000A0F5UH1", "DE0007164600"})
	if err != nil || len(quotes) != 1 || len(failures) != 2 {
		t.Fatal(quotes, failures, err)
	}
	if _, _, ok, err := g.ETFQuote(ctx, "u", "IE00B4X9L533"); err != nil || !ok {
		t.Fatal(ok, err)
	}
	if _, err = g.ETFProfile(ctx, "u", "XX0000000000"); !errors.Is(err, ErrEPNotFound) {
		t.Fatal(err)
	}
	if _, _, err = g.Bulk(ctx, "u", make([]string, 51)); err == nil {
		t.Fatal("51 ISINs accepted")
	}
}

func TestMinorUnits(t *testing.T) {
	for c, want := range map[string]string{"GBp": "GBP", "GBX": "GBP", "ZAc": "ZAR", "USD": "USD"} {
		if MajorUnit(c) != want {
			t.Fatal(c)
		}
	}
	q := &Quote{OrigPrice: "3665.0000", Currency: "GBp"}
	if err := ApplyFX(q, &FXRate{Date: "2026-11-05", Base: "GBP", Quote: "EUR", Rate: 115000000}); err != nil || q.Price != "42.1475" {
		t.Fatal(q, err)
	}
}

func TestUpstreamFailuresAreLoggedWithoutSecrets(t *testing.T) {
	ctx := context.Background()
	var logs bytes.Buffer
	log := slog.New(slog.NewTextHandler(&logs, nil))
	reply := func(status int, body string) *http.Client {
		return &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: nopCloser{strings.NewReader(body)}}, nil
		})}
	}
	broken := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) { return nil, errors.New("dial tcp: connection refused") })}

	// Eulerpool: error status with body, and a network failure.
	ep := &EPHTTP{HTTP: reply(500, `{"error":"boom"}`), BaseURL: "https://example.test/api/1", Key: "EPSECRET", Log: log}
	ep.Do(ctx, "GET", "/equity/search?q=x", nil)
	ep.HTTP = broken
	ep.Do(ctx, "GET", "/etf/profile/IE00B4X9L533", nil)
	// Alpha Vantage: the API key sits in the URL and must not reach the log.
	av := &AVHTTP{HTTP: broken, BaseURL: "https://av.example.test/query", Key: "AVSECRET", Log: log}
	av.Call(ctx, "GLOBAL_QUOTE", "AAPL")
	av.HTTP = reply(503, "unavailable")
	av.Call(ctx, "GLOBAL_QUOTE", "AAPL")
	// OpenFIGI and Frankfurter.
	figi := &Client{HTTP: reply(500, "nope"), BaseURL: "https://figi.example.test", APIKey: "FIGISECRET", Log: log}
	figi.Search(ctx, SearchRequest{Query: "apple", ExchCode: "GR"})
	fx := &Frankfurter{HTTP: reply(500, "fx down"), BaseURL: "https://fx.example.test", Provider: "ECB", Log: log}
	fx.get(ctx, "/rates", &struct{}{})

	out := logs.String()
	for _, want := range []string{"provider=EULERPOOL", "status=500", `body="{\"error\":\"boom\"}"`, "connection refused", "provider=ALPHAVANTAGE", "function=GLOBAL_QUOTE", "status=503", "provider=OPENFIGI", "provider=FRANKFURTER", "fx down", "path=/api/1/equity/search"} {
		if !strings.Contains(out, want) {
			t.Errorf("log lacks %q:\n%s", want, out)
		}
	}
	for _, secret := range []string{"EPSECRET", "AVSECRET", "FIGISECRET", "apikey"} {
		if strings.Contains(out, secret) {
			t.Errorf("log leaks %q:\n%s", secret, out)
		}
	}
}

func TestReconcileETFPriceRescalesByFactor100(t *testing.T) {
	for _, tc := range []struct {
		price, nav, want string
		changed          bool
	}{
		{"0.6973", "68.41", "69.7300", true},   // IE00B8GKDB10: series 100x too small
		{"0.5757", "56.93", "57.5700", true},   // IE00B3VVMM84
		{"36.6575", "36.67", "36.6575", false}, // HSBC: consistent
		{"3665.00", "36.67", "36.6500", true},  // 100x too large
		{"40.0000", "", "40.0000", false},      // no NAV: untouched
		{"10.0000", "100", "10.0000", false},   // ambiguous: not near either scale...
	} {
		got, changed := ReconcileETFPrice(tc.price, tc.nav)
		if tc.price == "10.0000" {
			// 10 vs NAV 100: x10 is not a power-of-100 mismatch; only exact 100x fixes apply.
			if changed {
				t.Fatalf("%+v -> %s", tc, got)
			}
			continue
		}
		if got != tc.want || changed != tc.changed {
			t.Fatalf("%+v -> %s %v", tc, got, changed)
		}
	}
}

func TestOutboundCallsAreLoggedWithSecretsScrubbed(t *testing.T) {
	var logs bytes.Buffer
	c := NewLoggedClient(time.Second, slog.New(slog.NewTextHandler(&logs, nil)))
	c.Transport = loggingTransport{log: slog.New(slog.NewTextHandler(&logs, nil)), base: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: nopCloser{strings.NewReader(`{}`)}}, nil
	})}
	for _, u := range []string{
		"https://api.example.test/api/1/equity/search?q=US0378331005&token=eu_prod_SECRET",
		"https://www.alphavantage.example/query?function=GLOBAL_QUOTE&apikey=AVSECRET&symbol=IBM",
		"https://user:PASSWORD@oauth.example/token?client_secret=CS&code=AUTHCODE&Access_Token=AT",
	} {
		resp, err := c.Get(u)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
	}
	out := logs.String()
	for _, want := range []string{"calling url: https://api.example.test/api/1/equity/search?q=US0378331005&token=***", "function=GLOBAL_QUOTE&apikey=***&symbol=IBM", "client_secret=***&code=***&Access_Token=***"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	for _, secret := range []string{"eu_prod_SECRET", "AVSECRET", "PASSWORD", "AUTHCODE", "=CS"} {
		if strings.Contains(out, secret) {
			t.Errorf("leaked %q:\n%s", secret, out)
		}
	}
}
