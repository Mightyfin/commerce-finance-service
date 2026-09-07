package commerce

import (
	"testing"
	"time"
)

func TestValidateCreateRequiresEvidence(t *testing.T) {
	in := CreateInput{FacilityID: "fac_1", RecipientParticipantID: "par_1", Currency: "ZMW", DeliveryConfirmedAt: time.Now(), IdempotencyKey: "idem-key-1", Items: []Item{{Description: "Delivered goods", ValueMinor: 100}}}
	if ValidateCreate(&in) == nil {
		t.Fatal("fulfillment without evidence must be rejected")
	}
	in.Evidence = []Evidence{{DocumentID: "doc_1", Type: "delivery_note"}}
	if err := ValidateCreate(&in); err != nil {
		t.Fatalf("valid fulfillment rejected: %v", err)
	}
}

func TestValidateCreateRejectsUnknownEvidenceType(t *testing.T) {
	in := CreateInput{FacilityID: "fac_1", RecipientParticipantID: "par_1", Currency: "ZMW", DeliveryConfirmedAt: time.Now(), IdempotencyKey: "idem-key-1", Items: []Item{{Description: "Delivered goods", ValueMinor: 100}}, Evidence: []Evidence{{DocumentID: "doc_1", Type: "unsupported"}}}
	if ValidateCreate(&in) == nil {
		t.Fatal("unknown evidence type must be rejected")
	}
}

func TestValidateCreateRejectsFutureDeliveryAndInvalidCurrency(t *testing.T) {
	valid := CreateInput{FacilityID: "fac_1", RecipientParticipantID: "npt_1", Currency: "ZMW", DeliveryConfirmedAt: time.Now(), IdempotencyKey: "idem-key-1", Items: []Item{{Description: "Goods", ValueMinor: 100}}, Evidence: []Evidence{{DocumentID: "doc_1", Type: "invoice"}}}
	future := valid
	future.DeliveryConfirmedAt = time.Now().Add(time.Hour)
	if ValidateCreate(&future) == nil {
		t.Fatal("future delivery must be rejected")
	}
	invalidCurrency := valid
	invalidCurrency.Currency = "123"
	if ValidateCreate(&invalidCurrency) == nil {
		t.Fatal("non-alphabetic currency must be rejected")
	}
}
