package commerce

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testDigest = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

type testEvidenceVerifier struct{}

func (testEvidenceVerifier) Verify(_ context.Context, _ Principal, party string, e Evidence) error {
	if party == "" || e.SHA256 != testDigest {
		return ErrEvidenceUnavailable
	}
	return nil
}

func TestDocumentVerificationBoundaries(t *testing.T) {
	p := Principal{Subject: "client", TenantID: "tenant", ApplicationID: "app", Environment: "sandbox", Token: "delegated-test-token"}
	e := Evidence{DocumentID: "doc", Type: "invoice", SHA256: testDigest}
	for _, bad := range []string{"", "tenant", "environment", "party", "owner", "digest", "scan", "status", "type", "id", "trailing", "oversized", "redirect", "denied"} {
		t.Run(bad, func(t *testing.T) {
			destinationHits := 0
			destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { destinationHits++ }))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer "+p.Token || r.URL.Path != "/v1/documents/doc/evidence-verification" {
					t.Error("delegation lost")
				}
				var request map[string]string
				if json.NewDecoder(r.Body).Decode(&request) != nil || request["party_id"] != "party" || request["sha256"] != testDigest {
					t.Error("invalid verification request")
				}
				if bad == "redirect" {
					http.Redirect(w, r, destination.URL, 307)
					return
				}
				if bad == "denied" {
					w.WriteHeader(403)
					return
				}
				document := map[string]string{"id": "doc", "party_id": "party", "owner_type": "PARTY", "owner_id": "party", "sha256": testDigest, "status": "available", "scan_status": "clean", "document_type": "invoice"}
				body := map[string]any{"tenant_id": "tenant", "environment": "sandbox", "verification": "available_clean_version", "document": document}
				switch bad {
				case "tenant":
					body["tenant_id"] = "other"
				case "environment":
					body["environment"] = "production"
				case "party":
					document["party_id"] = "other"
				case "owner":
					document["owner_id"] = "other"
				case "digest":
					document["sha256"] = strings.Repeat("b", 64)
				case "scan":
					document["scan_status"] = "pending"
				case "status":
					document["status"] = "deleted"
				case "type":
					document["document_type"] = "other"
				case "id":
					document["id"] = "other"
				}
				_ = json.NewEncoder(w).Encode(body)
				if bad == "trailing" {
					_, _ = w.Write([]byte(`{}`))
				}
				if bad == "oversized" {
					_, _ = w.Write([]byte(strings.Repeat(" ", 1<<20) + `{}`))
				}
			}))
			defer server.Close()
			err := (HTTPDocumentVerifier{BaseURL: server.URL}).Verify(context.Background(), p, "party", e)
			if (err != nil) != (bad != "") {
				t.Fatal("verification", bad, err)
			}
			if destinationHits != 0 {
				t.Fatal("delegated credential followed redirect")
			}
		})
	}
}
