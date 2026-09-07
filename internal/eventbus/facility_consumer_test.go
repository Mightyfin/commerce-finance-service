package eventbus

import (
	"fmt"
	"testing"
	"time"
)

func TestDecodeFacilityLifecycleEvents(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for eventType, expectedStatus := range map[string]string{
		"facility.funding.approved":        "funding_approved",
		"facility.disbursement.authorized": "authorized",
		"facility.disbursement.posted":     "disbursed",
	} {
		body := []byte(fmt.Sprintf(`{"id":"evt_1","type":%q,"version":"1","tenant_id":"ten_1","aggregate_id":"fac_1","occurred_at":%q,"data":{"facility_id":"fac_1","currency":"ZMW"}}`, eventType, now))
		_, data, status, relevant, err := decodeFacilityEvent(body)
		if err != nil || !relevant || status != expectedStatus || data.FacilityID != "fac_1" {
			t.Fatalf("%s: status=%q relevant=%v err=%v", eventType, status, relevant, err)
		}
	}
}

func TestDecodeFacilityEventDoesNotRequireApplicationOnLaterEvents(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	body := []byte(fmt.Sprintf(`{"id":"evt_1","type":"facility.disbursement.posted","version":"1","tenant_id":"ten_1","aggregate_id":"fac_1","occurred_at":%q,"data":{"facility_id":"fac_1","currency":"ZMW"}}`, now))
	_, data, _, relevant, err := decodeFacilityEvent(body)
	if err != nil || !relevant || data.ApplicationID != "" {
		t.Fatalf("real posted payload was rejected: relevant=%v err=%v", relevant, err)
	}
}

func TestDecodeFacilityEventRejectsAggregateMismatch(t *testing.T) {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	body := []byte(fmt.Sprintf(`{"id":"evt_1","type":"facility.funding.approved","version":"1","tenant_id":"ten_1","aggregate_id":"fac_1","occurred_at":%q,"data":{"facility_id":"fac_2","application_id":"cap_1","currency":"ZMW"}}`, now))
	if _, _, _, _, err := decodeFacilityEvent(body); err == nil {
		t.Fatal("aggregate mismatch must be rejected")
	}
}
