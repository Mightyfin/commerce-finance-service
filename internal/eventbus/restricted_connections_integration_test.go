package eventbus

import (
	"context"
	"encoding/json"
	"github.com/nats-io/nats.go"
	"os"
	"testing"
	"time"
)

func TestRestrictedCommerceConnections(t *testing.T) {
	url := os.Getenv("EVENTBUS_ACL_TEST_URL")
	if url == "" {
		t.Skip("isolated ACL broker required")
	}
	nc, err := nats.Connect(url, nats.UserInfo("bootstrap", os.Getenv("EVENTBUS_ACL_TEST_BOOTSTRAP_PASSWORD")))
	if err != nil {
		t.Fatal("bootstrap failed")
	}
	defer nc.Close()
	js, err := nc.JetStream()
	if err != nil {
		t.Fatal(err)
	}
	for name, subject := range map[string]string{"CREDIT_FACILITY_EVENTS": "mightyfin.facility.>", "COMMERCE_FINANCE_EVENTS": "mightyfin.commerce.>"} {
		if _, err = js.StreamInfo(name); err != nats.ErrStreamNotFound {
			t.Fatal("absent isolated streams required")
		}
		if _, err = js.AddStream(&nats.StreamConfig{Name: name, Subjects: []string{subject}, Storage: nats.MemoryStorage}); err != nil {
			t.Fatal(err)
		}
		defer js.DeleteStream(name)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	opts, err := ConnectionCredentials("", "commerce-consumer", os.Getenv("EVENTBUS_ACL_TEST_CONSUMER_PASSWORD"))
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, url, "", FacilityProjection{Environment: "sandbox"}, opts...) }()
	durable := "commerce-finance-facility-projection-sandbox"
	for {
		if _, err = js.ConsumerInfo("CREDIT_FACILITY_EVENTS", durable); err == nil {
			break
		}
		select {
		case e := <-done:
			t.Fatal("consumer failed", e)
		case <-ctx.Done():
			t.Fatal("consumer subscription timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
	// Transport-only probe: the other environment must be ACKed without using DB.
	payload, _ := json.Marshal(Envelope{ID: "synthetic-event", Type: "facility.disbursement.posted", Version: "1", TenantID: "synthetic", Environment: "production", AggregateID: "synthetic", OccurredAt: time.Now(), Data: json.RawMessage(`{}`)})
	if _, err = js.Publish("mightyfin.facility.disbursement.posted", payload); err != nil {
		t.Fatal(err)
	}
	for {
		info, e := js.ConsumerInfo("CREDIT_FACILITY_EVENTS", durable)
		if e == nil && info.Delivered.Consumer > 0 && info.NumAckPending == 0 {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("consumer ack timeout")
		case <-time.After(50 * time.Millisecond):
		}
	}
	opts, err = ConnectionCredentials("", "commerce-publisher", os.Getenv("EVENTBUS_ACL_TEST_PUBLISHER_PASSWORD"))
	if err != nil {
		t.Fatal(err)
	}
	p, closeP, err := NewPublisher(url, "", "sandbox", opts...)
	if err != nil {
		t.Fatal(err)
	}
	defer closeP()
	if err = p.Ensure(ctx); err != nil {
		t.Fatal(err)
	}
	event := OutboxEvent{ID: "synthetic-publish", Type: "commerce.fulfillment.confirmed", AggregateID: "synthetic", TenantID: "synthetic", Payload: json.RawMessage(`{}`), OccurredAt: time.Now()}
	for i := 0; i < 2; i++ {
		if err = p.Publish(ctx, event); err != nil {
			t.Fatal(err)
		}
	}
	info, err := js.StreamInfo("COMMERCE_FINANCE_EVENTS")
	if err != nil || info.State.Msgs != 1 {
		t.Fatal("duplicate durable event", err)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("consumer shutdown failed")
	}
}
