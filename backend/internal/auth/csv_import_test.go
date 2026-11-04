package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"holdings/backend/internal/database"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCSVImportAPI(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	for _, route := range []struct{ method, path string }{{"GET", "/api/users/me/csv-settings"}, {"PUT", "/api/users/me/csv-settings"}, {"POST", "/api/portfolios/1/imports/parse"}, {"POST", "/api/portfolios/1/imports/validate"}, {"POST", "/api/portfolios/1/imports"}} {
		w := featureRequest(handler, route.method, route.path, "")
		if w.Code != 401 {
			t.Fatal(route, w.Code)
		}
	}
	cookies := auditLogin(t, h, handler)
	ctx := context.Background()
	p, e := h.store.SavePortfolio(ctx, cookiesUser(t, h, cookies), 0, "CSV")
	if e != nil {
		t.Fatal(e)
	}
	path := fmt.Sprintf("/api/portfolios/%d/imports", p.ID)
	request := func(method, path string, body any, status int) *httptest.ResponseRecorder {
		t.Helper()
		b, _ := json.Marshal(body)
		w := featureRequest(handler, method, path, string(b), cookies...)
		if w.Code != status {
			t.Fatalf("%s: %d %s", path, w.Code, w.Body.String())
		}
		return w
	}
	defaults := database.DefaultCSVSettings()
	w := request("GET", "/api/users/me/csv-settings", nil, 200)
	var stored database.CSVSettingsResult
	json.Unmarshal(w.Body.Bytes(), &stored)
	if stored.Saved {
		t.Fatal(stored)
	}
	request("PUT", "/api/users/me/csv-settings", map[string]any{"settings": defaults}, 200)
	invalid := database.DefaultCSVSettings()
	invalid.Delimiter = ","
	w = request("PUT", "/api/users/me/csv-settings", map[string]any{"settings": invalid}, 400)
	if !strings.Contains(w.Body.String(), "INVALID_SETTINGS") {
		t.Fatal(w.Body.String())
	}
	upload := func(content string, status int) *httptest.ResponseRecorder {
		t.Helper()
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		f, _ := writer.CreateFormFile("file", "reference.csv")
		f.Write([]byte(content))
		config, _ := json.Marshal(defaults)
		writer.WriteField("settings", string(config))
		writer.Close()
		r := httptest.NewRequest("POST", path+"/parse", &body)
		r.Header.Set("Content-Type", writer.FormDataContentType())
		for _, c := range cookies {
			r.AddCookie(c)
		}
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != status {
			t.Fatalf("parse: %d %s", w.Code, w.Body.String())
		}
		return w
	}
	w = upload("ISIN;Amount;Open Price;Date;Total Invest\nUS0378331005;2;10;01.06.2024;20\n", 200)
	var preview database.ImportPreview
	if e := json.Unmarshal(w.Body.Bytes(), &preview); e != nil {
		t.Fatal(e)
	}
	w = request("POST", path+"/validate", map[string]any{"rows": preview.Rows}, 200)
	json.Unmarshal(w.Body.Bytes(), &preview)
	input := database.ImportInput{Rows: preview.Rows, Settings: defaults, Revision: preview.Revision}
	request("POST", path, input, 201)
	w = request("POST", path, input, 409)
	if !strings.Contains(w.Body.String(), "IMPORT_STALE") || !strings.Contains(w.Body.String(), "DUPLICATE") {
		t.Fatal(w.Body.String())
	}
	upload(strings.Repeat("a", database.CSVMaxBytes+1), 413)
	h.store.UpsertGoogleUser(ctx, "other", "other@example.com", "", "")
	foreign, e := h.store.SavePortfolio(ctx, "other", 0, "Private")
	if e != nil {
		t.Fatal(e)
	}
	for _, suffix := range []string{"", "/validate", "/parse"} {
		request("POST", fmt.Sprintf("/api/portfolios/%d/imports%s", foreign.ID, suffix), map[string]any{}, 404)
	}
}
func cookiesUser(t *testing.T, h *Handler, cookies []*http.Cookie) string {
	t.Helper()
	r := httptest.NewRequest("GET", "/", nil)
	for _, c := range cookies {
		r.AddCookie(c)
	}
	user, e := h.tokens.authenticate(r)
	if e != nil {
		t.Fatal(e)
	}
	return user
}
