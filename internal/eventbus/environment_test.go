package eventbus

import (
	"context"
	"encoding/json"
	"testing"
)

func TestPublisherPreservesTransactionScope(t *testing.T) {
	for _, raw := range []string{`{"tenant_id":"other"}`, `{"environment":"production"}`, `null`, `[]`} {
		if _, err := scopedPayload(OutboxEvent{TenantID: "ten_one", Payload: json.RawMessage(raw)}, "sandbox"); err == nil {
			t.Fatal("invalid scope accepted", raw)
		}
	}
	for _, raw := range []string{`{}`, `{"caller_application_id":"app_one","status":"confirmed"}`} {
		got, err := scopedPayload(OutboxEvent{TenantID: "ten_one", Payload: json.RawMessage(raw)}, "sandbox")
		if err != nil {
			t.Fatal(err)
		}
		var data map[string]string
		if err = json.Unmarshal(got, &data); err != nil || data["tenant_id"] != "ten_one" || data["environment"] != "sandbox" {
			t.Fatal(data, err)
		}
		if raw == `{}` && data["caller_application_id"] != "" {
			t.Fatal("invented historical caller")
		}
	}
}

func TestProjectionEnvironmentBeforeDatabaseAccess(t *testing.T) {
	p := FacilityProjection{Environment: "sandbox"}
	for _, tc := range []struct {
		payload string
		fails   bool
	}{
		{`{"environment":"production"}`, false},
		{`{}`, true},
		{`{"environment":"unknown"}`, true},
		{`{"environment":"sandbox"}`, true},
	} {
		if err := p.Apply(context.Background(), []byte(tc.payload)); (err != nil) != tc.fails {
			t.Fatal(tc, err)
		}
	}
}
