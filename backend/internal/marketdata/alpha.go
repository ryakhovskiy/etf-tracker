package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrAVLimit = errors.New("AV_RATE_LIMITED")
var ErrAVUserLimit = errors.New("AV_USER_LIMIT_REACHED")

// ErrAVRejected is an UPSTREAM_ERROR where AV answered with a Note/Information
// notice that is not a limit, e.g. the demo key used outside its examples.
var ErrAVRejected = fmt.Errorf("%w: Alpha Vantage rejected the request", ErrUpstream)
var ErrFXUnavailable = errors.New("FX_RATE_UNAVAILABLE")
var ErrFXUnsupported = errors.New("FX_UNSUPPORTED_CURRENCY")

type AVLimits struct {
	Minute, Day, User int
	Backoff           time.Duration
}

func (l AVLimits) Defaults() AVLimits {
	if l.Minute == 0 {
		l.Minute = 5
	}
	if l.Day == 0 {
		l.Day = 25
	}
	if l.User == 0 {
		l.User = 10
	}
	if l.Backoff == 0 {
		l.Backoff = time.Hour
	}
	return l
}

type Budget struct {
	GlobalRemaining int        `json:"globalRemaining"`
	UserRemaining   int        `json:"userRemaining"`
	UserLimit       int        `json:"userLimit"`
	NextSlotAt      time.Time  `json:"nextSlotAt"`
	BlockedUntil    *time.Time `json:"blockedUntil,omitempty"`
}
type Quote struct {
	ID           int64     `json:"quoteId"`
	InstrumentID int64     `json:"instrumentId"`
	Price        string    `json:"priceEur"`
	OrigPrice    string    `json:"origPrice"`
	Currency     string    `json:"currency"`
	FXRate       string    `json:"fxRate,omitempty"`
	FXRateDate   string    `json:"fxRateDate,omitempty"`
	QuoteDate    string    `json:"quoteDate"`
	FetchedAt    time.Time `json:"-"`
	Provider     string    `json:"provider,omitempty"`
}
type AVStore interface {
	AVBudget(context.Context, string, time.Time, AVLimits) (Budget, error)
	ReserveAV(context.Context, string, string, time.Time, AVLimits) error
	BlockAV(context.Context, time.Time) error
	FreshQuote(context.Context, int64, time.Time) (*Quote, error)
	SaveQuote(context.Context, *Quote, string) error
}
type AVTransport interface {
	Call(context.Context, string, string) ([]byte, error)
}
type AVHTTP struct {
	HTTP         *http.Client
	BaseURL, Key string
	Log          *slog.Logger
}

func (c *AVHTTP) Call(ctx context.Context, function, query string) ([]byte, error) {
	u, err := url.Parse(c.BaseURL)
	if err != nil {
		return nil, ErrUpstream
	}
	q := u.Query()
	q.Set("function", function)
	q.Set("apikey", c.Key)
	if function == "SYMBOL_SEARCH" {
		q.Set("keywords", query)
	} else {
		q.Set("symbol", query)
	}
	u.RawQuery = q.Encode()
	req, err := http.NewRequestWithContext(ctx, "GET", u.String(), nil)
	if err != nil {
		return nil, ErrUpstream
	}
	started := time.Now()
	res, err := c.HTTP.Do(req)
	if err != nil {
		// The query string holds the API key; log only the cleaned cause.
		logUpstream(c.Log, slog.LevelError, ProviderAlphaVantage, "request failed", "function", function, "elapsed", time.Since(started).Round(time.Millisecond).String(), "error", cleanErr(err))
		return nil, ErrUpstream
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		body, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		logUpstream(c.Log, slog.LevelError, ProviderAlphaVantage, "error response", "function", function, "status", res.StatusCode, "body", snippet(body))
		return nil, ErrUpstream
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
		logUpstream(c.Log, slog.LevelError, ProviderAlphaVantage, "reading response failed", "function", function, "error", cleanErr(err))
		return nil, ErrUpstream
	}
	return b, nil
}

type AVGateway struct {
	Store     AVStore
	Transport AVTransport
	Cache     *Cache
	Limits    AVLimits
	Now       func() time.Time
	Sleep     func(context.Context, time.Duration) error
	Log       *slog.Logger
	queue     chan avCall
	ctx       context.Context
	cancel    context.CancelFunc
	closeOnce sync.Once
}
type avCall struct {
	ctx                   context.Context
	user, function, query string
	instrument            int64
	build                 func([]byte) (*Quote, error)
	reply                 chan avReply
}
type avReply struct {
	data  []byte
	quote *Quote
	fresh bool
	err   error
}

func NewAVGateway(store AVStore, t AVTransport, cache *Cache, limits AVLimits) *AVGateway {
	g := &AVGateway{Store: store, Transport: t, Cache: cache, Limits: limits.Defaults(), Now: time.Now, Sleep: waitContext, queue: make(chan avCall, 128)}
	g.ctx, g.cancel = context.WithCancel(context.Background())
	go g.worker()
	return g
}
func waitContext(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
func (g *AVGateway) worker() {
	for {
		select {
		case <-g.ctx.Done():
			return
		case c := <-g.queue:
			ctx, cancel := context.WithCancel(c.ctx)
			stop := context.AfterFunc(g.ctx, cancel)
			c.ctx = ctx
			r := g.execute(c)
			stop()
			cancel()
			c.reply <- r
		}
	}
}
func (g *AVGateway) Close() { g.closeOnce.Do(g.cancel) }
func (g *AVGateway) execute(c avCall) avReply {
	if err := c.ctx.Err(); err != nil {
		return avReply{err: err}
	}
	key := "av:" + Normalize(c.query)
	if c.function == "SYMBOL_SEARCH" {
		if b, ok := g.Cache.Get(key); ok {
			return avReply{data: b}
		}
	} else {
		q, err := g.Store.FreshQuote(c.ctx, c.instrument, g.Now())
		if err != nil {
			return avReply{err: err}
		}
		if q != nil {
			return avReply{quote: q, fresh: true}
		}
	}
	// Only this worker sends AV traffic. Re-check durable state after every wait,
	// then reserve immediately before sending so queued requests cannot age out.
	for {
		b, err := g.Store.AVBudget(c.ctx, c.user, g.Now(), g.Limits)
		if err != nil {
			return avReply{err: err}
		}
		if b.BlockedUntil != nil && b.BlockedUntil.After(g.Now()) {
			return avReply{err: ErrAVLimit}
		}
		if b.GlobalRemaining <= 0 {
			return avReply{err: ErrAVLimit}
		}
		if b.UserRemaining <= 0 {
			return avReply{err: ErrAVUserLimit}
		}
		if delay := b.NextSlotAt.Sub(g.Now()); delay > 0 {
			if err = g.Sleep(c.ctx, delay); err != nil {
				return avReply{err: err}
			}
			continue
		}
		break
	}
	if err := g.Store.ReserveAV(c.ctx, c.user, c.function, g.Now(), g.Limits); err != nil {
		return avReply{err: err}
	}
	b, err := g.Transport.Call(c.ctx, c.function, c.query)
	if err != nil {
		// The transport has already logged the cause.
		return avReply{err: ErrUpstream}
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(b, &body) != nil {
		logUpstream(g.Log, slog.LevelError, ProviderAlphaVantage, "unparseable response", "function", c.function, "body", snippet(b))
		return avReply{err: ErrUpstream}
	}
	for _, k := range []string{"Note", "Information"} {
		if v, ok := body[k]; ok {
			message := strings.ToLower(string(v))
			if strings.Contains(message, "limit") || strings.Contains(message, "frequency") || strings.Contains(message, "requests per") {
				logUpstream(g.Log, slog.LevelWarn, ProviderAlphaVantage, "rate limit notice; pausing requests", "function", c.function, "backoff", g.Limits.Backoff.String(), "message", snippet(v))
				if err = g.Store.BlockAV(c.ctx, g.Now().Add(g.Limits.Backoff)); err != nil {
					return avReply{err: err}
				}
				return avReply{err: ErrAVLimit}
			}
		}
	}
	if m, ok := body["Error Message"]; ok {
		logUpstream(g.Log, slog.LevelError, ProviderAlphaVantage, "error message in response", "function", c.function, "message", snippet(m))
		return avReply{err: ErrUpstream}
	}
	if body["Note"] != nil || body["Information"] != nil {
		logUpstream(g.Log, slog.LevelError, ProviderAlphaVantage, "request rejected (check the API key)", "function", c.function, "message", snippet(append(append([]byte{}, body["Note"]...), body["Information"]...)))
		return avReply{err: ErrAVRejected}
	}
	if c.function == "SYMBOL_SEARCH" {
		var payload struct {
			Matches []json.RawMessage `json:"bestMatches"`
		}
		if json.Unmarshal(b, &payload) != nil || payload.Matches == nil {
			return avReply{err: ErrUpstream}
		}
		g.Cache.Set(key, b)
		return avReply{data: b}
	}
	q, err := c.build(b)
	if err != nil {
		logUpstream(g.Log, slog.LevelError, ProviderAlphaVantage, "unusable quote response", "function", c.function, "body", snippet(b))
	}
	if err == nil {
		err = g.Store.SaveQuote(c.ctx, q, c.query)
	}
	return avReply{quote: q, err: err}
}
func (g *AVGateway) dispatch(c avCall) avReply {
	// Fast cache hits must not wait behind another caller's quota delay.
	if c.function == "SYMBOL_SEARCH" {
		if b, ok := g.Cache.Get("av:" + Normalize(c.query)); ok {
			return avReply{data: b}
		}
	} else {
		q, err := g.Store.FreshQuote(c.ctx, c.instrument, g.Now())
		if err != nil {
			return avReply{err: err}
		}
		if q != nil {
			return avReply{quote: q, fresh: true}
		}
	}
	c.reply = make(chan avReply, 1)
	select {
	case <-g.ctx.Done():
		return avReply{err: context.Canceled}
	case g.queue <- c:
	case <-c.ctx.Done():
		return avReply{err: c.ctx.Err()}
	}
	select {
	case <-g.ctx.Done():
		return avReply{err: context.Canceled}
	case r := <-c.reply:
		return r
	case <-c.ctx.Done():
		return avReply{err: c.ctx.Err()}
	}
}
func (g *AVGateway) Search(ctx context.Context, user, q string) (Results, error) {
	r := g.dispatch(avCall{ctx: ctx, user: user, function: "SYMBOL_SEARCH", query: q})
	out := Results{Results: []Result{}}
	if r.err != nil {
		return out, r.err
	}
	var raw struct {
		Matches []map[string]string `json:"bestMatches"`
	}
	if json.Unmarshal(r.data, &raw) != nil {
		return out, ErrUpstream
	}
	for _, m := range raw.Matches {
		typ := m["3. type"]
		if typ != "Equity" && typ != "ETF" {
			continue
		}
		if typ == "Equity" {
			typ = "STOCK"
		}
		if m["1. symbol"] == "" || m["8. currency"] == "" {
			continue
		}
		out.Results = append(out.Results, Result{Ticker: m["1. symbol"], Name: m["2. name"], Type: typ, Currency: m["8. currency"], AVSymbol: m["1. symbol"], Region: m["4. region"], MatchScore: m["9. matchScore"], Source: "ALPHAVANTAGE"})
	}
	sort.SliceStable(out.Results, func(i, j int) bool {
		a, b := out.Results[i], out.Results[j]
		if (a.Currency == "EUR") != (b.Currency == "EUR") {
			return a.Currency == "EUR"
		}
		ar, _ := new(big.Rat).SetString(a.MatchScore)
		br, _ := new(big.Rat).SetString(b.MatchScore)
		return ar != nil && br != nil && ar.Cmp(br) > 0
	})
	if len(out.Results) > 10 {
		out.Results = out.Results[:10]
	}
	return out, nil
}
func (g *AVGateway) Quote(ctx context.Context, user, symbol, currency string, id int64, fx *FXRate) (*Quote, bool, error) {
	r := g.dispatch(avCall{ctx: ctx, user: user, function: "GLOBAL_QUOTE", query: symbol, instrument: id, build: func(b []byte) (*Quote, error) {
		var raw struct {
			Quote map[string]string `json:"Global Quote"`
		}
		if json.Unmarshal(b, &raw) != nil {
			return nil, ErrUpstream
		}
		p, err := Scaled(raw.Quote["05. price"], 4)
		if err != nil || p <= 0 {
			return nil, ErrUpstream
		}
		date := raw.Quote["07. latest trading day"]
		if _, err = time.Parse("2006-01-02", date); err != nil {
			return nil, ErrUpstream
		}
		q := &Quote{InstrumentID: id, OrigPrice: Fixed(p, 4), Currency: currency, Price: Fixed(p, 4), QuoteDate: date, FetchedAt: g.Now()}
		return q, ApplyFX(q, fx)
	}})
	return r.quote, r.fresh, r.err
}

// ApplyFX sets the EUR price of q from its original price using fx, the rate
// for the transaction date. EUR listings are left unchanged.
func ApplyFX(q *Quote, fx *FXRate) error {
	if q.Currency == "EUR" {
		q.Price, q.FXRate, q.FXRateDate = q.OrigPrice, "", ""
		return nil
	}
	if fx == nil {
		return ErrFXUnavailable
	}
	p, err := Scaled(q.OrigPrice, 4)
	if err != nil {
		return err
	}
	div := int64(1)
	if MinorUnit(q.Currency) {
		div = 100
	}
	v, err := Convert(p, fx.Rate, div)
	if err != nil {
		return err
	}
	q.Price, q.FXRate, q.FXRateDate = Fixed(v, 4), Fixed(fx.Rate, 8), fx.Date
	return nil
}

// ForDate returns q converted at fx, the rate for the transaction date. When q
// was converted at a different publication, the result is a new, unsaved quote
// (ID 0) with the same original price, quote date and fetch time; otherwise q.
func (q *Quote) ForDate(fx *FXRate) (*Quote, error) {
	if q.Currency == "EUR" || (fx != nil && q.FXRateDate == fx.Date && q.FXRate == Fixed(fx.Rate, 8)) {
		return q, nil
	}
	c := *q
	c.ID = 0
	return &c, ApplyFX(&c, fx)
}

// Scaled parses provider decimals exactly, using half-up rounding.
func Scaled(s string, scale int) (int64, error) {
	if len(s) > 64 {
		return 0, ErrUpstream
	}
	r, ok := new(big.Rat).SetString(s)
	if !ok || r.Sign() <= 0 {
		return 0, ErrUpstream
	}
	r.Mul(r, new(big.Rat).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(scale)), nil)))
	return rounded(r)
}
func rounded(r *big.Rat) (int64, error) {
	q, rem := new(big.Int), new(big.Int)
	q.QuoRem(r.Num(), r.Denom(), rem)
	if new(big.Int).Mul(rem, big.NewInt(2)).Cmp(r.Denom()) >= 0 {
		q.Add(q, big.NewInt(1))
	}
	if !q.IsInt64() || q.Sign() <= 0 {
		return 0, ErrUpstream
	}
	return q.Int64(), nil
}
func Convert(price, rate, div int64) (int64, error) {
	if price <= 0 || rate <= 0 || div <= 0 {
		return 0, ErrUpstream
	}
	return rounded(new(big.Rat).SetFrac(new(big.Int).Mul(big.NewInt(price), big.NewInt(rate)), new(big.Int).Mul(big.NewInt(100000000), big.NewInt(div))))
}
func Fixed(n int64, scale int) string {
	s := new(big.Int).SetInt64(n).String()
	if len(s) <= scale {
		s = strings.Repeat("0", scale+1-len(s)) + s
	}
	return s[:len(s)-scale] + "." + s[len(s)-scale:]
}

var berlin = func() *time.Location {
	l, e := time.LoadLocation("Europe/Berlin")
	if e != nil {
		panic(e)
	}
	return l
}()

func BerlinDay(t time.Time) string { return t.In(berlin).Format("2006-01-02") }
func AllowedDate(date string, now time.Time) bool {
	t := now.In(berlin)
	prev := t.AddDate(0, 0, -1)
	for prev.Weekday() == time.Saturday || prev.Weekday() == time.Sunday {
		prev = prev.AddDate(0, 0, -1)
	}
	return date == BerlinDay(now) || date == prev.Format("2006-01-02")
}

// MockAV uses embedded fixtures and never has an HTTP client. Searches return
// the fixture matches whose symbol starts with the keywords or whose name
// contains every keyword, like a (much smaller) Alpha Vantage index.
type MockAV struct{}

func (MockAV) Call(ctx context.Context, function, query string) ([]byte, error) {
	if function == "GLOBAL_QUOTE" {
		return fixtures.ReadFile("testdata/marketdata/alpha-quote.json")
	}
	// alpha-search-tesco.json is the verbatim response of the public demo request
	// function=SYMBOL_SEARCH&keywords=tesco&apikey=demo.
	var all []map[string]string
	for _, name := range []string{"alpha-search.json", "alpha-search-tesco.json"} {
		b, err := fixtures.ReadFile("testdata/marketdata/" + name)
		if err != nil {
			return nil, err
		}
		var page struct {
			Matches []map[string]string `json:"bestMatches"`
		}
		if err = json.Unmarshal(b, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Matches...)
	}
	q := strings.ToLower(strings.TrimSpace(query))
	matches := []map[string]string{}
	for _, m := range all {
		name, hit := strings.ToLower(m["2. name"]), q != ""
		for _, word := range strings.Fields(q) {
			hit = hit && strings.Contains(name, word)
		}
		if hit || (q != "" && strings.HasPrefix(strings.ToLower(m["1. symbol"]), q)) {
			matches = append(matches, m)
		}
	}
	return json.Marshal(map[string]any{"bestMatches": matches})
}

var (
	exchangeSuffix = regexp.MustCompile(`^([A-Z0-9]+)\.[A-Z]{1,2}$`)
	parenthesized  = regexp.MustCompile(`\([^)]*\)`)
	fundNoise      = map[string]bool{"UCITS": true, "ETF": true, "ACC": true, "DIST": true, "ACCUMULATING": true, "DISTRIBUTING": true, "USD": true, "EUR": true, "GBP": true, "1C": true, "1D": true, "INC": true, "CORP": true, "SE": true, "AG": true, "PLC": true}
)

// AVSearchTerm derives default SYMBOL_SEARCH keywords. Alpha Vantage matches
// symbols and names, not ISINs/WKNs, and uses its own exchange suffixes
// (".DEX", not Yahoo's ".DE"). Identifier tickers fall back to the name
// without share-class noise, which users can still edit before searching.
func AVSearchTerm(ticker, name string) string {
	t := strings.ToUpper(strings.TrimSpace(ticker))
	if t != "" && Classify(t) == "KEYWORD" {
		if m := exchangeSuffix.FindStringSubmatch(t); m != nil {
			return m[1]
		}
		return t
	}
	words := []string{}
	for _, w := range strings.Fields(parenthesized.ReplaceAllString(name, " ")) {
		if fundNoise[strings.ToUpper(strings.Trim(w, ".,"))] {
			continue
		}
		if words = append(words, w); len(words) == 5 {
			break
		}
	}
	if len(words) == 0 {
		return t
	}
	return strings.Join(words, " ")
}

// MinorUnit reports currency codes quoted in 1/100 of the major unit.
func MinorUnit(c string) bool { return c == "GBX" || c == "GBp" || c == "ZAc" }
