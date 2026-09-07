package config

import (
	"fmt"
	"os"
	"strings"
)

type Config struct{ Environment, HTTPAddress, DatabaseURL, OIDCIssuer, OIDCAudience string }

func Load() (Config, error) {
	c := Config{
		Environment:  strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_ENVIRONMENT")),
		HTTPAddress:  strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_HTTP_ADDRESS")),
		DatabaseURL:  strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_DATABASE_URL")),
		OIDCIssuer:   strings.TrimRight(strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_OIDC_ISSUER")), "/"),
		OIDCAudience: strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_OIDC_AUDIENCE")),
	}
	if c.HTTPAddress == "" {
		c.HTTPAddress = ":8080"
	}
	if c.Environment != "local" && c.Environment != "sandbox" && c.Environment != "staging" && c.Environment != "production" {
		return c, fmt.Errorf("valid environment is required")
	}
	if c.DatabaseURL == "" || c.OIDCIssuer == "" || c.OIDCAudience == "" {
		return c, fmt.Errorf("database and OIDC configuration are required")
	}
	return c, nil
}
