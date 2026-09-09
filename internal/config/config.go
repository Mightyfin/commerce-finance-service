package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"
)

type Config struct{ Environment, HTTPAddress, DatabaseURL, OIDCIssuer, OIDCAudience, DocumentServiceBaseURL string }

func Load() (Config, error) {
	c := Config{
		DocumentServiceBaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_DOCUMENT_SERVICE_BASE_URL")), "/"),
		Environment:            strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_ENVIRONMENT")),
		HTTPAddress:            strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_HTTP_ADDRESS")),
		DatabaseURL:            strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_DATABASE_URL")),
		OIDCIssuer:             strings.TrimRight(strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_OIDC_ISSUER")), "/"),
		OIDCAudience:           strings.TrimSpace(os.Getenv("COMMERCE_FINANCE_OIDC_AUDIENCE")),
	}
	if c.HTTPAddress == "" {
		c.HTTPAddress = ":8080"
	}
	if c.DocumentServiceBaseURL != "" {
		u, err := url.Parse(c.DocumentServiceBaseURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return c, fmt.Errorf("invalid document service URL")
		}
	}
	if c.Environment != "local" && c.Environment != "sandbox" && c.Environment != "staging" && c.Environment != "production" {
		return c, fmt.Errorf("valid environment is required")
	}
	if c.DatabaseURL == "" || c.OIDCIssuer == "" || c.OIDCAudience == "" {
		return c, fmt.Errorf("database and OIDC configuration are required")
	}
	return c, nil
}
