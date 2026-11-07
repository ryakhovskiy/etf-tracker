package auth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"holdings/backend/internal/config"
	"holdings/backend/internal/marketdata"
)

type googleClient struct {
	clientID     string
	clientSecret string
	redirectURL  string
	httpClient   *http.Client
}

func newGoogleClient(cfg config.Config, logger *slog.Logger) *googleClient {
	return &googleClient{clientID: cfg.GoogleClientID, clientSecret: cfg.GoogleClientSecret, redirectURL: cfg.GoogleRedirectURL, httpClient: marketdata.NewLoggedClient(10*time.Second, logger)}
}

func (g *googleClient) authorizationURL(state, challenge string) string {
	query := url.Values{"client_id": {g.clientID}, "redirect_uri": {g.redirectURL}, "response_type": {"code"}, "scope": {"openid email profile"}, "state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"}, "access_type": {"online"}}
	return "https://accounts.google.com/o/oauth2/v2/auth?" + query.Encode()
}

func (g *googleClient) userFromCode(ctx context.Context, code, verifier string) (User, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	form := url.Values{"code": {code}, "client_id": {g.clientID}, "client_secret": {g.clientSecret}, "redirect_uri": {g.redirectURL}, "grant_type": {"authorization_code"}, "code_verifier": {verifier}}
	tokenRequest, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://oauth2.googleapis.com/token", strings.NewReader(form.Encode()))
	if err != nil {
		return User{}, fmt.Errorf("create token request: %w", err)
	}
	tokenRequest.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	tokenResponse, err := g.httpClient.Do(tokenRequest)
	if err != nil {
		return User{}, fmt.Errorf("exchange authorization code: %w", err)
	}
	defer tokenResponse.Body.Close()
	if tokenResponse.StatusCode != http.StatusOK {
		return User{}, fmt.Errorf("exchange authorization code: status %s", tokenResponse.Status)
	}
	var tokenResponseBody struct {
		AccessToken string `json:"access_token"`
	}
	if err := json.NewDecoder(io.LimitReader(tokenResponse.Body, 1<<20)).Decode(&tokenResponseBody); err != nil {
		return User{}, fmt.Errorf("decode token response: %w", err)
	}
	if tokenResponseBody.AccessToken == "" {
		return User{}, fmt.Errorf("token response did not contain an access token")
	}

	profileRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://openidconnect.googleapis.com/v1/userinfo", nil)
	if err != nil {
		return User{}, fmt.Errorf("create profile request: %w", err)
	}
	profileRequest.Header.Set("Authorization", "Bearer "+tokenResponseBody.AccessToken)
	profileResponse, err := g.httpClient.Do(profileRequest)
	if err != nil {
		return User{}, fmt.Errorf("load Google profile: %w", err)
	}
	defer profileResponse.Body.Close()
	if profileResponse.StatusCode != http.StatusOK {
		return User{}, fmt.Errorf("load Google profile: status %s", profileResponse.Status)
	}
	var profile struct {
		ID            string `json:"sub"`
		Email         string `json:"email"`
		Name          string `json:"name"`
		Picture       string `json:"picture"`
		EmailVerified bool   `json:"email_verified"`
	}
	if err := json.NewDecoder(io.LimitReader(profileResponse.Body, 1<<20)).Decode(&profile); err != nil {
		return User{}, fmt.Errorf("decode Google profile: %w", err)
	}
	if profile.ID == "" || profile.Email == "" || !profile.EmailVerified {
		return User{}, fmt.Errorf("Google profile is missing a verified identity")
	}
	return User{ID: profile.ID, Email: profile.Email, Name: profile.Name, Picture: profile.Picture}, nil
}
