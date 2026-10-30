package service

// Audit 2026-10-30 M5: shipment booking, the invoice ship-to, the delivery
// partner's contact and the return pickup read the order's snapshot; the live
// address row only for an order that never had one.

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/google/uuid"
)

// editedLive is the saved address after the buyer edited it post-payment.
func editedLive(called *int) func(context.Context, *postgres.Order) (*pii.Address, error) {
	return func(context.Context, *postgres.Order) (*pii.Address, error) {
		*called++
		return &pii.Address{ContactName: "Edited", AddressLine1: "1 Far Road", City: "Guwahati", State: "AS", PostalCode: "781001", Country: "IN"}, nil
	}
}

func sealedSnapshot(t *testing.T, c *pii.Cipher, a pii.Address) []byte {
	t.Helper()
	blob, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	sealed, _, err := c.Seal(context.Background(), pii.ScopeOrderSnapshot, string(blob))
	if err != nil {
		t.Fatal(err)
	}
	return sealed
}

var paidFor = pii.Address{
	ContactName: "Asha Rao", Phone: "9876543210", AddressLine1: "12 Lavelle Road", AddressLine2: "Flat 4B",
	City: "Bengaluru", State: "KA", PostalCode: "560001", Country: "IN",
}

func TestOrderAddress_TheSnapshotWinsOverAnEditedLiveRow(t *testing.T) {
	c := addressTestCipher(t)
	for _, mode := range []pii.Mode{pii.ModeDual, pii.ModeCiphertext} {
		svc := (&Service{}).WithPII(c).WithPIICutover(mode)
		addrID := uuid.New()
		order := &postgres.Order{ID: uuid.New(), DeliveryAddressID: &addrID,
			DeliveryAddressSnapshotEnc: sealedSnapshot(t, c, paidFor)}
		calls := 0
		got, err := svc.resolveOrderAddress(context.Background(), order, editedLive(&calls))
		if err != nil {
			t.Fatal(err)
		}
		if calls != 0 {
			t.Fatalf("mode %v: the live row was read for an order with a snapshot", mode)
		}
		if *got != paidFor {
			t.Fatalf("mode %v: got %+v, want the paid-for address", mode, got)
		}
	}
}

func TestOrderAddress_AnUnreadableSnapshotIsAnErrorNotAFallback(t *testing.T) {
	c := addressTestCipher(t)
	svc := (&Service{}).WithPII(c).WithPIICutover(pii.ModeCiphertext)
	order := &postgres.Order{ID: uuid.New(), DeliveryAddressSnapshotEnc: []byte("not a sealed blob")}
	calls := 0
	if _, err := svc.resolveOrderAddress(context.Background(), order, editedLive(&calls)); err == nil {
		t.Fatal("an unopenable snapshot resolved")
	}
	if calls != 0 {
		t.Fatal("an unopenable snapshot fell back to the editable live row")
	}

	// No cipher configured: also an error, not the live row.
	order.DeliveryAddressSnapshotEnc = sealedSnapshot(t, c, paidFor)
	if _, err := (&Service{}).resolveOrderAddress(context.Background(), order, editedLive(&calls)); err == nil || calls != 0 {
		t.Fatalf("no cipher: err=%v live calls=%d", err, calls)
	}
}

func TestOrderAddress_PlaintextSnapshotOnlyDuringDualWrite(t *testing.T) {
	raw, _ := json.Marshal(paidFor)
	order := &postgres.Order{ID: uuid.New(), DeliveryAddressSnapshot: raw}

	calls := 0
	got, err := (&Service{}).WithPIICutover(pii.ModeDual).resolveOrderAddress(context.Background(), order, editedLive(&calls))
	if err != nil || calls != 0 || *got != paidFor {
		t.Fatalf("dual: got %+v err=%v live calls=%d", got, err, calls)
	}
	if _, err := (&Service{}).WithPIICutover(pii.ModeCiphertext).resolveOrderAddress(context.Background(), order, editedLive(&calls)); err == nil || calls != 0 {
		t.Fatalf("ciphertext mode: err=%v live calls=%d; an unsealed snapshot after cutover is a defect, not a fallback", err, calls)
	}
}

func TestOrderAddress_LegacyOrderWithoutASnapshotUsesTheLiveRow(t *testing.T) {
	c := addressTestCipher(t)
	svc := (&Service{}).WithPII(c).WithPIICutover(pii.ModeCiphertext)
	for name, order := range map[string]*postgres.Order{
		"no snapshot":                  {ID: uuid.New()},
		"plaintext without a street":   {ID: uuid.New(), DeliveryAddressSnapshot: []byte(`{"city":"Bengaluru","state":"KA"}`)},
		"sealed legacy without street": {ID: uuid.New(), DeliveryAddressSnapshotEnc: sealedSnapshot(t, c, pii.Address{City: "Bengaluru", State: "KA"})},
	} {
		calls := 0
		got, err := svc.resolveOrderAddress(context.Background(), order, editedLive(&calls))
		if err != nil || calls != 1 || got.AddressLine1 != "1 Far Road" {
			t.Fatalf("%s: got %+v err=%v live calls=%d", name, got, err, calls)
		}
	}

	// And with no address at all, the live reader's refusal comes through.
	order := &postgres.Order{ID: uuid.New()}
	_, err := svc.liveOrderAddress(context.Background(), order)
	if !errors.Is(err, errOrderHasNoAddress) {
		t.Fatalf("no address id: err = %v", err)
	}
}
