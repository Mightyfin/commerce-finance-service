package commerce

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrFacilityIneligible = errors.New("facility is not eligible")
var ErrFacilityCurrencyMismatch = errors.New("facility currency does not match fulfillment")

type Principal struct{ Subject, TenantID, ApplicationID string }
type Item struct {
	Description string `json:"description"`
	ValueMinor  int64  `json:"-"`
	Value       string `json:"value"`
}
type Evidence struct {
	DocumentID string `json:"document_id"`
	Type       string `json:"type"`
}
type Fulfillment struct {
	ID                     string     `json:"id"`
	FacilityID             string     `json:"facility_id"`
	RecipientParticipantID string     `json:"recipient_participant_id"`
	Status                 string     `json:"status"`
	Currency               string     `json:"currency"`
	TotalValueMinor        int64      `json:"-"`
	TotalValue             string     `json:"total_value"`
	DeliveryConfirmedAt    time.Time  `json:"delivery_confirmed_at"`
	Items                  []Item     `json:"items"`
	Evidence               []Evidence `json:"evidence"`
	CreatedAt              time.Time  `json:"created_at"`
}
type CreateInput struct {
	FacilityID, RecipientParticipantID, Currency string
	DeliveryConfirmedAt                          time.Time
	Items                                        []Item
	Evidence                                     []Evidence
	IdempotencyKey                               string
}
type DisputeInput struct{ Reason, Description, IdempotencyKey string }
type Page struct {
	Items      []Fulfillment `json:"items"`
	NextCursor string        `json:"next_cursor,omitempty"`
	HasMore    bool          `json:"has_more"`
}
type Store struct{ Pool *pgxpool.Pool }

func (s Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }
func (s Store) Create(ctx context.Context, p Principal, in CreateInput) (Fulfillment, bool, error) {
	requestHash := createRequestHash(in)
	id := newID("cfl")
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Fulfillment{}, false, err
	}
	defer tx.Rollback(ctx)
	var facilityCurrency string
	err = tx.QueryRow(ctx, `SELECT currency FROM eligible_facilities WHERE facility_id=$1 AND tenant_id=$2 AND status IN ('authorized','active','disbursed')`, in.FacilityID, p.TenantID).Scan(&facilityCurrency)
	if errors.Is(err, pgx.ErrNoRows) {
		return Fulfillment{}, false, ErrFacilityIneligible
	}
	if err != nil {
		return Fulfillment{}, false, err
	}
	if facilityCurrency != in.Currency {
		return Fulfillment{}, false, ErrFacilityCurrencyMismatch
	}
	var existingID, existingHash string
	err = tx.QueryRow(ctx, `SELECT id,request_hash FROM fulfillments WHERE tenant_id=$1 AND application_id=$2 AND idempotency_key=$3`, p.TenantID, p.ApplicationID, in.IdempotencyKey).Scan(&existingID, &existingHash)
	if err == nil {
		if existingHash != requestHash {
			return Fulfillment{}, false, ErrConflict
		}
		_ = tx.Commit(ctx)
		item, e := s.Get(ctx, p, existingID)
		return item, true, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Fulfillment{}, false, err
	}
	var total int64
	for _, item := range in.Items {
		total += item.ValueMinor
	}
	_, err = tx.Exec(ctx, `INSERT INTO fulfillments(id,tenant_id,application_id,facility_id,recipient_participant_id,status,currency,total_value_minor,delivery_confirmed_at,idempotency_key,request_hash,created_by) VALUES($1,$2,$3,$4,$5,'confirmed',$6,$7,$8,$9,$10,$11)`, id, p.TenantID, p.ApplicationID, in.FacilityID, in.RecipientParticipantID, in.Currency, total, in.DeliveryConfirmedAt, in.IdempotencyKey, requestHash, p.Subject)
	if err != nil {
		return Fulfillment{}, false, err
	}
	for _, item := range in.Items {
		if _, err = tx.Exec(ctx, `INSERT INTO fulfillment_items(fulfillment_id,description,value_minor) VALUES($1,$2,$3)`, id, item.Description, item.ValueMinor); err != nil {
			return Fulfillment{}, false, err
		}
	}
	for _, evidence := range in.Evidence {
		if _, err = tx.Exec(ctx, `INSERT INTO fulfillment_evidence(fulfillment_id,document_id,evidence_type) VALUES($1,$2,$3)`, id, evidence.DocumentID, evidence.Type); err != nil {
			return Fulfillment{}, false, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO commerce_actions(id,fulfillment_id,tenant_id,application_id,actor,action,reason,idempotency_key,request_hash) VALUES($1,$2,$3,$4,$5,'confirmed','Evidence-backed delivery confirmation',$6,$7)`, newID("cac"), id, p.TenantID, p.ApplicationID, p.Subject, in.IdempotencyKey, requestHash); err != nil {
		return Fulfillment{}, false, err
	}
	payload, _ := json.Marshal(map[string]any{"fulfillment_id": id, "facility_id": in.FacilityID, "total_value": minorToMoney(total), "currency": in.Currency})
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,event_type,aggregate_id,tenant_id,payload) VALUES($1,'commerce.fulfillment.confirmed',$2,$3,$4)`, newID("evt"), id, p.TenantID, payload); err != nil {
		return Fulfillment{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Fulfillment{}, false, err
	}
	item, err := s.Get(ctx, p, id)
	return item, false, err
}
func (s Store) Get(ctx context.Context, p Principal, id string) (Fulfillment, error) {
	var f Fulfillment
	err := s.Pool.QueryRow(ctx, `SELECT id,facility_id,recipient_participant_id,status,currency,total_value_minor,delivery_confirmed_at,created_at FROM fulfillments WHERE id=$1 AND tenant_id=$2 AND application_id=$3`, id, p.TenantID, p.ApplicationID).Scan(&f.ID, &f.FacilityID, &f.RecipientParticipantID, &f.Status, &f.Currency, &f.TotalValueMinor, &f.DeliveryConfirmedAt, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrNotFound
	}
	if err != nil {
		return f, err
	}
	f.TotalValue = minorToMoney(f.TotalValueMinor)
	rows, err := s.Pool.Query(ctx, `SELECT description,value_minor FROM fulfillment_items WHERE fulfillment_id=$1 ORDER BY id`, id)
	if err != nil {
		return f, err
	}
	defer rows.Close()
	for rows.Next() {
		var i Item
		if err = rows.Scan(&i.Description, &i.ValueMinor); err != nil {
			return f, err
		}
		i.Value = minorToMoney(i.ValueMinor)
		f.Items = append(f.Items, i)
	}
	if err = rows.Err(); err != nil {
		return f, err
	}
	erows, err := s.Pool.Query(ctx, `SELECT document_id,evidence_type FROM fulfillment_evidence WHERE fulfillment_id=$1 ORDER BY id`, id)
	if err != nil {
		return f, err
	}
	defer erows.Close()
	for erows.Next() {
		var e Evidence
		if err = erows.Scan(&e.DocumentID, &e.Type); err != nil {
			return f, err
		}
		f.Evidence = append(f.Evidence, e)
	}
	return f, erows.Err()
}
func (s Store) List(ctx context.Context, p Principal, cursor string, limit int) (Page, error) {
	if limit < 1 {
		limit = 20
	}
	if limit > 100 {
		limit = 100
	}
	if cursor != "" {
		if _, err := s.Get(ctx, p, cursor); err != nil {
			return Page{}, err
		}
	}
	rows, err := s.Pool.Query(ctx, `SELECT id FROM fulfillments WHERE tenant_id=$1 AND application_id=$2 AND ($3='' OR (created_at,id)<(SELECT created_at,id FROM fulfillments WHERE id=$3)) ORDER BY created_at DESC,id DESC LIMIT $4`, p.TenantID, p.ApplicationID, cursor, limit+1)
	if err != nil {
		return Page{}, err
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err = rows.Scan(&id); err != nil {
			return Page{}, err
		}
		ids = append(ids, id)
	}
	page := Page{Items: []Fulfillment{}}
	if len(ids) > limit {
		page.HasMore = true
		ids = ids[:limit]
		page.NextCursor = ids[len(ids)-1]
	}
	for _, id := range ids {
		f, e := s.Get(ctx, p, id)
		if e != nil {
			return Page{}, e
		}
		page.Items = append(page.Items, f)
	}
	return page, rows.Err()
}
func (s Store) Dispute(ctx context.Context, p Principal, id string, in DisputeInput) (Fulfillment, bool, error) {
	h := hash(in)
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Fulfillment{}, false, err
	}
	defer tx.Rollback(ctx)
	var status string
	err = tx.QueryRow(ctx, `SELECT status FROM fulfillments WHERE id=$1 AND tenant_id=$2 AND application_id=$3 FOR UPDATE`, id, p.TenantID, p.ApplicationID).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return Fulfillment{}, false, ErrNotFound
	}
	if err != nil {
		return Fulfillment{}, false, err
	}
	var oldHash string
	err = tx.QueryRow(ctx, `SELECT request_hash FROM commerce_actions WHERE fulfillment_id=$1 AND idempotency_key=$2`, id, in.IdempotencyKey).Scan(&oldHash)
	if err == nil {
		if oldHash != h {
			return Fulfillment{}, false, ErrConflict
		}
		_ = tx.Commit(ctx)
		f, e := s.Get(ctx, p, id)
		return f, true, e
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Fulfillment{}, false, err
	}
	if status != "confirmed" {
		return Fulfillment{}, false, ErrConflict
	}
	_, err = tx.Exec(ctx, `UPDATE fulfillments SET status='disputed',updated_at=now() WHERE id=$1`, id)
	if err != nil {
		return Fulfillment{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO fulfillment_disputes(id,fulfillment_id,tenant_id,reason,description,opened_by) VALUES($1,$2,$3,$4,$5,$6)`, newID("cdp"), id, p.TenantID, in.Reason, in.Description, p.Subject)
	if err != nil {
		return Fulfillment{}, false, err
	}
	_, err = tx.Exec(ctx, `INSERT INTO commerce_actions(id,fulfillment_id,tenant_id,application_id,actor,action,reason,idempotency_key,request_hash) VALUES($1,$2,$3,$4,$5,'disputed',$6,$7,$8)`, newID("cac"), id, p.TenantID, p.ApplicationID, p.Subject, in.Description, in.IdempotencyKey, h)
	if err != nil {
		return Fulfillment{}, false, err
	}
	payload, _ := json.Marshal(map[string]string{"fulfillment_id": id, "reason": in.Reason})
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,event_type,aggregate_id,tenant_id,payload) VALUES($1,'commerce.fulfillment.disputed',$2,$3,$4)`, newID("evt"), id, p.TenantID, payload)
	if err != nil {
		return Fulfillment{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Fulfillment{}, false, err
	}
	f, e := s.Get(ctx, p, id)
	return f, false, e
}
func hash(v any) string {
	b, _ := json.Marshal(v)
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func createRequestHash(in CreateInput) string {
	type hashItem struct {
		Description string `json:"description"`
		ValueMinor  int64  `json:"value_minor"`
	}
	type hashEvidence struct {
		DocumentID string `json:"document_id"`
		Type       string `json:"type"`
	}
	items := make([]hashItem, 0, len(in.Items))
	for _, item := range in.Items {
		items = append(items, hashItem{Description: item.Description, ValueMinor: item.ValueMinor})
	}
	evidence := make([]hashEvidence, 0, len(in.Evidence))
	for _, item := range in.Evidence {
		evidence = append(evidence, hashEvidence{DocumentID: item.DocumentID, Type: item.Type})
	}
	return hash(struct {
		FacilityID             string         `json:"facility_id"`
		RecipientParticipantID string         `json:"recipient_participant_id"`
		Currency               string         `json:"currency"`
		DeliveryConfirmedAt    time.Time      `json:"delivery_confirmed_at"`
		Items                  []hashItem     `json:"items"`
		Evidence               []hashEvidence `json:"evidence"`
	}{in.FacilityID, in.RecipientParticipantID, in.Currency, in.DeliveryConfirmedAt.UTC(), items, evidence})
}
func newID(prefix string) string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return prefix + "_" + hex.EncodeToString(b)
}
func minorToMoney(v int64) string { return fmt.Sprintf("%d.%02d", v/100, v%100) }
func ValidateCreate(in *CreateInput) error {
	in.FacilityID = strings.TrimSpace(in.FacilityID)
	in.RecipientParticipantID = strings.TrimSpace(in.RecipientParticipantID)
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	if len(in.FacilityID) > 160 || in.FacilityID == "" || len(in.RecipientParticipantID) > 160 || in.RecipientParticipantID == "" || !validCurrency(in.Currency) || len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 200 || in.DeliveryConfirmedAt.IsZero() || in.DeliveryConfirmedAt.After(time.Now().UTC().Add(5*time.Minute)) || len(in.Items) == 0 || len(in.Items) > 100 || len(in.Evidence) == 0 || len(in.Evidence) > 20 {
		return fmt.Errorf("invalid fulfillment")
	}
	for index := range in.Items {
		in.Items[index].Description = strings.TrimSpace(in.Items[index].Description)
		if in.Items[index].Description == "" || len(in.Items[index].Description) > 500 || in.Items[index].ValueMinor <= 0 {
			return fmt.Errorf("invalid fulfillment item")
		}
	}
	for index := range in.Evidence {
		in.Evidence[index].DocumentID = strings.TrimSpace(in.Evidence[index].DocumentID)
		if in.Evidence[index].DocumentID == "" || len(in.Evidence[index].DocumentID) > 160 || (in.Evidence[index].Type != "delivery_note" && in.Evidence[index].Type != "invoice" && in.Evidence[index].Type != "recipient_confirmation" && in.Evidence[index].Type != "other") {
			return fmt.Errorf("invalid evidence")
		}
	}
	return nil
}

func validCurrency(value string) bool {
	if len(value) != 3 {
		return false
	}
	for _, character := range value {
		if character < 'A' || character > 'Z' {
			return false
		}
	}
	return true
}
