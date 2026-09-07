package eventbus

import (
	"context"
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
	AggregateID string          `json:"aggregate_id"`
	OccurredAt  time.Time       `json:"occurred_at"`
	Data        json.RawMessage `json:"data"`
}
type FacilityProjection struct{ Pool *pgxpool.Pool }

type facilityEventData struct {
	FacilityID    string `json:"facility_id"`
	ApplicationID string `json:"application_id"`
	Currency      string `json:"currency"`
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
	return e, d, status, true, nil
}

func (p FacilityProjection) Apply(ctx context.Context, body []byte) error {
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
	var seen bool
	if err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM consumed_events WHERE event_id=$1)`, e.ID).Scan(&seen); err != nil {
		return err
	}
	if seen {
		return tx.Commit(ctx)
	}
	_, err = tx.Exec(ctx, `INSERT INTO eligible_facilities(facility_id,tenant_id,credit_application_id,currency,status,source_event_id,updated_at)
		VALUES($1,$2,NULLIF($3,''),$4,$5,$6,$7)
		ON CONFLICT(facility_id) DO UPDATE SET
			credit_application_id=COALESCE(EXCLUDED.credit_application_id,eligible_facilities.credit_application_id),
			currency=EXCLUDED.currency,
			status=CASE
				WHEN CASE EXCLUDED.status WHEN 'disbursed' THEN 3 WHEN 'authorized' THEN 2 ELSE 1 END >=
				     CASE eligible_facilities.status WHEN 'disbursed' THEN 3 WHEN 'authorized' THEN 2 ELSE 1 END
				THEN EXCLUDED.status ELSE eligible_facilities.status END,
			source_event_id=CASE WHEN EXCLUDED.updated_at >= eligible_facilities.updated_at THEN EXCLUDED.source_event_id ELSE eligible_facilities.source_event_id END,
			updated_at=GREATEST(EXCLUDED.updated_at,eligible_facilities.updated_at)
		WHERE eligible_facilities.tenant_id=EXCLUDED.tenant_id`, d.FacilityID, e.TenantID, d.ApplicationID, d.Currency, status, e.ID, e.OccurredAt)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO consumed_events(event_id) VALUES($1)`, e.ID)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}

func Run(ctx context.Context, url, token string, projection FacilityProjection) error {
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
			_ = m.Nak()
			return
		}
		_ = m.Ack()
	}, nats.Durable("commerce-finance-facility-projection"), nats.ManualAck(), nats.AckExplicit(), nats.AckWait(time.Minute), nats.MaxAckPending(20))
	if err != nil {
		return err
	}
	<-ctx.Done()
	return nil
}
