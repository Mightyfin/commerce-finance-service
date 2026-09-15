package main

import (
	"context"
	"github.com/Mightyfin/commerce-finance-service/internal/database"
	"github.com/Mightyfin/commerce-finance-service/internal/eventbus"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
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
	publisher, closePublisher, err := eventbus.NewPublisher(natsURL, "", os.Getenv("COMMERCE_FINANCE_ENVIRONMENT"), authOptions...)
	if err != nil {
		log.Fatal(err)
	}
	defer closePublisher()
	if err = publisher.Ensure(ctx); err != nil {
		log.Fatal(err)
	}
	store := eventbus.OutboxStore{Pool: pool}
	for ctx.Err() == nil {
		e, claimErr := store.Claim(ctx)
		if claimErr != nil {
			log.Print(claimErr)
			time.Sleep(time.Second)
			continue
		}
		if e == nil {
			time.Sleep(250 * time.Millisecond)
			continue
		}
		if err = publisher.Publish(ctx, *e); err != nil {
			_ = store.Fail(ctx, e.ID, err.Error())
			continue
		}
		_ = store.Complete(ctx, e.ID)
	}
}
