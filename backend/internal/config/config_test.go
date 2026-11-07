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

func TestAlphaConfiguration(t *testing.T) {
	t.Setenv("MARKETDATA_MODE", "live")
	t.Setenv("CI", "")
	t.Setenv("ALPHAVANTAGE_API_KEY", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "ALPHAVANTAGE_API_KEY") {
		t.Fatal("live accepted missing key", err)
	}
	t.Setenv("ALPHAVANTAGE_API_KEY", "test-key")
	t.Setenv("EULERPOOL_API_KEY", "")
	if _, err := Load(); err == nil || !strings.Contains(err.Error(), "EULERPOOL_API_KEY") {
		t.Fatal("live accepted missing Eulerpool key", err)
	}
	t.Setenv("EULERPOOL_API_KEY", "test-key")
	if _, err := Load(); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MARKETDATA_MODE", "mock")
	t.Setenv("ALPHAVANTAGE_API_KEY", "")
	for _, tc := range []struct{ key, value string }{{"AV_MAX_PER_DAY", "0"}, {"AV_MAX_PER_MINUTE", "-1"}, {"AV_MAX_PER_USER_PER_DAY", "no"}, {"AV_LIMIT_BACKOFF", "0s"}, {"FRANKFURTER_BASE_URL", "file:///tmp"}} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestEulerpoolConfiguration(t *testing.T) {
	t.Setenv("MARKETDATA_MODE", "mock")
	cfg, err := Load()
	if err != nil || cfg.MarketdataPrimary != "YAHOO" || cfg.EulerpoolSoftLimit != 500 || cfg.EulerpoolQuoteTTL.Minutes() != 15 || cfg.EulerpoolProfileTTL.Hours() != 720 || cfg.EulerpoolBaseURL != "https://api.eulerpool.com/api/1" {
		t.Fatalf("defaults: %+v %v", cfg, err)
	}
	t.Setenv("MARKETDATA_PRIMARY", "alphavantage")
	if cfg, err = Load(); err != nil || cfg.MarketdataPrimary != "ALPHAVANTAGE" {
		t.Fatal(cfg.MarketdataPrimary, err)
	}
	for _, tc := range []struct{ key, value string }{{"MARKETDATA_PRIMARY", "OTHER"}, {"EULERPOOL_SOFT_DAILY_LIMIT", "0"}, {"EULERPOOL_QUOTE_TTL", "0s"}, {"EULERPOOL_PROFILE_TTL", "x"}, {"EULERPOOL_BASE_URL", "ftp://x"}} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}

func TestYahooConfiguration(t *testing.T) {
	t.Setenv("MARKETDATA_MODE", "mock")
	cfg, err := Load()
	if err != nil || cfg.YahooQueryURL != "https://query1.finance.yahoo.com" || cfg.YahooCookieURL != "https://fc.yahoo.com" || cfg.YahooSoftLimit != 1000 || cfg.YahooMinInterval.Milliseconds() != 300 || cfg.YahooCooldown.Minutes() != 15 || !strings.Contains(cfg.YahooUserAgent, "Mozilla/5.0") {
		t.Fatalf("%+v %v", cfg, err)
	}
	for _, tc := range []struct{ key, value string }{{"YAHOO_QUERY_URL", "ftp://x"}, {"YAHOO_COOKIE_URL", "nope"}, {"YAHOO_SOFT_DAILY_LIMIT", "0"}, {"YAHOO_MIN_INTERVAL", "-1s"}, {"YAHOO_COOLDOWN", "0s"}} {
		t.Run(tc.key, func(t *testing.T) {
			t.Setenv(tc.key, tc.value)
			if _, err := Load(); err == nil {
				t.Fatal("invalid config accepted")
			}
		})
	}
}
