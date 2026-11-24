package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"holdings/backend/internal/auth"
	"holdings/backend/internal/config"
	"holdings/backend/internal/database"
	"holdings/backend/internal/httpapi"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		logger.Error("load configuration", "error", err)
		os.Exit(1)
	}
	store, err := database.Open(cfg.DatabasePath)
	if err != nil {
		logger.Error("open database", "error", err)
		os.Exit(1)
	}
	defer store.Close()
	logger.Info("market data configured", "mode", cfg.MarketdataMode, "exchanges", cfg.OpenFIGIExchCodes)

	authHandler := auth.NewHandler(cfg, store, logger)
	defer authHandler.Close()
	mux := http.NewServeMux()
	authHandler.RegisterRoutes(mux)
	mux.HandleFunc("GET /health", health)

	server := newServer(cfg, mux, authHandler)

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("API listening", "address", "http://"+cfg.ServerAddress)
		serverErrors <- server.ListenAndServe()
	}()

	shutdown := make(chan os.Signal, 1)
	signal.Notify(shutdown, os.Interrupt, syscall.SIGTERM)
	select {
	case err := <-serverErrors:
		if !errors.Is(err, http.ErrServerClosed) {
			logger.Error("server stopped", "error", err)
			os.Exit(1)
		}
	case sig := <-shutdown:
		logger.Info("shutting down", "signal", sig.String())
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			logger.Error("graceful shutdown failed", "error", err)
			os.Exit(1)
		}
	}
}

// newServer builds the HTTP server. WriteTimeout (15 s) stays above the 10 s a
// long-poll request may be held. Held requests are released as soon as Shutdown
// begins, so graceful shutdown never waits for them and Handler.Close (deferred in
// main, after Shutdown) still runs before the store is closed.
func newServer(cfg config.Config, mux *http.ServeMux, h *auth.Handler) *http.Server {
	server := &http.Server{
		Addr:              cfg.ServerAddress,
		Handler:           httpapi.SecurityHeaders(httpapi.CORS(mux, cfg.FrontendURL), cfg.IsProduction()),
		MaxHeaderBytes:    1 << 20,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	server.RegisterOnShutdown(h.Drain)
	return server
}

func health(w http.ResponseWriter, _ *http.Request) {
	httpapi.JSON(w, http.StatusOK, map[string]string{"status": "ok"})
}
