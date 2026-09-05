//go:build integration

package postgres

// The seller's pickup address — the origin of every shipment.
//
// Nothing in production ever wrote `seller_addresses`. `POST /sellers/onboard`
// stores only the flat `state`/`city`/`postal_code` columns on `sellers`, and
// the onboarding wizard leaves `pickup_address_id` NULL. So `SellerPickupPin`'s
// "fall back to the seller's own postcode" branch was the ONLY live branch,
// reading an optional column — and a seller who skipped it had every delivery
// quoted from an empty origin.
//
//	COMMERCE_TEST_DSN=... go test -tags=integration ./internal/store/postgres/... -v

import (
	"context"
	"testing"

	"github.com/google/uuid"
)

func sealedFor(t *testing.T, line1 string) SealedAddressWrite {
	t.Helper()
	return SealedAddressWrite{
		ContactName:    []byte("enc:" + line1 + ":name"),
		Phone:          []byte("enc:" + line1 + ":phone"),
		AddressLine1:   []byte("enc:" + line1),
		AddressLine2:   nil,
		KeyVersion:     1,
		WritePlaintext: true,
	}
}

func newSeller(t *testing.T) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mustExec(t, `INSERT INTO sellers (id,user_id,store_name,slug,email,state)
	             VALUES ($1,$2,'Pickup Store',$3,'pickup@example.test','KA')`,
		id, uuid.New(), "pickup-"+id.String()[:8])
	return id
}

func addr(sellerID uuid.UUID, pin string) SellerAddress {
	return SellerAddress{
		SellerID:     sellerID,
		AddressType:  "pickup",
		ContactName:  "Warehouse Desk",
		Phone:        "9000000000",
		AddressLine1: "1 Warehouse Rd",
		City:         "Bengaluru",
		State:        "KA",
		PostalCode:   pin,
		Country:      "IN",
		IsDefault:    true,
	}
}

// The whole point: a saved pickup address becomes the courier's origin.
func TestSavedPickupAddressBecomesTheCourierOrigin(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID := newSeller(t)

	if err := store.UpsertSellerAddress(ctx, addr(sellerID, "560001"), sealedFor(t, "1 Warehouse Rd")); err != nil {
		t.Fatalf("UpsertSellerAddress: %v", err)
	}

	pin, err := store.SellerPickupPin(ctx, sellerID)
	if err != nil {
		t.Fatalf("SellerPickupPin: %v", err)
	}
	if pin != "560001" {
		t.Fatalf("pickup pin = %q, want 560001 — every shipment would originate from the "+
			"wrong place, and the delivery rate would be quoted for it", pin)
	}
}

// Sealed, always. A seller's name, phone and street are in the same estate the
// backfill covers and the gated scrub clears; an unsealed row is one the scrub
// destroys.
func TestSellerAddressIsRefusedWithoutCiphertext(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID := newSeller(t)

	if err := store.UpsertSellerAddress(ctx, addr(sellerID, "560001"), SealedAddressWrite{}); err == nil {
		t.Fatal("a seller address was stored with no ciphertext; the gated scrub would clear its " +
			"plaintext and leave no address at all")
	}
}

func TestSellerAddressWritesItsCiphertext(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID := newSeller(t)

	if err := store.UpsertSellerAddress(ctx, addr(sellerID, "560001"), sealedFor(t, "1 Warehouse Rd")); err != nil {
		t.Fatal(err)
	}
	var enc []byte
	var ver *int
	if err := testPool.QueryRow(ctx,
		`SELECT address_line_1_enc, pii_key_version FROM seller_addresses WHERE seller_id=$1`,
		sellerID).Scan(&enc, &ver); err != nil {
		t.Fatal(err)
	}
	if len(enc) == 0 || ver == nil || *ver <= 0 {
		t.Fatalf("ciphertext=%d bytes key_version=%v; the backfill would have to find this row",
			len(enc), ver)
	}
}

// A pickup address with no PIN cannot originate a shipment, and one with no
// state silently bills the wrong GST. Both are refused rather than stored.
func TestSellerAddressRequiresThePINAndTheState(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID := newSeller(t)

	noPin := addr(sellerID, "")
	if err := store.UpsertSellerAddress(ctx, noPin, sealedFor(t, "x")); err == nil {
		t.Fatal("a pickup address with no postal code was stored; the courier would be quoted " +
			"from an empty origin")
	}

	noState := addr(sellerID, "560001")
	noState.State = "  "
	if err := store.UpsertSellerAddress(ctx, noState, sealedFor(t, "x")); err == nil {
		t.Fatal("a pickup address with no state was stored; an interstate sale would be billed " +
			"CGST+SGST")
	}
}

// One address per type. Two 'pickup' rows would leave SellerPickupPin's
// ORDER BY deciding where a courier collects from.
func TestSavingTheSamePickupTypeReplacesRatherThanAccumulates(t *testing.T) {
	ctx := context.Background()
	store := New(testPool)
	sellerID := newSeller(t)

	if err := store.UpsertSellerAddress(ctx, addr(sellerID, "560001"), sealedFor(t, "first")); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertSellerAddress(ctx, addr(sellerID, "560068"), sealedFor(t, "second")); err != nil {
		t.Fatalf("editing a pickup address: %v", err)
	}

	var rows int
	if err := testPool.QueryRow(ctx,
		`SELECT count(*) FROM seller_addresses WHERE seller_id=$1 AND address_type='pickup'`,
		sellerID).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Fatalf("%d pickup addresses for one seller; a tie-break would choose the origin", rows)
	}

	pin, _ := store.SellerPickupPin(ctx, sellerID)
	if pin != "560068" {
		t.Fatalf("pickup pin = %q after an edit, want the new 560068", pin)
	}
}

// The database enforces it too, independently of the Go upsert.
func TestTheSchemaRefusesTwoAddressesOfOneType(t *testing.T) {
	ctx := context.Background()
	sellerID := newSeller(t)
	ins := `INSERT INTO seller_addresses
	          (seller_id,address_type,contact_name,phone,address_line_1,city,state,postal_code)
	        VALUES ($1,'pickup','A','9','1 Rd','Bengaluru','KA','560001')`
	if _, err := testPool.Exec(ctx, ins, sellerID); err != nil {
		t.Fatalf("first direct insert: %v", err)
	}
	if _, err := testPool.Exec(ctx, ins, sellerID); err == nil {
		t.Fatal("the schema accepted a second pickup address; the Go upsert is then the only " +
			"thing standing between a courier and the wrong origin")
	}
}
