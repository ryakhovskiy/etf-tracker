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

// epWarning maps a Eulerpool failure to the warning code shown above fallback results.
func epWarning(err error) string {
	switch {
	case errors.Is(err, marketdata.ErrEPLimit):
		return marketdata.ErrEPLimit.Error()
	case errors.Is(err, marketdata.ErrEPAuth):
		return marketdata.ErrEPAuth.Error()
	}
	return "EULERPOOL_UNAVAILABLE"
}

// ensureProfile returns the stored ETF profile, fetching it from Eulerpool when
// it is missing, older than the profile TTL, or force is set.
func (h *Handler) ensureProfile(ctx context.Context, user string, ref database.InstrumentRef, force bool) (*marketdata.ETFProfile, error) {
	if ref.Type != "ETF" || ref.ISIN == "" {
		return nil, database.ErrInvalid
	}
	if !force {
		p, err := h.store.ETFProfile(ctx, ref.ID)
		if err == nil && time.Since(p.FetchedAt) < h.profileTTL {
			return p, nil
		}
		if err != nil && !errors.Is(err, database.ErrNotFound) {
			return nil, err
		}
	}
	p, err := h.ep.ETFProfile(ctx, user, ref.ISIN)
	if err != nil {
		return nil, err
	}
	// AUM is reported in the profile currency; convert at the latest ECB rate.
	if p.AUM != "" && p.Currency != "" {
		if p.Currency == "EUR" {
			p.AUMEUR = p.AUM
		} else if rate, e := h.fx.Rate(ctx, marketdata.MajorUnit(p.Currency), marketdata.BerlinDay(time.Now())); e == nil && rate != nil {
			p.AUMEUR = database.AUMToEUR(p.AUM, rate.Rate)
		}
	}
	if err = h.store.SaveETFProfile(ctx, ref.ID, p); err != nil {
		return nil, err
	}
	return h.store.ETFProfile(ctx, ref.ID)
}

// profileInBackground fetches an ETF profile without blocking the request.
func (h *Handler) profileInBackground(id int64) {
	h.bg.Add(1)
	go func() {
		defer h.bg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ref, err := h.store.InstrumentRef(ctx, id)
		if err != nil || ref.Type != "ETF" || ref.ISIN == "" || h.ep == nil {
			return
		}
		if _, err = h.ensureProfile(ctx, "", ref, false); err != nil {
			h.logger.Warn("ETF profile fetch failed", "instrument", id, "error", err.Error())
		}
	}()
}

// WaitBackground blocks until background profile fetches have finished.
func (h *Handler) WaitBackground() { h.bg.Wait() }

type profileResponse struct {
	*marketdata.ETFProfile
	InstrumentID int64 `json:"instrumentId"`
	Stale        bool  `json:"stale"`
}

func (h *Handler) registerEulerpoolRoutes(mux *http.ServeMux) {
	// Finds the ISIN of an instrument that lacks one. Costs one Eulerpool request.
	mux.HandleFunc("GET /api/instruments/{id}/isin/search", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
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
		hits, err := h.ep.Search(r.Context(), user, term)
		if err != nil {
			marketError(w, err)
			return
		}
		result := marketdata.Results{Provider: marketdata.ProviderEulerpool, Results: make([]marketdata.Result, 0, len(hits))}
		for _, hit := range hits {
			result.Results = append(result.Results, marketdata.Result{Source: marketdata.ProviderEulerpool, ISIN: hit.ISIN, Ticker: hit.Ticker, Name: hit.Name, Type: hit.Type, Currency: hit.Currency})
		}
		if err = h.store.MatchOnline(r.Context(), user, 0, result.Results); err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, result)
	}))
	mux.HandleFunc("PUT /api/instruments/{id}/isin", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		var in struct {
			ISIN string `json:"isin"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		if err := h.store.LinkISIN(r.Context(), id, in.ISIN); err != nil {
			marketError(w, err)
			return
		}
		h.profileInBackground(id)
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/instruments/{id}/etf-profile", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		p, err := h.store.ETFProfile(r.Context(), id)
		if errors.Is(err, database.ErrNotFound) {
			apiError(w, 404, "NOT_FOUND", "No ETF profile stored for this instrument.")
			return
		}
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, profileResponse{p, id, time.Since(p.FetchedAt) >= h.profileTTL})
	}))
	mux.HandleFunc("POST /api/instruments/{id}/etf-profile/refresh", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		ref, err := h.store.InstrumentRef(r.Context(), id)
		if err != nil {
			storeError(w, err)
			return
		}
		p, err := h.ensureProfile(r.Context(), user, ref, true)
		if errors.Is(err, marketdata.ErrEPNotFound) {
			apiError(w, 404, "NOT_FOUND", "Eulerpool has no profile for this ETF.")
			return
		}
		if err != nil {
			marketError(w, err)
			return
		}
		httpapi.JSON(w, 200, profileResponse{p, id, false})
	}))
}

type epEstimate struct {
	Requests  int     `json:"requests"`
	Remaining int     `json:"remaining"`
	Fresh     []int64 `json:"fresh"`
	NoISIN    []int64 `json:"noIsin"`
}

// eulerpoolEstimate counts the requests a Eulerpool run would send: one per 50
// stock ISINs, one per ETF, plus one profile for every ETF without a currency.
func (h *Handler) eulerpoolEstimate(ctx context.Context, rows []database.SavingPlanItem) (epEstimate, error) {
	out := epEstimate{Fresh: []int64{}, NoISIN: []int64{}}
	stocks := 0
	for _, row := range rows {
		q, err := h.store.FreshQuote(ctx, row.InstrumentID, time.Now())
		if err != nil {
			return out, err
		}
		switch {
		case q != nil:
			out.Fresh = append(out.Fresh, row.InstrumentID)
		case row.ISIN == "":
			out.NoISIN = append(out.NoISIN, row.InstrumentID)
		case row.Type == "ETF":
			out.Requests++
			ref, err := h.store.InstrumentRef(ctx, row.InstrumentID)
			if err != nil {
				return out, err
			}
			if ref.Currency == "" {
				out.Requests++
			}
		default:
			stocks++
		}
	}
	out.Requests += (stocks + marketdata.EPQuoteBatchSize - 1) / marketdata.EPQuoteBatchSize
	b, err := h.ep.Budget(ctx)
	out.Remaining = b.Remaining
	return out, err
}

func quoteFailure(err error) string {
	switch {
	case errors.Is(err, marketdata.ErrFXUnavailable):
		return "FX_RATE_UNAVAILABLE"
	case errors.Is(err, marketdata.ErrFXUnsupported):
		return "FX_UNSUPPORTED_CURRENCY"
	}
	return "ERROR"
}

// runEulerpoolPrices prices a job from Eulerpool only. Anything it cannot price
// ends as NEEDS_BACKUP; Alpha Vantage is never called from here.
func (h *Handler) runEulerpoolPrices(job *priceJob, rows []database.SavingPlanItem, date string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	h.jobs.mu.Lock()
	job.Status = "RUNNING"
	h.jobs.mu.Unlock()
	set := func(i int, status, reason string, q *marketdata.Quote) {
		h.jobs.mu.Lock()
		job.Items[i] = priceItem{InstrumentID: rows[i].InstrumentID, Status: status, Reason: reason, Quote: q}
		h.jobs.mu.Unlock()
	}
	backup := func(i int, reason string) { set(i, "NEEDS_BACKUP", reason, nil) }
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
	// finish converts a Eulerpool price to EUR and stores it.
	finish := func(i int, price, ccy string, at time.Time) {
		q := &marketdata.Quote{InstrumentID: rows[i].InstrumentID, OrigPrice: price, Price: price, Currency: ccy, QuoteDate: marketdata.BerlinDay(at), FetchedAt: time.Now(), Provider: marketdata.ProviderEulerpool}
		fx, err := rate(ccy)
		if err == nil {
			err = marketdata.ApplyFX(q, fx)
		}
		if err == nil {
			err = h.store.SaveQuote(ctx, q, "")
		}
		if err != nil {
			h.logger.Error("could not store Eulerpool quote", "instrument", rows[i].InstrumentID, "currency", ccy, "error", err.Error())
			set(i, quoteFailure(err), "", nil)
			return
		}
		set(i, "OK", "", q)
	}
	var stocks, etfs []int
	for i, row := range rows {
		q, err := h.store.FreshQuote(ctx, row.InstrumentID, time.Now())
		switch {
		case err != nil:
			set(i, "ERROR", "", nil)
		case q != nil:
			var fx *marketdata.FXRate
			if fx, err = rate(q.Currency); err == nil {
				q, err = h.quoteForDate(ctx, q, row.AVSymbol, fx)
			}
			if err != nil {
				set(i, quoteFailure(err), "", nil)
			} else {
				set(i, "FRESH", "", q)
			}
		case row.ISIN == "":
			backup(i, "NO_ISIN")
		case row.Type == "ETF":
			etfs = append(etfs, i)
		default:
			stocks = append(stocks, i)
		}
	}
	limited := false
	fail := func(i int, err error) {
		h.logger.Warn("Eulerpool could not price item", "instrument", rows[i].InstrumentID, "isin", rows[i].ISIN, "type", rows[i].Type, "error", err.Error())
		switch {
		case errors.Is(err, marketdata.ErrEPLimit):
			limited = true
			backup(i, "EULERPOOL_LIMIT_REACHED")
		case errors.Is(err, marketdata.ErrEPNotFound):
			backup(i, "NOT_FOUND")
		case errors.Is(err, marketdata.ErrEPAuth):
			backup(i, "EULERPOOL_AUTH_ERROR")
		default:
			backup(i, "ERROR")
		}
	}
	for start := 0; start < len(stocks); start += marketdata.EPQuoteBatchSize {
		batch := stocks[start:min(start+marketdata.EPQuoteBatchSize, len(stocks))]
		isins := make([]string, len(batch))
		for k, i := range batch {
			isins[k] = rows[i].ISIN
		}
		quotes, failures, err := h.ep.Bulk(ctx, job.user, isins)
		for _, i := range batch {
			q, ok := quotes[rows[i].ISIN]
			switch {
			case err != nil:
				fail(i, err)
			case ok:
				finish(i, q.Price, q.Currency, q.At)
			case failures[rows[i].ISIN] != "":
				backup(i, "NOT_FOUND")
			default:
				backup(i, "NOT_FOUND")
			}
		}
	}
	for _, i := range etfs {
		if limited {
			backup(i, "EULERPOOL_LIMIT_REACHED")
			continue
		}
		ref, err := h.store.InstrumentRef(ctx, rows[i].InstrumentID)
		if err != nil {
			set(i, "ERROR", "", nil)
			continue
		}
		// The price series has no currency, so it comes from the ETF profile.
		if ref.Currency == "" {
			p, e := h.ensureProfile(ctx, job.user, ref, true)
			if e != nil {
				fail(i, e)
				continue
			}
			ref.Currency = p.Currency
		}
		if ref.Currency == "" {
			backup(i, "NO_CURRENCY")
			continue
		}
		if fresh, e := h.store.InstrumentRef(ctx, rows[i].InstrumentID); e == nil {
			ref.NAV = fresh.NAV
		}
		price, at, ok, err := h.ep.ETFQuote(ctx, job.user, ref.ISIN)
		switch {
		case err != nil:
			fail(i, err)
		case !ok:
			backup(i, "EMPTY_SERIES")
		default:
			if scaled, changed := marketdata.ReconcileETFPrice(price, ref.NAV); changed {
				h.logger.Warn("Eulerpool ETF series looked mis-scaled; rescaled using the profile NAV", "isin", ref.ISIN, "series", price, "nav", ref.NAV, "used", scaled)
				price = scaled
			}
			finish(i, price, ref.Currency, at)
		}
	}
	h.jobs.mu.Lock()
	job.Status = "DONE"
	h.jobs.mu.Unlock()
}
