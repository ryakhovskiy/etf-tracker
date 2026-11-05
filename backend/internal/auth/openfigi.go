package auth

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"holdings/backend/internal/marketdata"
)

func (h *Handler) registerOnlineRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/instruments/search/online", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if utf8.RuneCountInString(q) < 4 || len(q) > 200 {
			apiError(w, 400, "INVALID_QUERY", "Enter between 4 and 200 characters.")
			return
		}
		var id int64
		if raw, exists := r.URL.Query()["portfolioId"]; exists {
			var err error
			id, err = strconv.ParseInt(raw[0], 10, 64)
			if err != nil || id < 1 {
				apiError(w, 404, "NOT_FOUND", "Portfolio not found.")
				return
			}
			if _, err = h.store.Portfolio(r.Context(), user, id); err != nil {
				storeError(w, err)
				return
			}
		}
		if r.URL.Query().Get("provider") == "av" {
			_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(30 * time.Minute))
			if marketdata.Classify(q) != "KEYWORD" {
				apiError(w, 400, "INVALID_QUERY", "Alpha Vantage fallback supports keyword queries only.")
				return
			}
			result, err := h.av.Search(r.Context(), user, q)
			if err != nil {
				marketError(w, err)
				return
			}
			if err = h.store.MatchOnline(r.Context(), user, id, result.Results); err != nil {
				storeError(w, err)
				return
			}
			httpapi.JSON(w, 200, result)
			return
		}
		result, err := h.figi.Lookup(r.Context(), q)
		if err != nil {
			if errors.Is(err, marketdata.ErrRateLimited) {
				apiError(w, 429, "FIGI_RATE_LIMITED", "Online search busy, try again in a minute")
			} else {
				apiError(w, 502, "UPSTREAM_ERROR", "Online search unavailable")
			}
			return
		}
		if err = h.store.MatchOnline(r.Context(), user, id, result.Results); err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, result)
	}))
	mux.HandleFunc("POST /api/instruments", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		var row marketdata.Result
		if !decodeBody(w, r, &row) {
			return
		}
		var instrument database.Instrument
		var err error
		if row.Source == "ALPHAVANTAGE" {
			instrument, err = h.store.SaveAVInstrument(r.Context(), user, row)
		} else {
			instrument, err = h.store.SaveOnlineInstrument(r.Context(), row)
		}
		if err != nil {
			marketError(w, err)
			return
		}
		httpapi.JSON(w, 200, instrument)
	}))
}
