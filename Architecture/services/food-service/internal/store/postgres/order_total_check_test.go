package postgres

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/uuid"
)

func TestCheckExpectedTotal(t *testing.T) {
	shown := int64(45600)
	if err := CheckExpectedTotal(nil, 45600); err != nil {
		t.Fatalf("no expectation refused: %v", err)
	}
	if err := CheckExpectedTotal(&shown, 45600); err != nil {
		t.Fatalf("matching total refused: %v", err)
	}
	for _, server := range []int64{45601, 45599, 0} {
		err := CheckExpectedTotal(&shown, server)
		var changed *OrderTotalChangedError
		if !errors.Is(err, ErrOrderTotalChanged) || !errors.As(err, &changed) {
			t.Fatalf("server %d: want OrderTotalChangedError, got %v", server, err)
		}
		if changed.ServerTotalPaise != server || changed.ExpectedTotalPaise != shown {
			t.Fatalf("server %d: carried %+v", server, changed)
		}
	}
}

// An older client sends no expectation; its checkout idempotency hash must
// not change, or a resend across the deploy would read as another request.
func TestExpectedTotalLeavesOldIdempotencyHashAlone(t *testing.T) {
	in := PlaceOrderInput{AddressID: uuid.MustParse("0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0099"), PaymentMethod: "ONLINE",
		PaymentInstrument: "upi", CustomerInstruction: "Ring twice"}
	raw, _ := json.Marshal(in)
	const before = `{"AddressID":"0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0099","PaymentMethod":"ONLINE","PaymentInstrument":"upi","CouponCode":"","CustomerInstruction":"Ring twice"}`
	if string(raw) != before {
		t.Fatalf("hash input changed for a client without expected_total_paise:\n got %s\nwant %s", raw, before)
	}
	shown := int64(45600)
	in.ExpectedTotalPaise = &shown
	if checkoutRequestHash(in) == checkoutRequestHash(PlaceOrderInput{AddressID: in.AddressID, PaymentMethod: "ONLINE", PaymentInstrument: "upi", CustomerInstruction: "Ring twice"}) {
		t.Fatal("the expected total is not bound to the idempotency key")
	}
}
