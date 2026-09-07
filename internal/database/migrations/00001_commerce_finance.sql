CREATE TABLE eligible_facilities (
 facility_id text PRIMARY KEY, tenant_id text NOT NULL, credit_application_id text,
 currency char(3) NOT NULL, status text NOT NULL,
 source_event_id text NOT NULL, updated_at timestamptz NOT NULL
);
CREATE INDEX eligible_facilities_tenant_idx ON eligible_facilities(tenant_id,facility_id);
CREATE TABLE consumed_events (event_id text PRIMARY KEY, consumed_at timestamptz NOT NULL DEFAULT now());
CREATE TABLE fulfillments (
 id text PRIMARY KEY, tenant_id text NOT NULL, application_id text NOT NULL,
 facility_id text NOT NULL, recipient_participant_id text NOT NULL,
 status text NOT NULL CHECK(status IN ('confirmed','disputed','cancelled')),
 currency char(3) NOT NULL, total_value_minor bigint NOT NULL CHECK(total_value_minor > 0),
 delivery_confirmed_at timestamptz NOT NULL, idempotency_key text NOT NULL,
 request_hash char(64) NOT NULL, created_by text NOT NULL, created_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(), UNIQUE(tenant_id,application_id,idempotency_key)
);
CREATE INDEX fulfillments_tenant_time_idx ON fulfillments(tenant_id,application_id,created_at DESC,id DESC);
CREATE TABLE fulfillment_items (
 id bigserial PRIMARY KEY, fulfillment_id text NOT NULL REFERENCES fulfillments(id),
 description text NOT NULL, value_minor bigint NOT NULL CHECK(value_minor > 0)
);
CREATE TABLE fulfillment_evidence (
 id bigserial PRIMARY KEY, fulfillment_id text NOT NULL REFERENCES fulfillments(id),
 document_id text NOT NULL, evidence_type text NOT NULL CHECK(evidence_type IN ('delivery_note','invoice','recipient_confirmation','other')),
 UNIQUE(fulfillment_id,document_id)
);
CREATE TABLE fulfillment_disputes (
 id text PRIMARY KEY, fulfillment_id text NOT NULL REFERENCES fulfillments(id), tenant_id text NOT NULL,
 reason text NOT NULL CHECK(reason IN ('goods_not_received','partial_delivery','damaged_goods','incorrect_goods','other')),
 description text NOT NULL, status text NOT NULL DEFAULT 'open' CHECK(status IN ('open','resolved')),
 opened_by text NOT NULL, opened_at timestamptz NOT NULL DEFAULT now(), resolved_at timestamptz
);
CREATE UNIQUE INDEX fulfillment_open_dispute_idx ON fulfillment_disputes(fulfillment_id) WHERE status='open';
CREATE TABLE commerce_actions (
 id text PRIMARY KEY, fulfillment_id text NOT NULL REFERENCES fulfillments(id), tenant_id text NOT NULL,
 application_id text NOT NULL, actor text NOT NULL, action text NOT NULL CHECK(action IN ('confirmed','disputed')),
 reason text, idempotency_key text NOT NULL, request_hash char(64) NOT NULL, occurred_at timestamptz NOT NULL DEFAULT now(),
 UNIQUE(fulfillment_id,idempotency_key)
);
CREATE TABLE outbox_events (
 id text PRIMARY KEY, event_type text NOT NULL, aggregate_id text NOT NULL, tenant_id text NOT NULL,
 payload jsonb NOT NULL, occurred_at timestamptz NOT NULL DEFAULT now(), published_at timestamptz,
 publishing_at timestamptz, publish_attempts integer NOT NULL DEFAULT 0, available_at timestamptz NOT NULL DEFAULT now(), last_error text
);
CREATE OR REPLACE FUNCTION prevent_commerce_action_mutation() RETURNS trigger AS $$ BEGIN RAISE EXCEPTION 'commerce actions are immutable'; END; $$ LANGUAGE plpgsql;
CREATE TRIGGER commerce_actions_immutable BEFORE UPDATE OR DELETE ON commerce_actions FOR EACH ROW EXECUTE FUNCTION prevent_commerce_action_mutation();
