package main

import (
	"context"
	"github.com/Mightyfin/commerce-finance-service/internal/config"
	"github.com/Mightyfin/commerce-finance-service/internal/database"
	"log"
	"os"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	pool, err := database.Open(context.Background(), cfg.DatabaseURL)
	if err != nil {
		log.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(context.Background(), pool); err != nil {
		log.Fatal(err)
	}
	os.Exit(0)
}
