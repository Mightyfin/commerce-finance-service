package eventbus

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"strings"
	"time"
)

type OutboxEvent struct {
	ID, Type, AggregateID, TenantID string
	Payload                         json.RawMessage
	OccurredAt                      time.Time
}
type OutboxStore struct{ Pool *pgxpool.Pool }

func (s OutboxStore) Claim(ctx context.Context) (*OutboxEvent, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var e OutboxEvent
	err = tx.QueryRow(ctx, `WITH c AS (SELECT id FROM outbox_events WHERE published_at IS NULL AND available_at<=now() AND (publishing_at IS NULL OR publishing_at<now()-interval '5 minutes') ORDER BY occurred_at,id FOR UPDATE SKIP LOCKED LIMIT 1) UPDATE outbox_events o SET publishing_at=now(),publish_attempts=publish_attempts+1 FROM c WHERE o.id=c.id RETURNING o.id,o.event_type,o.aggregate_id,o.tenant_id,o.payload,o.occurred_at`).Scan(&e.ID, &e.Type, &e.AggregateID, &e.TenantID, &e.Payload, &e.OccurredAt)
	if errorsIsNoRows(err) {
		if err = tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &e, tx.Commit(ctx)
}
func errorsIsNoRows(err error) bool { return err == pgx.ErrNoRows }
func (s OutboxStore) Complete(ctx context.Context, id string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE outbox_events SET published_at=now(),publishing_at=NULL,last_error=NULL WHERE id=$1`, id)
	return err
}
func (s OutboxStore) Fail(ctx context.Context, id, cause string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE outbox_events SET publishing_at=NULL,last_error=$2,available_at=now()+interval '5 seconds' WHERE id=$1`, id, cause)
	return err
}

type Publisher struct {
	js          nats.JetStreamContext
	environment string
}

func NewPublisher(url, token, environment string) (*Publisher, func(), error) {
	if !validEnvironment(environment) {
		return nil, nil, fmt.Errorf("explicit event environment required")
	}
	opts := []nats.Option{nats.Name("commerce-finance-outbox-publisher")}
	if token != "" {
		opts = append(opts, nats.Token(token))
	}
	nc, err := nats.Connect(url, opts...)
	if err != nil {
		return nil, nil, err
	}
	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, nil, err
	}
	return &Publisher{js: js, environment: environment}, nc.Close, nil
}
func (p *Publisher) Ensure(ctx context.Context) error {
	_, err := p.js.StreamInfo("COMMERCE_FINANCE_EVENTS", nats.Context(ctx))
	if err == nil {
		return nil
	}
	if err != nats.ErrStreamNotFound {
		return err
	}
	_, err = p.js.AddStream(&nats.StreamConfig{Name: "COMMERCE_FINANCE_EVENTS", Subjects: []string{"mightyfin.commerce.>"}}, nats.Context(ctx))
	return err
}
func (p *Publisher) Publish(ctx context.Context, e OutboxEvent) error {
	if !validEnvironment(p.environment) {
		return fmt.Errorf("explicit event environment required")
	}
	payload, err := scopedPayload(e, p.environment)
	if err != nil {
		return err
	}
	body, err := json.Marshal(Envelope{ID: e.ID, Type: e.Type, Version: "1", Environment: p.environment, TenantID: e.TenantID, AggregateID: e.AggregateID, OccurredAt: e.OccurredAt, Data: payload})
	if err != nil {
		return err
	}
	suffix := strings.TrimPrefix(e.Type, "commerce.")
	if suffix == e.Type || suffix == "" {
		return fmt.Errorf("invalid commerce event")
	}
	_, err = p.js.Publish("mightyfin.commerce."+suffix, body, nats.MsgId(p.environment+":"+e.ID), nats.Context(ctx))
	return err
}

// The deployment owns environment, while the transaction owns tenant and caller
// identity. Never invent an application for legacy unscoped events.
func scopedPayload(e OutboxEvent, environment string) (json.RawMessage, error) {
	var data map[string]json.RawMessage
	if !validEnvironment(environment) || json.Unmarshal(e.Payload, &data) != nil || data == nil {
		return nil, fmt.Errorf("invalid commerce event payload")
	}
	for key, expected := range map[string]string{"tenant_id": e.TenantID, "environment": environment} {
		if raw, ok := data[key]; ok {
			var actual string
			if json.Unmarshal(raw, &actual) != nil || actual != expected {
				return nil, fmt.Errorf("commerce event scope mismatch")
			}
		}
		data[key], _ = json.Marshal(expected)
	}
	return json.Marshal(data)
}

func validEnvironment(v string) bool {
	switch v {
	case "local", "dev", "staging", "sandbox", "production":
		return true
	}
	return false
}
