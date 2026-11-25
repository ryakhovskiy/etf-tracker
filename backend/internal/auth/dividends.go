package auth

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"holdings/backend/internal/marketdata"
)

// dividendRefetchDays is how far before the last sync a later sync starts, so
// the events that form the forecast basis are always refreshed.
const dividendRefetchDays = 400

var errNoYahooListing = errors.New("NO_YAHOO_LISTING")

type dividendJobItem struct {
	InstrumentID int64  `json:"instrumentId"`
	Status       string `json:"status"` // PENDING, OK, ERROR
	Error        string `json:"error,omitempty"`
}

type dividendJob struct {
	Status string            `json:"status"` // RUNNING, DONE
	Done   int               `json:"done"`
	Total  int               `json:"total"`
	Items  []dividendJobItem `json:"items"`
	// Version starts at 1 and grows with every change (see dividendJobs.touch); a
	// client long-polls with ?since=<version> to be answered when it changes.
	Version   int64 `json:"version"`
	user      string
	portfolio int64
	created   time.Time
	// changed wakes the waiters of this job on every touch.
	changed jobSignal
}

type dividendJobs struct {
	mu      sync.Mutex
	entries map[string]*dividendJob
	running map[int64]string // portfolio -> job key
}

// touch records a change of job and wakes everyone waiting for it. The caller
// holds j.mu.
func (j *dividendJobs) touch(job *dividendJob) {
	job.Version++
	job.changed.wake()
}

// snapshot copies a job for encoding outside the lock. The caller holds j.mu.
func (job *dividendJob) snapshot() dividendJob {
	s := *job
	s.Items = make([]dividendJobItem, len(job.Items)) // never nil: it encodes as []
	copy(s.Items, job.Items)
	s.changed = jobSignal{}
	return s
}

// dividendSync bundles what the sync job depends on so tests can replace the
// provider and the clock.
type dividendSync struct {
	jobs     *dividendJobs
	provider func(user string) marketdata.DividendProvider
	now      func() time.Time
	ctx      context.Context
	stop     context.CancelFunc
}

func newDividendSync(h *Handler) *dividendSync {
	ctx, stop := context.WithCancel(context.Background())
	return &dividendSync{
		jobs:     &dividendJobs{entries: map[string]*dividendJob{}, running: map[int64]string{}},
		provider: func(user string) marketdata.DividendProvider { return yahooDividends{h: h, user: user} },
		now:      time.Now,
		ctx:      ctx,
		stop:     stop,
	}
}

// yahooDividends is the Yahoo Finance implementation of DividendProvider. It
// uses the provider's instrument -> symbol resolution (cached on the instrument).
type yahooDividends struct {
	h    *Handler
	user string
}

func (y yahooDividends) Dividends(ctx context.Context, inst marketdata.Instrument, from, to time.Time) (*marketdata.DividendHistory, error) {
	ref, err := y.h.store.InstrumentRef(ctx, inst.ID)
	if err != nil {
		return nil, err
	}
	symbol, err := y.h.yahooSymbol(ctx, y.user, ref)
	if err != nil {
		return nil, err
	}
	if symbol == "" {
		return nil, errNoYahooListing
	}
	return y.h.yahoo.Dividends(ctx, y.user, symbol, from, to)
}

func dividendErrorMessage(err error) string {
	switch {
	case errors.Is(err, errNoYahooListing):
		return "Yahoo Finance has no listing for this instrument."
	case errors.Is(err, marketdata.ErrYahooNotFound):
		return "Yahoo Finance has no data for this symbol."
	case errors.Is(err, marketdata.ErrYahooLimit):
		return "Yahoo Finance is rate limiting requests. Try again later."
	case errors.Is(err, marketdata.ErrYahooAuth):
		return "Could not establish a Yahoo Finance session."
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "The sync was interrupted."
	}
	return "Yahoo Finance is unavailable."
}

// syncInstrument fetches and stores the dividends of one instrument and returns
// a message for the user on failure. The first sync (or one that must reach
// further back) starts at neededFrom; later syncs start 400 days before the
// last one. A failed fetch keeps the cached data and is recorded as last_error.
func (h *Handler) syncInstrument(ctx context.Context, provider marketdata.DividendProvider, id int64, neededFrom string) string {
	const saveFailed = "Could not save dividend data."
	state, err := h.store.DividendSyncState(ctx, id)
	if err != nil {
		if ctx.Err() != nil {
			return dividendErrorMessage(ctx.Err()) // shutting down: not a storage failure
		}
		h.logger.Error("could not read dividend sync state", "instrument", id, "error", err.Error())
		return saveFailed
	}
	now := h.dividends.now()
	today := marketdata.BerlinDay(now)
	from := neededFrom
	if state != nil && !state.LastSyncedAt.IsZero() && state.SyncedFrom <= neededFrom {
		last, _ := time.Parse("2006-01-02", marketdata.BerlinDay(state.LastSyncedAt))
		from = last.AddDate(0, 0, -dividendRefetchDays).Format("2006-01-02")
	}
	f, _ := time.Parse("2006-01-02", from)
	t, _ := time.Parse("2006-01-02", today)
	hist, err := provider.Dividends(ctx, marketdata.Instrument{ID: id}, f, t)
	if err != nil {
		msg := dividendErrorMessage(err)
		if ctx.Err() != nil {
			return msg // shutting down: not a provider failure
		}
		h.logger.Warn("dividend sync failed", "instrument", id, "error", err.Error())
		if e := h.store.RecordDividendError(ctx, id, from, msg, now); e != nil {
			h.logger.Error("could not record dividend sync error", "instrument", id, "error", e.Error())
		}
		return msg
	}
	if err = h.store.SaveDividends(ctx, id, hist, marketdata.ProviderYahoo, from, today, now); err != nil {
		if ctx.Err() != nil {
			return dividendErrorMessage(ctx.Err())
		}
		h.logger.Error("could not store dividends", "instrument", id, "error", err.Error())
		return saveFailed
	}
	return ""
}

// limitWatch stops the sync from sending once Yahoo has answered 429: the requests
// in flight finish, and an instrument that has not started fails with the rate-limit
// error without a request (and is recorded like any other failed fetch).
type limitWatch struct {
	marketdata.DividendProvider
	limited *atomic.Bool
}

func (w limitWatch) Dividends(ctx context.Context, inst marketdata.Instrument, from, to time.Time) (*marketdata.DividendHistory, error) {
	if w.limited.Load() {
		return nil, marketdata.ErrYahooLimit
	}
	hist, err := w.DividendProvider.Dividends(ctx, inst, from, to)
	if errors.Is(err, marketdata.ErrYahooLimit) {
		w.limited.Store(true)
	}
	return hist, err
}

// runDividendSync syncs the instruments in parallel, at most the Yahoo gateway's
// concurrency at a time (the gateway enforces the budget and the 429 cool-down).
// Instruments finish in any order; each one's status is set by its index, and every
// change bumps the job's version for the long pollers.
func (h *Handler) runDividendSync(ctx context.Context, key string, job *dividendJob, neededFrom string) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
	defer cancel()
	d := h.dividends
	var limited atomic.Bool
	provider := limitWatch{d.provider(job.user), &limited}
	d.jobs.mu.Lock()
	ids := make([]int64, len(job.Items))
	for i := range job.Items {
		ids[i] = job.Items[i].InstrumentID
	}
	d.jobs.mu.Unlock()
	set := func(i int, msg string) {
		d.jobs.mu.Lock()
		job.Items[i].Status = "OK"
		if msg != "" {
			job.Items[i].Status, job.Items[i].Error = "ERROR", msg
		}
		job.Done++
		d.jobs.touch(job)
		d.jobs.mu.Unlock()
	}
	forEachParallel(ctx, len(ids), h.yahoo.Concurrency(), func(i int) {
		// forEachParallel may hand out a slot that frees up just as ctx ends: an item
		// that starts after that is interrupted, whatever the store would answer.
		if ctx.Err() != nil {
			set(i, dividendErrorMessage(ctx.Err()))
			return
		}
		set(i, h.syncInstrument(ctx, provider, ids[i], neededFrom))
	}, func(i int) {
		set(i, dividendErrorMessage(ctx.Err()))
	})
	d.jobs.mu.Lock()
	job.Status = "DONE"
	d.jobs.touch(job)
	if d.jobs.running[job.portfolio] == key {
		delete(d.jobs.running, job.portfolio)
	}
	d.jobs.mu.Unlock()
}

func (h *Handler) registerDividendRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/portfolios/{id}/dividends", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		snap, err := h.store.DividendSnapshot(r.Context(), user, id)
		if err != nil {
			storeError(w, err)
			return
		}
		now := h.dividends.now()
		today := marketdata.BerlinDay(now)
		bounds := snap.Bounds(today)
		year := bounds.MaxYear - 1
		if raw := r.URL.Query().Get("year"); raw != "" {
			year, err = strconv.Atoi(raw)
			if err != nil || len(raw) != 4 {
				year = -1
			}
		}
		if year < bounds.MinYear || year > bounds.MaxYear {
			apiError(w, 400, "INVALID_YEAR", "Choose a year from "+strconv.Itoa(bounds.MinYear)+" to "+strconv.Itoa(bounds.MaxYear)+".")
			return
		}
		cal := database.BuildDividendCalendar(snap, year, today, func(currency string, dates []string) (map[string]*marketdata.FXRate, error) {
			rates, err := h.fx.RatesOn(r.Context(), currency, dates)
			if err != nil && !errors.Is(err, marketdata.ErrFXUnsupported) {
				h.logger.Warn("FX rates for dividends unavailable", "currency", currency, "error", err.Error())
			}
			return rates, err
		})
		cal.Sync = snap.SyncInfo(now, today)
		h.dividends.jobs.mu.Lock()
		if key, running := h.dividends.jobs.running[id]; running {
			cal.Sync.JobID = &key
		}
		h.dividends.jobs.mu.Unlock()
		httpapi.JSON(w, 200, cal)
	}))
	// Queues every item (scope ALL, the default) or only the stale ones (scope STALE).
	mux.HandleFunc("POST /api/portfolios/{id}/dividend-sync", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		in := struct {
			Scope string `json:"scope"`
		}{Scope: "ALL"}
		if r.ContentLength != 0 && !decodeBody(w, r, &in) {
			return
		}
		if in.Scope != "ALL" && in.Scope != "STALE" {
			apiError(w, 400, "INVALID_SCOPE", "Scope must be ALL or STALE.")
			return
		}
		snap, err := h.store.DividendSnapshot(r.Context(), user, id)
		if err != nil {
			storeError(w, err)
			return
		}
		d := h.dividends
		now := d.now()
		today := marketdata.BerlinDay(now)
		job := &dividendJob{Version: 1, Status: "RUNNING", Items: []dividendJobItem{}, user: user, portfolio: id, created: now}
		for _, it := range snap.Items {
			if in.Scope == "ALL" || snap.IsStale(it.InstrumentID, now, today) {
				job.Items = append(job.Items, dividendJobItem{InstrumentID: it.InstrumentID, Status: "PENDING"})
			}
		}
		job.Total = len(job.Items)
		d.jobs.mu.Lock()
		for key, j := range d.jobs.entries {
			if j.Status == "DONE" && now.Sub(j.created) > time.Hour {
				delete(d.jobs.entries, key)
			}
		}
		if running, busy := d.jobs.running[id]; busy {
			d.jobs.mu.Unlock()
			httpapi.JSON(w, 409, map[string]any{"error": map[string]string{"code": "SYNC_RUNNING", "message": "A dividend sync is already running for this portfolio.", "jobId": running}})
			return
		}
		key, err := token(24)
		if err != nil {
			d.jobs.mu.Unlock()
			storeError(w, err)
			return
		}
		d.jobs.entries[key] = job
		if job.Total == 0 {
			job.Status = "DONE"
		} else {
			d.jobs.running[id] = key
			if !h.goBackground(func() { h.runDividendSync(d.ctx, key, job, snap.NeededFrom(today)) }) {
				// Closing: nothing can run any more. The job ends at once.
				msg := dividendErrorMessage(context.Canceled)
				for i := range job.Items {
					job.Items[i].Status, job.Items[i].Error = "ERROR", msg
				}
				job.Done, job.Status = job.Total, "DONE"
				delete(d.jobs.running, id)
			}
		}
		d.jobs.mu.Unlock()
		httpapi.JSON(w, 202, map[string]string{"jobId": key})
	}))
	// Answers at once, or with ?since=<version> when the job has changed since that
	// version (long polling, see awaitChange). Every answer carries the job's version.
	mux.HandleFunc("GET /api/dividend-sync-jobs/{jobId}", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		since, long, err := parseSince(r)
		if err != nil {
			storeError(w, err)
			return
		}
		d := h.dividends
		id := r.PathValue("jobId")
		var snap dividendJob
		found := h.awaitChange(r.Context(), &d.jobs.mu, long, func(long bool) (found, ready bool, wait <-chan struct{}) {
			j := d.jobs.entries[id]
			if j == nil || j.user != user || (j.Status == "DONE" && d.now().Sub(j.created) > time.Hour) {
				return false, true, nil
			}
			if !long || j.Version > since || j.Status == "DONE" {
				snap = j.snapshot()
				return true, true, nil
			}
			return true, false, j.changed.wait()
		})
		if !found {
			apiError(w, 404, "NOT_FOUND", "Dividend sync job not found.")
			return
		}
		httpapi.JSON(w, 200, snap)
	}))
}
