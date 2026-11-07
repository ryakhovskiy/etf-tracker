package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const ProviderYahoo = "YAHOO"

var ErrYahooLimit = errors.New("YAHOO_LIMIT_REACHED")
var ErrYahooAuth = errors.New("YAHOO_AUTH_ERROR")
var ErrYahooNotFound = errors.New("YAHOO_NOT_FOUND")

const (
	// DefaultYahooUserAgent is required: Yahoo answers 429 "Edge: Too Many Requests" to clients without one.
	DefaultYahooUserAgent = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/124.0.0.0 Safari/537.36"
	YahooQuoteBatchSize   = 40
)

// YahooTransport performs one GET. The real implementation owns a cookie jar.
type YahooTransport interface {
	Get(ctx context.Context, rawURL string) (status int, header http.Header, body []byte, err error)
}

// YahooHTTP is the live transport: browser User-Agent, a cookie jar that keeps
// the A3 cookie between the cookie request and the crumb request, and redirects.
type YahooHTTP struct {
	mu        sync.Mutex
	client    *http.Client
	timeout   time.Duration
	log       *slog.Logger
	UserAgent string
}

func NewYahooHTTP(timeout time.Duration, log *slog.Logger, userAgent string) *YahooHTTP {
	if userAgent == "" {
		userAgent = DefaultYahooUserAgent
	}
	y := &YahooHTTP{timeout: timeout, log: log, UserAgent: userAgent}
	y.ResetCookies()
	return y
}

// ResetCookies drops every cookie so a new session starts from scratch.
func (y *YahooHTTP) ResetCookies() {
	jar, _ := cookiejar.New(nil)
	c := NewLoggedClient(y.timeout, y.log)
	c.Jar = jar
	y.mu.Lock()
	y.client = c
	y.mu.Unlock()
}

func (y *YahooHTTP) Get(ctx context.Context, rawURL string) (int, http.Header, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return 0, nil, nil, ErrUpstream
	}
	req.Header.Set("User-Agent", y.UserAgent)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")
	y.mu.Lock()
	client := y.client
	y.mu.Unlock()
	started := time.Now()
	res, err := client.Do(req)
	if err != nil {
		logUpstream(y.log, slog.LevelError, ProviderYahoo, "request failed", "elapsed", time.Since(started).Round(time.Millisecond).String(), "error", cleanErr(err))
		return 0, nil, nil, ErrUpstream
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 8<<20))
	if err != nil {
		logUpstream(y.log, slog.LevelError, ProviderYahoo, "reading response failed", "status", res.StatusCode, "error", cleanErr(err))
		return 0, nil, nil, ErrUpstream
	}
	return res.StatusCode, res.Header, b, nil
}

type YahooBudget struct {
	Remaining    int        `json:"remaining"`
	BlockedUntil *time.Time `json:"blockedUntil,omitempty"`
}

// YahooGateway is the only code that talks to Yahoo Finance. It owns the
// cookie + crumb session, paces requests, records every exchange in the ledger
// and stops for a cool-down after a 429.
type YahooGateway struct {
	Store       EPStore
	Transport   YahooTransport
	CookieURL   string // answers 404 but sets the A3 cookie
	QueryURL    string // query1: chart, quote, crumb
	FallbackURL string // query2: search, second crumb host
	SoftDaily   int
	MinInterval time.Duration
	Cooldown    time.Duration
	Now         func() time.Time
	Sleep       func(context.Context, time.Duration) error
	Log         *slog.Logger

	mu    sync.Mutex // serializes requests: the crumb belongs to one cookie session
	crumb string
	last  time.Time
}

func NewYahooGateway(store EPStore, t YahooTransport, softDaily int, minInterval, cooldown time.Duration) *YahooGateway {
	if softDaily <= 0 {
		softDaily = 1000
	}
	if cooldown <= 0 {
		cooldown = 15 * time.Minute
	}
	return &YahooGateway{Store: store, Transport: t, CookieURL: "https://fc.yahoo.com", QueryURL: "https://query1.finance.yahoo.com", FallbackURL: "https://query2.finance.yahoo.com",
		SoftDaily: softDaily, MinInterval: minInterval, Cooldown: cooldown, Now: time.Now, Sleep: waitContext}
}

func (g *YahooGateway) Budget(ctx context.Context) (YahooBudget, error) {
	b := YahooBudget{}
	used, err := g.Store.ProviderUsage(ctx, ProviderYahoo, g.Now().Add(-24*time.Hour))
	if err != nil {
		return b, err
	}
	b.Remaining = max(0, g.SoftDaily-used)
	v, err := g.Store.ProviderState(ctx, ProviderYahoo, "blocked_until")
	if err != nil {
		return b, err
	}
	if t, e := time.Parse(time.RFC3339Nano, v); e == nil && t.After(g.Now()) {
		b.BlockedUntil = &t
		b.Remaining = 0
	}
	return b, nil
}

// crumbPattern accepts what Yahoo hands out (letters, digits and . _ - /) and
// rejects error pages and "Too Many Requests" text, which contain spaces or markup.
var crumbPattern = regexp.MustCompile(`^[A-Za-z0-9._/\-]{4,64}$`)

// get performs one counted, paced exchange. Callers hold g.mu.
func (g *YahooGateway) get(ctx context.Context, user, function, rawURL string) (int, []byte, error) {
	b, err := g.Budget(ctx)
	if err != nil {
		return 0, nil, err
	}
	if b.Remaining <= 0 {
		logUpstream(g.Log, slog.LevelWarn, ProviderYahoo, "request refused locally: budget exhausted or cooling down", "function", function, "blockedUntil", b.BlockedUntil)
		return 0, nil, ErrYahooLimit
	}
	if wait := g.MinInterval - g.Now().Sub(g.last); wait > 0 && !g.last.IsZero() {
		if err = g.Sleep(ctx, wait); err != nil {
			return 0, nil, err
		}
	}
	status, _, body, err := g.Transport.Get(ctx, rawURL)
	g.last = g.Now()
	if err != nil {
		return 0, nil, ErrUpstream
	}
	if e := g.Store.RecordProviderRequest(ctx, ProviderYahoo, user, function, g.Now(), true); e != nil {
		return 0, nil, e
	}
	if status == http.StatusTooManyRequests {
		until := g.Now().Add(g.Cooldown)
		logUpstream(g.Log, slog.LevelWarn, ProviderYahoo, "rate limited (429); cooling down", "function", function, "until", until.Format(time.RFC3339), "body", snippet(body))
		if e := g.Store.SetProviderState(ctx, ProviderYahoo, "blocked_until", until.UTC().Format(time.RFC3339Nano)); e != nil {
			return 0, nil, e
		}
		return status, body, ErrYahooLimit
	}
	return status, body, nil
}

// refreshCrumb starts a new session: fresh cookie jar, A3 cookie from the cookie
// host (its 404 is expected), then the crumb text from query1, falling back to query2.
func (g *YahooGateway) refreshCrumb(ctx context.Context, user string) error {
	g.crumb = ""
	if r, ok := g.Transport.(interface{ ResetCookies() }); ok {
		r.ResetCookies()
	}
	if _, _, err := g.get(ctx, user, "cookie", g.CookieURL+"/"); err != nil {
		return err
	}
	var lastStatus int
	for _, host := range []string{g.QueryURL, g.FallbackURL} {
		status, body, err := g.get(ctx, user, "crumb", host+"/v1/test/getcrumb")
		if err != nil {
			return err
		}
		lastStatus = status
		crumb := strings.TrimSpace(string(body))
		if status == http.StatusOK && crumbPattern.MatchString(crumb) {
			g.crumb = crumb
			return nil
		}
		logUpstream(g.Log, slog.LevelWarn, ProviderYahoo, "crumb request did not yield a crumb", "host", host, "status", status, "body", snippet(body))
	}
	logUpstream(g.Log, slog.LevelError, ProviderYahoo, "could not obtain a crumb", "lastStatus", lastStatus)
	return ErrYahooAuth
}

// call sends a request with the session crumb. A 401/403 means the crumb or
// cookie expired: the session is rebuilt once and the request retried once.
func (g *YahooGateway) call(ctx context.Context, user, function, host, path string, q url.Values) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for attempt := 0; ; attempt++ {
		if g.crumb == "" {
			if err := g.refreshCrumb(ctx, user); err != nil {
				return nil, err
			}
		}
		q.Set("crumb", g.crumb)
		status, body, err := g.get(ctx, user, function, host+path+"?"+q.Encode())
		if err != nil {
			return body, err
		}
		switch {
		case status == http.StatusUnauthorized || status == http.StatusForbidden:
			logUpstream(g.Log, slog.LevelWarn, ProviderYahoo, "session rejected; rebuilding cookie and crumb", "function", function, "status", status, "attempt", attempt+1, "body", snippet(body))
			g.crumb = ""
			if attempt == 0 {
				continue
			}
			return nil, ErrYahooAuth
		case status == http.StatusNotFound:
			return body, ErrYahooNotFound
		case status >= 400:
			logUpstream(g.Log, slog.LevelError, ProviderYahoo, "error response", "function", function, "status", status, "body", snippet(body))
			return nil, ErrUpstream
		}
		return body, nil
	}
}

// ---- parsing ----

type YahooHit struct{ Symbol, Name, Type, Exchange string }

// ParseYahooSearch keeps equities and ETFs, in Yahoo's ranking order.
func ParseYahooSearch(b []byte) ([]YahooHit, error) {
	var raw struct {
		Quotes []struct {
			Symbol    string `json:"symbol"`
			QuoteType string `json:"quoteType"`
			Longname  string `json:"longname"`
			Shortname string `json:"shortname"`
			Exchange  string `json:"exchange"`
		} `json:"quotes"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil, ErrUpstream
	}
	out := []YahooHit{}
	for _, q := range raw.Quotes {
		if q.Symbol == "" || (q.QuoteType != "EQUITY" && q.QuoteType != "ETF") {
			continue
		}
		name := q.Longname
		if name == "" {
			name = strings.Join(strings.Fields(q.Shortname), " ")
		}
		out = append(out, YahooHit{Symbol: q.Symbol, Name: name, Type: q.QuoteType, Exchange: q.Exchange})
	}
	return out, nil
}

// YahooQuote is a latest price.
type YahooQuote struct {
	Symbol, Currency, Price string
	At                      time.Time
}

// ParseYahooQuotes reads v7/finance/quote. Unknown symbols are simply absent.
func ParseYahooQuotes(b []byte) (map[string]YahooQuote, error) {
	var raw struct {
		Response struct {
			Result []struct {
				Symbol   string      `json:"symbol"`
				Currency string      `json:"currency"`
				Price    json.Number `json:"regularMarketPrice"`
				Time     json.Number `json:"regularMarketTime"`
			} `json:"result"`
			Error *struct {
				Code, Description string
			} `json:"error"`
		} `json:"quoteResponse"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil, ErrUpstream
	}
	if raw.Response.Error != nil {
		return nil, ErrUpstream
	}
	out := map[string]YahooQuote{}
	for _, r := range raw.Response.Result {
		ts, e1 := r.Time.Int64()
		price, e2 := Scaled(r.Price.String(), 4)
		if e1 != nil || e2 != nil || ts <= 0 || len(r.Currency) < 3 || len(r.Currency) > 4 || r.Symbol == "" {
			continue
		}
		out[r.Symbol] = YahooQuote{Symbol: r.Symbol, Currency: r.Currency, Price: Fixed(price, 4), At: time.Unix(ts, 0)}
	}
	return out, nil
}

// YahooPoint is one daily close, dated in the exchange's time zone.
type YahooPoint struct{ Day, Close string }

type YahooSeries struct {
	Symbol, Currency string
	Points           []YahooPoint // ascending, nulls removed
}

// ParseYahooChart reads v8/finance/chart with interval=1d. The unadjusted
// close is used (split-adjusted but not dividend-adjusted: what was payable).
func ParseYahooChart(b []byte) (*YahooSeries, error) {
	var raw struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Symbol   string `json:"symbol"`
					Currency string `json:"currency"`
					Timezone string `json:"exchangeTimezoneName"`
				} `json:"meta"`
				Timestamp  []int64 `json:"timestamp"`
				Indicators struct {
					Quote []struct {
						Close []*json.Number `json:"close"`
					} `json:"quote"`
				} `json:"indicators"`
			} `json:"result"`
			Error *struct {
				Code        string `json:"code"`
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil, ErrUpstream
	}
	if e := raw.Chart.Error; e != nil {
		if strings.EqualFold(e.Code, "Not Found") {
			return nil, ErrYahooNotFound
		}
		return nil, fmt.Errorf("%w: %s", ErrUpstream, e.Code)
	}
	if len(raw.Chart.Result) != 1 {
		return nil, ErrYahooNotFound
	}
	r := raw.Chart.Result[0]
	s := &YahooSeries{Symbol: r.Meta.Symbol, Currency: r.Meta.Currency, Points: []YahooPoint{}}
	if len(s.Currency) < 3 || len(s.Currency) > 4 {
		return nil, ErrUpstream
	}
	loc := time.UTC
	if l, err := time.LoadLocation(r.Meta.Timezone); err == nil {
		loc = l
	}
	if len(r.Indicators.Quote) == 0 {
		return s, nil
	}
	closes := r.Indicators.Quote[0].Close
	if len(closes) != len(r.Timestamp) {
		return nil, ErrUpstream
	}
	for i, ts := range r.Timestamp {
		if closes[i] == nil {
			continue // holiday or missing bar
		}
		v, err := Scaled(closes[i].String(), 4)
		if err != nil {
			continue
		}
		s.Points = append(s.Points, YahooPoint{Day: time.Unix(ts, 0).In(loc).Format("2006-01-02"), Close: Fixed(v, 4)})
	}
	return s, nil
}

// ---- gateway operations ----

// ResolveSymbol maps an ISIN to the Yahoo symbol of its best listing.
func (g *YahooGateway) ResolveSymbol(ctx context.Context, user, isin string) (string, error) {
	b, err := g.call(ctx, user, "search", g.FallbackURL, "/v1/finance/search", url.Values{"q": {isin}, "quotesCount": {"5"}, "newsCount": {"0"}, "listsCount": {"0"}})
	if err != nil {
		if errors.Is(err, ErrYahooNotFound) {
			return "", nil
		}
		return "", err
	}
	hits, err := ParseYahooSearch(b)
	if err != nil {
		logUpstream(g.Log, slog.LevelError, ProviderYahoo, "unparseable search response", "body", snippet(b))
		return "", err
	}
	if len(hits) == 0 {
		return "", nil
	}
	return hits[0].Symbol, nil
}

// Quotes fetches the latest prices of up to YahooQuoteBatchSize symbols in one request.
func (g *YahooGateway) Quotes(ctx context.Context, user string, symbols []string) (map[string]YahooQuote, error) {
	if len(symbols) == 0 || len(symbols) > YahooQuoteBatchSize {
		return nil, fmt.Errorf("quotes need 1..%d symbols", YahooQuoteBatchSize)
	}
	b, err := g.call(ctx, user, "quote", g.QueryURL, "/v7/finance/quote", url.Values{"symbols": {strings.Join(symbols, ",")}})
	if err != nil {
		return nil, err
	}
	out, err := ParseYahooQuotes(b)
	if err != nil {
		logUpstream(g.Log, slog.LevelError, ProviderYahoo, "unparseable quote response", "body", snippet(b))
	}
	return out, err
}

// History fetches daily closes for the inclusive day range [from, to].
func (g *YahooGateway) History(ctx context.Context, user, symbol string, from, to time.Time) (*YahooSeries, error) {
	q := url.Values{"period1": {strconv.FormatInt(from.Unix(), 10)}, "period2": {strconv.FormatInt(to.Add(24*time.Hour).Unix(), 10)}, "interval": {"1d"}, "includeAdjustedClose": {"false"}}
	b, err := g.call(ctx, user, "chart", g.QueryURL, "/v8/finance/chart/"+url.PathEscape(symbol), q)
	if err != nil {
		return nil, err
	}
	s, err := ParseYahooChart(b)
	if err != nil && !errors.Is(err, ErrYahooNotFound) {
		logUpstream(g.Log, slog.LevelError, ProviderYahoo, "unparseable chart response", "symbol", symbol, "body", snippet(b))
	}
	return s, err
}

// LastCloseOnOrBefore returns the newest point whose day is <= day.
func (s *YahooSeries) LastCloseOnOrBefore(day string) (YahooPoint, bool) {
	var best YahooPoint
	found := false
	for _, p := range s.Points {
		if p.Day <= day && (!found || p.Day >= best.Day) {
			best, found = p, true
		}
	}
	return best, found
}

// MockYahoo answers from the recorded fixtures and emulates the crumb rules:
// the quote endpoint is Unauthorized without the mock crumb.
type MockYahoo struct{}

const mockCrumb = "mockcrumb1"

func (MockYahoo) Get(ctx context.Context, rawURL string) (int, http.Header, []byte, error) {
	read := func(name string) []byte {
		b, err := fixtures.ReadFile("testdata/marketdata/yahoo/" + name + ".json")
		if err != nil {
			panic(err)
		}
		return b
	}
	u, err := url.Parse(rawURL)
	if err != nil {
		return 0, nil, nil, err
	}
	h := http.Header{}
	q := u.Query()
	switch {
	case u.Path == "/" || u.Path == "":
		return 404, h, []byte("not found"), nil
	case u.Path == "/v1/test/getcrumb":
		return 200, h, []byte(mockCrumb), nil
	case u.Path == "/v1/finance/search":
		switch strings.ToUpper(q.Get("q")) {
		case "IE00B8GKDB10":
			return 200, h, read("search-isin-etf"), nil
		case "DE0007164600":
			return 200, h, read("search-isin-stock"), nil
		}
		return 200, h, read("search-empty"), nil
	case u.Path == "/v7/finance/quote":
		if q.Get("crumb") != mockCrumb {
			return 401, h, []byte(`{"finance":{"result":null,"error":{"code":"Unauthorized"}}}`), nil
		}
		var page struct {
			Response struct {
				Result []json.RawMessage `json:"result"`
			} `json:"quoteResponse"`
		}
		if json.Unmarshal(read("quote-multi"), &page) != nil {
			return 0, nil, nil, ErrUpstream
		}
		want := map[string]bool{}
		for _, s := range strings.Split(q.Get("symbols"), ",") {
			want[s] = true
		}
		keep := []json.RawMessage{}
		for _, raw := range page.Response.Result {
			var r struct {
				Symbol string `json:"symbol"`
			}
			_ = json.Unmarshal(raw, &r)
			if want[r.Symbol] {
				keep = append(keep, raw)
			}
		}
		b, _ := json.Marshal(map[string]any{"quoteResponse": map[string]any{"result": keep, "error": nil}})
		return 200, h, b, nil
	case strings.HasPrefix(u.Path, "/v8/finance/chart/"):
		switch strings.TrimPrefix(u.Path, "/v8/finance/chart/") {
		case "SAP.DE":
			return 200, h, read("chart-sap"), nil
		case "VHYD.L":
			return 200, h, read("chart-vhyd"), nil
		}
		return 404, h, read("chart-not-found"), nil
	}
	return 404, h, []byte("not found"), nil
}
