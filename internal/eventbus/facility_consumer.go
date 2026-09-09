package eventbus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
)

type Envelope struct {
	ID          string          `json:"id"`
	Type        string          `json:"type"`
	Version     string          `json:"version"`
	TenantID    string          `json:"tenant_id"`
	Environment string          `json:"environment"`
	AggregateID string          `json:"aggregate_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Data        json.RawMessage `json:"data"`
}
type FacilityProjection struct {
	Pool        *pgxpool.Pool
	Environment string
}

type facilityEventData struct {
	CallerApplicationID string `json:"caller_application_id"`
	RelationshipID      string `json:"relationship_id"`
	PartyID             string `json:"party_id"`
	CreditApplicationID string `json:"credit_application_id"`
	FacilityID          string `json:"facility_id"`
	ApplicationID       string `json:"application_id"`
	Currency            string `json:"currency"`
}

func decodeFacilityEvent(body []byte) (Envelope, facilityEventData, string, bool, error) {
	var e Envelope
	if json.Unmarshal(body, &e) != nil || e.ID == "" || e.TenantID == "" || e.AggregateID == "" || e.Version != "1" || e.OccurredAt.IsZero() {
		return Envelope{}, facilityEventData{}, "", false, fmt.Errorf("invalid facility event")
	}
	statusByType := map[string]string{
		"facility.funding.approved":        "funding_approved",
		"facility.disbursement.authorized": "authorized",
		"facility.disbursement.posted":     "disbursed",
	}
	status, relevant := statusByType[e.Type]
	if !relevant {
		return e, facilityEventData{}, "", false, nil
	}
	var d facilityEventData
	if json.Unmarshal(e.Data, &d) != nil || d.FacilityID == "" || len(d.Currency) != 3 || d.FacilityID != e.AggregateID {
		return Envelope{}, facilityEventData{}, "", false, fmt.Errorf("invalid facility event data")
	}
	if d.CreditApplicationID != "" {
		if d.ApplicationID != "" && d.ApplicationID != d.CreditApplicationID {
			return Envelope{}, facilityEventData{}, "", false, fmt.Errorf("conflicting application identity")
		}
		d.ApplicationID = d.CreditApplicationID
	}
	return e, d, status, true, nil
}

func (p FacilityProjection) Apply(ctx context.Context, body []byte) error {
	var scope struct {
		Environment string `json:"environment"`
	}
	if json.Unmarshal(body, &scope) != nil || !validEnvironment(scope.Environment) || !validEnvironment(p.Environment) {
		return fmt.Errorf("explicit event environment required")
	}
	if scope.Environment != p.Environment {
		return nil
	}
	e, d, status, relevant, err := decodeFacilityEvent(body)
	if err != nil {
		return err
	}
	if !relevant {
		return nil
	}
	tx, err := p.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	canonical, err := json.Marshal(e)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(canonical)
	fingerprint := hex.EncodeToString(sum[:])
	// A duplicate must be the same fact, and a facility must retain its owner
	// and contracted currency across out-of-order and concurrent deliveries.
	for _, key := range []string{"commerce-source:" + e.ID, "commerce-facility:" + d.FacilityID} {
		if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, key); err != nil {
			return err
		}
	}
	var seen, same bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM consumed_events WHERE event_id=$1),EXISTS(SELECT 1 FROM consumed_events WHERE event_id=$1 AND payload_hash=$2)`, e.ID, fingerprint).Scan(&seen, &same); err != nil {
		return err
	}
	if seen {
		if !same {
			return fmt.Errorf("facility event identity conflict; source review required")
		}
		return tx.Commit(ctx)
	}
	tag, err := tx.Exec(ctx, `INSERT INTO eligible_facilities(facility_id,tenant_id,credit_application_id,currency,status,source_event_id,updated_at,caller_application_id,recipient_relationship_id,party_id)
		VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT(facility_id) DO UPDATE SET
			caller_application_id=COALESCE(NULLIF(EXCLUDED.caller_application_id,''),eligible_facilities.caller_application_id),
			recipient_relationship_id=COALESCE(NULLIF(EXCLUDED.recipient_relationship_id,''),eligible_facilities.recipient_relationship_id),
			party_id=COALESCE(NULLIF(EXCLUDED.party_id,''),eligible_facilities.party_id),
			credit_application_id=COALESCE(EXCLUDED.credit_application_id,eligible_facilities.credit_application_id),
			currency=EXCLUDED.currency,
			status=CASE
				WHEN CASE EXCLUDED.status WHEN 'disbursed' THEN 3 WHEN 'authorized' THEN 2 ELSE 1 END >=
				     CASE eligible_facilities.status WHEN 'disbursed' THEN 3 WHEN 'authorized' THEN 2 ELSE 1 END
				THEN EXCLUDED.status ELSE eligible_facilities.status END,
			source_event_id=CASE WHEN EXCLUDED.updated_at >= eligible_facilities.updated_at THEN EXCLUDED.source_event_id ELSE eligible_facilities.source_event_id END,
			updated_at=GREATEST(EXCLUDED.updated_at,eligible_facilities.updated_at)
		WHERE eligible_facilities.tenant_id=EXCLUDED.tenant_id
		 AND eligible_facilities.currency=EXCLUDED.currency
		 AND (eligible_facilities.credit_application_id IS NULL OR EXCLUDED.credit_application_id IS NULL OR eligible_facilities.credit_application_id=EXCLUDED.credit_application_id)
		 AND (eligible_facilities.caller_application_id='' OR EXCLUDED.caller_application_id='' OR eligible_facilities.caller_application_id=EXCLUDED.caller_application_id)
		 AND (eligible_facilities.recipient_relationship_id='' OR EXCLUDED.recipient_relationship_id='' OR eligible_facilities.recipient_relationship_id=EXCLUDED.recipient_relationship_id)
		 AND (eligible_facilities.party_id='' OR EXCLUDED.party_id='' OR eligible_facilities.party_id=EXCLUDED.party_id)`, d.FacilityID, e.TenantID, d.ApplicationID, d.Currency, status, e.ID, e.OccurredAt, d.CallerApplicationID, d.RelationshipID, d.PartyID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("facility ownership or terms conflict")
	}
	_, err = tx.Exec(ctx, `INSERT INTO consumed_events(event_id,payload_hash) VALUES($1,$2)`, e.ID, fingerprint)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func Run(ctx context.Context, url, token string, projection FacilityProjection) error {
	if !validEnvironment(projection.Environment) {
		return fmt.Errorf("explicit event environment required")
	}
	opts := []nats.Option{nats.Name("commerce-finance-facility-consumer")}
	if token != "" {
		opts = append(opts, nats.Token(token))
	}
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return err
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		return err
	}
	_, err = js.Subscribe("mightyfin.facility.>", func(m *nats.Msg) {
		if err := projection.Apply(ctx, m.Data); err != nil {
			_ = m.NakWithDelay(5 * time.Second)
			return
		}
		_ = m.Ack()
	}, nats.Durable("commerce-finance-facility-projection-"+projection.Environment), nats.ManualAck(), nats.AckExplicit(), nats.AckWait(time.Minute), nats.MaxAckPending(20))
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
