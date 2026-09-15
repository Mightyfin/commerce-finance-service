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
	authOptions, err := eventbus.ConnectionCredentials(os.Getenv("COMMERCE_FINANCE_NATS_TOKEN"), os.Getenv("COMMERCE_FINANCE_NATS_USER"), os.Getenv("COMMERCE_FINANCE_NATS_PASSWORD"))
	if err != nil {
		log.Fatal(err)
	}
	if err = eventbus.Run(ctx, natsURL, "", eventbus.FacilityProjection{Pool: pool, Environment: os.Getenv("COMMERCE_FINANCE_ENVIRONMENT")}, authOptions...); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
