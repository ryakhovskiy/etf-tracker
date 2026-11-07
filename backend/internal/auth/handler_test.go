package auth

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"holdings/backend/internal/config"
	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"holdings/backend/internal/marketdata"
)

type auditTransport func(*http.Request) (*http.Response, error)

func (f auditTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func auditHandler(t *testing.T, path string) (*Handler, http.Handler) {
	t.Helper()
	store, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := config.Config{FrontendURL: "https://app.example.com", GoogleClientID: "client", GoogleClientSecret: "secret", GoogleRedirectURL: "https://api.example.com/auth/google/callback", JWTSecret: []byte("audit-secret-with-at-least-32-bytes"), SecureSessionCookie: true, MarketdataPrimary: marketdata.ProviderAlphaVantage}
	h := NewHandler(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(h.Close)
	mux := http.NewServeMux()
	h.RegisterRoutes(mux)
	return h, httpapi.SecurityHeaders(httpapi.CORS(mux, cfg.FrontendURL), true)
}

func auditGoogle(h *Handler, profile string) {
	h.google.httpClient = &http.Client{Transport: auditTransport(func(r *http.Request) (*http.Response, error) {
		body := profile
		if r.URL.Host == "oauth2.googleapis.com" {
			body = `{"access_token":"provider-access-token"}`
		}
		return &http.Response{StatusCode: 200, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
}

func auditRequest(handler http.Handler, method, path, origin string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, nil)
	if origin != "" {
		r.Header.Set("Origin", origin)
	}
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, r)
	return w
}

func auditLogin(t *testing.T, h *Handler, handler http.Handler) []*http.Cookie {
	t.Helper()
	auditGoogle(h, `{"sub":"google-user-1","email":"person@example.com","email_verified":true,"name":"Person"}`)
	start := auditRequest(handler, "GET", "/auth/google", "")
	location, err := url.Parse(start.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	callback := auditRequest(handler, "GET", "/auth/google/callback?code=valid&state="+location.Query().Get("state"), "", start.Result().Cookies()...)
	if callback.Code != 302 {
		t.Fatalf("callback: %d %s", callback.Code, callback.Body.String())
	}
	return callback.Result().Cookies()
}

func TestAuditGoogleOIDCSubject(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	auditGoogle(h, `{"sub":"google-user-1","email":"person@example.com","email_verified":true,"name":"Person"}`)
	w := auditRequest(handler, "GET", "/auth/google/callback?code=valid&state=state", "", &http.Cookie{Name: stateCookie, Value: "state"}, &http.Cookie{Name: verifierCookie, Value: "verifier"})
	if w.Code != 302 {
		t.Fatalf("valid Google OIDC userinfo rejected: status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestAuditMeFrontendContract(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	w := auditRequest(handler, "GET", "/auth/me", "https://app.example.com", cookies...)
	if w.Code != 200 {
		t.Fatalf("me: %d", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body["name"] != "Person" || body["email"] != "person@example.com" {
		t.Fatalf("frontend contract missing lowercase name/email: %s", w.Body.String())
	}
	if len(body) != 4 || body["id"] != "google-user-1" {
		t.Fatalf("unexpected public user fields: %v", body)
	}
}

func TestAuditFlowAndGuards(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	start := auditRequest(handler, "GET", "/auth/google", "")
	location, _ := url.Parse(start.Header().Get("Location"))
	var verifier string
	for _, c := range start.Result().Cookies() {
		if c.Name == verifierCookie {
			verifier = c.Value
		}
		if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteLaxMode {
			t.Fatal("unsafe OAuth cookie")
		}
	}
	if location.Query().Get("code_challenge") != pkceChallenge(verifier) || location.Query().Get("code_challenge_method") != "S256" {
		t.Fatal("PKCE mismatch")
	}
	cases := []struct {
		name, path string
		cookies    []*http.Cookie
	}{
		{"missing state", "/auth/google/callback?code=x", nil},
		{"wrong state", "/auth/google/callback?code=x&state=wrong", []*http.Cookie{{Name: stateCookie, Value: "correct"}}},
		{"missing verifier", "/auth/google/callback?code=x&state=s", []*http.Cookie{{Name: stateCookie, Value: "s"}}},
		{"missing code", "/auth/google/callback?state=s", []*http.Cookie{{Name: stateCookie, Value: "s"}, {Name: verifierCookie, Value: "v"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if w := auditRequest(handler, "GET", tc.path, "", tc.cookies...); w.Code != 400 {
				t.Fatalf("status=%d", w.Code)
			}
		})
	}
	auditGoogle(h, `{"sub":"user","email":"person@example.com","email_verified":false}`)
	if w := auditRequest(handler, "GET", "/auth/google/callback?code=x&state=s", "", &http.Cookie{Name: stateCookie, Value: "s"}, &http.Cookie{Name: verifierCookie, Value: "v"}); w.Code != 502 {
		t.Fatal("unverified email accepted")
	}
	cookies := auditLogin(t, h, handler)
	for _, c := range cookies {
		if c.Name == accessCookie || c.Name == refreshCookie {
			if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode {
				t.Fatal("unsafe session cookie")
			}
		}
	}
	if w := auditRequest(handler, "GET", "/auth/me", ""); w.Code != 401 {
		t.Fatal("unauthenticated me accepted")
	}
	for _, path := range []string{"/auth/refresh", "/auth/logout"} {
		if w := auditRequest(handler, "POST", path, "https://evil.example", cookies...); w.Code != 403 {
			t.Fatal("foreign origin accepted")
		}
	}
	if w := auditRequest(handler, "OPTIONS", "/auth/me", "https://evil.example"); w.Code != 403 {
		t.Fatal("foreign preflight accepted")
	}
	if w := auditRequest(handler, "OPTIONS", "/auth/me", "https://app.example.com"); w.Code != 204 || w.Header().Get("Access-Control-Allow-Origin") != "https://app.example.com" {
		t.Fatal("allowed preflight rejected")
	}
	refreshed := auditRequest(handler, "POST", "/auth/refresh", "https://app.example.com", cookies...)
	if refreshed.Code != 204 {
		t.Fatalf("refresh: %d", refreshed.Code)
	}
	current := refreshed.Result().Cookies()
	if w := auditRequest(handler, "GET", "/auth/me", "https://app.example.com", current...); w.Code != 200 {
		t.Fatal("new access rejected")
	}
	if w := auditRequest(handler, "POST", "/auth/logout", "https://app.example.com", current...); w.Code != 204 {
		t.Fatal("logout failed")
	}
	if w := auditRequest(handler, "POST", "/auth/refresh", "https://app.example.com", current...); w.Code != 401 {
		t.Fatal("logged out refresh accepted")
	}
	me := auditRequest(handler, "GET", "/auth/me", "https://app.example.com", current...)
	if me.Code != 401 {
		t.Fatal("copied access token remains usable after logout")
	}
	if me.Header().Get("Cache-Control") != "no-store, max-age=0" || me.Header().Get("Strict-Transport-Security") == "" {
		t.Fatal("missing security headers")
	}
}

func TestAuditJWTRejections(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	r := httptest.NewRequest("GET", "/auth/me", nil)
	for _, cookie := range cookies {
		r.AddCookie(cookie)
	}
	valid, err := h.tokens.accessClaims(r)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.tokens.authenticate(r); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{"expired", "missing-expiry", "wrong-issuer", "wrong-audience", "empty-subject", "wrong-key", "wrong-algorithm", "missing-session", "unknown-session", "future-nbf"} {
		t.Run(kind, func(t *testing.T) {
			claims := *valid
			key := h.tokens.secret
			var method jwt.SigningMethod = jwt.SigningMethodHS256
			switch kind {
			case "expired":
				claims.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Hour))
			case "missing-expiry":
				claims.ExpiresAt = nil
			case "wrong-issuer":
				claims.Issuer = "evil"
			case "wrong-audience":
				claims.Audience = []string{"other"}
			case "empty-subject":
				claims.Subject = ""
			case "wrong-key":
				key = []byte("different-key")
			case "wrong-algorithm":
				method = jwt.SigningMethodHS384
			case "missing-session":
				claims.ID = ""
			case "unknown-session":
				claims.ID = "unknown"
			case "future-nbf":
				claims.NotBefore = jwt.NewNumericDate(time.Now().Add(time.Hour))
			}
			signed, err := jwt.NewWithClaims(method, claims).SignedString(key)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := h.tokens.authenticate(func() *http.Request {
				r := httptest.NewRequest("GET", "/auth/me", nil)
				r.AddCookie(&http.Cookie{Name: accessCookie, Value: signed})
				return r
			}()); err == nil {
				t.Fatal("invalid JWT accepted")
			}
			if w := auditRequest(handler, "GET", "/auth/me", "", &http.Cookie{Name: accessCookie, Value: signed}); w.Code != 401 {
				t.Fatal("invalid JWT accepted by route")
			}
		})
	}
}

func TestAuditLogoutStorageFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	h, handler := auditHandler(t, path)
	cookies := auditLogin(t, h, handler)
	h.store.Close()
	w := auditRequest(handler, "POST", "/auth/logout", "https://app.example.com", cookies...)
	if w.Code != 503 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("failed logout must permit retry: %d %v", w.Code, w.Result().Cookies())
	}
	for _, path := range []string{"/auth/me", "/auth/refresh"} {
		method := "GET"
		if path == "/auth/refresh" {
			method = "POST"
		}
		if failed := auditRequest(handler, method, path, "https://app.example.com", cookies...); failed.Code != 503 {
			t.Fatalf("storage failure at %s: %d", path, failed.Code)
		}
	}
	store, err := database.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	h.store = store
	h.tokens.store = store
	if retry := auditRequest(handler, "POST", "/auth/logout", "https://app.example.com", cookies...); retry.Code != 204 {
		t.Fatal("logout retry failed")
	}
	if replay := auditRequest(handler, "POST", "/auth/refresh", "https://app.example.com", cookies...); replay.Code != 401 {
		t.Fatal("logout retry did not revoke session")
	}
}

func TestAuditRefreshReplayRevokesFamily(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	original := auditLogin(t, h, handler)
	otherSession := auditLogin(t, h, handler)
	rotated := auditRequest(handler, "POST", "/auth/refresh", "https://app.example.com", original...)
	if rotated.Code != 204 {
		t.Fatal("initial rotation failed")
	}
	if w := auditRequest(handler, "POST", "/auth/refresh", "https://app.example.com", original...); w.Code != 401 {
		t.Fatal("replay accepted")
	}
	if w := auditRequest(handler, "POST", "/auth/refresh", "https://app.example.com", rotated.Result().Cookies()...); w.Code != 401 {
		t.Fatalf("replayed ancestor did not revoke active descendant: status=%d", w.Code)
	}
	for _, cookies := range [][]*http.Cookie{original, rotated.Result().Cookies()} {
		if w := auditRequest(handler, "GET", "/auth/me", "", cookies...); w.Code != 401 {
			t.Fatal("replay left access token active")
		}
	}
	if w := auditRequest(handler, "GET", "/auth/me", "", otherSession...); w.Code != 200 {
		t.Fatal("replay revoked unrelated session")
	}
}

func TestAuditProductionDefaultSigningKey(t *testing.T) {
	for key, value := range map[string]string{"APP_ENV": "production", "SECURE_SESSION_COOKIE": "true", "GOOGLE_CLIENT_ID": "client", "GOOGLE_CLIENT_SECRET": "secret", "FRONTEND_URL": "https://app.example.com", "GOOGLE_REDIRECT_URL": "https://api.example.com/auth/google/callback", "JWT_SECRET": ""} {
		t.Setenv(key, value)
	}
	if _, err := config.Load(); err == nil {
		t.Fatal("production accepted missing signing key")
	}
}

func TestGoogleExchangeContract(t *testing.T) {
	h, _ := auditHandler(t, ":memory:")
	calls := 0
	h.google.httpClient.Transport = auditTransport(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 10*time.Second {
			t.Fatal("missing exchange deadline")
		}
		body := `{"sub":"user","email":"person@example.com","email_verified":true}`
		if calls == 1 {
			if r.Method != "POST" {
				t.Fatal("token request must use POST")
			}
			if err := r.ParseForm(); err != nil {
				t.Fatal(err)
			}
			if r.Form.Get("code") != "code" || r.Form.Get("code_verifier") != "verifier" || r.Form.Get("redirect_uri") != h.google.redirectURL {
				t.Fatal("invalid token exchange form")
			}
			body = `{"access_token":"google-access"}`
		} else if r.Header.Get("Authorization") != "Bearer google-access" {
			t.Fatal("missing Google access token")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}, nil
	})
	if user, err := h.google.userFromCode(context.Background(), "code", "verifier"); err != nil || user.ID != "user" || calls != 2 {
		t.Fatalf("exchange: user=%v err=%v calls=%d", user, err, calls)
	}
}

func TestLogoutWithOnlyAccessCookie(t *testing.T) {
	h, handler := auditHandler(t, ":memory:")
	cookies := auditLogin(t, h, handler)
	var access *http.Cookie
	for _, cookie := range cookies {
		if cookie.Name == accessCookie {
			access = cookie
		}
	}
	if w := auditRequest(handler, "POST", "/auth/logout", "https://app.example.com", access); w.Code != 204 {
		t.Fatal("logout failed")
	}
	if w := auditRequest(handler, "POST", "/auth/refresh", "https://app.example.com", cookies...); w.Code != 401 {
		t.Fatal("access-only logout did not revoke refresh")
	}
}
