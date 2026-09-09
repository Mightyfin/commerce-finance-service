package commerce

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/Mightyfin/commerce-finance-service/internal/database"
)

func TestStoreIsolationIdempotencyAndEligibility(t *testing.T) {
	databaseURL := os.Getenv("COMMERCE_FINANCE_TEST_DATABASE_URL")
	if databaseURL == "" {
		t.Skip("COMMERCE_FINANCE_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `INSERT INTO eligible_facilities(facility_id,tenant_id,credit_application_id,currency,status,source_event_id,updated_at,caller_application_id,recipient_relationship_id,party_id) VALUES('fac_test_1','ten_1','cap_1','ZMW','disbursed','evt_1',now(),'app_1','npt_1','party_1')`)
	if err != nil {
		t.Fatal(err)
	}

	store := Store{Pool: pool, Verifier: testEvidenceVerifier{}}
	owner := Principal{Subject: "client-1", TenantID: "ten_1", ApplicationID: "app_1"}
	input := CreateInput{
		FacilityID:             "fac_test_1",
		RecipientParticipantID: "npt_1",
		Currency:               "ZMW",
		DeliveryConfirmedAt:    time.Now().UTC(),
		Items:                  []Item{{Description: "Delivered stock", ValueMinor: 10000}},
		Evidence:               []Evidence{{DocumentID: "doc_1", Type: "delivery_note", SHA256: testDigest}},
		IdempotencyKey:         "fulfillment-test-1",
	}
	created, replayed, err := store.Create(ctx, owner, input)
	if err != nil || replayed {
		t.Fatalf("create failed: replayed=%v err=%v", replayed, err)
	}
	if _, err = store.Get(ctx, Principal{Subject: "client-2", TenantID: "ten_1", ApplicationID: "app_2"}, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant application read result: %v", err)
	}
	if _, err = store.Get(ctx, Principal{Subject: "client-3", TenantID: "ten_2", ApplicationID: "app_1"}, created.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("another tenant read result: %v", err)
	}
	if _, replayed, err = store.Create(ctx, owner, input); err != nil || !replayed {
		t.Fatalf("idempotent replay failed: replayed=%v err=%v", replayed, err)
	}
	input.Items[0].ValueMinor++
	if _, _, err = store.Create(ctx, owner, input); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed request reused key result: %v", err)
	}
	input.IdempotencyKey = "fulfillment-test-2"
	input.FacilityID = "fac_not_authorized"
	if _, _, err = store.Create(ctx, owner, input); !errors.Is(err, ErrFacilityIneligible) {
		t.Fatalf("ineligible facility result: %v", err)
	}
	input.IdempotencyKey = "fulfillment-test-3"
	input.FacilityID = "fac_test_1"
	input.Currency = "USD"
	if _, _, err = store.Create(ctx, owner, input); !errors.Is(err, ErrFacilityCurrencyMismatch) {
		t.Fatalf("facility currency mismatch result: %v", err)
	}
}
