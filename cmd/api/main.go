package main

import (
	"context"
	"github.com/Mightyfin/commerce-finance-service/internal/auth"
	"github.com/Mightyfin/commerce-finance-service/internal/commerce"
	"github.com/Mightyfin/commerce-finance-service/internal/config"
	"github.com/Mightyfin/commerce-finance-service/internal/database"
	"github.com/Mightyfin/commerce-finance-service/internal/httpapi"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		r, e := http.Get("http://127.0.0.1:8080/health/ready")
		if e != nil || r.StatusCode != 200 {
			os.Exit(1)
		}
		return
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	cfg, err := config.Load()
	if err != nil {
		log.Error("configuration rejected", "error", err)
		os.Exit(1)
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	if cfg.DocumentServiceBaseURL == "" {
		log.Error("COMMERCE_FINANCE_DOCUMENT_SERVICE_BASE_URL is required for verified confirmations")
		os.Exit(1)
	}
	defer stop()
	pool, err := database.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Error("database unavailable", "error", err)
		os.Exit(1)
	}
	defer pool.Close()
	verifier, err := auth.NewOIDCVerifier(ctx, cfg.OIDCIssuer, cfg.OIDCAudience, cfg.Environment)
	if err != nil {
		log.Error("OIDC unavailable", "error", err)
		os.Exit(1)
	}
	server := http.Server{Addr: cfg.HTTPAddress, Handler: httpapi.Server{Verifier: verifier, Store: commerce.Store{Pool: pool, Verifier: commerce.HTTPDocumentVerifier{BaseURL: cfg.DocumentServiceBaseURL}}}.Handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 15 * time.Second, IdleTimeout: time.Minute}
	go func() {
		<-ctx.Done()
		c, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = server.Shutdown(c)
	}()
	log.Info("commerce finance API ready", "environment", cfg.Environment)
	if err = server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
