package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"holdings/backend/internal/marketdata"
)

type priceItem struct {
	InstrumentID int64  `json:"instrumentId"`
	Status       string `json:"status"`
	*marketdata.Quote
}
type priceJob struct {
	Status        string      `json:"status"`
	NextRequestAt time.Time   `json:"nextRequestAt"`
	Items         []priceItem `json:"items"`
	user          string
	created       time.Time
}
type priceJobs struct {
	mu      sync.Mutex
	entries map[string]*priceJob
}
type fetchInput struct {
	Date string  `json:"date"`
	IDs  []int64 `json:"instrumentIds"`
}

func marketError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, marketdata.ErrAVLimit), errors.Is(err, marketdata.ErrAVUserLimit):
		apiError(w, 429, err.Error(), "Alpha Vantage budget is unavailable. Try again later.")
	case errors.Is(err, database.ErrAlreadyLinked):
		apiError(w, 409, "ALREADY_LINKED", "This instrument is already linked.")
	case errors.Is(err, database.ErrSymbolInUse):
		apiError(w, 409, "SYMBOL_IN_USE", "This symbol is already linked to another instrument.")
	case errors.Is(err, marketdata.ErrUpstream):
		apiError(w, 502, "UPSTREAM_ERROR", "Market data provider unavailable.")
	default:
		storeError(w, err)
	}
}
func (h *Handler) fetchRows(ctx context.Context, user string, id int64, in fetchInput) ([]database.SavingPlanItem, error) {
	rows, err := h.store.SavingPlan(ctx, user, id)
	if err != nil {
		return nil, err
	}
	if !marketdata.AllowedDate(in.Date, time.Now()) {
		return nil, &database.ValidationError{Code: "VALIDATION_ERROR", Message: "Fetched prices are current prices; enter historical prices manually.", Fields: map[string]string{"date": "Choose today or the previous weekday."}}
	}
	if len(in.IDs) > 100 {
		return nil, database.ErrInvalid
	}
	byID := map[int64]database.SavingPlanItem{}
	for _, r := range rows {
		byID[r.InstrumentID] = r
	}
	seen := map[int64]bool{}
	selected := []database.SavingPlanItem{}
	for _, id := range in.IDs {
		row, ok := byID[id]
		if !ok {
			return nil, database.ErrInvalid
		}
		if !seen[id] {
			selected = append(selected, row)
			seen[id] = true
		}
	}
	return selected, nil
}
func (h *Handler) registerAlphaRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/market-data/budget", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		b, err := h.store.AVBudget(r.Context(), user, time.Now(), h.av.Limits)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, b)
	}))
	mux.HandleFunc("GET /api/portfolios/{id}/price-fetch/estimate", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		in := fetchInput{Date: r.URL.Query().Get("date")}
		if raw := r.URL.Query().Get("instrumentIds"); raw != "" {
			for _, v := range strings.Split(raw, ",") {
				n, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					storeError(w, database.ErrInvalid)
					return
				}
				in.IDs = append(in.IDs, n)
			}
		}
		rows, err := h.fetchRows(r.Context(), user, id, in)
		if err != nil {
			storeError(w, err)
			return
		}
		fresh, noSymbol := []int64{}, []int64{}
		needed := 0
		for _, row := range rows {
			if row.AVSymbol == "" {
				noSymbol = append(noSymbol, row.InstrumentID)
				continue
			}
			q, err := h.store.FreshQuote(r.Context(), row.InstrumentID, time.Now())
			if err != nil {
				storeError(w, err)
				return
			}
			if q != nil {
				fresh = append(fresh, row.InstrumentID)
			} else {
				needed++
			}
		}
		b, err := h.store.AVBudget(r.Context(), user, time.Now(), h.av.Limits)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, map[string]any{"requestsNeeded": needed, "fresh": fresh, "noSymbol": noSymbol, "budget": b})
	}))
	mux.HandleFunc("POST /api/portfolios/{id}/price-fetch-jobs", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		var in fetchInput
		if !decodeBody(w, r, &in) {
			return
		}
		rows, err := h.fetchRows(r.Context(), user, id, in)
		if err != nil {
			storeError(w, err)
			return
		}
		if len(rows) == 0 {
			storeError(w, database.ErrInvalid)
			return
		}
		h.jobs.mu.Lock()
		now := time.Now()
		for key, j := range h.jobs.entries {
			if j.Status == "DONE" && now.Sub(j.created) > time.Hour {
				delete(h.jobs.entries, key)
				continue
			}
			if j.user == user && j.Status != "DONE" {
				h.jobs.mu.Unlock()
				apiError(w, 409, "JOB_RUNNING", "A price fetch job is already running.")
				return
			}
		}
		key, err := token(24)
		if err != nil {
			h.jobs.mu.Unlock()
			storeError(w, err)
			return
		}
		job := &priceJob{Status: "QUEUED", user: user, created: now, Items: []priceItem{}}
		for _, row := range rows {
			job.Items = append(job.Items, priceItem{InstrumentID: row.InstrumentID, Status: "PENDING"})
		}
		h.jobs.entries[key] = job
		h.jobs.mu.Unlock()
		time.AfterFunc(time.Hour, func() { h.jobs.mu.Lock(); delete(h.jobs.entries, key); h.jobs.mu.Unlock() })
		go h.runPrices(job, rows, in.Date)
		httpapi.JSON(w, 202, map[string]string{"jobId": key})
	}))
	mux.HandleFunc("GET /api/price-fetch-jobs/{jobId}", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		h.jobs.mu.Lock()
		defer h.jobs.mu.Unlock()
		j := h.jobs.entries[r.PathValue("jobId")]
		if j == nil || j.user != user || (j.Status == "DONE" && time.Since(j.created) > time.Hour) {
			apiError(w, 404, "NOT_FOUND", "Price fetch job not found.")
			return
		}
		b, err := h.store.AVBudget(r.Context(), user, time.Now(), h.av.Limits)
		if err != nil {
			storeError(w, err)
			return
		}
		j.NextRequestAt = b.NextSlotAt
		httpapi.JSON(w, 200, j)
	}))
	mux.HandleFunc("GET /api/instruments/{id}/av-symbol/search", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		ticker, err := h.store.InstrumentTicker(r.Context(), id)
		if err != nil {
			storeError(w, err)
			return
		}
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
		result, err := h.av.Search(r.Context(), user, ticker)
		if err != nil {
			marketError(w, err)
			return
		}
		httpapi.JSON(w, 200, result)
	}))
	mux.HandleFunc("PUT /api/instruments/{id}/av-symbol", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		var in struct {
			Symbol   string `json:"avSymbol"`
			Currency string `json:"currency"`
			Region   string `json:"region"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		if err := h.store.LinkSymbol(r.Context(), user, id, in.Symbol, in.Currency, in.Region); err != nil {
			marketError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("DELETE /api/instruments/{id}/av-symbol", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		if err := h.store.UnlinkSymbol(r.Context(), id); err != nil {
			marketError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}

// quoteForDate converts a reused quote at the rate for the job's transaction
// date. A quote converted for another day is saved as a new quote (no AV
// request) so committing it keeps matching original price and FX details.
func (h *Handler) quoteForDate(ctx context.Context, q *marketdata.Quote, symbol string, fx *marketdata.FXRate) (*marketdata.Quote, error) {
	c, err := q.ForDate(fx)
	if err != nil || c == q {
		return c, err
	}
	return c, h.store.SaveQuote(ctx, c, symbol)
}
func fxCurrency(c string) string {
	if c == "GBX" || c == "GBp" {
		return "GBP"
	}
	return c
}
func (h *Handler) runPrices(job *priceJob, rows []database.SavingPlanItem, date string) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	h.jobs.mu.Lock()
	job.Status = "RUNNING"
	h.jobs.mu.Unlock()
	rates := map[string]*marketdata.FXRate{}
	fxErrors := map[string]error{}
	fresh := map[int64]*marketdata.Quote{}
	failures := map[int64]error{}
	// Resolve all needed FX before spending any AV budget. Shared currencies fetch once.
	// Fresh quotes need the rate too: another user may have fetched them for another date.
	for _, r := range rows {
		if r.AVSymbol == "" {
			continue
		}
		q, err := h.store.FreshQuote(ctx, r.InstrumentID, time.Now())
		if err != nil {
			failures[r.InstrumentID] = err
			continue
		}
		if q != nil {
			fresh[r.InstrumentID] = q
		}
		ccy := fxCurrency(r.AVCurrency)
		if ccy == "EUR" {
			continue
		}
		if _, ok := rates[ccy]; !ok {
			if _, failed := fxErrors[ccy]; !failed {
				rate, e := h.fx.Rate(ctx, ccy, date)
				if e != nil {
					fxErrors[ccy] = e
				} else {
					rates[ccy] = rate
				}
			}
		}
	}
	limited := false
	for i, r := range rows {
		status := "OK"
		var q *marketdata.Quote
		var err error
		ccy := fxCurrency(r.AVCurrency)
		switch {
		case r.AVSymbol == "":
			status = "NO_SYMBOL"
		case failures[r.InstrumentID] != nil:
			err = failures[r.InstrumentID]
		case fxErrors[ccy] != nil:
			err = fxErrors[ccy]
		case fresh[r.InstrumentID] != nil:
			status = "FRESH"
			q, err = h.quoteForDate(ctx, fresh[r.InstrumentID], r.AVSymbol, rates[ccy])
		case limited:
			status = "RATE_LIMITED"
		default:
			var reused bool
			q, reused, err = h.av.Quote(ctx, job.user, r.AVSymbol, r.AVCurrency, r.InstrumentID, rates[ccy])
			if reused && err == nil {
				status = "FRESH"
				q, err = h.quoteForDate(ctx, q, r.AVSymbol, rates[ccy])
			}
		}
		if err != nil {
			q = nil
			switch {
			case errors.Is(err, marketdata.ErrAVLimit), errors.Is(err, marketdata.ErrAVUserLimit):
				status = "RATE_LIMITED"
				limited = true
			case errors.Is(err, marketdata.ErrFXUnavailable):
				status = "FX_RATE_UNAVAILABLE"
			case errors.Is(err, marketdata.ErrFXUnsupported):
				status = "FX_UNSUPPORTED_CURRENCY"
			default:
				status = "ERROR"
			}
		}
		h.jobs.mu.Lock()
		job.Items[i] = priceItem{InstrumentID: r.InstrumentID, Status: status, Quote: q}
		h.jobs.mu.Unlock()
	}
	h.jobs.mu.Lock()
	job.Status = "DONE"
	h.jobs.mu.Unlock()
}
