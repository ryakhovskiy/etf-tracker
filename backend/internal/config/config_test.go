package config

import (
	"strings"
	"testing"
)

func TestProductionConfiguration(t *testing.T) {
	for key, value := range map[string]string{
		"APP_ENV": "production", "SECURE_SESSION_COOKIE": "true", "JWT_SECRET": strings.Repeat("a", 32),
		"GOOGLE_CLIENT_ID": "client", "GOOGLE_CLIENT_SECRET": "secret",
		"FRONTEND_URL": "https://app.example.com", "GOOGLE_REDIRECT_URL": "https://api.example.com/auth/google/callback",
	} {
		t.Setenv(key, value)
	}
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ key, value string }{
		{"JWT_SECRET", ""}, {"JWT_SECRET", developmentJWTSecret}, {"JWT_SECRET", "replace-with-a-long-random-value"}, {"JWT_SECRET", "short"},
		{"SECURE_SESSION_COOKIE", "false"}, {"GOOGLE_CLIENT_ID", ""}, {"GOOGLE_CLIENT_SECRET", ""},
		{"FRONTEND_URL", "http://app.example.com"}, {"FRONTEND_URL", "https://"}, {"FRONTEND_URL", "https://app.example.com/path"},
		{"GOOGLE_REDIRECT_URL", "https://"}, {"GOOGLE_REDIRECT_URL", "http://api.example.com/callback"},
	} {
		t.Run(tc.key+"="+tc.value, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("unsafe configuration accepted")
			}
		})
	}
	t.Setenv("FRONTEND_URL", "https://app.example.com/")
	if cfg, err := Load(); err != nil || cfg.FrontendURL != "https://app.example.com" {
		t.Fatalf("origin normalization: %v %v", cfg.FrontendURL, err)
	}
}
