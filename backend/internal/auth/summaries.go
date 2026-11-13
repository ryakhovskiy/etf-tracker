package auth

import (
	"context"
	"net/http"
	"time"

	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"holdings/backend/internal/marketdata"
)

// summaryMaxAge is how old the newest quote of the user's held instruments may
// be before the list page refreshes prices (strictly older than this is stale),
// and the minimum gap between two unforced refresh attempts.
const summaryMaxAge = 15 * time.Minute

// Reasons why a refresh was not started.
const (
	refreshFresh        = "FRESH"
	refreshThrottled    = "THROTTLED"
	refreshYahooDown    = "YAHOO_UNAVAILABLE"
	refreshNothingToDo  = "NOTHING_TO_PRICE"
	jobKindUserRealtime = "USER_REALTIME"
)

func (h *Handler) now() time.Time {
	if h.clock != nil {
		return h.clock()
	}
	return time.Now()
}

type refreshYahoo struct {
	Available    bool       `json:"available"`
	BlockedUntil *time.Time `json:"blockedUntil"`
}

type refreshInfo struct {
	Stale           bool              `json:"stale"`
	NewestFetchedAt *time.Time        `json:"newestFetchedAt"`
	MaxAgeSeconds   int               `json:"maxAgeSeconds"`
	AutoFetch       bool              `json:"autoFetch"`
	RunningJobID    *string           `json:"runningJobId"`
	LastAttemptAt   *time.Time        `json:"lastAttemptAt"`
	Yahoo           refreshYahoo      `json:"yahoo"`
	Estimate        *realtimeEstimate `json:"estimate"`
}

// runningJobLocked returns the id of the user's unfinished price job, dropping
// finished jobs older than an hour. Callers hold h.jobs.mu.
func (h *Handler) runningJobLocked(user string) (string, *priceJob) {
	now := time.Now()
	for key, j := range h.jobs.entries {
		if j.Status == "DONE" && now.Sub(j.created) > time.Hour {
			delete(h.jobs.entries, key)
			continue
		}
		if j.user == user && j.Status != "DONE" {
			return key, j
		}
	}
	return "", nil
}

// refreshDecision gathers the facts for a user-level refresh. reason is empty
// when a job may start now (ignoring a running job and the force flag).
func (h *Handler) refreshDecision(ctx context.Context, user string, force bool) (info refreshInfo, rows []database.SavingPlanItem, reason string, err error) {
	now := h.now()
	info = refreshInfo{MaxAgeSeconds: int(summaryMaxAge / time.Second)}
	if rows, err = h.store.UserHeldInstruments(ctx, user); err != nil {
		return
	}
	newest, ok, err := h.store.NewestHeldFetchedAt(ctx, user)
	if err != nil {
		return
	}
	if ok {
		n := newest.UTC()
		info.NewestFetchedAt = &n
	}
	info.Stale = len(rows) > 0 && (!ok || now.Sub(newest) > summaryMaxAge)
	h.jobs.mu.Lock()
	if key, _ := h.runningJobLocked(user); key != "" {
		info.RunningJobID = &key
	}
	last := h.jobs.lastRefresh[user]
	h.jobs.mu.Unlock()
	if !last.IsZero() {
		l := last.UTC()
		info.LastAttemptAt = &l
	}
	b, err := h.yahoo.Budget(ctx)
	if err != nil {
		return
	}
	info.Yahoo = refreshYahoo{Available: b.Remaining > 0, BlockedUntil: b.BlockedUntil}
	if len(rows) > 0 {
		if info.Estimate, err = h.realtimeEstimate(ctx, rows); err != nil {
			return
		}
	}
	switch {
	case len(rows) == 0:
		reason = refreshNothingToDo
	case !info.Yahoo.Available:
		reason = refreshYahooDown
	case !force && !info.Stale:
		reason = refreshFresh
	case !force && !last.IsZero() && now.Sub(last) <= summaryMaxAge:
		reason = refreshThrottled
	}
	info.AutoFetch = reason == "" && info.RunningJobID == nil
	return
}

type refreshResult struct {
	Started      bool       `json:"started"`
	Reused       bool       `json:"reused"`
	JobID        *string    `json:"jobId,omitempty"`
	Reason       string     `json:"reason,omitempty"`
	Requests     *int       `json:"requests,omitempty"`
	BlockedUntil *time.Time `json:"blockedUntil,omitempty"`
}

func (h *Handler) registerSummaryRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/portfolios/summaries", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		s, err := h.store.Summaries(r.Context(), user)
		if err != nil {
			storeError(w, err)
			return
		}
		info, _, _, err := h.refreshDecision(r.Context(), user, false)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, map[string]any{"summaries": s.Summaries, "total": s.Total, "refresh": info})
	}))
	// One Yahoo-only real-time job over the user's held instruments in all
	// portfolios. Never Alpha Vantage, never Eulerpool.
	mux.HandleFunc("POST /api/price-refresh-jobs", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		var in struct {
			Force bool `json:"force"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		ctx := r.Context()
		reuse := func() bool {
			h.jobs.mu.Lock()
			key, _ := h.runningJobLocked(user)
			h.jobs.mu.Unlock()
			if key == "" {
				return false
			}
			httpapi.JSON(w, 200, refreshResult{Reused: true, JobID: &key})
			return true
		}
		if reuse() {
			return
		}
		info, rows, reason, err := h.refreshDecision(ctx, user, in.Force)
		if err != nil {
			storeError(w, err)
			return
		}
		if reason != "" {
			httpapi.JSON(w, 200, refreshResult{Reason: reason, BlockedUntil: blockedUntilFor(reason, info)})
			return
		}
		key, err := token(24)
		if err != nil {
			storeError(w, err)
			return
		}
		h.jobs.mu.Lock()
		now := h.now()
		if existing, _ := h.runningJobLocked(user); existing != "" {
			h.jobs.mu.Unlock()
			httpapi.JSON(w, 200, refreshResult{Reused: true, JobID: &existing})
			return
		}
		// Re-check the throttle under the same lock that inserts the job: a request
		// that passed refreshDecision before another job started and finished must
		// not start a second one inside the window.
		if last := h.jobs.lastRefresh[user]; !in.Force && !last.IsZero() && now.Sub(last) <= summaryMaxAge {
			h.jobs.mu.Unlock()
			httpapi.JSON(w, 200, refreshResult{Reason: refreshThrottled})
			return
		}
		job := &priceJob{Status: "QUEUED", Kind: jobKindUserRealtime, user: user, created: time.Now(), Items: make([]priceItem, 0, len(rows))}
		for _, row := range rows {
			job.Items = append(job.Items, priceItem{InstrumentID: row.InstrumentID, Status: "PENDING"})
		}
		h.jobs.entries[key] = job
		if h.jobs.lastRefresh == nil {
			h.jobs.lastRefresh = map[string]time.Time{}
		}
		h.jobs.lastRefresh[user] = now
		h.jobs.mu.Unlock()
		time.AfterFunc(time.Hour, func() { h.jobs.mu.Lock(); delete(h.jobs.entries, key); h.jobs.mu.Unlock() })
		go h.runYahooRealtime(job, rows, marketdata.BerlinDay(now))
		requests := 0
		if info.Estimate != nil {
			requests = info.Estimate.Requests
		}
		httpapi.JSON(w, 202, refreshResult{Started: true, JobID: &key, Requests: &requests})
	}))
}

func blockedUntilFor(reason string, info refreshInfo) *time.Time {
	if reason == refreshYahooDown {
		return info.Yahoo.BlockedUntil
	}
	return nil
}
