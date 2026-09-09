package commerce

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")
var ErrConflict = errors.New("conflict")
var ErrFacilityIneligible = errors.New("facility is not eligible")
var ErrFacilityCurrencyMismatch = errors.New("facility currency does not match fulfillment")
var ErrInvalid = errors.New("invalid fulfillment")
var ErrEvidenceUnavailable = errors.New("evidence verification unavailable")

type Principal struct {
	Subject, TenantID, ApplicationID, Environment string
	Token                                         string `json:"-"`
}
type Item struct {
	Description string `json:"description"`
	ValueMinor  int64  `json:"-"`
	Value       string `json:"value"`
}
type Evidence struct {
	DocumentID string     `json:"document_id"`
	Type       string     `json:"type"`
	SHA256     string     `json:"sha256,omitempty"`
	VerifiedAt *time.Time `json:"verified_at,omitempty"`
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
type EvidenceVerifier interface {
	Verify(context.Context, Principal, string, Evidence) error
}
type Store struct {
	Pool     *pgxpool.Pool
	Verifier EvidenceVerifier
}

func (s Store) Ping(ctx context.Context) error { return s.Pool.Ping(ctx) }
func (s Store) Create(ctx context.Context, p Principal, in CreateInput) (Fulfillment, bool, error) {
	in.Items = append([]Item(nil), in.Items...)
	in.Evidence = append([]Evidence(nil), in.Evidence...)
	if err := ValidateCreate(&in); err != nil {
		return Fulfillment{}, false, ErrInvalid
	}
	if p.Subject == "" || p.TenantID == "" || p.ApplicationID == "" {
		return Fulfillment{}, false, ErrInvalid
	}
	requestHash := createRequestHash(in)
	id := newID("cfl")
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return Fulfillment{}, false, err
	}
	defer tx.Rollback(ctx)
	// Serialize the same request key before eligibility can change or a second
	// request attempts the unique insert. No financial capacity is consumed here.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1,0))`, hash([]string{p.TenantID, p.ApplicationID, in.IdempotencyKey})); err != nil {
		return Fulfillment{}, false, err
	}
	var existingHash string
	var existingBody []byte
	err = tx.QueryRow(ctx, `SELECT f.request_hash,r.response FROM fulfillments f JOIN commerce_actions a ON a.fulfillment_id=f.id AND a.action='confirmed' JOIN commerce_replays r ON r.action_id=a.id
	 WHERE f.tenant_id=$1 AND f.application_id=$2 AND f.idempotency_key=$3`, p.TenantID, p.ApplicationID, in.IdempotencyKey).Scan(&existingHash, &existingBody)
	if err == nil {
		if existingHash != requestHash {
			return Fulfillment{}, false, ErrConflict
		}
		var item Fulfillment
		if err = json.Unmarshal(existingBody, &item); err != nil {
			return Fulfillment{}, false, err
		}
		return item, true, tx.Commit(ctx)
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return Fulfillment{}, false, err
	}
	var facilityCurrency, partyID string
	err = tx.QueryRow(ctx, `SELECT currency,party_id FROM eligible_facilities WHERE facility_id=$1 AND tenant_id=$2 AND caller_application_id=$3 AND recipient_relationship_id=$4 AND party_id<>'' AND credit_application_id IS NOT NULL AND status IN ('authorized','active','disbursed') FOR SHARE`, in.FacilityID, p.TenantID, p.ApplicationID, in.RecipientParticipantID).Scan(&facilityCurrency, &partyID)
	if errors.Is(err, pgx.ErrNoRows) {
		return Fulfillment{}, false, ErrFacilityIneligible
	}
	if err != nil {
		return Fulfillment{}, false, err
	}
	if facilityCurrency != in.Currency {
		return Fulfillment{}, false, ErrFacilityCurrencyMismatch
	}
	// Preserve old successful replays above; only new bindings require a digest.
	for _, e := range in.Evidence {
		if e.SHA256 == "" {
			return Fulfillment{}, false, ErrInvalid
		}
	}
	if s.Verifier == nil {
		return Fulfillment{}, false, ErrEvidenceUnavailable
	}
	verificationCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	for i := range in.Evidence {
		if in.Evidence[i].SHA256 == "" {
			return Fulfillment{}, false, ErrInvalid
		}
		if err := s.Verifier.Verify(verificationCtx, p, partyID, in.Evidence[i]); err != nil {
			return Fulfillment{}, false, ErrEvidenceUnavailable
		}
		now := time.Now().UTC()
		in.Evidence[i].VerifiedAt = &now
	}
	total, err := itemTotal(in.Items)
	if err != nil {
		return Fulfillment{}, false, err
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
		if _, err = tx.Exec(ctx, `INSERT INTO fulfillment_evidence(fulfillment_id,document_id,evidence_type,sha256,verified_at) VALUES($1,$2,$3,$4,$5)`, id, evidence.DocumentID, evidence.Type, evidence.SHA256, evidence.VerifiedAt); err != nil {
			return Fulfillment{}, false, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO commerce_actions(id,fulfillment_id,tenant_id,application_id,actor,action,reason,idempotency_key,request_hash) VALUES($1,$2,$3,$4,$5,'confirmed','Delivery confirmation with verified clean document versions; document contents are not independently certified',$6,$7)`, newID("cac"), id, p.TenantID, p.ApplicationID, p.Subject, in.IdempotencyKey, requestHash); err != nil {
		return Fulfillment{}, false, err
	}
	payload, _ := json.Marshal(map[string]any{"fulfillment_id": id, "facility_id": in.FacilityID, "total_value": minorToMoney(total), "currency": in.Currency, "tenant_id": p.TenantID, "caller_application_id": p.ApplicationID, "status": "confirmed"})
	if _, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,event_type,aggregate_id,tenant_id,payload) VALUES($1,'commerce.fulfillment.confirmed',$2,$3,$4)`, newID("evt"), id, p.TenantID, payload); err != nil {
		return Fulfillment{}, false, err
	}
	item, err := readFulfillment(ctx, tx, p, id)
	if err != nil {
		return Fulfillment{}, false, err
	}
	if err = recordReplay(ctx, tx, id, in.IdempotencyKey, item); err != nil {
		return Fulfillment{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Fulfillment{}, false, err
	}
	return item, false, nil
}
func (s Store) Get(ctx context.Context, p Principal, id string) (Fulfillment, error) {
	return readFulfillment(ctx, s.Pool, p, id)
}

type queryer interface {
	QueryRow(context.Context, string, ...any) pgx.Row
	Query(context.Context, string, ...any) (pgx.Rows, error)
}

func readFulfillment(ctx context.Context, db queryer, p Principal, id string) (Fulfillment, error) {
	var f Fulfillment
	err := db.QueryRow(ctx, `SELECT id,facility_id,recipient_participant_id,status,currency,total_value_minor,delivery_confirmed_at,created_at FROM fulfillments WHERE id=$1 AND tenant_id=$2 AND application_id=$3`, id, p.TenantID, p.ApplicationID).Scan(&f.ID, &f.FacilityID, &f.RecipientParticipantID, &f.Status, &f.Currency, &f.TotalValueMinor, &f.DeliveryConfirmedAt, &f.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return f, ErrNotFound
	}
	if err != nil {
		return f, err
	}
	f.TotalValue = minorToMoney(f.TotalValueMinor)
	rows, err := db.Query(ctx, `SELECT description,value_minor FROM fulfillment_items WHERE fulfillment_id=$1 ORDER BY id`, id)
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
	rows.Close()
	erows, err := db.Query(ctx, `SELECT document_id,evidence_type,COALESCE(sha256,''),verified_at FROM fulfillment_evidence WHERE fulfillment_id=$1 ORDER BY id`, id)
	if err != nil {
		return f, err
	}
	defer erows.Close()
	for erows.Next() {
		var e Evidence
		if err = erows.Scan(&e.DocumentID, &e.Type, &e.SHA256, &e.VerifiedAt); err != nil {
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
	if err = rows.Err(); err != nil {
		return Page{}, err
	}
	rows.Close()
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
	if p.Subject == "" || p.TenantID == "" || p.ApplicationID == "" {
		return Fulfillment{}, false, ErrInvalid
	}
	if err := ValidateDispute(&in); err != nil {
		return Fulfillment{}, false, err
	}
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
	var oldBody []byte
	err = tx.QueryRow(ctx, `SELECT a.request_hash,r.response FROM commerce_actions a JOIN commerce_replays r ON r.action_id=a.id WHERE a.fulfillment_id=$1 AND a.idempotency_key=$2`, id, in.IdempotencyKey).Scan(&oldHash, &oldBody)
	if err == nil {
		if oldHash != h {
			return Fulfillment{}, false, ErrConflict
		}
		var f Fulfillment
		if err = json.Unmarshal(oldBody, &f); err != nil {
			return Fulfillment{}, false, err
		}
		return f, true, tx.Commit(ctx)
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
	payload, _ := json.Marshal(map[string]string{"fulfillment_id": id, "reason": in.Reason, "tenant_id": p.TenantID, "caller_application_id": p.ApplicationID, "status": "disputed"})
	_, err = tx.Exec(ctx, `INSERT INTO outbox_events(id,event_type,aggregate_id,tenant_id,payload) VALUES($1,'commerce.fulfillment.disputed',$2,$3,$4)`, newID("evt"), id, p.TenantID, payload)
	if err != nil {
		return Fulfillment{}, false, err
	}
	f, err := readFulfillment(ctx, tx, p, id)
	if err != nil {
		return Fulfillment{}, false, err
	}
	if err = recordReplay(ctx, tx, id, in.IdempotencyKey, f); err != nil {
		return Fulfillment{}, false, err
	}
	if err = tx.Commit(ctx); err != nil {
		return Fulfillment{}, false, err
	}
	return f, false, nil
}
func recordReplay(ctx context.Context, tx pgx.Tx, id, key string, f Fulfillment) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	tag, err := tx.Exec(ctx, `INSERT INTO commerce_replays(action_id,response) SELECT id,$3 FROM commerce_actions WHERE fulfillment_id=$1 AND idempotency_key=$2`, id, key, body)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrConflict
	}
	return nil
}
func itemTotal(items []Item) (int64, error) {
	var total int64
	for _, item := range items {
		if item.ValueMinor <= 0 || total > math.MaxInt64-item.ValueMinor {
			return 0, ErrInvalid
		}
		total += item.ValueMinor
	}
	if total <= 0 {
		return 0, ErrInvalid
	}
	return total, nil
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
		SHA256     string `json:"sha256,omitempty"`
		Type       string `json:"type"`
	}
	items := make([]hashItem, 0, len(in.Items))
	for _, item := range in.Items {
		items = append(items, hashItem{Description: item.Description, ValueMinor: item.ValueMinor})
	}
	evidence := make([]hashEvidence, 0, len(in.Evidence))
	for _, item := range in.Evidence {
		evidence = append(evidence, hashEvidence{DocumentID: item.DocumentID, Type: item.Type, SHA256: item.SHA256})
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
	if _, err := itemTotal(in.Items); err != nil {
		return err
	}
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
	seen := map[string]bool{}
	for index := range in.Evidence {
		digest := in.Evidence[index].SHA256
		if digest != "" && (len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "") {
			return ErrInvalid
		}
		in.Evidence[index].DocumentID = strings.TrimSpace(in.Evidence[index].DocumentID)
		if in.Evidence[index].DocumentID == "" || len(in.Evidence[index].DocumentID) > 160 || (in.Evidence[index].Type != "delivery_note" && in.Evidence[index].Type != "invoice" && in.Evidence[index].Type != "recipient_confirmation" && in.Evidence[index].Type != "other") {
			return fmt.Errorf("invalid evidence")
		}
		if seen[in.Evidence[index].DocumentID] {
			return ErrInvalid
		}
		seen[in.Evidence[index].DocumentID] = true
	}
	return nil
}

func ValidateDispute(in *DisputeInput) error {
	in.IdempotencyKey = strings.TrimSpace(in.IdempotencyKey)
	in.Reason = strings.TrimSpace(in.Reason)
	in.Description = strings.TrimSpace(in.Description)
	if len(in.IdempotencyKey) < 8 || len(in.IdempotencyKey) > 200 || len(in.Description) < 8 || len(in.Description) > 500 {
		return ErrInvalid
	}
	switch in.Reason {
	case "goods_not_received", "partial_delivery", "damaged_goods", "incorrect_goods", "other":
		return nil
	default:
		return ErrInvalid
	}
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
