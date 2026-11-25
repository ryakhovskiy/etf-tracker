package auth

import (
	"net/http"
	"strconv"

	"holdings/backend/internal/httpapi"
)

// instrumentParam parses the {instrumentId} path value; anything that is not a
// positive id is a 404 like any other unknown resource.
func instrumentParam(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("instrumentId"), 10, 64)
	if err != nil || id < 1 {
		apiError(w, 404, "NOT_FOUND", "Item not found.")
		return 0, false
	}
	return id, true
}

// registerFavoriteRoutes wires the per-user favorites (starred titles). Every
// route is session-scoped; a title the user does not hold or has not starred is
// a 404, never a 403.
func (h *Handler) registerFavoriteRoutes(mux *http.ServeMux) {
	list := func(w http.ResponseWriter, r *http.Request, user string) {
		favorites, err := h.store.Favorites(r.Context(), user)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, map[string]any{"favorites": favorites})
	}
	mux.HandleFunc("GET /api/favorites", h.protected(list))
	// Registered before {instrumentId}; the literal path wins in the mux anyway.
	mux.HandleFunc("PUT /api/favorites/order", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		var body struct {
			IDs []int64 `json:"instrumentIds"`
		}
		if !decodeBody(w, r, &body) {
			return
		}
		if body.IDs == nil {
			apiError(w, 400, "INVALID_BODY", "instrumentIds is required.")
			return
		}
		if err := h.store.ReorderFavorites(r.Context(), user, body.IDs); err != nil {
			storeError(w, err)
			return
		}
		list(w, r, user)
	}))
	mux.HandleFunc("PUT /api/favorites/{instrumentId}", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := instrumentParam(w, r)
		if !ok {
			return
		}
		position, err := h.store.StarFavorite(r.Context(), user, id)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, map[string]int64{"instrumentId": id, "position": int64(position)})
	}))
	mux.HandleFunc("DELETE /api/favorites/{instrumentId}", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		id, ok := instrumentParam(w, r)
		if !ok {
			return
		}
		if err := h.store.UnstarFavorite(r.Context(), user, id); err != nil {
			storeError(w, err)
			return
		}
		w.WriteHeader(204)
	}))
}
