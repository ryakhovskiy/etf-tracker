package auth

import (
	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"net/http"
	"strconv"
	"strings"
)

func (h *Handler) registerTransactionRoutes(mux *http.ServeMux) {
	for _, resource := range []string{"saving-plan", "performance", "transactions"} {
		mux.HandleFunc("GET /api/portfolios/{id}/"+resource, h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
			id, ok := portfolioID(w, r)
			if !ok {
				return
			}
			var value any
			var err error
			switch resource {
			case "saving-plan":
				value, err = h.store.SavingPlan(r.Context(), user, id)
			case "performance":
				var instrumentIDs []int64
				if values, present := r.URL.Query()["instrumentIds"]; present {
					instrumentIDs = []int64{}
					if len(values) != 1 || len(strings.Split(values[0], ",")) > 100 {
						apiError(w, 400, "INVALID_ITEMS", "Choose up to 100 instrument IDs.")
						return
					}
					if values[0] != "" {
						for _, raw := range strings.Split(values[0], ",") {
							instrumentID, parseErr := strconv.ParseInt(raw, 10, 64)
							if parseErr != nil || instrumentID < 1 {
								apiError(w, 400, "INVALID_ITEMS", "Instrument IDs must be positive integers.")
								return
							}
							instrumentIDs = append(instrumentIDs, instrumentID)
						}
					}
				}
				value, err = h.store.PerformanceForInstruments(r.Context(), user, id, instrumentIDs)
			case "transactions":
				page := 1
				if raw := r.URL.Query().Get("page"); raw != "" {
					page, err = strconv.Atoi(raw)
					if err != nil || page < 1 || page > 1000000 {
						apiError(w, 400, "INVALID_PAGE", "Page must be between 1 and 1000000.")
						return
					}
				}
				pageSize := 50
				if raw := r.URL.Query().Get("pageSize"); raw != "" {
					pageSize, err = strconv.Atoi(raw)
					if err != nil || pageSize < 1 || pageSize > 250 {
						apiError(w, 400, "INVALID_PAGE_SIZE", "Page size must be between 1 and 250.")
						return
					}
				}
				value, err = h.store.Transactions(r.Context(), user, id, page, pageSize)
			}
			if err != nil {
				storeError(w, err)
				return
			}
			httpapi.JSON(w, 200, value)
		}))
	}
	mux.HandleFunc("POST /api/portfolios/{id}/transactions", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		if _, err := h.store.Portfolio(r.Context(), user, id); err != nil {
			storeError(w, err)
			return
		}
		var in database.TransactionInput
		if !decodeBody(w, r, &in) {
			return
		}
		txID, err := h.store.CreateTransaction(r.Context(), user, id, in)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 201, map[string]int64{"id": txID})
	}))
	mux.HandleFunc("DELETE /api/portfolios/{id}/transactions/{txId}", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := portfolioID(w, r)
		if !ok {
			return
		}
		txID, err := strconv.ParseInt(r.PathValue("txId"), 10, 64)
		if err != nil || txID < 1 {
			apiError(w, 404, "NOT_FOUND", "Transaction not found.")
			return
		}
		if err := h.store.DeleteTransaction(r.Context(), user, id, txID); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}
