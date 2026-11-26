package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/marketdata"
)

// The fixed "now" of the ticker chart tests: Thursday 2026-11-26, 10:00 Berlin.
// The last completed weekday is Wednesday 2026-11-25; five months earlier is 2021-11-26.
var chartNow = time.Date(2026, 11, 26, 10, 0, 0, 0, marketdata.Berlin())

const (
	chartToday  = "2026-11-26"
	chartTarget = "2026-11-25"
	chartFrom   = "2021-11-26"
)

type chartSeries struct {
	currency string
	points   []marketdata.YahooPoint
}

// chartYahoo serves the chart endpoint from series set by the test, counts the
// chart requests and answers everything else (crumb, search) from the mock.
type chartYahoo struct {
	mu     sync.Mutex
	series map[string]chartSeries
	chart  int
	ranges [][2]string
	asked  []string
	fail   int
}

func (c *chartYahoo) Get(ctx context.Context, rawURL string) (int, http.Header, []byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil || !strings.HasPrefix(u.Path, "/v8/finance/chart/") {
		return marketdata.MockYahoo{}.Get(ctx, rawURL)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.chart++
	c.asked = append(c.asked, strings.TrimPrefix(u.Path, "/v8/finance/chart/"))
	if c.fail != 0 {
		return c.fail, http.Header{}, []byte("Edge: Too Many Requests"), nil
	}
	symbol := strings.TrimPrefix(u.Path, "/v8/finance/chart/")
	s, ok := c.series[symbol]
	if !ok {
		return 200, http.Header{}, []byte(`{"chart":{"result":null,"error":{"code":"Not Found","description":"No data found"}}}`), nil
	}
	p1, _ := strconv.ParseInt(u.Query().Get("period1"), 10, 64)
	p2, _ := strconv.ParseInt(u.Query().Get("period2"), 10, 64)
	c.ranges = append(c.ranges, [2]string{time.Unix(p1, 0).UTC().Format("2006-01-02"), time.Unix(p2, 0).UTC().Add(-time.Hour).Format("2006-01-02")})
	var ts []string
	var closes []string
	for _, p := range s.points {
		d, _ := time.Parse("2006-01-02", p.Day)
		at := d.Add(12 * time.Hour).Unix()
		if at >= p1 && at < p2 {
			ts = append(ts, strconv.FormatInt(at, 10))
			closes = append(closes, p.Close)
		}
	}
	body := fmt.Sprintf(`{"chart":{"result":[{"meta":{"symbol":%q,"currency":%q,"exchangeTimezoneName":"UTC"},"timestamp":[%s],"indicators":{"quote":[{"close":[%s]}]}}],"error":null}}`,
		symbol, s.currency, strings.Join(ts, ","), strings.Join(closes, ","))
	return 200, http.Header{}, []byte(body), nil
}

func (c *chartYahoo) calls() int { c.mu.Lock(); defer c.mu.Unlock(); return c.chart }

// chartFX publishes one rate per weekday that depends on the day, so a wrong
// day's rate shows. USD: 0.90 + dom×0.001, GBP: 1.10 + dom×0.001.
type chartFX struct {
	mu      sync.Mutex
	calls   int
	missing bool
}

func chartRate(currency, day string) int64 {
	d, _ := time.Parse("2006-01-02", day)
	base := int64(90000000)
	if currency == "GBP" {
		base = 110000000
	}
	return base + int64(d.Day())*100000
}

func (f *chartFX) Currencies(context.Context) (map[string]bool, error) {
	return map[string]bool{"USD": true, "GBP": true}, nil
}
func (f *chartFX) Rates(_ context.Context, currency, from, to string) ([]marketdata.FXRate, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.missing {
		return nil, marketdata.ErrFXUnavailable
	}
	var out []marketdata.FXRate
	d, _ := time.Parse("2006-01-02", from)
	end, _ := time.Parse("2006-01-02", to)
	for ; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			day := d.Format("2006-01-02")
			out = append(out, marketdata.FXRate{Date: day, Base: currency, Quote: "EUR", Rate: chartRate(currency, day)})
		}
	}
	return out, nil
}

// weekdayPoints lists the weekdays of [from, to] with a close of 100.0000 + n×0.5.
func weekdayPoints(from, to string) []marketdata.YahooPoint {
	var out []marketdata.YahooPoint
	d, _ := time.Parse("2006-01-02", from)
	end, _ := time.Parse("2006-01-02", to)
	for n := 0; !d.After(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			out = append(out, marketdata.YahooPoint{Day: d.Format("2006-01-02"), Close: marketdata.Fixed(1000000+int64(n)*5000, 4)})
			n++
		}
	}
	return out
}

func without(points []marketdata.YahooPoint, days ...string) []marketdata.YahooPoint {
	var out []marketdata.YahooPoint
	for _, p := range points {
		skip := false
		for _, d := range days {
			skip = skip || p.Day == d
		}
		if !skip {
			out = append(out, p)
		}
	}
	return out
}

type chartEnv struct {
	h       *Handler
	handler http.Handler
	cookies []*http.Cookie
	y       *chartYahoo
	fx      *chartFX
	pid     int64
}

// chartSetup builds a handler on the fixed clock whose user holds instrument 18
// (Yahoo symbol VHYD.L) in one portfolio.
func chartSetup(t *testing.T) *chartEnv {
	t.Helper()
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	e := &chartEnv{h: h, handler: handler, cookies: cookies, y: &chartYahoo{series: map[string]chartSeries{}}, fx: &chartFX{}}
	h.clock = func() time.Time { return chartNow }
	h.fx.Now = h.clock
	h.fx.Provider = e.fx
	h.yahoo.Transport = e.y
	h.yahoo.MinInterval = 0
	if err := h.store.SetYahooSymbol(context.Background(), 18, "VHYD.L", chartNow); err != nil {
		t.Fatal(err)
	}
	e.pid = chartPortfolio(t, h, "google-user-1", "Main", []int64{18})
	return e
}

func chartPortfolio(t *testing.T, h *Handler, user, name string, items []int64) int64 {
	t.Helper()
	p, err := h.store.SavePortfolio(context.Background(), user, 0, name)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.AddItems(context.Background(), user, p.ID, items); err != nil {
		t.Fatal(err)
	}
	return p.ID
}

func chartBuy(t *testing.T, h *Handler, user string, pid, id int64, day, units, price, amount string) {
	t.Helper()
	_, err := h.store.CreateTransaction(context.Background(), user, pid, database.TransactionInput{Date: day, Lines: []database.TransactionLine{{InstrumentID: id, Amount: amount, Price: price, Quantity: units}}})
	if err != nil {
		t.Fatal(err)
	}
}

// seedHistory stores closes as one SaveHistory call made at the given time.
func (e *chartEnv) seedHistory(t *testing.T, id int64, currency string, points []marketdata.YahooPoint, at time.Time) {
	t.Helper()
	if err := e.h.store.SaveHistory(context.Background(), id, "VHYD.L", currency, marketdata.ProviderYahoo, points, at); err != nil {
		t.Fatal(err)
	}
}

// seedComplete stores every weekday of [from, chartTarget] in a single call.
func (e *chartEnv) seedComplete(t *testing.T, id int64, currency, from string) []marketdata.YahooPoint {
	t.Helper()
	pts := weekdayPoints(from, chartTarget)
	e.seedHistory(t, id, currency, pts, chartNow.Add(-time.Hour))
	return pts
}

type chartPointJSON struct{ Day, CloseEur string }
type chartResult struct {
	Favorite   bool
	Instrument struct {
		ID                 int64
		Name, ISIN, Ticker string
	}
	Latest struct {
		PriceEur *string
		Change   *struct {
			Amount  string
			Percent *string
		}
		AsOf    *struct{ QuoteDate string }
		Reasons []string
	}
	Points []chartPointJSON
	Buys   []struct {
		Day, Units, PriceEur, PortfolioName string
		PortfolioID                         int64
	}
	From, To string
	Warning  string
}

func (e *chartEnv) get(t *testing.T, id int64, status int) (chartResult, string) {
	t.Helper()
	w := featureRequest(e.handler, "GET", fmt.Sprintf("/api/instruments/%d/chart", id), "", e.cookies...)
	if w.Code != status {
		t.Fatalf("GET chart %d: %d (want %d) %s", id, w.Code, status, w.Body.String())
	}
	var out chartResult
	if status == 200 {
		if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		return out, ""
	}
	return out, errorCode(t, w.Body.Bytes())
}

func TestTickerChartScoping(t *testing.T) {
	e := chartSetup(t)
	ctx := context.Background()
	if w := featureRequest(e.handler, "GET", "/api/instruments/18/chart", ""); w.Code != 401 {
		t.Fatalf("no session: %d", w.Code)
	}
	if _, err := e.h.store.UpsertGoogleUser(ctx, "foreign", "foreign@example.invalid", "", ""); err != nil {
		t.Fatal(err)
	}
	chartPortfolio(t, e.h, "foreign", "Secret", []int64{6, 18})
	e.seedComplete(t, 18, "EUR", chartFrom)
	e.seedHistory(t, 6, "EUR", weekdayPoints("2026-11-02", chartTarget), chartNow)
	// Held by the caller: 200. Held only by another user, unknown, or malformed: 404 and no Yahoo request.
	e.get(t, 18, 200)
	for _, id := range []string{"6", "9999", "abc", "0", "-1"} {
		w := featureRequest(e.handler, "GET", "/api/instruments/"+id+"/chart", "", e.cookies...)
		if w.Code != 404 || errorCode(t, w.Body.Bytes()) != "NOT_FOUND" {
			t.Fatalf("instrument %s: %d %s", id, w.Code, w.Body.String())
		}
	}
	if e.y.calls() != 0 {
		t.Fatalf("Yahoo asked for a title the user does not hold: %d", e.y.calls())
	}
	// favorite is the caller's own star, not another user's.
	if res, _ := e.get(t, 18, 200); res.Favorite {
		t.Fatal("not starred yet")
	}
	if _, err := e.h.store.StarFavorite(ctx, "foreign", 18); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.get(t, 18, 200); res.Favorite {
		t.Fatal("another user's star leaked")
	}
	if _, err := e.h.store.StarFavorite(ctx, "google-user-1", 18); err != nil {
		t.Fatal(err)
	}
	if res, _ := e.get(t, 18, 200); !res.Favorite {
		t.Fatal("starred by the caller")
	}
	// A held title without any history is a different 404 from a title that is not held.
	e.y.series = map[string]chartSeries{}
	if _, err := e.h.store.AddItems(ctx, "google-user-1", e.pid, []int64{1}); err != nil {
		t.Fatal(err)
	}
	if _, code := e.get(t, 1, 404); code != "NO_PRICE_HISTORY" {
		t.Fatalf("held title without history: %s", code)
	}
	// A foreign buy of a shared instrument never shows.
	chartBuy(t, e.h, "foreign", 2, 18, "2026-11-03", "1", "50.00", "50.00")
	if res, _ := e.get(t, 18, 200); len(res.Buys) != 0 {
		t.Fatalf("foreign buys leaked: %+v", res.Buys)
	}
}

func TestTickerChartBoundsAndBuysAcrossPortfolios(t *testing.T) {
	e := chartSetup(t)
	second := chartPortfolio(t, e.h, "google-user-1", "Second", []int64{18})
	chartBuy(t, e.h, "google-user-1", e.pid, 18, "2026-11-03", "5", "50.50", "252.50")
	chartBuy(t, e.h, "google-user-1", e.pid, 18, "2026-11-10", "1", "55.00", "55.00")
	chartBuy(t, e.h, "google-user-1", second, 18, "2026-11-10", "2", "60.00", "120.00")
	e.seedComplete(t, 18, "EUR", chartFrom)

	res, _ := e.get(t, 18, 200)
	// Default range: 60 months back from today, to today; complete cache → no Yahoo.
	if res.From != chartFrom || res.To != chartToday || res.Points[0].Day != chartFrom || res.Points[len(res.Points)-1].Day != chartTarget {
		t.Fatalf("bounds from=%s to=%s first=%s last=%s", res.From, res.To, res.Points[0].Day, res.Points[len(res.Points)-1].Day)
	}
	if res.Instrument.ID != 18 || res.Instrument.ISIN != "IE00B8GKDB10" || res.Instrument.Name == "" || res.Instrument.Ticker == "" {
		t.Fatalf("instrument %+v", res.Instrument)
	}
	// Every buy of both portfolios, ascending by day, price as stored.
	got := []string{}
	for _, b := range res.Buys {
		got = append(got, strings.Join([]string{b.Day, b.Units, b.PriceEur, b.PortfolioName, strconv.FormatInt(b.PortfolioID, 10)}, "|"))
	}
	want := []string{
		"2026-11-03|5.000000|50.5000|Main|" + strconv.FormatInt(e.pid, 10),
		"2026-11-10|1.000000|55.0000|Main|" + strconv.FormatInt(e.pid, 10),
		"2026-11-10|2.000000|60.0000|Second|" + strconv.FormatInt(second, 10),
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Fatalf("buys\n%s\nwant\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	if e.y.calls() != 0 {
		t.Fatal("complete cache must not call Yahoo")
	}
}

func TestTickerChartFromReachesBackToTheFirstBuy(t *testing.T) {
	e := chartSetup(t)
	chartBuy(t, e.h, "google-user-1", e.pid, 18, "2019-03-04", "1", "50.00", "50.00")
	e.y.series["VHYD.L"] = chartSeries{"EUR", weekdayPoints("2019-03-04", chartTarget)}
	res, _ := e.get(t, 18, 200)
	if res.From != "2019-03-04" || res.Points[0].Day != "2019-03-04" || e.y.calls() != 1 || e.y.ranges[0][0] != "2019-03-04" {
		t.Fatalf("from=%s first=%s calls=%d ranges=%v", res.From, res.Points[0].Day, e.y.calls(), e.y.ranges)
	}
}

func TestTickerChartFromIsLimitedByTheFirstDayYahooHasData(t *testing.T) {
	e := chartSetup(t)
	chartBuy(t, e.h, "google-user-1", e.pid, 18, "2024-01-02", "1", "50.00", "50.00")
	// The listing is younger than the chart range: its first session is Wed 2024-01-03.
	e.y.series["VHYD.L"] = chartSeries{"EUR", weekdayPoints("2024-01-03", chartTarget)}
	res, _ := e.get(t, 18, 200)
	if res.From != "2024-01-03" || res.Points[0].Day != "2024-01-03" || e.y.calls() != 1 {
		t.Fatalf("from=%s calls=%d", res.From, e.y.calls())
	}
	// It stays one request a day, although the stored data never reaches back to the requested start.
	e.get(t, 18, 200)
	e.get(t, 18, 200)
	if e.y.calls() != 1 {
		t.Fatalf("Yahoo asked again the same day: %d", e.y.calls())
	}
	// The next day is a new day.
	e.h.clock = func() time.Time { return chartNow.AddDate(0, 0, 1) }
	e.y.series["VHYD.L"] = chartSeries{"EUR", weekdayPoints("2024-01-03", "2026-11-26")}
	e.get(t, 18, 200)
	if e.y.calls() != 2 {
		t.Fatalf("next day: %d", e.y.calls())
	}
}

func TestTickerChartConvertsEachDayAtItsEcbRate(t *testing.T) {
	e := chartSetup(t)
	chartPortfolio(t, e.h, "google-user-1", "Pence", []int64{7})
	usd := []marketdata.YahooPoint{{Day: "2026-11-03", Close: "100.0000"}, {Day: "2026-11-04", Close: "100.0000"}, {Day: "2026-11-25", Close: "123.4567"}}
	pence := []marketdata.YahooPoint{{Day: "2026-11-03", Close: "1234.5000"}, {Day: "2026-11-12", Close: "1234.5000"}, {Day: "2026-11-25", Close: "99.9500"}}
	e.seedHistory(t, 18, "USD", append(weekdayPoints("2026-10-01", "2026-11-02"), usd...), chartNow.Add(-time.Hour))
	e.h.store.SetYahooSymbol(context.Background(), 7, "ULVR.L", chartNow)
	if err := e.h.store.SaveHistory(context.Background(), 7, "ULVR.L", "GBp", marketdata.ProviderYahoo, append(weekdayPoints("2026-10-01", "2026-11-02"), pence...), chartNow.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	e.y.series["VHYD.L"] = chartSeries{"USD", usd}
	e.y.series["ULVR.L"] = chartSeries{"GBp", pence}

	eur := func(close string, rate int64, div int64) string {
		c, _ := marketdata.Scaled(close, 4)
		v, err := marketdata.Convert(c, rate, div)
		if err != nil {
			t.Fatal(err)
		}
		return marketdata.Fixed(v, 4)
	}
	byDay := func(res chartResult) map[string]string {
		m := map[string]string{}
		for _, p := range res.Points {
			m[p.Day] = p.CloseEur
		}
		return m
	}
	res, _ := e.get(t, 18, 200)
	m := byDay(res)
	// Two days with the same USD close but different ECB rates convert differently.
	if m["2026-11-03"] != eur("100.0000", chartRate("USD", "2026-11-03"), 1) || m["2026-11-04"] != eur("100.0000", chartRate("USD", "2026-11-04"), 1) || m["2026-11-03"] == m["2026-11-04"] {
		t.Fatalf("USD per-day conversion: %v %v", m["2026-11-03"], m["2026-11-04"])
	}
	if m["2026-11-25"] != eur("123.4567", chartRate("USD", "2026-11-25"), 1) {
		t.Fatalf("USD last day %s", m["2026-11-25"])
	}
	if e.fx.calls > 1 {
		t.Fatalf("one FX request per currency, got %d", e.fx.calls)
	}

	// Pence: the major currency's rate, divided by 100.
	e.h.store.AddItems(context.Background(), "google-user-1", e.pid, []int64{7})
	res, _ = e.get(t, 7, 200)
	m = byDay(res)
	if m["2026-11-03"] != eur("1234.5000", chartRate("GBP", "2026-11-03"), 100) || m["2026-11-12"] != eur("1234.5000", chartRate("GBP", "2026-11-12"), 100) || m["2026-11-03"] == m["2026-11-12"] {
		t.Fatalf("GBp conversion: %v %v", m["2026-11-03"], m["2026-11-12"])
	}
	if m["2026-11-25"] != eur("99.9500", chartRate("GBP", "2026-11-25"), 100) {
		t.Fatalf("GBp last day %s", m["2026-11-25"])
	}
	// 1234.50 GBp on the 3rd is 12.345 GBP × 1.1030 = 13.6165 EUR.
	if m["2026-11-03"] != "13.6165" {
		t.Fatalf("GBp arithmetic: %s", m["2026-11-03"])
	}
}

func TestTickerChartCacheCoverage(t *testing.T) {
	complete := weekdayPoints(chartFrom, chartTarget)
	for _, tc := range []struct {
		name  string
		seed  func(e *chartEnv)
		calls int
	}{
		{"complete cache", func(e *chartEnv) { e.seedHistory(t, 18, "EUR", complete, chartNow.Add(-time.Hour)) }, 0},
		{"tail missing, last close two days old", func(e *chartEnv) {
			e.seedHistory(t, 18, "EUR", weekdayPoints(chartFrom, "2026-11-23"), chartNow.Add(-72*time.Hour))
		}, 1},
		{"only yesterday missing", func(e *chartEnv) {
			e.seedHistory(t, 18, "EUR", weekdayPoints(chartFrom, "2026-11-24"), chartNow.Add(-24*time.Hour))
		}, 1},
		{"nothing stored", func(e *chartEnv) {}, 1},
		{"one ordinary day missing between two fetches", func(e *chartEnv) {
			// Wed 2026-11-18 was never fetched: its neighbours come from different requests.
			e.seedHistory(t, 18, "EUR", weekdayPoints(chartFrom, "2026-11-17"), chartNow.Add(-48*time.Hour))
			e.seedHistory(t, 18, "EUR", weekdayPoints("2026-11-19", chartTarget), chartNow.Add(-time.Hour))
		}, 1},
		{"a closure inside one fetch is confirmed", func(e *chartEnv) {
			// Same request on both sides of Wed 2026-11-18: Yahoo had no session that day.
			e.seedHistory(t, 18, "EUR", without(complete, "2026-11-18"), chartNow.Add(-time.Hour))
		}, 0},
		{"series starts after a weekend", func(e *chartEnv) {
			// The first buy is Sat 2021-11-20, so from is that Saturday; the first close is Mon 2021-11-22: no weekday is missing.
			chartBuy(t, e.h, "google-user-1", e.pid, 18, "2021-11-20", "1", "50.00", "50.00")
			e.seedHistory(t, 18, "EUR", weekdayPoints("2021-11-22", chartTarget), chartNow.Add(-time.Hour))
		}, 0},
		{"series starts one weekday late", func(e *chartEnv) {
			// from is Fri 2021-11-26 but the first close is Mon 2021-11-29.
			e.seedHistory(t, 18, "EUR", weekdayPoints("2021-11-29", chartTarget), chartNow.Add(-time.Hour))
		}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := chartSetup(t)
			tc.seed(e)
			e.y.series["VHYD.L"] = chartSeries{"EUR", complete}
			e.get(t, 18, 200)
			if got := e.y.calls(); got != tc.calls {
				t.Fatalf("Yahoo chart requests: %d, want %d", got, tc.calls)
			}
			// Whatever happened, the next request of the day is free.
			e.get(t, 18, 200)
			if got := e.y.calls(); got != tc.calls {
				t.Fatalf("second request: %d, want %d", got, tc.calls)
			}
		})
	}
}

func TestTickerChartMissingFirstWeekdayAndInteriorGapAreFetched(t *testing.T) {
	// The first buy is Mon 2021-11-15, so the chart starts there; the cache starts Tue 2021-11-16.
	e := chartSetup(t)
	chartBuy(t, e.h, "google-user-1", e.pid, 18, "2021-11-15", "1", "50.00", "50.00")
	e.seedHistory(t, 18, "EUR", weekdayPoints("2021-11-16", chartTarget), chartNow.Add(-time.Hour))
	e.y.series["VHYD.L"] = chartSeries{"EUR", weekdayPoints("2021-11-15", chartTarget)}
	res, _ := e.get(t, 18, 200)
	if e.y.calls() != 1 || res.From != "2021-11-15" || res.Points[0].Day != "2021-11-15" {
		t.Fatalf("calls=%d from=%s first=%s", e.y.calls(), res.From, res.Points[0].Day)
	}

	// An interior gap: the genuinely missing Wed 2026-11-18 is filled by the request.
	e = chartSetup(t)
	e.seedHistory(t, 18, "EUR", weekdayPoints(chartFrom, "2026-11-17"), chartNow.Add(-48*time.Hour))
	e.seedHistory(t, 18, "EUR", weekdayPoints("2026-11-19", chartTarget), chartNow.Add(-time.Hour))
	e.y.series["VHYD.L"] = chartSeries{"EUR", weekdayPoints(chartFrom, chartTarget)}
	res, _ = e.get(t, 18, 200)
	found := false
	for _, p := range res.Points {
		found = found || p.Day == "2026-11-18"
	}
	if e.y.calls() != 1 || !found {
		t.Fatalf("calls=%d, 2026-11-18 present=%v", e.y.calls(), found)
	}
}

func TestChartCoveredRules(t *testing.T) {
	pt := func(day, at string) database.HistoryPoint { return database.HistoryPoint{Day: day, FetchedAt: at} }
	for _, tc := range []struct {
		name   string
		points []database.HistoryPoint
		from   string
		want   bool
	}{
		{"empty", nil, "2026-11-16", false},
		{"first weekday missing", []database.HistoryPoint{pt("2026-11-17", "a"), pt("2026-11-18", "a"), pt("2026-11-19", "a"), pt("2026-11-20", "a"), pt("2026-11-23", "a"), pt("2026-11-24", "a"), pt("2026-11-25", "a")}, "2026-11-16", false},
		{"starts on from", []database.HistoryPoint{pt("2026-11-16", "a"), pt("2026-11-25", "a")}, "2026-11-16", true},
		{"from on a Sunday", []database.HistoryPoint{pt("2026-11-16", "a"), pt("2026-11-25", "a")}, "2026-11-15", true},
		{"one weekday missing, one fetch", []database.HistoryPoint{pt("2026-11-17", "a"), pt("2026-11-19", "a"), pt("2026-11-25", "a")}, "2026-11-17", true},
		{"one weekday missing, two fetches", []database.HistoryPoint{pt("2026-11-17", "a"), pt("2026-11-19", "b"), pt("2026-11-25", "b")}, "2026-11-17", false},
		{"weekend between two fetches", []database.HistoryPoint{pt("2026-11-20", "a"), pt("2026-11-23", "b"), pt("2026-11-25", "b")}, "2026-11-20", true},
		{"tail short of target", []database.HistoryPoint{pt("2026-11-17", "a"), pt("2026-11-24", "a")}, "2026-11-17", false},
	} {
		if got := chartCovered(tc.points, tc.from, "2026-11-25"); got != tc.want {
			t.Errorf("%s: %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTickerChartYahooFailureFallsBackToStoredCloses(t *testing.T) {
	e := chartSetup(t)
	e.seedHistory(t, 18, "EUR", weekdayPoints("2026-10-01", "2026-11-23"), chartNow.Add(-72*time.Hour))
	e.y.fail = 429
	res, _ := e.get(t, 18, 200)
	if res.Warning != "YAHOO_LIMIT_REACHED" || len(res.Points) != len(weekdayPoints("2026-10-01", "2026-11-23")) || res.Points[len(res.Points)-1].Day != "2026-11-23" {
		t.Fatalf("warning=%q points=%d", res.Warning, len(res.Points))
	}
	if res.From != "2026-10-01" || res.To != chartToday {
		t.Fatalf("from=%s to=%s", res.From, res.To)
	}
}

func TestTickerChartFailedFetchIsRetriedAndRecovers(t *testing.T) {
	e := chartSetup(t)
	e.seedHistory(t, 18, "EUR", weekdayPoints("2026-10-01", "2026-11-23"), chartNow.Add(-72*time.Hour))
	e.y.fail = 500
	res, _ := e.get(t, 18, 200)
	if res.Warning != "YAHOO_UNAVAILABLE" || res.Points[len(res.Points)-1].Day != "2026-11-23" {
		t.Fatalf("warning=%q", res.Warning)
	}
	// A failure is not remembered as a fetch: the next request tries again and recovers.
	calls := e.y.calls()
	e.y.fail = 0
	e.y.series["VHYD.L"] = chartSeries{"EUR", weekdayPoints("2026-10-01", chartTarget)}
	res, _ = e.get(t, 18, 200)
	if e.y.calls() != calls+1 || res.Warning != "" || res.Points[len(res.Points)-1].Day != chartTarget {
		t.Fatalf("recovery: calls %d→%d warning=%q last=%s", calls, e.y.calls(), res.Warning, res.Points[len(res.Points)-1].Day)
	}
}

func TestTickerChartWithoutAnyPointsIsAnError(t *testing.T) {
	e := chartSetup(t)
	// Yahoo does not know the symbol: no stored data, nothing to warn about.
	if _, code := e.get(t, 18, 404); code != "NO_PRICE_HISTORY" {
		t.Fatal(code)
	}
	e.y.fail = 429
	if _, code := e.get(t, 18, 429); code != "YAHOO_LIMIT_REACHED" {
		t.Fatal(code)
	}
}

func TestTickerChartLivePointOfToday(t *testing.T) {
	e := chartSetup(t)
	e.seedComplete(t, 18, "EUR", "2026-11-02")
	save := func(day, price string) {
		t.Helper()
		q := &marketdata.Quote{InstrumentID: 18, OrigPrice: price, Price: price, Currency: "EUR", QuoteDate: day, FetchedAt: chartNow, Provider: marketdata.ProviderYahoo, PrevClose: "100.0000", PrevCloseDate: chartTarget}
		if err := e.h.store.SaveQuote(context.Background(), q, ""); err != nil {
			t.Fatal(err)
		}
	}
	// A quote of yesterday is not a point.
	save(chartTarget, "101.0000")
	res, _ := e.get(t, 18, 200)
	if last := res.Points[len(res.Points)-1]; last.Day != chartTarget {
		t.Fatalf("yesterday's quote appended: %+v", last)
	}
	if res.Latest.PriceEur == nil || *res.Latest.PriceEur != "101.0000" || res.Latest.Change == nil || res.Latest.Change.Amount != "1.00" {
		t.Fatalf("latest %+v", res.Latest)
	}
	// A quote of today ends the line at the live price, after the last close.
	save(chartToday, "103.2500")
	res, _ = e.get(t, 18, 200)
	n := len(res.Points)
	if res.Points[n-1] != (chartPointJSON{Day: chartToday, CloseEur: "103.2500"}) || res.Points[n-2].Day != chartTarget || res.To != chartToday {
		t.Fatalf("live point: %+v", res.Points[n-2:])
	}
	if res.Latest.PriceEur == nil || *res.Latest.PriceEur != "103.2500" || res.Latest.Change == nil || res.Latest.Change.Amount != "3.25" || res.Latest.AsOf == nil || res.Latest.AsOf.QuoteDate != chartToday {
		t.Fatalf("latest %+v", res.Latest)
	}
	// A title without a quote still has a chart, and null latest figures.
	e2 := chartSetup(t)
	e2.seedComplete(t, 18, "EUR", "2026-11-02")
	res, _ = e2.get(t, 18, 200)
	if res.Latest.PriceEur != nil || res.Latest.Change != nil || len(res.Latest.Reasons) == 0 {
		t.Fatalf("latest without quote: %+v", res.Latest)
	}
}

func TestTickerChartPointsWithoutEcbRateAreLeftOut(t *testing.T) {
	e := chartSetup(t)
	e.seedComplete(t, 18, "USD", chartFrom)
	e.fx.missing = true
	if _, code := e.get(t, 18, 502); code != "FX_UNAVAILABLE" {
		t.Fatal(code)
	}
	// With a live quote of today (already EUR) the chart still answers, with a warning.
	q := &marketdata.Quote{InstrumentID: 18, OrigPrice: "10.0000", Price: "10.0000", Currency: "EUR", QuoteDate: chartToday, FetchedAt: chartNow, Provider: marketdata.ProviderYahoo}
	if err := e.h.store.SaveQuote(context.Background(), q, ""); err != nil {
		t.Fatal(err)
	}
	res, _ := e.get(t, 18, 200)
	if res.Warning != "FX_UNAVAILABLE" || len(res.Points) != 1 || res.Points[0].Day != chartToday {
		t.Fatalf("%+v", res)
	}
}

func TestTickerChartConcurrentOpensCostOneYahooRequest(t *testing.T) {
	e := chartSetup(t)
	e.y.series["VHYD.L"] = chartSeries{"EUR", weekdayPoints("2026-06-01", chartTarget)}
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w := featureRequest(e.handler, "GET", "/api/instruments/18/chart", "", e.cookies...); w.Code != 200 {
				t.Errorf("status %d %s", w.Code, w.Body.String())
			}
		}()
	}
	wg.Wait()
	if e.y.calls() != 1 {
		t.Fatalf("Yahoo chart requests: %d", e.y.calls())
	}
}

func TestTickerChartRelinkedSymbolIsFetchedAgain(t *testing.T) {
	e := chartSetup(t)
	// Both listings are younger than the range, so the same-day memo is what answers a second open.
	e.y.series["VHYD.L"] = chartSeries{"EUR", weekdayPoints("2024-01-03", chartTarget)}
	e.y.series["NEWL.L"] = chartSeries{"EUR", weekdayPoints("2025-03-03", chartTarget)}
	res, _ := e.get(t, 18, 200)
	if e.y.calls() != 1 || res.Points[0].Day != "2024-01-03" {
		t.Fatalf("first open: calls=%d first=%s", e.y.calls(), res.Points[0].Day)
	}
	e.get(t, 18, 200)
	if e.y.calls() != 1 {
		t.Fatalf("second open must be a memo hit: %d", e.y.calls())
	}
	// Relinking deletes the stored history of the old listing.
	w := featureRequest(e.handler, "PUT", "/api/instruments/18/yahoo-symbol", `{"symbol":"NEWL.L"}`, e.cookies...)
	if w.Code != 200 {
		t.Fatalf("relink: %d %s", w.Code, w.Body.String())
	}
	// A price job may store a close of the new listing before the chart is opened: not empty, not complete.
	if err := e.h.store.SaveHistory(context.Background(), 18, "NEWL.L", "EUR", marketdata.ProviderYahoo, weekdayPoints("2026-11-20", "2026-11-20"), chartNow); err != nil {
		t.Fatal(err)
	}
	res, _ = e.get(t, 18, 200)
	if e.y.calls() != 2 || e.y.asked[1] != "NEWL.L" || res.Points[0].Day != "2025-03-03" || res.From != "2025-03-03" || res.Points[len(res.Points)-1].Day != chartTarget {
		t.Fatalf("after relink: calls=%d asked=%v from=%s", e.y.calls(), e.y.asked, res.From)
	}
	// An emptied history is never a hit: wipe it behind the memo's back.
	if err := e.h.store.LinkYahooSymbol(context.Background(), 18, "NEWL.L"); err != nil {
		t.Fatal(err)
	}
	res, _ = e.get(t, 18, 200)
	if e.y.calls() != 3 || len(res.Points) == 0 {
		t.Fatalf("empty history served from the memo: calls=%d points=%d", e.y.calls(), len(res.Points))
	}
}

func TestAddMonthsClampsToTheEndOfAShorterMonth(t *testing.T) {
	for _, tc := range [][3]string{
		{"2032-02-29", "-60", "2027-02-28"},
		{"2026-11-26", "-60", "2021-11-26"},
		{"2026-03-31", "-1", "2026-02-28"},
		{"2026-01-31", "-2", "2025-11-30"},
		{"2024-02-29", "12", "2025-02-28"},
	} {
		n, _ := strconv.Atoi(tc[1])
		if got := addMonths(tc[0], n); got != tc[2] {
			t.Errorf("addMonths(%s, %d) = %s, want %s", tc[0], n, got, tc[2])
		}
	}
}

func TestTickerChartCachedFallbackWarnsWhenYahooHasNoData(t *testing.T) {
	// Yahoo knows no data for the symbol: the stored closes are shown with a warning.
	e := chartSetup(t)
	e.seedHistory(t, 18, "EUR", weekdayPoints("2026-10-01", "2026-11-23"), chartNow.Add(-72*time.Hour))
	res, _ := e.get(t, 18, 200)
	if e.y.calls() != 1 || res.Warning != "YAHOO_UNAVAILABLE" || len(res.Points) != len(weekdayPoints("2026-10-01", "2026-11-23")) {
		t.Fatalf("not found: calls=%d warning=%q points=%d", e.y.calls(), res.Warning, len(res.Points))
	}
	// No Yahoo listing at all (the mock search does not know instrument 1): the same, without a chart request.
	e = chartSetup(t)
	if _, err := e.h.store.AddItems(context.Background(), "google-user-1", e.pid, []int64{1}); err != nil {
		t.Fatal(err)
	}
	if err := e.h.store.SaveHistory(context.Background(), 1, "X", "EUR", marketdata.ProviderYahoo, weekdayPoints("2026-10-01", "2026-11-23"), chartNow.Add(-72*time.Hour)); err != nil {
		t.Fatal(err)
	}
	res, _ = e.get(t, 1, 200)
	if e.y.calls() != 0 || res.Warning != "YAHOO_UNAVAILABLE" || len(res.Points) == 0 {
		t.Fatalf("no listing: calls=%d warning=%q points=%d", e.y.calls(), res.Warning, len(res.Points))
	}
}
