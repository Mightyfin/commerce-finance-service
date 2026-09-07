package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Mightyfin/commerce-finance-service/internal/auth"
	"github.com/Mightyfin/commerce-finance-service/internal/commerce"
)

type Store interface {
	Ping(context.Context) error
	Create(context.Context, commerce.Principal, commerce.CreateInput) (commerce.Fulfillment, bool, error)
	Get(context.Context, commerce.Principal, string) (commerce.Fulfillment, error)
	List(context.Context, commerce.Principal, string, int) (commerce.Page, error)
	Dispute(context.Context, commerce.Principal, string, commerce.DisputeInput) (commerce.Fulfillment, bool, error)
}
type Server struct {
	Verifier auth.Verifier
	Store    Store
}

func (s Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("GET /health/live", func(w http.ResponseWriter, _ *http.Request) { write(w, 200, map[string]string{"status": "live"}) })
	m.HandleFunc("GET /health/ready", s.ready)
	m.HandleFunc("POST /v1/commerce/fulfillments", s.create)
	m.HandleFunc("GET /v1/commerce/fulfillments", s.list)
	m.HandleFunc("GET /v1/commerce/fulfillments/{id}", s.get)
	m.HandleFunc("POST /v1/commerce/fulfillments/{id}/dispute", s.dispute)
	return m
}
func (s Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if s.Store.Ping(ctx) != nil {
		write(w, 503, map[string]string{"status": "not_ready"})
		return
	}
	write(w, 200, map[string]string{"status": "ready"})
}
func (s Server) principal(w http.ResponseWriter, r *http.Request, scope string) (commerce.Principal, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(r.Header.Get("Authorization")), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return commerce.Principal{}, false
	}
	p, err := s.Verifier.Verify(r.Context(), token)
	if err != nil {
		write(w, 401, map[string]string{"error": "unauthorized"})
		return commerce.Principal{}, false
	}
	if !p.HasScope(scope) {
		write(w, 403, map[string]string{"error": "forbidden"})
		return commerce.Principal{}, false
	}
	return commerce.Principal{Subject: p.Subject, TenantID: p.TenantID, ApplicationID: p.ApplicationID}, true
}
func (s Server) create(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "commerce:write")
	if !ok {
		return
	}
	var body struct {
		FacilityID             string                                `json:"facility_id"`
		RecipientParticipantID string                                `json:"recipient_participant_id"`
		Currency               string                                `json:"currency"`
		DeliveryConfirmedAt    time.Time                             `json:"delivery_confirmed_at"`
		Items                  []struct{ Description, Value string } `json:"items"`
		Evidence               []commerce.Evidence                   `json:"evidence"`
	}
	if decode(r, &body) != nil {
		write(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	in := commerce.CreateInput{FacilityID: body.FacilityID, RecipientParticipantID: body.RecipientParticipantID, Currency: body.Currency, DeliveryConfirmedAt: body.DeliveryConfirmedAt, Evidence: body.Evidence, IdempotencyKey: strings.TrimSpace(r.Header.Get("Idempotency-Key"))}
	for _, item := range body.Items {
		minor, err := moneyToMinor(item.Value)
		if err != nil {
			write(w, 400, map[string]string{"error": "invalid_item_value"})
			return
		}
		in.Items = append(in.Items, commerce.Item{Description: item.Description, ValueMinor: minor})
	}
	if err := commerce.ValidateCreate(&in); err != nil {
		write(w, 400, map[string]string{"error": "invalid_fulfillment"})
		return
	}
	item, replay, err := s.Store.Create(r.Context(), p, in)
	if !result(w, err) {
		return
	}
	if replay {
		w.Header().Set("Idempotent-Replayed", "true")
		write(w, 200, item)
		return
	}
	write(w, 201, item)
}
func (s Server) list(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "commerce:read")
	if !ok {
		return
	}
	limit := 20
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 100 {
			write(w, 400, map[string]string{"error": "invalid_limit"})
			return
		}
		limit = n
	}
	page, err := s.Store.List(r.Context(), p, r.URL.Query().Get("cursor"), limit)
	if !result(w, err) {
		return
	}
	write(w, 200, page)
}
func (s Server) get(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "commerce:read")
	if !ok {
		return
	}
	item, err := s.Store.Get(r.Context(), p, r.PathValue("id"))
	if !result(w, err) {
		return
	}
	write(w, 200, item)
}
func (s Server) dispute(w http.ResponseWriter, r *http.Request) {
	p, ok := s.principal(w, r, "commerce:write")
	if !ok {
		return
	}
	var in commerce.DisputeInput
	if decode(r, &in) != nil {
		write(w, 400, map[string]string{"error": "invalid_request"})
		return
	}
	in.IdempotencyKey = strings.TrimSpace(r.Header.Get("Idempotency-Key"))
	in.Reason = strings.TrimSpace(in.Reason)
	in.Description = strings.TrimSpace(in.Description)
	if len(in.IdempotencyKey) < 8 || len(in.Description) < 8 || len(in.Description) > 500 || (in.Reason != "goods_not_received" && in.Reason != "partial_delivery" && in.Reason != "damaged_goods" && in.Reason != "incorrect_goods" && in.Reason != "other") {
		write(w, 400, map[string]string{"error": "invalid_dispute"})
		return
	}
	item, replay, err := s.Store.Dispute(r.Context(), p, r.PathValue("id"), in)
	if !result(w, err) {
		return
	}
	if replay {
		w.Header().Set("Idempotent-Replayed", "true")
	}
	write(w, 200, item)
}
func result(w http.ResponseWriter, err error) bool {
	if err == nil {
		return true
	}
	switch {
	case errors.Is(err, commerce.ErrNotFound):
		write(w, 404, map[string]string{"error": "not_found"})
	case errors.Is(err, commerce.ErrConflict):
		write(w, 409, map[string]string{"error": "conflict"})
	case errors.Is(err, commerce.ErrFacilityIneligible):
		write(w, 422, map[string]string{"error": "facility_not_eligible"})
	case errors.Is(err, commerce.ErrFacilityCurrencyMismatch):
		write(w, 422, map[string]string{"error": "facility_currency_mismatch"})
	default:
		write(w, 500, map[string]string{"error": "internal_error"})
	}
	return false
}
func decode(r *http.Request, v any) error {
	d := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return err
	}
	if d.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("multiple values")
	}
	return nil
}
func write(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func moneyToMinor(value string) (int64, error) {
	whole, fraction, ok := strings.Cut(value, ".")
	if !ok || len(fraction) != 2 || whole == "" || (strings.HasPrefix(whole, "0") && whole != "0") {
		return 0, fmt.Errorf("invalid money")
	}
	w, err := strconv.ParseInt(whole, 10, 64)
	if err != nil || w < 0 {
		return 0, fmt.Errorf("invalid money")
	}
	f, err := strconv.ParseInt(fraction, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid money")
	}
	if w > (1<<63-1-f)/100 {
		return 0, fmt.Errorf("invalid money")
	}
	minor := w*100 + f
	if minor <= 0 {
		return 0, fmt.Errorf("invalid money")
	}
	return minor, nil
}
