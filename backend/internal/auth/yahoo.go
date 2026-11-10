package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

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
	item := func(i int, attrs ...any) []any {
		return append([]any{"user", job.user, "instrument", rows[i].InstrumentID, "isin", rows[i].ISIN, "date", date}, attrs...)
	}
	finish := func(i int, price, ccy, quoteDay string) {
		q := &marketdata.Quote{InstrumentID: rows[i].InstrumentID, OrigPrice: price, Price: price, Currency: ccy, QuoteDate: quoteDay, FetchedAt: time.Now(), Provider: marketdata.ProviderYahoo}
		quote := []any{"price", price, "currency", ccy, "quoteDay", quoteDay}
		fx, err := rate(ccy)
		if err != nil {
			h.logger.Error("Yahoo price obtained but no EUR FX rate", item(i, append(quote, "fxCurrency", fxCurrency(ccy), "status", quoteFailure(err), "error", err.Error())...)...)
			set(i, quoteFailure(err), "", nil)
			return
		}
		if err = marketdata.ApplyFX(q, fx); err != nil {
			h.logger.Error("could not convert Yahoo quote to EUR", item(i, append(quote, "fxDate", fx.Date, "error", err.Error())...)...)
			set(i, quoteFailure(err), "", nil)
			return
		}
		if err = h.store.SaveQuote(ctx, q, ""); err != nil {
			h.logger.Error("could not store Yahoo quote", item(i, append(quote, "error", err.Error())...)...)
			set(i, quoteFailure(err), "", nil)
			return
		}
		if fx != nil {
			quote = append(quote, "fxDate", fx.Date, "eurPrice", q.Price)
		}
		h.logger.Info("Yahoo quote stored", item(i, quote...)...)
		set(i, "OK", "", q)
	}
	today := marketdata.BerlinDay(time.Now())
	historical := date != today
	limited := false
	fail := func(i int, err error) {
		h.logger.Warn("Yahoo could not price item", item(i, "error", err.Error())...)
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
			h.logger.Error("could not load instrument for Yahoo pricing", item(i, "error", err.Error())...)
			set(i, "ERROR", "", nil)
			continue
		}
		if !historical {
			q, err := h.store.FreshQuote(ctx, row.InstrumentID, time.Now())
			if err != nil {
				h.logger.Error("could not read cached quote", item(i, "error", err.Error())...)
				set(i, "ERROR", "", nil)
				continue
			}
			if q != nil {
				ccy := q.Currency
				fx, err := rate(ccy)
				if err == nil {
					q, err = h.quoteForDate(ctx, q, row.AVSymbol, fx)
				}
				if err != nil {
					h.logger.Error("cached quote found but could not be converted to EUR", item(i, "currency", ccy, "status", quoteFailure(err), "error", err.Error())...)
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
			h.logger.Warn("no Yahoo symbol for instrument", item(i)...)
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
					h.logger.Warn("Yahoo returned no usable quote for symbol", item(w.i, "symbol", w.symbol)...)
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
				h.logger.Error("could not store Yahoo history", item(w.i, "symbol", w.symbol, "error", err.Error())...)
			}
			p, ok := series.LastCloseOnOrBefore(date)
			if !ok {
				h.logger.Warn("Yahoo history has no close on or before date", item(w.i, "symbol", w.symbol, "points", len(series.Points))...)
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
	// Linking: find the Yahoo symbol of an instrument by name or ticker, then store it.
	mux.HandleFunc("GET /api/instruments/{id}/yahoo-symbol/search", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		term, err := h.store.AVSearchTerm(r.Context(), id)
		if err != nil {
			storeError(w, err)
			return
		}
		if q := strings.TrimSpace(r.URL.Query().Get("q")); q != "" {
			term = q
		}
		if utf8.RuneCountInString(term) < 2 || utf8.RuneCountInString(term) > 100 || strings.ContainsFunc(term, unicode.IsControl) {
			apiError(w, 400, "INVALID_QUERY", "Enter between 2 and 100 characters.")
			return
		}
		result, err := h.searchProvider(r.Context(), marketdata.ProviderYahoo, user, term)
		if err != nil {
			marketError(w, err)
			return
		}
		if err = h.store.MatchOnline(r.Context(), user, 0, result.Results); err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, result)
	}))
	mux.HandleFunc("PUT /api/instruments/{id}/yahoo-symbol", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		var in struct {
			Symbol string `json:"symbol"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		if err := h.store.LinkYahooSymbol(r.Context(), id, in.Symbol); err != nil {
			marketError(w, err)
			return
		}
		// Link the ISIN as well where possible: needed by the Eulerpool fallback.
		ref, err := h.store.InstrumentRef(r.Context(), id)
		if err != nil {
			storeError(w, err)
			return
		}
		isin := ref.ISIN
		if isin == "" {
			var name string
			if name, err = h.store.InstrumentName(r.Context(), id); err == nil {
				if found := h.lookupISIN(r.Context(), user, name, ref.Type); found != "" {
					if filled, e := h.store.FillISIN(r.Context(), id, found); e == nil && filled {
						isin = found
						h.profileInBackground(id)
					}
				}
			}
		}
		httpapi.JSON(w, 200, map[string]string{"symbol": strings.ToUpper(strings.TrimSpace(in.Symbol)), "isin": isin})
	}))
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

func yahooWarning(err error) string {
	switch {
	case errors.Is(err, marketdata.ErrYahooLimit):
		return marketdata.ErrYahooLimit.Error()
	case errors.Is(err, marketdata.ErrYahooAuth):
		return marketdata.ErrYahooAuth.Error()
	}
	return "YAHOO_UNAVAILABLE"
}

func providerWarning(provider string, err error) string {
	if provider == marketdata.ProviderYahoo {
		return yahooWarning(err)
	}
	return epWarning(err)
}

func yahooType(t string) string {
	if t == "ETF" {
		return "ETF"
	}
	return "STOCK"
}

// searchProvider runs one online search against Yahoo or Eulerpool and shapes
// the hits as picker rows. An ISIN query makes the ISIN of the rows known.
func (h *Handler) searchProvider(ctx context.Context, provider, user, q string) (marketdata.Results, error) {
	out := marketdata.Results{Provider: provider, Results: []marketdata.Result{}}
	if provider == marketdata.ProviderYahoo {
		hits, err := h.yahoo.Search(ctx, user, q, 10)
		if err != nil {
			return out, err
		}
		isin := ""
		if marketdata.Classify(q) == "ID_ISIN" {
			isin = marketdata.Normalize(q)
		}
		seen := map[string]bool{}
		for _, hit := range hits {
			if seen[hit.Symbol] {
				continue
			}
			seen[hit.Symbol] = true
			out.Results = append(out.Results, marketdata.Result{Source: provider, YahooSymbol: hit.Symbol, Ticker: hit.Symbol, Name: hit.Name, Type: yahooType(hit.Type), ExchangeName: hit.Exchange, Sector: hit.Sector, ISIN: isin})
		}
		return out, nil
	}
	hits, err := h.ep.Search(ctx, user, q)
	if err != nil {
		return out, err
	}
	for _, hit := range hits {
		out.Results = append(out.Results, marketdata.Result{Source: provider, ISIN: hit.ISIN, Ticker: hit.Ticker, Name: hit.Name, Type: hit.Type, Currency: hit.Currency})
	}
	return out, nil
}

// lookupISIN finds the ISIN of an instrument known by name and type through a
// Eulerpool search (1 request). It returns "" unless the match is clear, and
// never fails the caller: the instrument simply stays without an ISIN.
func (h *Handler) lookupISIN(ctx context.Context, user, name, typ string) string {
	hits, err := h.ep.Search(ctx, user, name)
	if err != nil {
		h.logger.Info("ISIN lookup via Eulerpool skipped", "error", err.Error())
		return ""
	}
	return marketdata.PickISIN(hits, name, typ)
}

// saveYahooPick stores a picked Yahoo row, reusing an instrument that already has
// the symbol or (when the ISIN is found) the ISIN.
func (h *Handler) saveYahooPick(ctx context.Context, user string, row marketdata.Result) (database.Instrument, error) {
	isin := marketdata.Normalize(row.ISIN)
	if !marketdata.ValidISIN(isin) {
		isin = ""
	}
	if isin == "" {
		exists, err := h.store.YahooSymbolExists(ctx, row.YahooSymbol)
		if err != nil {
			return database.Instrument{}, err
		}
		if !exists {
			isin = h.lookupISIN(ctx, user, row.Name, row.Type)
		}
	}
	instrument, err := h.store.SaveYahooInstrument(ctx, row, isin)
	if err == nil && instrument.Type == "ETF" && instrument.ISIN != "" {
		h.profileInBackground(instrument.ID)
	}
	return instrument, err
}
