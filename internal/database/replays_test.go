package database

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestReplayMigrationPreservesHistoricalActionStatus(t *testing.T) {
	dsn := os.Getenv("COMMERCE_FINANCE_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("disposable database required")
	}
	ctx := context.Background()
	admin, err := Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	schema := fmt.Sprintf("commerce_upgrade_%d", time.Now().UnixNano())
	quoted := pgx.Identifier{schema}.Sanitize()
	if _, err = admin.Exec(ctx, "CREATE SCHEMA "+quoted); err != nil {
		t.Fatal(err)
	}
	defer admin.Exec(ctx, "DROP SCHEMA "+quoted+" CASCADE")
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatal(err)
	}
	cfg.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	body, err := files.ReadFile("migrations/00001_commerce_finance.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = pool.Exec(ctx, string(body)); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(ctx, `CREATE TABLE schema_migrations(version text PRIMARY KEY,applied_at timestamptz NOT NULL DEFAULT now());
	INSERT INTO schema_migrations(version) VALUES('00001_commerce_finance.sql');
	INSERT INTO fulfillments(id,tenant_id,application_id,facility_id,recipient_participant_id,status,currency,total_value_minor,delivery_confirmed_at,idempotency_key,request_hash,created_by)
	VALUES('f','t','a','fac','p','disputed','ZMW',12500,now(),'create-key',repeat('a',64),'test');
	INSERT INTO fulfillment_items(fulfillment_id,description,value_minor) VALUES('f','Goods',12500);
	INSERT INTO fulfillment_evidence(fulfillment_id,document_id,evidence_type) VALUES('f','doc','delivery_note');
	INSERT INTO commerce_actions(id,fulfillment_id,tenant_id,application_id,actor,action,idempotency_key,request_hash) VALUES('created','f','t','a','test','confirmed','create-key',repeat('a',64)),('disputed','f','t','a','test','disputed','dispute-key',repeat('b',64));`)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = Migrate(ctx, pool); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	err = pool.QueryRow(ctx, `SELECT count(*) FROM commerce_replays WHERE response->>'total_value'='125.00' AND response#>>'{items,0,value}'='125.00' AND response#>>'{evidence,0,document_id}'='doc' AND response->>'status'=CASE action_id WHEN 'created' THEN 'confirmed' ELSE 'disputed' END`).Scan(&count)
	if err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if _, err = pool.Exec(ctx, `DELETE FROM commerce_replays`); err == nil {
		t.Fatal("snapshot deletion allowed")
	}
}
