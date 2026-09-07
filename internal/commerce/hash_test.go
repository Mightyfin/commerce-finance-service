package commerce

import (
	"testing"
	"time"
)

func TestCreateRequestHashIncludesMoneyAndEvidence(t *testing.T) {
	base := CreateInput{FacilityID: "fac_1", RecipientParticipantID: "npt_1", Currency: "ZMW", DeliveryConfirmedAt: time.Unix(1, 0), Items: []Item{{Description: "Goods", ValueMinor: 100}}, Evidence: []Evidence{{DocumentID: "doc_1", Type: "invoice"}}}
	changedMoney := base
	changedMoney.Items = []Item{{Description: "Goods", ValueMinor: 101}}
	changedEvidence := base
	changedEvidence.Evidence = []Evidence{{DocumentID: "doc_2", Type: "invoice"}}
	if createRequestHash(base) == createRequestHash(changedMoney) {
		t.Fatal("money must be part of the idempotency request hash")
	}
	if createRequestHash(base) == createRequestHash(changedEvidence) {
		t.Fatal("evidence must be part of the idempotency request hash")
	}
}
