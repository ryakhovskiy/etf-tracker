package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"holdings/backend/internal/marketdata"
)

// yahooRecheck is how long "Yahoo does not know this ISIN" is remembered.
const yahooRecheck = 7 * 24 * time.Hour

// yahooHistoryLookback is the window fetched before the requested day, so a
// weekend or holiday still finds the previous close.
const yahooHistoryLookback = 10

// yahooSymbol returns the cached Yahoo symbol of an instrument, resolving it
// from the ISIN (one search request) when needed. An empty result means Yahoo
// has no listing for it.
func (h *Handler) yahooSymbol(ctx context.Context, user string, ref database.InstrumentRef) (string, error) {
	if ref.YahooSymbol != "" {
		return ref.YahooSymbol, nil
	}
	if ref.ISIN == "" || (!ref.YahooCheckedAt.IsZero() && time.Since(ref.YahooCheckedAt) < yahooRecheck) {
		return "", nil
	}
	symbol, err := h.yahoo.ResolveSymbol(ctx, user, ref.ISIN)
	if err != nil {
		return "", err
	}
	if err = h.store.SetYahooSymbol(ctx, ref.ID, symbol, time.Now()); err != nil {
		return "", err
	}
	return symbol, nil
}

type yahooEstimate struct {
	Requests   int     `json:"requests"`
	Remaining  int     `json:"remaining"`
	Historical bool    `json:"historical"`
	Fresh      []int64 `json:"fresh"`
	NoSymbol   []int64 `json:"noSymbol"`
}

// yahooEstimate counts the requests a Yahoo run would send: one search per
// instrument whose symbol is unknown, then one batched quote request per 40
// symbols (today) or one chart request per instrument (past dates).
func (h *Handler) yahooEstimate(ctx context.Context, rows []database.SavingPlanItem, date string) (yahooEstimate, error) {
	out := yahooEstimate{Fresh: []int64{}, NoSymbol: []int64{}, Historical: date != marketdata.BerlinDay(time.Now())}
	priced := 0
	for _, row := range rows {
		ref, err := h.store.InstrumentRef(ctx, row.InstrumentID)
		if err != nil {
			return out, err
		}
		if !out.Historical {
			q, err := h.store.FreshQuote(ctx, row.InstrumentID, time.Now())
			if err != nil {
				return out, err
			}
			if q != nil {
				out.Fresh = append(out.Fresh, row.InstrumentID)
				continue
			}
		} else if p, err := h.store.HistoryDay(ctx, row.InstrumentID, marketdata.ProviderYahoo, date); err != nil {
			return out, err
		} else if p != nil {
			out.Fresh = append(out.Fresh, row.InstrumentID)
			continue
		}
		switch {
		case ref.YahooSymbol != "":
		case ref.ISIN == "" || (!ref.YahooCheckedAt.IsZero() && time.Since(ref.YahooCheckedAt) < yahooRecheck):
			out.NoSymbol = append(out.NoSymbol, row.InstrumentID)
			continue
		default:
			out.Requests++ // symbol search
		}
		priced++
		if out.Historical {
			out.Requests++
		}
	}
	if !out.Historical {
		out.Requests += (priced + marketdata.YahooQuoteBatchSize - 1) / marketdata.YahooQuoteBatchSize
	}
	b, err := h.yahoo.Budget(ctx)
	out.Remaining = b.Remaining
	return out, err
}

// runYahooPrices prices a job from Yahoo Finance. Today's date uses the batched
// latest-quote endpoint; any earlier date uses the daily history and takes the
// last close on or before that date. It only runs when the user asked for it.
func (h *Handler) runYahooPrices(job *priceJob, rows []database.SavingPlanItem, date string) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	h.jobs.mu.Lock()
	job.Status = "RUNNING"
	h.jobs.mu.Unlock()
	set := func(i int, status, reason string, q *marketdata.Quote) {
		h.jobs.mu.Lock()
		job.Items[i] = priceItem{InstrumentID: rows[i].InstrumentID, Status: status, Reason: reason, Quote: q}
		h.jobs.mu.Unlock()
	}
	rates := map[string]*marketdata.FXRate{}
	rate := func(ccy string) (*marketdata.FXRate, error) {
		ccy = fxCurrency(ccy)
		if ccy == "EUR" {
			return nil, nil
		}
		if r, ok := rates[ccy]; ok {
			return r, nil
		}
		r, err := h.fx.Rate(ctx, ccy, date)
		if err == nil {
			rates[ccy] = r
		}
		return r, err
	}
	finish := func(i int, price, ccy, quoteDay string) {
		q := &marketdata.Quote{InstrumentID: rows[i].InstrumentID, OrigPrice: price, Price: price, Currency: ccy, QuoteDate: quoteDay, FetchedAt: time.Now(), Provider: marketdata.ProviderYahoo}
		fx, err := rate(ccy)
		if err == nil {
			err = marketdata.ApplyFX(q, fx)
		}
		if err == nil {
			err = h.store.SaveQuote(ctx, q, "")
		}
		if err != nil {
			h.logger.Error("could not store Yahoo quote", "instrument", rows[i].InstrumentID, "currency", ccy, "error", err.Error())
			set(i, quoteFailure(err), "", nil)
			return
		}
		set(i, "OK", "", q)
	}
	today := marketdata.BerlinDay(time.Now())
	historical := date != today
	limited := false
	fail := func(i int, err error) {
		h.logger.Warn("Yahoo could not price item", "instrument", rows[i].InstrumentID, "isin", rows[i].ISIN, "error", err.Error())
		switch {
		case errors.Is(err, marketdata.ErrYahooLimit):
			limited = true
			set(i, "RATE_LIMITED", "", nil)
		case errors.Is(err, marketdata.ErrYahooNotFound):
			set(i, "NOT_FOUND", "", nil)
		default:
			set(i, "ERROR", "", nil)
		}
	}
	type todo struct {
		i      int
		ref    database.InstrumentRef
		symbol string
	}
	var work []todo
	for i, row := range rows {
		ref, err := h.store.InstrumentRef(ctx, row.InstrumentID)
		if err != nil {
			set(i, "ERROR", "", nil)
			continue
		}
		if !historical {
			q, err := h.store.FreshQuote(ctx, row.InstrumentID, time.Now())
			if err != nil {
				set(i, "ERROR", "", nil)
				continue
			}
			if q != nil {
				fx, err := rate(q.Currency)
				if err == nil {
					q, err = h.quoteForDate(ctx, q, row.AVSymbol, fx)
				}
				if err != nil {
					set(i, quoteFailure(err), "", nil)
				} else {
					set(i, "FRESH", "", q)
				}
				continue
			}
		}
		if limited {
			set(i, "RATE_LIMITED", "", nil)
			continue
		}
		symbol, err := h.yahooSymbol(ctx, job.user, ref)
		switch {
		case err != nil:
			fail(i, err)
		case symbol == "":
			set(i, "NO_SYMBOL", "", nil)
		default:
			work = append(work, todo{i, ref, symbol})
		}
	}
	if !historical {
		for start := 0; start < len(work); start += marketdata.YahooQuoteBatchSize {
			batch := work[start:min(start+marketdata.YahooQuoteBatchSize, len(work))]
			symbols := make([]string, len(batch))
			for k, w := range batch {
				symbols[k] = w.symbol
			}
			var quotes map[string]marketdata.YahooQuote
			var err error
			if limited {
				err = marketdata.ErrYahooLimit
			} else {
				quotes, err = h.yahoo.Quotes(ctx, job.user, symbols)
			}
			for _, w := range batch {
				q, ok := quotes[w.symbol]
				switch {
				case err != nil:
					fail(w.i, err)
				case !ok:
					set(w.i, "NOT_FOUND", "", nil)
				default:
					finish(w.i, q.Price, q.Currency, marketdata.BerlinDay(q.At))
				}
			}
		}
	} else {
		day, _ := time.Parse("2006-01-02", date)
		for _, w := range work {
			if cached, err := h.store.HistoryDay(ctx, w.ref.ID, marketdata.ProviderYahoo, date); err == nil && cached != nil {
				finish(w.i, cached.Close, cached.Currency, cached.Day)
				continue
			}
			if limited {
				set(w.i, "RATE_LIMITED", "", nil)
				continue
			}
			series, err := h.yahoo.History(ctx, job.user, w.symbol, day.AddDate(0, 0, -yahooHistoryLookback), day)
			if err != nil {
				fail(w.i, err)
				continue
			}
			if err = h.store.SaveHistory(ctx, w.ref.ID, w.symbol, series.Currency, marketdata.ProviderYahoo, series.Points, time.Now()); err != nil {
				h.logger.Error("could not store Yahoo history", "instrument", w.ref.ID, "error", err.Error())
			}
			p, ok := series.LastCloseOnOrBefore(date)
			if !ok {
				set(w.i, "NOT_FOUND", "", nil)
				continue
			}
			finish(w.i, p.Close, series.Currency, p.Day)
		}
	}
	h.jobs.mu.Lock()
	job.Status = "DONE"
	h.jobs.mu.Unlock()
}

type historyResponse struct {
	InstrumentID int64                   `json:"instrumentId"`
	Provider     string                  `json:"provider"`
	Symbol       string                  `json:"symbol"`
	Currency     string                  `json:"currency"`
	Prices       []database.HistoryPoint `json:"prices"`
}

func (h *Handler) registerYahooRoutes(mux *http.ServeMux) {
	// Daily closes in the listing currency. Costs 1 Yahoo request (plus a symbol
	// search the first time). Results are stored and also feed historical price fetches.
	mux.HandleFunc("GET /api/instruments/{id}/price-history", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		today := marketdata.BerlinDay(time.Now())
		to, from := r.URL.Query().Get("to"), r.URL.Query().Get("from")
		if to == "" {
			to = today
		}
		if from == "" {
			t, _ := time.Parse("2006-01-02", to)
			from = t.AddDate(-1, 0, 0).Format("2006-01-02")
		}
		f, e1 := time.Parse("2006-01-02", from)
		t, e2 := time.Parse("2006-01-02", to)
		if e1 != nil || e2 != nil || from < "1990-01-01" || to > today || t.Before(f) || t.Sub(f) > 40*366*24*time.Hour {
			apiError(w, 400, "INVALID_RANGE", "Use from/to as YYYY-MM-DD, from 1990-01-01 through today, at most 40 years.")
			return
		}
		ref, err := h.store.InstrumentRef(r.Context(), id)
		if err != nil {
			storeError(w, err)
			return
		}
		symbol, err := h.yahooSymbol(r.Context(), user, ref)
		if err != nil {
			marketError(w, err)
			return
		}
		if symbol == "" {
			apiError(w, 404, "NOT_FOUND", "Yahoo Finance has no listing for this instrument.")
			return
		}
		series, err := h.yahoo.History(r.Context(), user, symbol, f, t)
		if errors.Is(err, marketdata.ErrYahooNotFound) {
			apiError(w, 404, "NOT_FOUND", "Yahoo Finance has no data for this symbol.")
			return
		}
		if err != nil {
			marketError(w, err)
			return
		}
		if err = h.store.SaveHistory(r.Context(), id, symbol, series.Currency, marketdata.ProviderYahoo, series.Points, time.Now()); err != nil {
			storeError(w, err)
			return
		}
		prices := make([]database.HistoryPoint, 0, len(series.Points))
		for _, p := range series.Points {
			prices = append(prices, database.HistoryPoint{Day: p.Day, Close: p.Close, Currency: series.Currency})
		}
		httpapi.JSON(w, 200, historyResponse{InstrumentID: id, Provider: marketdata.ProviderYahoo, Symbol: symbol, Currency: series.Currency, Prices: prices})
	}))
}
