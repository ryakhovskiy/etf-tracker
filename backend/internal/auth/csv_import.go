package auth

import (
	"encoding/json"
	"errors"
	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"io"
	"net/http"
)

func importError(w http.ResponseWriter, err error) {
	var e *database.ImportError
	if errors.As(err, &e) {
		status := 409
		if e.Code == "FILE_TOO_LARGE" {
			status = 413
		}
		httpapi.JSON(w, status, map[string]any{"error": e})
		return
	}
	storeError(w, err)
}
func decodeImportBody(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 12*1024*1024))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		apiError(w, 400, "INVALID_BODY", "Invalid import JSON.")
		return false
	}
	if d.Decode(&struct{}{}) != io.EOF {
		apiError(w, 400, "INVALID_BODY", "Expected one JSON object.")
		return false
	}
	return true
}
func (h *Handler) registerImportRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/users/me/csv-settings", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		v, err := h.store.CSVSettings(r.Context(), user)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, v)
	}))
	mux.HandleFunc("PUT /api/users/me/csv-settings", h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
		var in struct {
			Settings json.RawMessage `json:"settings"`
		}
		if !decodeBody(w, r, &in) {
			return
		}
		settings, err := database.DecodeCSVSettings(in.Settings)
		if err != nil {
			storeError(w, err)
			return
		}
		v, err := h.store.SaveCSVSettings(r.Context(), user, settings)
		if err != nil {
			storeError(w, err)
			return
		}
		httpapi.JSON(w, 200, v)
	}))
	for _, action := range []string{"parse", "validate", ""} {
		path := "POST /api/portfolios/{id}/imports"
		if action != "" {
			path += "/" + action
		}
		mux.HandleFunc(path, h.protected(func(w http.ResponseWriter, r *http.Request, user string) {
			id, ok := portfolioID(w, r)
			if !ok {
				return
			}
			if _, err := h.store.Portfolio(r.Context(), user, id); err != nil {
				storeError(w, err)
				return
			}
			if action == "parse" {
				r.Body = http.MaxBytesReader(w, r.Body, database.CSVMaxBytes+64*1024)
				if err := r.ParseMultipartForm(database.CSVMaxBytes + 64*1024); err != nil {
					var large *http.MaxBytesError
					if errors.As(err, &large) {
						apiError(w, 413, "FILE_TOO_LARGE", "File must be at most 1 MB.")
					} else {
						apiError(w, 400, "INVALID_CSV", "Invalid multipart CSV upload.")
					}
					return
				}
				defer r.MultipartForm.RemoveAll()
				settings, err := database.DecodeCSVSettings([]byte(r.FormValue("settings")))
				if err != nil {
					storeError(w, err)
					return
				}
				file, _, err := r.FormFile("file")
				if err != nil {
					apiError(w, 400, "INVALID_CSV", "Choose a CSV file.")
					return
				}
				defer file.Close()
				data, err := io.ReadAll(io.LimitReader(file, database.CSVMaxBytes+1))
				if err != nil {
					apiError(w, 400, "INVALID_CSV", "Could not read file.")
					return
				}
				v, err := h.store.ParseImport(r.Context(), user, id, data, settings)
				if err != nil {
					importError(w, err)
					return
				}
				httpapi.JSON(w, 200, v)
				return
			}
			if action == "validate" {
				var in struct {
					Rows []database.ImportRow `json:"rows"`
				}
				if !decodeImportBody(w, r, &in) {
					return
				}
				v, err := h.store.ValidateImport(r.Context(), user, id, in.Rows)
				if err != nil {
					importError(w, err)
					return
				}
				httpapi.JSON(w, 200, v)
				return
			}
			var in struct {
				Rows     []database.ImportRow `json:"rows"`
				Settings json.RawMessage      `json:"settings"`
				Revision string               `json:"revision"`
			}
			if !decodeImportBody(w, r, &in) {
				return
			}
			settings, err := database.DecodeCSVSettings(in.Settings)
			if err != nil {
				storeError(w, err)
				return
			}
			v, err := h.store.CommitImport(r.Context(), user, id, database.ImportInput{Rows: in.Rows, Settings: settings, Revision: in.Revision})
			if err != nil {
				importError(w, err)
				return
			}
			httpapi.JSON(w, 201, v)
		}))
	}
}
