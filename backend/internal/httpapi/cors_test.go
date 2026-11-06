package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Every method registered by the API must pass the browser's CORS preflight.
func TestCORSPreflightAllowsAPIMethods(t *testing.T) {
	h := CORS(http.NotFoundHandler(), "http://localhost:3000")
	for _, method := range []string{"GET", "POST", "PUT", "PATCH", "DELETE"} {
		r := httptest.NewRequest("OPTIONS", "/api/instruments/17/av-symbol", nil)
		r.Header.Set("Origin", "http://localhost:3000")
		r.Header.Set("Access-Control-Request-Method", method)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		allowed := strings.Split(w.Header().Get("Access-Control-Allow-Methods"), ",")
		if w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "http://localhost:3000" || !contains(allowed, method) {
			t.Fatalf("%s preflight: %d %v", method, w.Code, w.Header())
		}
	}
	r := httptest.NewRequest("OPTIONS", "/api/x", nil)
	r.Header.Set("Origin", "https://evil.example")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("foreign origin allowed", w.Code, w.Header())
	}
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
