package auth

import (
	"context"
	"fmt"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
)

type Principal struct {
	Subject, TenantID, ApplicationID, Environment string
	Scopes                                        map[string]bool
}

func (p Principal) HasScope(scope string) bool { return p.Scopes[scope] }

type Verifier interface {
	Verify(context.Context, string) (Principal, error)
}
type OIDCVerifier struct {
	verifier    *oidc.IDTokenVerifier
	environment string
}

func NewOIDCVerifier(ctx context.Context, issuer, audience, environment string) (*OIDCVerifier, error) {
	provider, err := oidc.NewProvider(ctx, issuer)
	if err != nil {
		return nil, err
	}
	return &OIDCVerifier{verifier: provider.Verifier(&oidc.Config{ClientID: audience}), environment: environment}, nil
}
func (v *OIDCVerifier) Verify(ctx context.Context, raw string) (Principal, error) {
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return Principal{}, err
	}
	var claims struct {
		Subject       string `json:"sub"`
		TenantID      string `json:"tenant_id"`
		ApplicationID string `json:"application_id"`
		Environment   string `json:"environment"`
		Scope         string `json:"scope"`
	}
	if err = token.Claims(&claims); err != nil {
		return Principal{}, err
	}
	if claims.Subject == "" || claims.TenantID == "" || claims.ApplicationID == "" || claims.Environment != v.environment {
		return Principal{}, fmt.Errorf("required workload claims are missing")
	}
	p := Principal{Subject: claims.Subject, TenantID: claims.TenantID, ApplicationID: claims.ApplicationID, Environment: claims.Environment, Scopes: map[string]bool{}}
	for _, scope := range strings.Fields(claims.Scope) {
		p.Scopes[scope] = true
	}
	return p, nil
}
