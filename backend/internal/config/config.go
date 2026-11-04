package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/joho/godotenv"
)

const developmentJWTSecret = "local-development-jwt-secret-change-me"

type Config struct {
	Environment         string
	ServerAddress       string
	FrontendURL         string
	GoogleClientID      string
	GoogleClientSecret  string
	GoogleRedirectURL   string
	DatabasePath        string
	JWTSecret           []byte
	SecureSessionCookie bool
}

func Load() (Config, error) {

	// Environment variables are sufficient in deployments; a local file is optional.
	if err := godotenv.Load(".env"); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return Config{}, err
		}
		if err := godotenv.Load("../.env"); err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, err
		}
	}

	secureCookie, err := boolValue("SECURE_SESSION_COOKIE", false)
	if err != nil {
		return Config{}, err
	}
	cfg := Config{
		Environment:         value("APP_ENV", "development"),
		ServerAddress:       ":" + value("PORT", "8080"),
		FrontendURL:         value("FRONTEND_URL", "http://localhost:3000"),
		GoogleClientID:      os.Getenv("GOOGLE_CLIENT_ID"),
		GoogleClientSecret:  os.Getenv("GOOGLE_CLIENT_SECRET"),
		GoogleRedirectURL:   value("GOOGLE_REDIRECT_URL", "http://localhost:8080/auth/google/callback"),
		DatabasePath:        value("DATABASE_PATH", "data/holdings.db"),
		JWTSecret:           []byte(value("JWT_SECRET", developmentJWTSecret)),
		SecureSessionCookie: secureCookie,
	}

	if len(cfg.JWTSecret) < 16 {
		return Config{}, errors.New("JWT_SECRET must be at least 16 characters")
	}
	if cfg.IsProduction() {
		if os.Getenv("JWT_SECRET") == "" || string(cfg.JWTSecret) == developmentJWTSecret || string(cfg.JWTSecret) == "replace-with-a-long-random-value" {
			return Config{}, errors.New("JWT_SECRET must be explicitly set to a random secret in production")
		}
		if cfg.GoogleClientID == "" || cfg.GoogleClientSecret == "" {
			return Config{}, errors.New("GOOGLE_CLIENT_ID and GOOGLE_CLIENT_SECRET are required in production")
		}
		if len(cfg.JWTSecret) < 32 {
			return Config{}, errors.New("JWT_SECRET must be at least 32 characters in production")
		}
		if !cfg.SecureSessionCookie {
			return Config{}, errors.New("SECURE_SESSION_COOKIE must be true in production")
		}
		if !isHTTPS(cfg.FrontendURL) || !isHTTPS(cfg.GoogleRedirectURL) {
			return Config{}, errors.New("FRONTEND_URL and GOOGLE_REDIRECT_URL must use HTTPS in production")
		}
	}
	frontend, err := url.Parse(cfg.FrontendURL)
	if err != nil || frontend.Hostname() == "" || frontend.User != nil || (frontend.Scheme != "http" && frontend.Scheme != "https") || frontend.RawQuery != "" || frontend.Fragment != "" || (frontend.Path != "" && frontend.Path != "/") {
		return Config{}, errors.New("FRONTEND_URL must be an HTTP(S) origin without a path, query, or fragment")
	}
	cfg.FrontendURL = strings.TrimSuffix(cfg.FrontendURL, "/")
	return cfg, nil
}

func (c Config) IsProduction() bool {
	var env = strings.ToLower(c.Environment)
	return env == "production" || env == "prod"
}

func isHTTPS(rawURL string) bool {
	u, err := url.Parse(rawURL)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil && u.Fragment == ""
}

func value(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func boolValue(key string, fallback bool) (bool, error) {
	v := os.Getenv(key)
	if v == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return false, fmt.Errorf("invalid %s: %q", key, v)
	}
	return parsed, nil
}
