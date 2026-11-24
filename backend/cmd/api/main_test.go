package main

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"holdings/backend/internal/auth"
	"holdings/backend/internal/config"
	"holdings/backend/internal/database"
)

// Shutdown must release held long-poll requests the moment it begins, because
// Handler.Close only runs after Shutdown returns (deferred in main). newServer is
// the production constructor, so this guards the wiring itself.
func TestNewServerDrainsTheHandlerWhenShutdownBegins(t *testing.T) {
	store, err := database.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	cfg := config.Config{FrontendURL: "https://app.example.com", GoogleClientID: "c", GoogleClientSecret: "s", GoogleRedirectURL: "https://api.example.com/auth/google/callback", JWTSecret: []byte("audit-secret-with-at-least-32-bytes"), SecureSessionCookie: true, ServerAddress: "127.0.0.1:0"}
	h := auth.NewHandler(cfg, store, slog.New(slog.NewTextHandler(io.Discard, nil)))
	t.Cleanup(h.Close)
	server := newServer(cfg, http.NewServeMux(), h)

	if server.WriteTimeout != 15*time.Second {
		t.Fatalf("WriteTimeout %v: long polls are held for 10 s and need the 15 s margin", server.WriteTimeout)
	}
	select {
	case <-h.Draining():
		t.Fatal("draining before shutdown began")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-h.Draining():
	case <-time.After(2 * time.Second):
		t.Fatal("Server.Shutdown did not drain the handler: held long polls would delay it")
	}
}
