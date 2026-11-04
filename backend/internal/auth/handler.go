package auth

import (
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"time"

	"holdings/backend/internal/config"
	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
	"holdings/backend/internal/marketdata"
)

type Handler struct {
	figi        marketdata.Service
	frontendURL string
	google      *googleClient
	store       *database.Store
	tokens      *tokenManager
	logger      *slog.Logger
}

func NewHandler(cfg config.Config, store *database.Store, logger *slog.Logger) *Handler {
	codes := cfg.OpenFIGIExchCodes
	if len(codes) == 0 {
		codes = []string{"GR", "GY"}
	}
	size := cfg.ExternalCacheSize
	if size == 0 {
		size = 1000
	}
	ttl := cfg.ExternalCacheTTL
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	var provider marketdata.Provider = marketdata.Mock{}
	if cfg.MarketdataMode == "live" {
		provider = marketdata.NewLive(cfg.OpenFIGIAPIKey, marketdata.NewCache(size, ttl))
	}
	return &Handler{figi: marketdata.Service{Provider: provider, Codes: codes}, frontendURL: cfg.FrontendURL, google: newGoogleClient(cfg), store: store, tokens: newTokenManager(cfg, store), logger: logger}
}

func (h *Handler) RegisterRoutes(mux *http.ServeMux) {
	h.registerPortfolioRoutes(mux)
	h.registerTransactionRoutes(mux)
	h.registerImportRoutes(mux)
	h.registerOnlineRoutes(mux)
	mux.HandleFunc("GET /auth/google", h.googleLogin)
	mux.HandleFunc("GET /auth/google/callback", h.googleCallback)
	mux.HandleFunc("GET /auth/me", h.me)
	mux.HandleFunc("POST /auth/refresh", h.refresh)
	mux.HandleFunc("POST /auth/logout", h.logout)
}

func (h *Handler) googleLogin(w http.ResponseWriter, r *http.Request) {
	if h.google.clientID == "" || h.google.clientSecret == "" {
		http.Error(w, "Google OAuth is not configured. Set GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET.", http.StatusServiceUnavailable)
		return
	}
	state, err := token(32)
	if err != nil {
		http.Error(w, "could not create OAuth state", http.StatusInternalServerError)
		return
	}
	verifier, err := token(32)
	if err != nil {
		http.Error(w, "could not create OAuth verifier", http.StatusInternalServerError)
		return
	}
	http.SetCookie(w, &http.Cookie{Name: stateCookie, Value: state, Path: "/", HttpOnly: true, Secure: h.tokens.secure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.SetCookie(w, &http.Cookie{Name: verifierCookie, Value: verifier, Path: "/", HttpOnly: true, Secure: h.tokens.secure, SameSite: http.SameSiteLaxMode, MaxAge: 600})
	http.Redirect(w, r, h.google.authorizationURL(state, pkceChallenge(verifier)), http.StatusFound)
}

func (h *Handler) googleCallback(w http.ResponseWriter, r *http.Request) {
	stateCookie, err := r.Cookie(stateCookie)
	if err != nil || stateCookie.Value == "" || stateCookie.Value != r.URL.Query().Get("state") {
		http.Error(w, "invalid OAuth state", http.StatusBadRequest)
		return
	}
	verifierCookie, err := r.Cookie(verifierCookie)
	if err != nil || verifierCookie.Value == "" {
		http.Error(w, "invalid OAuth verifier", http.StatusBadRequest)
		return
	}
	h.clearOAuthCookies(w)
	if oauthError := r.URL.Query().Get("error"); oauthError != "" {
		http.Redirect(w, r, h.frontendURL+"/?error="+url.QueryEscape(oauthError), http.StatusFound)
		return
	}

	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "missing authorization code", http.StatusBadRequest)
		return
	}
	profile, err := h.google.userFromCode(r.Context(), code, verifierCookie.Value)
	if err != nil {
		h.logger.Error("Google OAuth callback failed", "error", err)
		http.Error(w, "Google sign-in failed", http.StatusBadGateway)
		return
	}
	user, err := h.store.UpsertGoogleUser(r.Context(), profile.ID, profile.Email, profile.Name, profile.Picture)
	if err != nil {
		var limit *database.LimitError
		if errors.As(err, &limit) && limit.Code == "USER_LIMIT_REACHED" {
			http.Redirect(w, r, h.frontendURL+"/?error=signups_closed", http.StatusFound)
			return
		}
		h.logger.Error("upsert user", "error", err)
		http.Error(w, "could not save user", http.StatusInternalServerError)
		return
	}
	if err := h.tokens.issue(r.Context(), w, user.ID); err != nil {
		h.logger.Error("issue tokens", "error", err)
		http.Error(w, "could not create session", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, h.frontendURL+"/", http.StatusFound)
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func (h *Handler) me(w http.ResponseWriter, r *http.Request) {
	userID, err := h.tokens.authenticate(r)
	if err != nil {
		if !errors.Is(err, ErrUnauthorized) {
			h.logger.Error("authenticate session", "error", err)
			httpapi.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session service unavailable"})
			return
		}
		httpapi.JSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	user, err := h.store.UserByID(r.Context(), userID)
	if err != nil {
		if !errors.Is(err, database.ErrNotFound) {
			h.logger.Error("load user", "error", err)
			httpapi.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session service unavailable"})
			return
		}
		httpapi.JSON(w, http.StatusUnauthorized, map[string]string{"error": "not authenticated"})
		return
	}
	httpapi.JSON(w, http.StatusOK, User{ID: user.ID, Email: user.Email, Name: user.Name, Picture: user.Picture})
}

func (h *Handler) refresh(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && origin != h.frontendURL {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if err := h.tokens.refresh(w, r); err != nil {
		if !errors.Is(err, ErrUnauthorized) {
			h.logger.Error("refresh session", "error", err)
			httpapi.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "session service unavailable"})
			return
		}
		httpapi.JSON(w, http.StatusUnauthorized, map[string]string{"error": "refresh token invalid or expired"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) logout(w http.ResponseWriter, r *http.Request) {
	if origin := r.Header.Get("Origin"); origin != "" && origin != h.frontendURL {
		http.Error(w, "origin not allowed", http.StatusForbidden)
		return
	}
	if err := h.tokens.revoke(w, r); err != nil {
		h.logger.Error("revoke session", "error", err)
		httpapi.JSON(w, http.StatusServiceUnavailable, map[string]string{"error": "could not sign out; retry"})
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) clearOAuthCookies(w http.ResponseWriter) {
	for _, name := range []string{stateCookie, verifierCookie} {
		http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", HttpOnly: true, Secure: h.tokens.secure, SameSite: http.SameSiteLaxMode, MaxAge: -1})
	}
}
