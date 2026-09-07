package main

import (
	"context"
	"github.com/Mightyfin/commerce-finance-service/internal/database"
	"github.com/Mightyfin/commerce-finance-service/internal/eventbus"
	"log"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	dbURL, natsURL := os.Getenv("COMMERCE_FINANCE_DATABASE_URL"), os.Getenv("COMMERCE_FINANCE_NATS_URL")
	if dbURL == "" || natsURL == "" {
		log.Fatal("database and NATS configuration are required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	pool, err := database.Open(ctx, dbURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err = eventbus.Run(ctx, natsURL, os.Getenv("COMMERCE_FINANCE_NATS_TOKEN"), eventbus.FacilityProjection{Pool: pool}); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
