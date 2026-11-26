package auth

import (
	"context"
	"errors"
	"net/http"
	"sync"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"holdings/backend/internal/marketdata"
)

const (
	// chartMonths is how far back a ticker chart reaches at least.
	chartMonths = 60
	// warningFXUnavailable marks points left out because no ECB rate was available.
	warningFXUnavailable = "FX_UNAVAILABLE"
)

// tickerChartState remembers, per instrument, the last Yahoo history request of
// this process, so a title whose history cannot be complete (it is younger than
// the chart range, or has a long market closure) is asked for at most once a
// day. The zero value is ready to use.
type tickerChartState struct {
	mu    sync.Mutex
	locks map[int64]*sync.Mutex
	seen  map[int64]chartFetch
}

type chartFetch struct{ day, from, symbol string }

// lock serializes the cache check and the Yahoo request of one instrument, so
// concurrent opens of the same chart cost one request.
func (s *tickerChartState) lock(id int64) func() {
	s.mu.Lock()
	if s.locks == nil {
		s.locks = map[int64]*sync.Mutex{}
	}
	m := s.locks[id]
	if m == nil {
		m = &sync.Mutex{}
		s.locks[id] = m
	}
	s.mu.Unlock()
	m.Lock()
	return m.Unlock
}

// fetchedToday reports a successful request of today for this very listing: a
// title linked to another Yahoo symbol since (its stored history is deleted on
// relinking) is a different listing and is requested again.
func (s *tickerChartState) fetchedToday(id int64, today, from, symbol string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, ok := s.seen[id]
	return ok && f.day == today && f.from <= from && f.symbol == symbol
}

func (s *tickerChartState) markFetched(id int64, today, from, symbol string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.seen == nil {
		s.seen = map[int64]chartFetch{}
	}
	s.seen[id] = chartFetch{day: today, from: from, symbol: symbol}
}

type tickerPoint struct {
	Day      string `json:"day"`
	CloseEUR string `json:"closeEur"`
}

type tickerChartResponse struct {
	Instrument database.TickerInstrument `json:"instrument"`
	Favorite   bool                      `json:"favorite"`
	Latest     database.TickerLatest     `json:"latest"`
	Points     []tickerPoint             `json:"points"`
	Buys       []database.TickerBuy      `json:"buys"`
	From       string                    `json:"from"`
	To         string                    `json:"to"`
	Warning    string                    `json:"warning,omitempty"`
}

// lastTradingDay is the newest weekday before day: the latest session whose
// close can be complete. Market holidays are not known here.
func lastTradingDay(day string) string {
	d, _ := time.Parse("2006-01-02", day)
	for d = d.AddDate(0, 0, -1); d.Weekday() == time.Saturday || d.Weekday() == time.Sunday; d = d.AddDate(0, 0, -1) {
	}
	return d.Format("2006-01-02")
}

func addDays(day string, n int) string {
	d, _ := time.Parse("2006-01-02", day)
	return d.AddDate(0, 0, n).Format("2006-01-02")
}

// hasWeekday reports whether a Monday to Friday lies in [from, to).
func hasWeekday(from, to string) bool {
	d, _ := time.Parse("2006-01-02", from)
	end, _ := time.Parse("2006-01-02", to)
	for ; d.Before(end); d = d.AddDate(0, 0, 1) {
		if d.Weekday() != time.Saturday && d.Weekday() != time.Sunday {
			return true
		}
	}
	return false
}

// chartCovered reports whether the stored closes are confirmed complete over
// [from, target], where target is the latest weekday that can have a close. A
// weekday without a close counts as a market closure only when one Yahoo
// request saw it: its neighbours were stored by the same SaveHistory call (the
// same fetched_at), because a range request returns every session in between.
// Anything else is a gap and is requested. So:
//   - the series must start at from, or at the first weekday after it;
//   - it must reach target (a closure on target costs one request a day, which
//     the per-day memo of the handler bounds);
//   - between two closes of different fetches no weekday may be missing.
func chartCovered(points []database.HistoryPoint, from, target string) bool {
	if len(points) == 0 || hasWeekday(from, points[0].Day) || points[len(points)-1].Day < target {
		return false
	}
	for i := 1; i < len(points); i++ {
		a, b := points[i-1], points[i]
		if a.FetchedAt != b.FetchedAt && hasWeekday(addDays(a.Day, 1), b.Day) {
			return false
		}
	}
	return true
}

// tickerChartPoints converts stored closes to EUR at the ECB rate of each day
// (minor units via the major currency ÷ 100). A point without a usable rate is
// left out and the result says so.
func (h *Handler) tickerChartPoints(ctx context.Context, stored []database.HistoryPoint) (points []tickerPoint, rateMissing bool, err error) {
	points = make([]tickerPoint, 0, len(stored)+1)
	days := map[string][]string{}
	for _, p := range stored {
		if p.Currency != "EUR" {
			days[p.Currency] = append(days[p.Currency], p.Day)
		}
	}
	rates := map[string]map[string]*marketdata.FXRate{}
	for currency, dates := range days {
		if rates[currency], err = h.fx.RatesOn(ctx, currency, dates); err != nil {
			if !errors.Is(err, marketdata.ErrFXUnsupported) && !errors.Is(err, marketdata.ErrFXUnavailable) && ctx.Err() == nil {
				h.logger.Warn("FX rates for ticker chart unavailable", "currency", currency, "error", err.Error())
			}
			rates[currency] = nil
		}
	}
	for _, p := range stored {
		scaled, e := marketdata.Scaled(p.Close, 4)
		if e != nil {
			continue
		}
		if p.Currency == "EUR" {
			points = append(points, tickerPoint{Day: p.Day, CloseEUR: marketdata.Fixed(scaled, 4)})
			continue
		}
		rate := rates[p.Currency][p.Day]
		if rate == nil {
			rateMissing = true
			continue
		}
		div := int64(1)
		if marketdata.MinorUnit(p.Currency) {
			div = 100
		}
		eur, e := marketdata.Convert(scaled, rate.Rate, div)
		if e != nil {
			continue
		}
		points = append(points, tickerPoint{Day: p.Day, CloseEUR: marketdata.Fixed(eur, 4)})
	}
	return points, rateMissing, nil
}

func (h *Handler) registerTickerChartRoutes(mux *http.ServeMux) {
	// The chart of a title in one of the user's portfolios: daily closes in EUR
	// (ECB rate of each day), the user's buys of it and its latest figures. Stored
	// closes are used when they cover the range; Yahoo is asked only for a gap.
	mux.HandleFunc("GET /api/instruments/{id}/chart", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		ctx := r.Context()
		base, err := h.store.TickerBase(ctx, user, id)
		if err != nil {
			storeError(w, err)
			return
		}
		today := marketdata.BerlinDay(h.now())
		from := addMonths(today, -chartMonths)
		if len(base.Buys) > 0 && base.Buys[0].Day < from {
			from = base.Buys[0].Day
		}
		yesterday, target := addDays(today, -1), lastTradingDay(today)

		stored, yahooErr, err := h.chartHistory(ctx, user, id, from, yesterday, today, target)
		if err != nil {
			storeError(w, err)
			return
		}
		points, rateMissing, err := h.tickerChartPoints(ctx, stored)
		if err != nil {
			storeError(w, err)
			return
		}
		// A quote of today is the live end of the line; the closes stop at yesterday.
		if q := base.Quote; q != nil && q.QuoteDate == today {
			if scaled, e := marketdata.Scaled(q.Price, 4); e == nil {
				points = append(points, tickerPoint{Day: today, CloseEUR: marketdata.Fixed(scaled, 4)})
			}
		}
		// Yahoo having no data for the title is trouble too, whenever stored points
		// are shown instead: the existing YAHOO_UNAVAILABLE code covers it.
		noListing := errors.Is(yahooErr, errChartNoListing) || errors.Is(yahooErr, marketdata.ErrYahooNotFound)
		warning := ""
		switch {
		case yahooErr != nil:
			warning = yahooWarning(yahooErr)
		case rateMissing:
			warning = warningFXUnavailable
		}
		if len(points) == 0 {
			switch {
			case rateMissing:
				apiError(w, 502, warningFXUnavailable, "No ECB exchange rate is available to convert the price history to EUR.")
			case yahooErr != nil && !noListing:
				marketError(w, yahooErr)
			default:
				apiError(w, 404, "NO_PRICE_HISTORY", "There is no price history for this instrument.")
			}
			return
		}
		if points[0].Day > from {
			from = points[0].Day
		}
		httpapi.JSON(w, 200, tickerChartResponse{Instrument: base.Instrument, Favorite: base.Favorite, Latest: base.Latest, Points: points, Buys: base.Buys, From: from, To: today, Warning: warning})
	}))
}

// addMonths moves a day by whole months and clamps to the end of a shorter
// month (2032-02-29 minus 60 months is 2027-02-28, not 2027-03-01).
func addMonths(day string, n int) string {
	d, _ := time.Parse("2006-01-02", day)
	first := time.Date(d.Year(), d.Month(), 1, 0, 0, 0, 0, time.UTC).AddDate(0, n, 0)
	last := first.AddDate(0, 1, -1).Day()
	return time.Date(first.Year(), first.Month(), min(d.Day(), last), 0, 0, 0, 0, time.UTC).Format("2006-01-02")
}

// errChartNoListing means Yahoo has no symbol for the title.
var errChartNoListing = errors.New("no Yahoo listing")

// chartHistory returns the stored closes of [from, to], first asking Yahoo for
// [from, to] when they do not cover [from, target]. Yahoo trouble is not a
// failure: the stored closes are returned together with yahooErr (an
// errChartNoListing or ErrYahooNotFound means Yahoo has no data for the title).
// Only store failures are returned as err.
func (h *Handler) chartHistory(ctx context.Context, user string, id int64, from, to, today, target string) (stored []database.HistoryPoint, yahooErr, err error) {
	defer h.chart.lock(id)()
	ref, err := h.store.InstrumentRef(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	if stored, err = h.store.History(ctx, id, marketdata.ProviderYahoo, from, to); err != nil {
		return nil, nil, err
	}
	// An empty history is never a hit, whatever was remembered.
	if chartCovered(stored, from, target) || (len(stored) > 0 && h.chart.fetchedToday(id, today, from, ref.YahooSymbol)) {
		return stored, nil, nil
	}
	symbol, err := h.yahooSymbol(ctx, user, ref)
	if err != nil {
		return stored, err, nil
	}
	if symbol == "" {
		return stored, errChartNoListing, nil
	}
	f, _ := time.Parse("2006-01-02", from)
	t, _ := time.Parse("2006-01-02", to)
	series, err := h.yahoo.History(ctx, user, symbol, f, t)
	if err != nil {
		return stored, err, nil
	}
	if err = h.store.SaveHistory(ctx, id, symbol, series.Currency, marketdata.ProviderYahoo, series.Points, h.now()); err != nil {
		return nil, nil, err
	}
	h.chart.markFetched(id, today, from, symbol)
	stored, err = h.store.History(ctx, id, marketdata.ProviderYahoo, from, to)
	return stored, nil, err
}
