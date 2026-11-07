package marketdata

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ProviderEulerpool    = "EULERPOOL"
	ProviderAlphaVantage = "ALPHAVANTAGE"
)

var ErrEPLimit = errors.New("EULERPOOL_LIMIT_REACHED")
var ErrEPAuth = errors.New("EULERPOOL_AUTH_ERROR")

// ErrEPNotFound means Eulerpool answered 404 for a security.
var ErrEPNotFound = errors.New("EULERPOOL_NOT_FOUND")

// EPStore persists the Eulerpool request ledger and rate-limit state.
type EPStore interface {
	RecordProviderRequest(ctx context.Context, provider, user, function string, at time.Time, counted bool) error
	ProviderUsage(ctx context.Context, provider string, since time.Time) (int, error)
	ProviderState(ctx context.Context, provider, key string) (string, error)
	SetProviderState(ctx context.Context, provider, key, value string) error
}

// EPTransport performs one HTTP exchange with Eulerpool.
type EPTransport interface {
	Do(ctx context.Context, method, path string, body []byte) (status int, header http.Header, data []byte, err error)
}

type EPHTTP struct {
	HTTP         *http.Client
	BaseURL, Key string
	Log          *slog.Logger
}

func (c *EPHTTP) Do(ctx context.Context, method, path string, body []byte) (int, http.Header, []byte, error) {
	u, err := url.Parse(strings.TrimSuffix(c.BaseURL, "/") + path)
	if err != nil {
		return 0, nil, nil, ErrUpstream
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, u.String(), reader)
	if err != nil {
		return 0, nil, nil, ErrUpstream
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	started := time.Now()
	res, err := c.HTTP.Do(req)
	if err != nil {
		logUpstream(c.Log, slog.LevelError, ProviderEulerpool, "request failed", "method", method, "path", u.Path, "elapsed", time.Since(started).Round(time.Millisecond).String(), "error", cleanErr(err))
		return 0, nil, nil, ErrUpstream
	}
	defer res.Body.Close()
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		logUpstream(c.Log, slog.LevelError, ProviderEulerpool, "reading response failed", "method", method, "path", u.Path, "status", res.StatusCode, "error", cleanErr(err))
		return 0, nil, nil, ErrUpstream
	}
	if res.StatusCode >= 400 {
		level := slog.LevelWarn
		if res.StatusCode == http.StatusNotFound {
			level = slog.LevelInfo // unknown ISINs are routine
		}
		logUpstream(c.Log, level, ProviderEulerpool, "error response", "method", method, "path", u.Path, "status", res.StatusCode, "elapsed", time.Since(started).Round(time.Millisecond).String(), "rateLimitRemaining", res.Header.Get("X-RateLimit-Remaining"), "body", snippet(b))
	}
	return res.StatusCode, res.Header, b, nil
}

type EPBudget struct {
	Remaining    int        `json:"remaining"`
	HeaderLimit  int        `json:"limit,omitempty"`
	ResetAt      *time.Time `json:"resetAt,omitempty"`
	BlockedUntil *time.Time `json:"blockedUntil,omitempty"`
	Disabled     bool       `json:"disabled,omitempty"`
}

// EPGateway is the only code that talks to Eulerpool. It records every request,
// keeps the rate-limit headers, and refuses to call once the budget is spent.
type EPGateway struct {
	Store       EPStore
	Transport   EPTransport
	SoftDaily   int
	Now         func() time.Time
	Log         *slog.Logger
	mu          sync.Mutex
	authFailed  bool
	authLogOnce sync.Once
}

func NewEPGateway(store EPStore, t EPTransport, softDaily int) *EPGateway {
	if softDaily <= 0 {
		softDaily = 500
	}
	return &EPGateway{Store: store, Transport: t, SoftDaily: softDaily, Now: time.Now}
}

// Budget reports how many requests may still be sent: the lower of the header
// value (while its window is current) and the soft limit minus the last 24h.
func (g *EPGateway) Budget(ctx context.Context) (EPBudget, error) {
	now := g.Now()
	b := EPBudget{Disabled: g.authFailed}
	used, err := g.Store.ProviderUsage(ctx, ProviderEulerpool, now.Add(-24*time.Hour))
	if err != nil {
		return b, err
	}
	b.Remaining = max(0, g.SoftDaily-used)
	var resetAt time.Time
	if v, err := g.Store.ProviderState(ctx, ProviderEulerpool, "reset"); err != nil {
		return b, err
	} else if n, e := strconv.ParseInt(v, 10, 64); e == nil && n > 0 {
		resetAt = time.Unix(n, 0)
		b.ResetAt = &resetAt
	}
	if v, err := g.Store.ProviderState(ctx, ProviderEulerpool, "remaining"); err != nil {
		return b, err
	} else if n, e := strconv.Atoi(v); e == nil && (resetAt.IsZero() || resetAt.After(now)) {
		b.Remaining = min(b.Remaining, max(0, n))
	}
	if v, err := g.Store.ProviderState(ctx, ProviderEulerpool, "limit"); err == nil {
		b.HeaderLimit, _ = strconv.Atoi(v)
	}
	if v, err := g.Store.ProviderState(ctx, ProviderEulerpool, "blocked_until"); err != nil {
		return b, err
	} else if t, e := time.Parse(time.RFC3339Nano, v); e == nil && t.After(now) {
		b.BlockedUntil = &t
		b.Remaining = 0
	}
	if b.Disabled {
		b.Remaining = 0
	}
	return b, nil
}

// do sends one request. function labels the ledger row.
func (g *EPGateway) do(ctx context.Context, user, function, method, path string, body []byte) ([]byte, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.authFailed {
		return nil, ErrEPAuth
	}
	b, err := g.Budget(ctx)
	if err != nil {
		return nil, err
	}
	if b.BlockedUntil != nil || b.Remaining <= 0 {
		logUpstream(g.Log, slog.LevelWarn, ProviderEulerpool, "request refused locally: budget exhausted", "function", function, "remaining", b.Remaining, "blockedUntil", b.BlockedUntil)
		return nil, ErrEPLimit
	}
	status, header, data, err := g.Transport.Do(ctx, method, path, body)
	if err != nil {
		// The transport has already logged the cause.
		return nil, ErrUpstream
	}
	now := g.Now()
	counted := header.Get("X-RateLimit-Remaining") != ""
	if e := g.Store.RecordProviderRequest(ctx, ProviderEulerpool, user, function, now, counted); e != nil {
		return nil, e
	}
	for key, name := range map[string]string{"remaining": "X-RateLimit-Remaining", "reset": "X-RateLimit-Reset", "limit": "X-RateLimit-Limit", "window": "X-RateLimit-Window"} {
		if v := header.Get(name); v != "" {
			if e := g.Store.SetProviderState(ctx, ProviderEulerpool, key, v); e != nil {
				return nil, e
			}
		}
	}
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		g.authFailed = true
		g.authLogOnce.Do(func() {
			logUpstream(g.Log, slog.LevelError, ProviderEulerpool, "API key rejected; Eulerpool is disabled until restart", "function", function, "status", status)
		})
		return nil, ErrEPAuth
	case status == http.StatusTooManyRequests:
		until := now.Add(time.Hour)
		if n, e := strconv.ParseInt(header.Get("X-RateLimit-Reset"), 10, 64); e == nil && n > now.Unix() {
			until = time.Unix(n, 0)
		}
		logUpstream(g.Log, slog.LevelWarn, ProviderEulerpool, "rate limited (429); blocking requests", "function", function, "until", until.Format(time.RFC3339))
		if e := g.Store.SetProviderState(ctx, ProviderEulerpool, "blocked_until", until.UTC().Format(time.RFC3339Nano)); e != nil {
			return nil, e
		}
		return nil, ErrEPLimit
	case status == http.StatusNotFound:
		return data, ErrEPNotFound
	case status >= 400:
		return nil, ErrUpstream
	}
	return data, nil
}

// EPHit is one merged search result.
type EPHit struct {
	ISIN, Ticker, Name, Type, Currency string
}

// ParseEPSearch reads /equity/search, keeps stocks and ETFs, merges the
// per-listing rows by ISIN (first row wins) and returns at most limit hits.
func ParseEPSearch(b []byte, limit int) ([]EPHit, error) {
	var raw struct {
		Results []struct {
			Name     string  `json:"name"`
			ISIN     string  `json:"isin"`
			Ticker   string  `json:"ticker"`
			Type     string  `json:"type"`
			Currency *string `json:"currency"`
		} `json:"results"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil, ErrUpstream
	}
	out := []EPHit{}
	seen := map[string]bool{}
	for _, r := range raw.Results {
		isin := Normalize(r.ISIN)
		var typ string
		switch strings.ToLower(r.Type) {
		case "stock":
			typ = "STOCK"
		case "etf":
			typ = "ETF"
		}
		if typ == "" || !ValidISIN(isin) || strings.TrimSpace(r.Name) == "" || strings.TrimSpace(r.Ticker) == "" || seen[isin] {
			continue
		}
		seen[isin] = true
		h := EPHit{ISIN: isin, Ticker: strings.TrimSpace(r.Ticker), Name: strings.TrimSpace(r.Name), Type: typ}
		if r.Currency != nil && len(strings.TrimSpace(*r.Currency)) == 3 {
			h.Currency = strings.ToUpper(strings.TrimSpace(*r.Currency))
		}
		out = append(out, h)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

func (g *EPGateway) Search(ctx context.Context, user, q string) ([]EPHit, error) {
	b, err := g.do(ctx, user, "equity/search", http.MethodGet, "/equity/search?q="+url.QueryEscape(q), nil)
	if err != nil {
		return nil, err
	}
	hits, err := ParseEPSearch(b, 10)
	if err != nil {
		logUpstream(g.Log, slog.LevelError, ProviderEulerpool, "unparseable search response", "body", snippet(b))
	}
	return hits, err
}

// EPQuote is a parsed latest price with the currency it is quoted in.
type EPQuote struct {
	ISIN, Currency, Price string
	At                    time.Time
}

// EPQuoteBatchSize is the documented bulk maximum.
const EPQuoteBatchSize = 50

// ParseEPBulk reads /market/quotes/bulk. Timestamps are unix seconds.
func ParseEPBulk(b []byte) (map[string]EPQuote, map[string]string, error) {
	var raw struct {
		Quotes []struct {
			ISIN      string      `json:"isin"`
			Price     json.Number `json:"price"`
			Currency  string      `json:"currency"`
			Timestamp json.Number `json:"timestamp"`
		} `json:"quotes"`
		Errors []struct {
			ISIN  string `json:"isin"`
			Error string `json:"error"`
		} `json:"errors"`
	}
	if json.Unmarshal(b, &raw) != nil {
		return nil, nil, ErrUpstream
	}
	quotes, failures := map[string]EPQuote{}, map[string]string{}
	for _, q := range raw.Quotes {
		ts, err := q.Timestamp.Int64()
		price, perr := Scaled(q.Price.String(), 4)
		if err != nil || perr != nil || ts <= 0 || len(q.Currency) < 3 || len(q.Currency) > 4 {
			failures[Normalize(q.ISIN)] = "INVALID_QUOTE"
			continue
		}
		quotes[Normalize(q.ISIN)] = EPQuote{ISIN: Normalize(q.ISIN), Currency: q.Currency, Price: Fixed(price, 4), At: time.Unix(ts, 0)}
	}
	for _, e := range raw.Errors {
		failures[Normalize(e.ISIN)] = e.Error
	}
	return quotes, failures, nil
}

// Bulk prices up to EPQuoteBatchSize stock ISINs in one request.
func (g *EPGateway) Bulk(ctx context.Context, user string, isins []string) (map[string]EPQuote, map[string]string, error) {
	if len(isins) == 0 || len(isins) > EPQuoteBatchSize {
		return nil, nil, fmt.Errorf("bulk needs 1..%d ISINs", EPQuoteBatchSize)
	}
	body, _ := json.Marshal(map[string][]string{"isins": isins})
	b, err := g.do(ctx, user, "market/quotes/bulk", http.MethodPost, "/market/quotes/bulk", body)
	if err != nil {
		return nil, nil, err
	}
	quotes, failures, err := ParseEPBulk(b)
	if err != nil {
		logUpstream(g.Log, slog.LevelError, ProviderEulerpool, "unparseable bulk response", "body", snippet(b))
	}
	for isin, reason := range failures {
		logUpstream(g.Log, slog.LevelInfo, ProviderEulerpool, "bulk quote unavailable", "isin", isin, "reason", reason)
	}
	return quotes, failures, err
}

// ParseEPSeries returns the last point of an /etf/quotes history
// ([[ms_timestamp, price], ...]); ok is false for an empty series.
func ParseEPSeries(b []byte) (price string, at time.Time, ok bool, err error) {
	var points [][]json.Number
	if json.Unmarshal(b, &points) != nil {
		return "", time.Time{}, false, ErrUpstream
	}
	var best int64
	for _, p := range points {
		if len(p) != 2 {
			return "", time.Time{}, false, ErrUpstream
		}
		ms, e1 := p[0].Int64()
		v, e2 := Scaled(p[1].String(), 4)
		if e1 != nil || e2 != nil {
			continue
		}
		if ms >= best {
			best, price, ok = ms, Fixed(v, 4), true
		}
	}
	return price, time.UnixMilli(best), ok, nil
}

// ETFQuote fetches the daily series of the last 10 days and returns its last point.
// The series carries no currency; the caller supplies it from the ETF profile.
func (g *EPGateway) ETFQuote(ctx context.Context, user, isin string) (price string, at time.Time, ok bool, err error) {
	start := g.Now().AddDate(0, 0, -10).UnixMilli()
	b, err := g.do(ctx, user, "etf/quotes", http.MethodGet, fmt.Sprintf("/etf/quotes/%s?startdate=%d", url.PathEscape(isin), start), nil)
	if err != nil {
		return "", time.Time{}, false, err
	}
	price, at, ok, err = ParseEPSeries(b)
	if err != nil {
		logUpstream(g.Log, slog.LevelError, ProviderEulerpool, "unparseable ETF series", "isin", isin, "body", snippet(b))
	} else if !ok {
		logUpstream(g.Log, slog.LevelWarn, ProviderEulerpool, "ETF series is empty", "isin", isin)
	}
	return price, at, ok, err
}

// ETFProfile is the parsed /etf/profile response.
type ETFProfile struct {
	ISIN          string          `json:"isin"`
	Symbol        string          `json:"symbol,omitempty"`
	Name          string          `json:"name,omitempty"`
	ETFCompany    string          `json:"etfCompany"`
	TrackingIndex string          `json:"trackingIndex"`
	AssetClass    string          `json:"assetClass"`
	Domicile      string          `json:"domicile"`
	ExpenseRatio  string          `json:"expenseRatio"`
	AUM           string          `json:"-"`
	AUMEUR        string          `json:"aumEur"`
	DividendYield string          `json:"dividendYield"`
	InceptionDate string          `json:"inceptionDate"`
	Currency      string          `json:"currency"`
	Website       string          `json:"website"`
	FetchedAt     time.Time       `json:"fetchedAt"`
	Raw           json.RawMessage `json:"-"`
}

func decimalOrEmpty(n *json.Number) string {
	if n == nil {
		return ""
	}
	r, ok := new(big.Rat).SetString(n.String())
	if !ok {
		return ""
	}
	return strings.TrimRight(strings.TrimRight(r.FloatString(6), "0"), ".")
}

// ParseEPProfile reads /etf/profile/{isin}.
func ParseEPProfile(b []byte) (*ETFProfile, error) {
	var raw struct {
		ISIN          string       `json:"isin"`
		Symbol        string       `json:"symbol"`
		Name          string       `json:"name"`
		ETFCompany    string       `json:"etfCompany"`
		ExpenseRatio  *json.Number `json:"expenseRatio"`
		AUM           *json.Number `json:"aum"`
		Currency      string       `json:"currency"`
		InceptionDate string       `json:"inceptionDate"`
		Domicile      string       `json:"domicile"`
		TrackingIndex string       `json:"trackingIndex"`
		AssetClass    string       `json:"assetClass"`
		DividendYield *json.Number `json:"dividendYield"`
		Website       string       `json:"website"`
	}
	if json.Unmarshal(b, &raw) != nil || !ValidISIN(Normalize(raw.ISIN)) {
		return nil, ErrUpstream
	}
	p := &ETFProfile{ISIN: Normalize(raw.ISIN), Symbol: raw.Symbol, Name: raw.Name, ETFCompany: raw.ETFCompany, TrackingIndex: raw.TrackingIndex, AssetClass: raw.AssetClass, Domicile: raw.Domicile, ExpenseRatio: decimalOrEmpty(raw.ExpenseRatio), AUM: decimalOrEmpty(raw.AUM), DividendYield: decimalOrEmpty(raw.DividendYield), Website: raw.Website, Raw: append(json.RawMessage(nil), b...)}
	if len(raw.Currency) >= 3 && len(raw.Currency) <= 4 {
		p.Currency = raw.Currency
	}
	if _, err := time.Parse("2006-01-02", raw.InceptionDate); err == nil {
		p.InceptionDate = raw.InceptionDate
	}
	return p, nil
}

func (g *EPGateway) ETFProfile(ctx context.Context, user, isin string) (*ETFProfile, error) {
	b, err := g.do(ctx, user, "etf/profile", http.MethodGet, "/etf/profile/"+url.PathEscape(isin), nil)
	if err != nil {
		return nil, err
	}
	p, err := ParseEPProfile(b)
	if err != nil {
		logUpstream(g.Log, slog.LevelError, ProviderEulerpool, "unparseable ETF profile", "isin", isin, "body", snippet(b))
		return nil, err
	}
	p.FetchedAt = g.Now()
	return p, nil
}

// MockEP answers from the recorded step-zero fixtures and never touches the network.
type MockEP struct{}

func (MockEP) Do(ctx context.Context, method, path string, body []byte) (int, http.Header, []byte, error) {
	h := http.Header{}
	h.Set("X-RateLimit-Limit", "100000")
	h.Set("X-RateLimit-Remaining", "99000")
	h.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().AddDate(0, 1, 0).Unix(), 10))
	h.Set("X-RateLimit-Window", "month")
	read := func(name string) []byte {
		b, err := fixtures.ReadFile("testdata/marketdata/eulerpool/" + name + ".json")
		if err != nil {
			panic(err)
		}
		return b
	}
	notFound := func() (int, http.Header, []byte, error) {
		return 404, h, read("etf-profile-unknown"), nil
	}
	u, err := url.Parse(path)
	if err != nil {
		return 0, nil, nil, err
	}
	switch {
	case method == http.MethodGet && u.Path == "/equity/search":
		q := strings.ToLower(strings.TrimSpace(u.Query().Get("q")))
		switch {
		case strings.EqualFold(q, "IE00B4X9L533"):
			return 200, h, read("equity-search-isin"), nil
		case strings.Contains(q, "hsbc"):
			return 200, h, read("equity-search-keyword"), nil
		case q == "sap" || q == "sap se":
			return 200, h, []byte(`{"count":1,"results":[{"name":"SAP SE","isin":"DE0007164600","ticker":"SAP.DE","type":"stock","currency":"EUR"}]}`), nil
		case strings.Contains(q, "all-world high dividend"):
			return 200, h, []byte(`{"count":2,"results":[{"name":"Vanguard FTSE All-World High Dividend Yield UCITS ETF","isin":"IE00B8GKDB10","ticker":"VHYL.L","type":"etf","currency":null},{"name":"Vanguard FTSE All-World UCITS ETF","isin":"IE00B3RBWM25","ticker":"VWRL.L","type":"etf","currency":null}]}`), nil
		}
		return 200, h, read("equity-search-empty"), nil
	case method == http.MethodGet && strings.HasPrefix(u.Path, "/etf/profile/"):
		switch strings.TrimPrefix(u.Path, "/etf/profile/") {
		case "IE00B4X9L533":
			return 200, h, read("etf-profile"), nil
		case "IE00B4L5Y983":
			return 200, h, read("etf-profile-ishares"), nil
		}
		return notFound()
	case method == http.MethodGet && strings.HasPrefix(u.Path, "/equity/quotes/"):
		if strings.TrimPrefix(u.Path, "/equity/quotes/") == "DE0007164600" {
			return 200, h, read("equity-quotes-sap"), nil
		}
		return notFound()
	case method == http.MethodGet && strings.HasPrefix(u.Path, "/equity/profile/"):
		if strings.TrimPrefix(u.Path, "/equity/profile/") == "DE0007164600" {
			return 200, h, read("equity-profile-sap"), nil
		}
		return notFound()
	case method == http.MethodGet && strings.HasPrefix(u.Path, "/etf/quotes/"):
		if strings.TrimPrefix(u.Path, "/etf/quotes/") == "IE00B4X9L533" {
			return 200, h, read("etf-quotes"), nil
		}
		return notFound()
	case method == http.MethodPost && u.Path == "/market/quotes/bulk":
		var in struct {
			ISINs []string `json:"isins"`
		}
		if json.Unmarshal(body, &in) != nil {
			return 400, h, []byte(`{"error":"bad body"}`), nil
		}
		known := map[string]json.RawMessage{}
		for _, name := range []string{"quotes-bulk", "quotes-bulk-mixed"} {
			var page struct {
				Quotes []json.RawMessage `json:"quotes"`
			}
			if json.Unmarshal(read(name), &page) != nil {
				return 0, nil, nil, ErrUpstream
			}
			for _, raw := range page.Quotes {
				var q struct {
					ISIN string `json:"isin"`
				}
				_ = json.Unmarshal(raw, &q)
				known[q.ISIN] = raw
			}
		}
		quotes, failures := []json.RawMessage{}, []map[string]string{}
		for _, isin := range in.ISINs {
			if raw, ok := known[isin]; ok {
				quotes = append(quotes, raw)
			} else {
				failures = append(failures, map[string]string{"isin": isin, "error": "ISIN not found"})
			}
		}
		b, _ := json.Marshal(map[string]any{"quotes": quotes, "errors": failures})
		return 200, h, b, nil
	}
	return 404, http.Header{}, read("quotes-latest-isin-404"), nil
}

// ReconcileETFPrice guards against Eulerpool series that are off by a factor
// of 100 (observed for IE00B8GKDB10 and IE00B3VVMM84: a 0.6973 series next to a
// 68.41 NAV). The profile NAV is the reference: if the price is wildly off the
// NAV but exactly 100x (or 1/100) closer, the series is rescaled. It returns
// the price to use and whether it was rescaled.
func ReconcileETFPrice(price, nav string) (string, bool) {
	p, ok1 := new(big.Rat).SetString(price)
	n, ok2 := new(big.Rat).SetString(nav)
	if !ok1 || !ok2 || p.Sign() <= 0 || n.Sign() <= 0 {
		return price, false
	}
	near := func(v *big.Rat) bool {
		ratio := new(big.Rat).Quo(v, n)
		return ratio.Cmp(big.NewRat(1, 2)) >= 0 && ratio.Cmp(big.NewRat(2, 1)) <= 0
	}
	if near(p) {
		return price, false
	}
	hundred := big.NewRat(100, 1)
	if up := new(big.Rat).Mul(p, hundred); near(up) {
		return up.FloatString(4), true
	}
	if down := new(big.Rat).Quo(p, hundred); near(down) {
		return down.FloatString(4), true
	}
	return price, false
}

// EPPoint is one daily price from a Eulerpool history, dated by its UTC day.
type EPPoint struct{ Day, Price string }

// ParseEPPoints reads both history shapes: ETFs answer [[ms, price], ...],
// equities [{"timestamp": ms, "price": p}, ...].
func ParseEPPoints(b []byte) ([]EPPoint, error) {
	var raw []json.RawMessage
	if json.Unmarshal(b, &raw) != nil {
		return nil, ErrUpstream
	}
	out := make([]EPPoint, 0, len(raw))
	for _, r := range raw {
		var ms, price json.Number
		var pair []json.Number
		var obj struct {
			Timestamp json.Number `json:"timestamp"`
			Price     json.Number `json:"price"`
		}
		switch {
		case json.Unmarshal(r, &pair) == nil && len(pair) == 2:
			ms, price = pair[0], pair[1]
		case json.Unmarshal(r, &obj) == nil && obj.Timestamp != "":
			ms, price = obj.Timestamp, obj.Price
		default:
			return nil, ErrUpstream
		}
		t, e1 := ms.Int64()
		v, e2 := Scaled(price.String(), 4)
		if e1 != nil || e2 != nil || t <= 0 {
			continue
		}
		out = append(out, EPPoint{Day: time.UnixMilli(t).UTC().Format("2006-01-02"), Price: Fixed(v, 4)})
	}
	return out, nil
}

// LastEPPointOnOrBefore returns the newest point dated on or before day.
func LastEPPointOnOrBefore(points []EPPoint, day string) (EPPoint, bool) {
	var best EPPoint
	found := false
	for _, p := range points {
		if p.Day <= day && (!found || p.Day >= best.Day) {
			best, found = p, true
		}
	}
	return best, found
}

// History fetches daily prices in [from, to]; kind is "etf" or "equity".
func (g *EPGateway) History(ctx context.Context, user, kind, isin string, from, to time.Time) ([]EPPoint, error) {
	path := fmt.Sprintf("/%s/quotes/%s?startdate=%d&enddate=%d", kind, url.PathEscape(isin), from.UnixMilli(), to.Add(24*time.Hour).UnixMilli())
	b, err := g.do(ctx, user, kind+"/quotes", http.MethodGet, path, nil)
	if err != nil {
		return nil, err
	}
	points, err := ParseEPPoints(b)
	if err != nil {
		logUpstream(g.Log, slog.LevelError, ProviderEulerpool, "unparseable history", "isin", isin, "body", snippet(b))
	}
	return points, err
}

// StockProfile returns the listing currency (and WKN, when known) of an equity.
func (g *EPGateway) StockProfile(ctx context.Context, user, isin string) (currency, wkn string, err error) {
	b, err := g.do(ctx, user, "equity/profile", http.MethodGet, "/equity/profile/"+url.PathEscape(isin), nil)
	if err != nil {
		return "", "", err
	}
	var raw struct {
		Currency string `json:"currency"`
		WKN      string `json:"wkn"`
	}
	if json.Unmarshal(b, &raw) != nil {
		logUpstream(g.Log, slog.LevelError, ProviderEulerpool, "unparseable equity profile", "isin", isin, "body", snippet(b))
		return "", "", ErrUpstream
	}
	if len(raw.Currency) >= 3 && len(raw.Currency) <= 4 {
		currency = raw.Currency
	}
	return currency, strings.ToUpper(strings.TrimSpace(raw.WKN)), nil
}
