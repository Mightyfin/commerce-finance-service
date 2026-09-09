package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Mightyfin/commerce-finance-service/internal/database"
)

func TestFacilityProjectionIdentityAndConcurrency(t *testing.T) {
	dsn := os.Getenv("COMMERCE_FINANCE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable database required")
	}
	ctx := context.Background()
	db, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err = database.Migrate(ctx, db); err != nil {
		t.Fatal(err)
	}
	id := fmt.Sprintf("commerce_identity_%d", time.Now().UnixNano())
	p := FacilityProjection{Pool: db, Environment: "sandbox"}
	data, _ := json.Marshal(facilityEventData{FacilityID: id, ApplicationID: "credit_one", Currency: "ZMW", CallerApplicationID: "app_one", RelationshipID: "recipient_one", PartyID: "party_one"})
	e := Envelope{ID: id, Type: "facility.disbursement.posted", Version: "1", TenantID: "ten_one", Environment: "sandbox", AggregateID: id, OccurredAt: time.Now().UTC(), Data: data}
	apply := func(e Envelope) error { raw, _ := json.Marshal(e); return p.Apply(ctx, raw) }
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() { defer wg.Done(); errs <- apply(e) }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for i, mutate := range []func(*Envelope){
		func(e *Envelope) { e.TenantID = "ten_other" },
		func(e *Envelope) {
			e.Data = json.RawMessage(fmt.Sprintf(`{"facility_id":%q,"application_id":"credit_one","currency":"ZMW","caller_application_id":"other"}`, id))
		},
		func(e *Envelope) {
			e.Data = json.RawMessage(fmt.Sprintf(`{"facility_id":%q,"application_id":"credit_one","currency":"ZMW","relationship_id":"other"}`, id))
		},
		func(e *Envelope) {
			e.Data = json.RawMessage(fmt.Sprintf(`{"facility_id":%q,"application_id":"credit_one","currency":"ZMW","party_id":"other"}`, id))
		},
		func(e *Envelope) {
			e.Data = json.RawMessage(fmt.Sprintf(`{"facility_id":%q,"application_id":"credit_one","currency":"USD"}`, id))
		},
		func(e *Envelope) {
			e.Data = json.RawMessage(fmt.Sprintf(`{"facility_id":%q,"application_id":"credit_other","currency":"ZMW"}`, id))
		},
	} {
		bad := e
		mutate(&bad)
		if err := apply(bad); err == nil {
			t.Fatal("changed source ID accepted")
		}
		bad.ID = fmt.Sprintf("%s_bad_%d", id, i)
		if err := apply(bad); err == nil {
			t.Fatal("facility identity was replaced")
		}
		var consumed bool
		if err := db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM consumed_events WHERE event_id=$1)`, bad.ID).Scan(&consumed); err != nil || consumed {
			t.Fatal("rejected event consumed", consumed, err)
		}
	}
	older := e
	older.ID += "_older"
	older.Type = "facility.funding.approved"
	older.OccurredAt = e.OccurredAt.Add(-time.Hour)
	if err := apply(older); err != nil {
		t.Fatal(err)
	}
	var intact bool
	if err := db.QueryRow(ctx, `SELECT tenant_id='ten_one' AND currency='ZMW' AND credit_application_id='credit_one' AND status='disbursed' AND source_event_id=$1 FROM eligible_facilities WHERE facility_id=$1`, id).Scan(&intact); err != nil || !intact {
		t.Fatal("facility regressed", intact, err)
	}
	legacy := e
	legacy.ID += "_legacy"
	if _, err := db.Exec(ctx, `INSERT INTO consumed_events(event_id) VALUES($1)`, legacy.ID); err != nil {
		t.Fatal(err)
	}
	if err := apply(legacy); err == nil {
		t.Fatal("unproven legacy replay accepted")
	}
}
