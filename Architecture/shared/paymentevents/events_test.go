package paymentevents

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

func envelope(t *testing.T, eventType string, payload any) *events.EventEnvelope {
	t.Helper()
	raw, ok := payload.([]byte)
	if !ok {
		b, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		raw = b
	}
	return &events.EventEnvelope{EventID: "evt-1", EventType: eventType, Payload: raw}
}

// recorder records every call and returns err.
type recorder struct {
	NopHandler
	succeeded    []Succeeded
	failed       []Failed
	refunded     []Refunded
	refundFailed []RefundFailed
	err          error
}

func (r *recorder) OnSucceeded(_ context.Context, _ *events.EventEnvelope, ev Succeeded) error {
	r.succeeded = append(r.succeeded, ev)
	return r.err
}
func (r *recorder) OnFailed(_ context.Context, _ *events.EventEnvelope, ev Failed) error {
	r.failed = append(r.failed, ev)
	return r.err
}
func (r *recorder) OnRefunded(_ context.Context, _ *events.EventEnvelope, ev Refunded) error {
	r.refunded = append(r.refunded, ev)
	return r.err
}
func (r *recorder) OnRefundFailed(_ context.Context, _ *events.EventEnvelope, ev RefundFailed) error {
	r.refundFailed = append(r.refundFailed, ev)
	return r.err
}

func (r *recorder) total() int {
	return len(r.succeeded) + len(r.failed) + len(r.refunded) + len(r.refundFailed)
}

// intentRow is what payments-service publishes for succeeded/failed: the
// marshalled intent row, including the deprecated float mirror.
func intentRow(status string) map[string]any {
	return map[string]any{
		"id": "cccccccc-0000-0000-0000-000000000003", "payer_id": "bbbbbbbb-0000-0000-0000-000000000002",
		"payee_id": "dddddddd-0000-0000-0000-000000000004", "reference_type": "food_order",
		"reference_id": "aaaaaaaa-0000-0000-0000-000000000001", "amount": 1.0, "amount_minor": 25000,
		"currency": "INR", "method": "upi", "status": status, "provider_ref": "order_RZP1",
		"idempotency_key": "food_order:aaaaaaaa-0000-0000-0000-000000000001", "refunded_amount_minor": 0,
		"owner_domain": "food-service", "created_at": "2026-09-14T10:00:00Z", "updated_at": "2026-09-14T10:00:01Z",
	}
}

func TestDispatch_DecodesSucceededAndFailed(t *testing.T) {
	want := Payment{
		ID: "cccccccc-0000-0000-0000-000000000003", PayerID: "bbbbbbbb-0000-0000-0000-000000000002",
		PayeeID: "dddddddd-0000-0000-0000-000000000004", ReferenceType: "food_order",
		ReferenceID: "aaaaaaaa-0000-0000-0000-000000000001", AmountMinor: 25000, Currency: "INR",
		Method: "upi", Status: "succeeded", ProviderRef: "order_RZP1",
	}
	r := &recorder{}
	if err := Dispatch(context.Background(), envelope(t, TypeSucceeded, intentRow("succeeded")), r); err != nil {
		t.Fatal(err)
	}
	if len(r.succeeded) != 1 || r.succeeded[0].Payment != want {
		t.Fatalf("succeeded = %+v\nwant        %+v", r.succeeded, want)
	}

	want.Status = "failed"
	if err := Dispatch(context.Background(), envelope(t, TypeFailed, intentRow("failed")), r); err != nil {
		t.Fatal(err)
	}
	if len(r.failed) != 1 || r.failed[0].Payment != want {
		t.Fatalf("failed = %+v", r.failed)
	}
	if r.total() != 2 {
		t.Fatalf("calls = %d", r.total())
	}
}

func TestDispatch_DecodesProviderRefunded(t *testing.T) {
	r := &recorder{}
	p := map[string]any{
		"id": "i-1", "intent_id": "i-1", "provider": "razorpay", "provider_refund_id": "rfnd_1",
		"amount_minor": 5000, "status": "partially_refunded", "reference_type": "order", "reference_id": "o-1",
	}
	if err := Dispatch(context.Background(), envelope(t, TypeRefunded, p), r); err != nil {
		t.Fatal(err)
	}
	want := Refunded{ID: "i-1", IntentID: "i-1", Provider: "razorpay", ProviderRefundID: "rfnd_1",
		AmountMinor: 5000, Status: "partially_refunded", ReferenceType: "order", ReferenceID: "o-1"}
	if len(r.refunded) != 1 || r.refunded[0] != want {
		t.Fatalf("refunded = %+v", r.refunded)
	}
	if r.refunded[0].Manual || r.refunded[0].CommandID != "" {
		t.Fatal("a provider refund decoded as manual")
	}
}

func TestDispatch_DecodesManualRefunded(t *testing.T) {
	r := &recorder{}
	cmd := uuid.NewString()
	p := map[string]any{
		"id": "i-1", "intent_id": "i-1", "provider": "razorpay", "provider_refund_id": "manual:" + cmd,
		"amount_minor": 25000, "status": "refunded", "reference_type": "food_order", "reference_id": "o-1",
		"command_id": cmd, "manual": true,
	}
	if err := Dispatch(context.Background(), envelope(t, TypeRefunded, p), r); err != nil {
		t.Fatal(err)
	}
	if len(r.refunded) != 1 {
		t.Fatalf("refunded calls = %d", len(r.refunded))
	}
	got := r.refunded[0]
	if !got.Manual || got.CommandID != cmd || got.ProviderRefundID != "manual:"+cmd || got.AmountMinor != 25000 || got.Status != "refunded" {
		t.Fatalf("manual refunded = %+v", got)
	}

	// intent_id alone still names the intent.
	if (Refunded{IntentID: "i-2"}).Intent() != "i-2" || (Refunded{ID: "i-1", IntentID: "i-2"}).Intent() != "i-1" {
		t.Fatal("Intent() does not prefer id, then intent_id")
	}
}

func TestDispatch_DecodesRefundFailed(t *testing.T) {
	r := &recorder{}
	p := map[string]any{
		"id": "i-1", "intent_id": "i-1", "command_id": "c-1", "reference_type": "order", "reference_id": "o-1",
		"provider": "razorpay", "amount_minor": 700, "currency": "INR", "reason_code": "provider_rejected",
		"reason": "status 400: BAD_REQUEST_ERROR", "status": "needs_attention",
	}
	if err := Dispatch(context.Background(), envelope(t, TypeRefundFailed, p), r); err != nil {
		t.Fatal(err)
	}
	want := RefundFailed{ID: "i-1", IntentID: "i-1", CommandID: "c-1", ReferenceType: "order", ReferenceID: "o-1",
		Provider: "razorpay", AmountMinor: 700, Currency: "INR", ReasonCode: "provider_rejected",
		Reason: "status 400: BAD_REQUEST_ERROR", Status: "needs_attention"}
	if len(r.refundFailed) != 1 || r.refundFailed[0] != want {
		t.Fatalf("refund_failed = %+v", r.refundFailed)
	}
}

func TestDispatch_IgnoresUnknownTypes(t *testing.T) {
	r := &recorder{err: errors.New("must not be called")}
	for _, typ := range []string{"payment.status_changed", "post.created", "", "PAYMENT.SUCCEEDED"} {
		// Even an unparseable payload of an unknown type is not an error.
		if err := Dispatch(context.Background(), envelope(t, typ, []byte("{not json")), r); err != nil {
			t.Fatalf("type %q: err = %v", typ, err)
		}
	}
	if err := Dispatch(context.Background(), nil, r); err != nil {
		t.Fatalf("nil envelope: %v", err)
	}
	if r.total() != 0 {
		t.Fatalf("handler called %d times for unknown types", r.total())
	}
}

func TestDispatch_OnlyIgnoresExcludedTypes(t *testing.T) {
	r := &recorder{}
	only := Only(TypeSucceeded, TypeFailed, TypeRefunded)
	// A malformed refund_failed is ignored, not reported, when excluded.
	if err := Dispatch(context.Background(), envelope(t, TypeRefundFailed, []byte("{not json")), r, only); err != nil {
		t.Fatalf("excluded type: err = %v", err)
	}
	if err := Dispatch(context.Background(), envelope(t, TypeSucceeded, intentRow("succeeded")), r, only); err != nil {
		t.Fatal(err)
	}
	if r.total() != 1 || len(r.succeeded) != 1 {
		t.Fatalf("calls = %+v", r)
	}
}

func TestDispatch_MalformedIsReportedAndNothingRuns(t *testing.T) {
	r := &recorder{}
	for _, p := range [][]byte{[]byte("{not json"), []byte(`{"amount_minor": 250.5}`), []byte(`{"manual": "yes"}`)} {
		typ := TypeSucceeded
		if string(p) == `{"manual": "yes"}` {
			typ = TypeRefunded
		}
		err := Dispatch(context.Background(), envelope(t, typ, p), r)
		if !errors.Is(err, ErrMalformed) {
			t.Fatalf("payload %s: err = %v, want ErrMalformed", p, err)
		}
	}
	if r.total() != 0 {
		t.Fatal("handler ran for a malformed payload")
	}
}

func TestDispatch_ReturnsHandlerError(t *testing.T) {
	boom := errors.New("store down")
	r := &recorder{err: boom}
	if err := Dispatch(context.Background(), envelope(t, TypeSucceeded, intentRow("succeeded")), r); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want the handler's", err)
	}
}

func TestApplicationID_DecodedWhenPresentAndOptional(t *testing.T) {
	r := &recorder{}
	// An event without application_id (from before payments-service stored it)
	// decodes as empty.
	if err := Dispatch(context.Background(), envelope(t, TypeSucceeded, intentRow("succeeded")), r); err != nil {
		t.Fatal(err)
	}
	if r.succeeded[0].ApplicationID != "" {
		t.Fatalf("absent application_id decoded as %q", r.succeeded[0].ApplicationID)
	}
	// payments-service stamps it on every type, and every type decodes it.
	for _, typ := range []string{TypeSucceeded, TypeFailed, TypeRefunded, TypeRefundFailed} {
		if err := Dispatch(context.Background(), envelope(t, typ, map[string]any{"id": "x", "application_id": "feast"}), r); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
	if r.succeeded[1].ApplicationID != "feast" || r.failed[0].ApplicationID != "feast" ||
		r.refunded[0].ApplicationID != "feast" || r.refundFailed[0].ApplicationID != "feast" {
		t.Fatalf("application_id not decoded on every type: %+v", r)
	}
}

func TestForApplication_IgnoresOnlyOtherStampedApplications(t *testing.T) {
	for _, typ := range []string{TypeSucceeded, TypeFailed, TypeRefunded, TypeRefundFailed} {
		r := &recorder{}
		feast := ForApplication("feast")
		// Unstamped (every event today): delivered.
		if err := Dispatch(context.Background(), envelope(t, typ, map[string]any{"id": "a"}), r, feast); err != nil {
			t.Fatal(err)
		}
		// Stamped with this application: delivered.
		if err := Dispatch(context.Background(), envelope(t, typ, map[string]any{"id": "b", "application_id": "feast"}), r, feast); err != nil {
			t.Fatal(err)
		}
		// Stamped with another application: ignored, no error.
		if err := Dispatch(context.Background(), envelope(t, typ, map[string]any{"id": "c", "application_id": "mstore"}), r, feast); err != nil {
			t.Fatal(err)
		}
		if r.total() != 2 {
			t.Fatalf("%s: delivered %d, want 2 (unstamped + own)", typ, r.total())
		}
		// Off by default: without the option another application's event is delivered.
		if err := Dispatch(context.Background(), envelope(t, typ, map[string]any{"id": "d", "application_id": "mstore"}), r); err != nil {
			t.Fatal(err)
		}
		if r.total() != 3 {
			t.Fatalf("%s: filtering applied without ForApplication", typ)
		}
	}
	if !BelongsToApplication("", "feast") || !BelongsToApplication("feast", "feast") || BelongsToApplication("mstore", "feast") {
		t.Fatal("BelongsToApplication rule wrong")
	}
}

func TestNopHandler_DefaultsDoNothing(t *testing.T) {
	var h Handler = struct{ NopHandler }{}
	for _, typ := range []string{TypeSucceeded, TypeFailed, TypeRefunded, TypeRefundFailed} {
		if err := Dispatch(context.Background(), envelope(t, typ, map[string]any{"id": "x"}), h); err != nil {
			t.Fatalf("%s: %v", typ, err)
		}
	}
}
