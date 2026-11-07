package marketdata

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// logUpstream records a failed or unusual exchange with an external provider.
// It never receives URLs with credentials: callers pass provider, a label for
// the operation, and sanitized details only. A nil logger is a no-op.
func logUpstream(l *slog.Logger, level slog.Level, provider, msg string, attrs ...any) {
	if l == nil {
		return
	}
	l.Log(context.Background(), level, msg, append([]any{"provider", provider}, attrs...)...)
}

// snippet returns the start of a response body on one line, for diagnostics.
func snippet(b []byte) string {
	s := strings.Join(strings.Fields(string(b[:min(len(b), 300)])), " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

// cleanErr removes the request URL from transport errors (Alpha Vantage puts
// its API key in the query string, and net/http errors embed the full URL).
func cleanErr(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + ": " + ue.Err.Error()
	}
	return err.Error()
}

var sensitiveParam = []string{"crumb", "token", "key", "secret", "password", "passwd", "auth", "code", "sig", "credential", "bearer"}

func isSensitive(name string) bool {
	name = strings.ToLower(name)
	for _, s := range sensitiveParam {
		if strings.Contains(name, s) {
			return true
		}
	}
	return false
}

// ScrubURL renders u for logging with credentials removed: userinfo, and the
// value of every query parameter whose name suggests a secret (token, apikey,
// secret, code, ...), keeping the original parameter order.
func ScrubURL(u *url.URL) string {
	c := *u
	if c.User != nil {
		c.User = url.User("***")
	}
	c.Fragment = ""
	parts := strings.Split(u.RawQuery, "&")
	for i, p := range parts {
		name, _, found := strings.Cut(p, "=")
		if raw, err := url.QueryUnescape(name); err == nil {
			name = raw
		}
		if found && isSensitive(name) {
			parts[i] = p[:strings.Index(p, "=")+1] + "***"
		}
	}
	c.RawQuery = strings.Join(parts, "&")
	return c.String()
}

type loggingTransport struct {
	base http.RoundTripper
	log  *slog.Logger
}

func (t loggingTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if t.log != nil {
		t.log.Info("calling url: "+ScrubURL(r.URL), "method", r.Method)
	}
	return t.base.RoundTrip(r)
}

// NewLoggedClient returns an HTTP client that logs "calling url: <url>" (with
// secrets scrubbed) before every outbound request.
func NewLoggedClient(timeout time.Duration, log *slog.Logger) *http.Client {
	return &http.Client{Timeout: timeout, Transport: loggingTransport{base: http.DefaultTransport, log: log}}
}
