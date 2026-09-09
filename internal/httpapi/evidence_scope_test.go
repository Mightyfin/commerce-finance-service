package httpapi

import (
	"context"
	"github.com/Mightyfin/commerce-finance-service/internal/auth"
	"net/http/httptest"
	"strings"
	"testing"
)

type scopeVerifier struct{}

func (scopeVerifier) Verify(context.Context, string) (auth.Principal, error) {
	return auth.Principal{Subject: "client", TenantID: "tenant", ApplicationID: "app", Environment: "sandbox", Scopes: map[string]bool{"commerce:write": true}}, nil
}
func TestCreateRequiresEvidencePermissionBeforeStore(t *testing.T) {
	s := Server{Verifier: scopeVerifier{}}
	req := httptest.NewRequest("POST", "/v1/commerce/fulfillments", strings.NewReader(`{}`))
	req.Header.Set("Authorization", "Bearer test")
	res := httptest.NewRecorder()
	s.Handler().ServeHTTP(res, req)
	if res.Code != 403 || !strings.Contains(res.Body.String(), "documents.evidence.verify") {
		t.Fatal(res.Code, res.Body.String())
	}
}
