package commerce

import (
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Mightyfin/commerce-finance-service/internal/database"
)

func TestConcurrentReplayPreservesOriginalOutcome(t *testing.T) {
	dsn := os.Getenv("COMMERCE_FINANCE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable database required")
	}
	ctx := context.Background()
	pool, err := database.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err = database.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	fac := newID("fac")
	if _, err = pool.Exec(ctx, `INSERT INTO eligible_facilities(facility_id,tenant_id,currency,status,source_event_id,updated_at,credit_application_id,caller_application_id,recipient_relationship_id,party_id) VALUES($1,'ten_replay','ZMW','active',$1,now(),'cap_replay','app_replay','npt_replay','party_replay')`, fac); err != nil {
		t.Fatal(err)
	}
	s := Store{Pool: pool, Verifier: testEvidenceVerifier{}}
	p := Principal{Subject: "test", TenantID: "ten_replay", ApplicationID: "app_replay"}
	in := CreateInput{FacilityID: fac, RecipientParticipantID: "npt_replay", Currency: "ZMW", DeliveryConfirmedAt: time.Now().UTC(), Items: []Item{{Description: "Goods", ValueMinor: 12500}}, Evidence: []Evidence{{DocumentID: "doc_test", Type: "delivery_note"}}, IdempotencyKey: newID("idem")}
	var wg sync.WaitGroup
	in.Evidence[0].SHA256 = testDigest
	results := make(chan Fulfillment, 8)
	errs := make(chan error, 8)
	replays := make(chan bool, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			f, replay, err := s.Create(ctx, p, in)
			results <- f
			replays <- replay
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	close(replays)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	var first Fulfillment
	var expected string
	for f := range results {
		b, _ := json.Marshal(f)
		if expected != "" && expected != string(b) {
			t.Fatal("retry response changed")
		}
		expected = string(b)
		first = f
	}
	count := 0
	for replay := range replays {
		if !replay {
			count++
		}
	}
	if count != 1 {
		t.Fatal("creations", count)
	}
	dispute := DisputeInput{Reason: "partial_delivery", Description: "Only part of delivery received", IdempotencyKey: newID("dispute")}
	disputed, replay, err := s.Dispute(ctx, p, first.ID, dispute)
	if err != nil || replay || disputed.Status != "disputed" {
		t.Fatal(disputed, replay, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE eligible_facilities SET status='closed' WHERE facility_id=$1`, fac); err != nil {
		t.Fatal(err)
	}
	again, replay, err := s.Create(ctx, p, in)
	b, _ := json.Marshal(again)
	if err != nil || !replay || string(b) != expected {
		t.Fatal("replay changed after dispute/closure", again, replay, err)
	}
	if f, replay, err := s.Dispute(ctx, p, first.ID, dispute); err != nil || !replay || f.Status != "disputed" {
		t.Fatal(f, replay, err)
	}
	var actions, events, snapshots int
	err = pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM commerce_actions WHERE fulfillment_id=$1),(SELECT count(*) FROM outbox_events WHERE aggregate_id=$1),(SELECT count(*) FROM commerce_replays r JOIN commerce_actions a ON a.id=r.action_id WHERE a.fulfillment_id=$1)`, first.ID).Scan(&actions, &events, &snapshots)
	if err != nil || actions != 2 || events != 2 || snapshots != 2 {
		t.Fatal(actions, events, snapshots, err)
	}
	var scoped bool
	err = pool.QueryRow(ctx, `SELECT bool_and(payload->>'tenant_id'=$2 AND payload->>'caller_application_id'=$3 AND event_type='commerce.fulfillment.'||(payload->>'status')) FROM outbox_events WHERE aggregate_id=$1`, first.ID, p.TenantID, p.ApplicationID).Scan(&scoped)
	if err != nil || !scoped {
		t.Fatal("commerce events lost authenticated routing provenance", scoped, err)
	}
	if _, err = pool.Exec(ctx, `UPDATE commerce_replays SET response='{}' WHERE action_id IN (SELECT id FROM commerce_actions WHERE fulfillment_id=$1)`, first.ID); err == nil {
		t.Fatal("immutable replay changed")
	}
	if _, err = pool.Exec(ctx, `UPDATE eligible_facilities SET status='active' WHERE facility_id=$1`, fac); err != nil {
		t.Fatal(err)
	}
	fn := newID("reject_replay")
	if _, err = pool.Exec(ctx, `CREATE FUNCTION `+fn+`() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.response->>'facility_id'='`+fac+`' THEN RAISE EXCEPTION 'test replay failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER `+fn+` BEFORE INSERT ON commerce_replays FOR EACH ROW EXECUTE FUNCTION `+fn+`() `); err != nil {
		t.Fatal(err)
	}
	defer pool.Exec(ctx, `DROP TRIGGER `+fn+` ON commerce_replays; DROP FUNCTION `+fn+`() `)
	in.IdempotencyKey = newID("rollback")
	if _, _, err = s.Create(ctx, p, in); err == nil {
		t.Fatal("replay persistence failure ignored")
	}
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM fulfillments WHERE tenant_id=$1 AND application_id=$2 AND idempotency_key=$3`, p.TenantID, p.ApplicationID, in.IdempotencyKey).Scan(&count); err != nil || count != 0 {
		t.Fatal("partial fulfillment committed", count, err)
	}
}
