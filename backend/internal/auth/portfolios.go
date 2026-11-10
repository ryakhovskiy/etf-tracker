package auth

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
)

func apiError(w http.ResponseWriter, status int, code, message string) {
	httpapi.JSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

// errorLog receives failures that are about to become 5xx responses; NewHandler
// replaces it with the application logger.
var errorLog = slog.Default()

func storeError(w http.ResponseWriter, err error) {
	var limit *database.LimitError
	var validation *database.ValidationError
	var duplicate *database.DuplicateError
	switch {
	case errors.As(err, &validation):
		httpapi.JSON(w, 400, map[string]any{"error": validation})
	case errors.As(err, &duplicate):
		httpapi.JSON(w, 409, map[string]any{"error": duplicate})
	case errors.Is(err, database.ErrItemHasTransactions):
		apiError(w, 409, "ITEM_HAS_TRANSACTIONS", err.Error())
	case errors.As(err, &limit):
		httpapi.JSON(w, 409, map[string]any{"error": limit})
	case errors.Is(err, database.ErrNotFound):
		apiError(w, 404, "NOT_FOUND", "Portfolio or item not found.")
	case errors.Is(err, database.ErrNameTaken):
		apiError(w, 409, "NAME_TAKEN", err.Error())
	case errors.Is(err, database.ErrInvalid):
		apiError(w, 400, "INVALID_ITEMS", err.Error())
	default:
		errorLog.Error("request failed with an internal error", "error", err.Error())
		apiError(w, 500, "INTERNAL_ERROR", "Could not complete the request.")
	}
}

// All feature routes authenticate against the existing server-side session.
func (h *Handler) protected(next func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		user, err := h.tokens.authenticate(r)
		if err != nil {
			if errors.Is(err, ErrUnauthorized) {
				apiError(w, 401, "UNAUTHORIZED", "Please sign in.")
			} else {
				apiError(w, 503, "UNAVAILABLE", "Session service unavailable.")
			}
			return
		}
		if r.Method != "GET" {
			if origin := r.Header.Get("Origin"); origin != "" && origin != h.frontendURL {
				apiError(w, 403, "INVALID_ORIGIN", "Origin not allowed.")
				return
			}
		}
		next(w, r, user)
	}
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 16384))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(v); err != nil {
		apiError(w, 400, "INVALID_BODY", "Invalid JSON body.")
		return false
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		apiError(w, 400, "INVALID_BODY", "Expected a single JSON object.")
		return false
	}
	return true
}
func portfolioID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 404, "NOT_FOUND", "Portfolio not found.")
		return 0, false
	}
	return id, true
}
func (h *Handler) registerPortfolioRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/portfolios", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		list, err := h.store.Portfolios(r.Context(), user)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, map[string]any{"portfolios": list, "limit": h.store.Limits().Portfolios})
	}))
	save := func(w http.ResponseWriter, r *http.Request, user string) {
		var id int64
		if r.Method == "PATCH" {
			var ok bool
			id, ok = portfolioID(w, r)
			if !ok {
				return
			}
			if _, err := h.store.Portfolio(r.Context(), user, id); err != nil {
				storeError(w, err)
				return
			}
		}
		var body struct {
			Name string `json:"name"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		body.Name = strings.TrimSpace(body.Name)
		if n := utf8.RuneCountInString(body.Name); n < 1 || n > 100 {
			apiError(w, 400, "INVALID_NAME", "Name must be between 1 and 100 characters.")
			return
		}
		p, err := h.store.SavePortfolio(r.Context(), user, id, body.Name)
		if err != nil {
			storeError(w, err)
			return
		}
		status := 200
		if id == 0 {
			status = 201
		}
		httpapi.JSON(w, status, p)
	}
	mux.HandleFunc("POST /api/portfolios", h.protected(save))
	mux.HandleFunc("PATCH /api/portfolios/{id}", h.protected(save))
	mux.HandleFunc("GET /api/portfolios/{id}", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		p, err := h.store.Portfolio(r.Context(), user, id)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, p)
	}))
	mux.HandleFunc("DELETE /api/portfolios/{id}", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		if err := h.store.DeletePortfolio(r.Context(), user, id); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("POST /api/portfolios/{id}/items", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		var body struct {
			IDs []int64 `json:"instrumentIds"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if len(body.IDs) < 1 || len(body.IDs) > 30 {
			apiError(w, 400, "INVALID_ITEMS", "Select between 1 and 30 instrument ids.")
			return
		}
		result, err := h.store.AddItems(r.Context(), user, id, body.IDs)
		if err != nil {
			storeError(w, err)
			return
		}
		for _, instrument := range body.IDs {
			h.profileInBackground(instrument)
		}
		httpapi.JSON(w, 200, result)
	}))
	// The items of a portfolio with their latest stored quote and the user's pay-date delay.
	mux.HandleFunc("GET /api/portfolios/{id}/items", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		items, err := h.store.PortfolioItems(r.Context(), user, id)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, items)
	}))
	// One pay-date delay per user and instrument, shared by all of the user's portfolios.
	mux.HandleFunc("PUT /api/users/me/instruments/{instrumentId}/pay-delay", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		item, err := strconv.ParseInt(r.PathValue("instrumentId"), 10, 64)
		if err != nil || item < 1 {
			apiError(w, 404, "NOT_FOUND", "Item not found.")
			return
		}
		var body struct {
			Days *int `json:"payDelayDays"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if body.Days == nil {
			apiError(w, 400, "INVALID_BODY", "payDelayDays is required.")
			return
		}
		if err := h.store.SetPayDelay(r.Context(), user, item, *body.Days); err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, map[string]int{"payDelayDays": *body.Days})
	}))
	mux.HandleFunc("DELETE /api/portfolios/{id}/items/{instrumentId}", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		item, err := strconv.ParseInt(r.PathValue("instrumentId"), 10, 64)
		if err != nil || item < 1 {
			apiError(w, 404, "NOT_FOUND", "Item not found.")
			return
		}
		if err := h.store.RemoveItem(r.Context(), user, id, item); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
	mux.HandleFunc("GET /api/instruments/search", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		q := strings.TrimSpace(r.URL.Query().Get("q"))
		if utf8.RuneCountInString(q) < 4 {
			apiError(w, 400, "INVALID_QUERY", "Enter at least 4 characters.")
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
		}
		result, err := h.store.Search(r.Context(), user, q, id)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, result)
	}))
}
