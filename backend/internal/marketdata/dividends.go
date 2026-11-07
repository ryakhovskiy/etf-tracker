package marketdata

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/url"
	"sort"
	"strconv"
	"time"
)

// Instrument identifies an instrument for a provider capability. Providers
// resolve their own symbol from it.
type Instrument struct {
	ID          int64
	ISIN        string
	YahooSymbol string
}

// DividendEvent is one distribution: the ex-date (exchange day) and the gross
// amount per share in the listing currency, as a decimal string with 6 places.
type DividendEvent struct {
	ExDate         string
	AmountPerShare string
}

// DividendHistory is what one provider request returns for an instrument.
type DividendHistory struct {
	Symbol          string
	Currency        string            // listing currency of the symbol used
	Events          []DividendEvent   // ascending, only ex-dates inside the requested range
	CloseBefore     map[string]string // ex-date -> close on the last trading day before it
	LatestClose     string            // most recent close, "" when the response has none
	LatestCloseDate string
}

// DividendProvider is the dividend capability of a market data provider.
type DividendProvider interface {
	Dividends(ctx context.Context, inst Instrument, from, to time.Time) (*DividendHistory, error)
}

// dividendLookback is fetched before the range so the first ex-date still has
// the close of the trading day before it.
const dividendLookback = 10

// ParseYahooDividends reads v8/finance/chart with interval=1d&events=div. Events,
// closes and the currency come from the same response. Only events with an
// ex-date in [from, to] are kept; two distributions on one day are added up.
func ParseYahooDividends(b []byte, from, to string) (*DividendHistory, error) {
	series, err := ParseYahooChart(b)
	if err != nil {
		return nil, err
	}
	var raw struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Timezone string `json:"exchangeTimezoneName"`
				} `json:"meta"`
				Events struct {
					Dividends map[string]struct {
						Amount json.Number `json:"amount"`
						Date   int64       `json:"date"`
					} `json:"dividends"`
				} `json:"events"`
			} `json:"result"`
		} `json:"chart"`
	}
	if json.Unmarshal(b, &raw) != nil || len(raw.Chart.Result) != 1 {
		return nil, ErrUpstream
	}
	r := raw.Chart.Result[0]
	loc := time.UTC
	if l, err := time.LoadLocation(r.Meta.Timezone); err == nil {
		loc = l
	}
	amounts := map[string]int64{}
	for _, e := range r.Events.Dividends {
		if e.Date <= 0 {
			continue
		}
		v, err := Scaled(e.Amount.String(), 6)
		if err != nil {
			continue // zero or unreadable amounts carry no distribution
		}
		day := time.Unix(e.Date, 0).In(loc).Format("2006-01-02")
		if day < from || day > to {
			continue
		}
		amounts[day] += v
	}
	out := &DividendHistory{Symbol: series.Symbol, Currency: series.Currency, Events: []DividendEvent{}, CloseBefore: map[string]string{}}
	for day, v := range amounts {
		out.Events = append(out.Events, DividendEvent{ExDate: day, AmountPerShare: Fixed(v, 6)})
	}
	sort.Slice(out.Events, func(i, j int) bool { return out.Events[i].ExDate < out.Events[j].ExDate })
	// Points are ascending: the close before an ex-date is the last point before its day.
	for _, e := range out.Events {
		for i := len(series.Points) - 1; i >= 0; i-- {
			if series.Points[i].Day < e.ExDate {
				out.CloseBefore[e.ExDate] = series.Points[i].Close
				break
			}
		}
	}
	if n := len(series.Points); n > 0 {
		out.LatestClose, out.LatestCloseDate = series.Points[n-1].Close, series.Points[n-1].Day
	}
	return out, nil
}

// Dividends fetches the dividend events, daily closes and listing currency of
// symbol for [from, to] with one chart request.
func (g *YahooGateway) Dividends(ctx context.Context, user, symbol string, from, to time.Time) (*DividendHistory, error) {
	start := from.AddDate(0, 0, -dividendLookback)
	q := url.Values{"period1": {strconv.FormatInt(start.Unix(), 10)}, "period2": {strconv.FormatInt(to.Add(24*time.Hour).Unix(), 10)}, "interval": {"1d"}, "includeAdjustedClose": {"false"}, "events": {"div"}}
	b, err := g.call(ctx, user, "dividends", g.QueryURL, "/v8/finance/chart/"+url.PathEscape(symbol), q)
	if err != nil {
		return nil, err
	}
	h, err := ParseYahooDividends(b, from.Format("2006-01-02"), to.Format("2006-01-02"))
	if err != nil && !errors.Is(err, ErrYahooNotFound) {
		logUpstream(g.Log, slog.LevelError, ProviderYahoo, "unparseable dividend response", "symbol", symbol, "body", snippet(b))
	}
	return h, err
}
