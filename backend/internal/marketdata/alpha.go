package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"
)

var ErrAVLimit = errors.New("AV_RATE_LIMITED")
var ErrAVUserLimit = errors.New("AV_USER_LIMIT_REACHED")
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
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, ErrUpstream
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return nil, ErrUpstream
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if err != nil {
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
		return avReply{err: ErrUpstream}
	}
	var body map[string]json.RawMessage
	if json.Unmarshal(b, &body) != nil {
		return avReply{err: ErrUpstream}
	}
	for _, k := range []string{"Note", "Information"} {
		if v, ok := body[k]; ok {
			message := strings.ToLower(string(v))
			if strings.Contains(message, "limit") || strings.Contains(message, "frequency") || strings.Contains(message, "requests per") {
				if err = g.Store.BlockAV(c.ctx, g.Now().Add(g.Limits.Backoff)); err != nil {
					return avReply{err: err}
				}
				return avReply{err: ErrAVLimit}
			}
		}
	}
	if _, ok := body["Error Message"]; ok {
		return avReply{err: ErrUpstream}
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
	if q.Currency == "GBX" || q.Currency == "GBp" {
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

// MockAV uses embedded fixtures and never has an HTTP client.
type MockAV struct{}

func (MockAV) Call(ctx context.Context, function, query string) ([]byte, error) {
	name := "alpha-search.json"
	if function == "GLOBAL_QUOTE" {
		name = "alpha-quote.json"
	}
	return fixtures.ReadFile("testdata/marketdata/" + name)
}
