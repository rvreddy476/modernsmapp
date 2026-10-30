//go:build integration

package http

// Audit 2026-10-30 M5: after payment the buyer edits the saved address the
// order points at. The courier is still booked to the address the order was
// quoted and paid for, its sealed snapshot.

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/store/postgres"
)

func TestShipmentIsBookedToTheOrderSnapshotNotTheEditedAddress(t *testing.T) {
	ctx := context.Background()
	var got courier.ShipmentRequest
	r, svc := cutoverEngine(t, pii.ModeCiphertext,
		capturingCourier{bookingCourier: bookingCourier{stubCourier{chargeMinor: 4900}}, got: &got})
	f := seedOwnOrder(t, 88000, 4900, 14172)
	addrID := sealedAddressFor(t, svc, f.buyerUserID)

	// The snapshot checkout sealed: the Bengaluru address the order was paid for.
	cipher, err := pii.New(devKeyProvider{}, []byte("cutover-read-test-salt"))
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := json.Marshal(pii.Address{
		ContactName: "Asha Rao", Phone: "9876543210", AddressLine1: "12 Lavelle Road", AddressLine2: "Flat 4B",
		City: "Bengaluru", State: "KA", PostalCode: "560002", Country: "IN",
	})
	sealed, ver, err := cipher.Seal(ctx, pii.ScopeOrderSnapshot, string(blob))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := edgePool.Exec(ctx,
		`UPDATE orders SET delivery_address_id=$2, delivery_address_snapshot_enc=$3, snapshot_key_version=$4 WHERE id=$1`,
		f.orderID, addrID, sealed, ver); err != nil {
		t.Fatal(err)
	}

	// After payment, the buyer edits that saved address to Assam.
	if err := svc.UpdateAddress(ctx, addrID, f.buyerUserID, &postgres.CustomerAddress{
		UserID: f.buyerUserID, Label: "Home", AddressType: "home",
		ContactName: "Someone Else", Phone: "9000000001", AddressLine1: "1 Far Road",
		City: "Guwahati", State: "AS", PostalCode: "781001", Country: "IN",
	}); err != nil {
		t.Fatalf("edit address: %v", err)
	}

	w := call(t, r, http.MethodPost, "/v1/commerce/orders/"+f.orderID.String()+"/shipment", f.sellerUserID, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("booking = %d\n%s", w.Code, w.Body.String())
	}
	d := got.DropAddress
	if d.Postal != "560002" || d.State != "KA" || d.Line1 != "12 Lavelle Road" || d.Name != "Asha Rao" {
		t.Fatalf("the courier was booked to %+v; want the order's snapshot (Bengaluru), not the edited saved address", d)
	}
}
