package commerce

import (
	"context"
	"errors"
	"github.com/Mightyfin/commerce-finance-service/internal/database"
	"os"
	"testing"
	"time"
)

type evidenceCheckFunc func(context.Context, Principal, string, Evidence) error

func (f evidenceCheckFunc) Verify(c context.Context, p Principal, party string, e Evidence) error {
	return f(c, p, party, e)
}

func TestFulfillmentAuthorityAndEvidenceFailure(t *testing.T) {
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
	if _, err = pool.Exec(ctx, `INSERT INTO eligible_facilities(facility_id,tenant_id,currency,status,source_event_id,updated_at,credit_application_id,caller_application_id,recipient_relationship_id,party_id) VALUES($1,'tenant_auth','ZMW','disbursed',$1,now(),'cap','app_auth','recipient_auth','party_auth')`, fac); err != nil {
		t.Fatal(err)
	}
	checks := 0
	s := Store{Pool: pool, Verifier: evidenceCheckFunc(func(_ context.Context, p Principal, party string, e Evidence) error {
		checks++
		if party != "party_auth" || p.ApplicationID != "app_auth" {
			t.Fatal("wrong authority passed to verifier")
		}
		return ErrEvidenceUnavailable
	})}
	p := Principal{Subject: "client", TenantID: "tenant_auth", ApplicationID: "app_auth"}
	in := CreateInput{FacilityID: fac, RecipientParticipantID: "recipient_auth", Currency: "ZMW", DeliveryConfirmedAt: time.Now(), Items: []Item{{Description: "goods", ValueMinor: 1000}}, Evidence: []Evidence{{DocumentID: "doc", Type: "invoice", SHA256: testDigest}}, IdempotencyKey: newID("key")}
	for _, change := range []func(*Principal, *CreateInput){func(p *Principal, _ *CreateInput) { p.TenantID = "other" }, func(p *Principal, _ *CreateInput) { p.ApplicationID = "other" }, func(_ *Principal, in *CreateInput) { in.RecipientParticipantID = "other" }} {
		badP, badIn := p, in
		change(&badP, &badIn)
		if _, _, err = s.Create(ctx, badP, badIn); !errors.Is(err, ErrFacilityIneligible) {
			t.Fatal("unauthorized binding", err)
		}
	}
	if checks != 0 {
		t.Fatal("unauthorized request reached documents")
	}
	if _, _, err = s.Create(ctx, p, in); !errors.Is(err, ErrEvidenceUnavailable) {
		t.Fatal(err)
	}
	var n int
	if err = pool.QueryRow(ctx, `SELECT count(*) FROM fulfillments WHERE facility_id=$1`, fac).Scan(&n); err != nil || n != 0 {
		t.Fatal("partial confirmation", n, err)
	}
	s.Verifier = testEvidenceVerifier{}
	result, replay, err := s.Create(ctx, p, in)
	if err != nil || replay || len(result.Evidence) != 1 || result.Evidence[0].VerifiedAt == nil || result.Evidence[0].SHA256 != testDigest {
		t.Fatal(result, replay, err)
	}
	s.Verifier = nil
	if _, replay, err = s.Create(ctx, p, in); err != nil || !replay {
		t.Fatal("retry depends on current provider availability", replay, err)
	}
	changed := in
	changed.Evidence = []Evidence{{DocumentID: "doc", Type: "invoice", SHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"}}
	if _, _, err = s.Create(ctx, p, changed); !errors.Is(err, ErrConflict) {
		t.Fatal("changed document version replay", err)
	}
	in.IdempotencyKey = newID("other")
	if _, err = pool.Exec(ctx, `UPDATE eligible_facilities SET caller_application_id='' WHERE facility_id=$1`, fac); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.Create(ctx, p, in); !errors.Is(err, ErrFacilityIneligible) {
		t.Fatal("unknown legacy ownership accepted", err)
	}
}
