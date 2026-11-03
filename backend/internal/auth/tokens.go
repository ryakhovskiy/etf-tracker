package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"holdings/backend/internal/config"
	"holdings/backend/internal/database"
)

const (
	accessCookie   = "access_token"
	refreshCookie  = "refresh_token"
	stateCookie    = "oauth_state"
	verifierCookie = "oauth_verifier"
	issuer         = "holdings"
	audience       = "holdings-web"
)

var ErrUnauthorized = errors.New("unauthorized")

type tokenManager struct {
	secret []byte
	secure bool
	store  *database.Store
}

func newTokenManager(cfg config.Config, store *database.Store) *tokenManager {
	return &tokenManager{secret: cfg.JWTSecret, secure: cfg.SecureSessionCookie, store: store}
}

func (m *tokenManager) issue(ctx context.Context, w http.ResponseWriter, userID string) error {
	refresh, err := randomToken(48)
	if err != nil {
		return err
	}
	expires := time.Now().UTC().Add(30 * 24 * time.Hour)
	refreshID, err := randomToken(16)
	if err != nil {
		return err
	}
	access, err := m.accessToken(userID, refreshID)
	if err != nil {
		return err
	}
	if err := m.store.CreateRefreshToken(ctx, refreshID, userID, database.HashToken(refresh), expires); err != nil {
		return err
	}
	m.setCookies(w, access, refresh)
	return nil
}

func (m *tokenManager) refresh(w http.ResponseWriter, r *http.Request) error {
	cookie, err := r.Cookie(refreshCookie)
	if err != nil || cookie.Value == "" {
		return ErrUnauthorized
	}
	newRefresh, err := randomToken(48)
	if err != nil {
		return err
	}
	refreshID, err := randomToken(16)
	if err != nil {
		return err
	}
	user, err := m.store.RotateRefreshToken(r.Context(), database.HashToken(cookie.Value), refreshID, database.HashToken(newRefresh), time.Now().UTC().Add(30*24*time.Hour))
	if errors.Is(err, database.ErrNotFound) {
		return ErrUnauthorized
	}
	if err != nil {
		return err
	}
	access, err := m.accessToken(user.ID, refreshID)
	if err != nil {
		return err
	}
	m.setCookies(w, access, newRefresh)
	return nil
}

func (m *tokenManager) authenticate(r *http.Request) (string, error) {
	claims, err := m.accessClaims(r)
	if err != nil {
		return "", err
	}
	if err := m.store.CheckSession(r.Context(), claims.ID, claims.Subject); err != nil {
		if errors.Is(err, database.ErrNotFound) {
			return "", ErrUnauthorized
		}
		return "", err
	}
	return claims.Subject, nil
}

func (m *tokenManager) accessClaims(r *http.Request) (*jwt.RegisteredClaims, error) {
	cookie, err := r.Cookie(accessCookie)
	if err != nil || cookie.Value == "" {
		return nil, ErrUnauthorized
	}
	claims := &jwt.RegisteredClaims{}
	token, err := jwt.ParseWithClaims(cookie.Value, claims, func(token *jwt.Token) (any, error) {
		if token.Method != jwt.SigningMethodHS256 {
			return nil, ErrUnauthorized
		}
		return m.secret, nil
	}, jwt.WithIssuer(issuer), jwt.WithAudience(audience), jwt.WithExpirationRequired())
	if err != nil || !token.Valid || claims.Subject == "" || claims.ID == "" {
		return nil, ErrUnauthorized
	}
	return claims, nil
}

func (m *tokenManager) revoke(w http.ResponseWriter, r *http.Request) error {
	var refreshHash, accessID string
	if cookie, err := r.Cookie(refreshCookie); err == nil && cookie.Value != "" {
		refreshHash = database.HashToken(cookie.Value)
	}
	if claims, err := m.accessClaims(r); err == nil {
		accessID = claims.ID
	}
	if refreshHash != "" || accessID != "" {
		if err := m.store.RevokeSession(r.Context(), refreshHash, accessID); err != nil {
			return err
		}
	}
	m.clearCookies(w)
	return nil
}

func (m *tokenManager) accessToken(userID, tokenID string) (string, error) {
	now := time.Now().UTC()
	claims := jwt.RegisteredClaims{Issuer: issuer, Audience: []string{audience}, Subject: userID, ID: tokenID, IssuedAt: jwt.NewNumericDate(now), NotBefore: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(15 * time.Minute))}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(m.secret)
}

func (m *tokenManager) setCookies(w http.ResponseWriter, access, refresh string) {
	http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: access, Path: "/", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteStrictMode, MaxAge: 15 * 60})
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: refresh, Path: "/auth", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteStrictMode, MaxAge: 30 * 24 * 60 * 60})
}

func (m *tokenManager) clearCookies(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: accessCookie, Value: "", Path: "/", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
	http.SetCookie(w, &http.Cookie{Name: refreshCookie, Value: "", Path: "/auth", HttpOnly: true, Secure: m.secure, SameSite: http.SameSiteStrictMode, MaxAge: -1})
}

func randomToken(size int) (string, error) {
	b := make([]byte, size)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func token(size int) (string, error) { return randomToken(size) }
