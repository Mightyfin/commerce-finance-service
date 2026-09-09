package commerce

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Uses the verified caller's delegated credential, never a global tenant bypass.
type HTTPDocumentVerifier struct {
	BaseURL string
	Client  *http.Client
}

func (v HTTPDocumentVerifier) Verify(ctx context.Context, p Principal, party string, e Evidence) error {
	if v.BaseURL == "" || p.Token == "" || p.TenantID == "" || p.ApplicationID == "" || (p.Environment != "sandbox" && p.Environment != "production") || party == "" || len(e.SHA256) != 64 {
		return ErrEvidenceUnavailable
	}
	body, _ := json.Marshal(map[string]string{"party_id": party, "sha256": e.SHA256})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(v.BaseURL, "/")+"/v1/documents/"+url.PathEscape(e.DocumentID)+"/evidence-verification", bytes.NewReader(body))
	if err != nil {
		return ErrEvidenceUnavailable
	}
	req.Header.Set("Authorization", "Bearer "+p.Token)
	req.Header.Set("Content-Type", "application/json")
	client := v.Client
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	safe := *client
	safe.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	res, err := safe.Do(req)
	if err != nil {
		return ErrEvidenceUnavailable
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return ErrEvidenceUnavailable
	}
	var out struct {
		TenantID     string `json:"tenant_id"`
		Environment  string `json:"environment"`
		Verification string `json:"verification"`
		Document     struct {
			ID         string `json:"id"`
			PartyID    string `json:"party_id"`
			OwnerType  string `json:"owner_type"`
			OwnerID    string `json:"owner_id"`
			SHA256     string `json:"sha256"`
			Status     string `json:"status"`
			ScanStatus string `json:"scan_status"`
			Type       string `json:"document_type"`
		} `json:"document"`
	}
	// Read one byte beyond the limit so a valid JSON prefix cannot hide an
	// oversized or trailing response behind an artificial EOF.
	response, err := io.ReadAll(io.LimitReader(res.Body, (1<<20)+1))
	if err != nil || len(response) > 1<<20 {
		return ErrEvidenceUnavailable
	}
	decoder := json.NewDecoder(bytes.NewReader(response))
	if decoder.Decode(&out) != nil || decoder.Decode(new(any)) != io.EOF {
		return ErrEvidenceUnavailable
	}
	d := out.Document
	if out.TenantID != p.TenantID || out.Environment != p.Environment || out.Verification != "available_clean_version" || d.ID != e.DocumentID || d.PartyID != party || d.OwnerType != "PARTY" || d.OwnerID != party || d.SHA256 != e.SHA256 || d.Status != "available" || d.ScanStatus != "clean" || d.Type != e.Type {
		return ErrEvidenceUnavailable
	}
	return nil
}
