//go:build integration

package http

// The address PII cutover, read side.
//
// Writers have sealed identifying address fields since B4, and in ciphertext
// mode (COMMERCE_PII_CUTOVER=ciphertext) they write the plaintext columns
// EMPTY. gated/1000 later clears whatever plaintext is left. So every reader
// that still selects only `contact_name`, `phone`, `address_line_1`, ... sees
// '' the moment the cutover flips — the address book shows nameless rows, a
// courier is booked to a blank street, and checkout refuses every quote
// because it re-hashes a blank street against the real one.
//
// Each test here builds a row the way the ciphertext-mode writer does
// (sealed, plaintext ''), then goes through the real route.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/http/ -run Cutover -v

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/atpost/commerce-service/internal/courier"
	"github.com/atpost/commerce-service/internal/pii"
	"github.com/atpost/commerce-service/internal/service"
	"github.com/atpost/commerce-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// capturingCourier books like bookingCourier and records what it was asked
// to deliver to.
type capturingCourier struct {
	bookingCourier
	got *courier.ShipmentRequest
}

func (c capturingCourier) CreateShipment(ctx context.Context, req courier.ShipmentRequest) (*courier.ShipmentResponse, error) {
	*c.got = req
	return c.bookingCourier.CreateShipment(ctx, req)
}

func cutoverEngine(t *testing.T, mode pii.Mode, p courier.Provider) (*gin.Engine, *service.Service) {
	t.Helper()
	cipher, err := pii.New(devKeyProvider{}, []byte("cutover-read-test-salt"))
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	svc := service.New(postgres.New(edgePool), nil, "").
		WithCourier(p).
		WithPII(cipher).
		WithPIICutover(mode)
	r := gin.New()
	r.Use(FenceMiddleware())
	h := New(svc)
	h.RegisterRoutes(r)
	h.RegisterP0Routes(r)
	return r, svc
}

func strp(s string) *string { return &s }

// sealedAddressFor stores an address through the service in CIPHERTEXT mode,
// and proves the precondition: nothing identifying is left in plaintext.
func sealedAddressFor(t *testing.T, svc *service.Service, userID uuid.UUID) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	addr := &postgres.CustomerAddress{
		UserID: userID, Label: "Home", AddressType: "home",
		ContactName: "Asha Rao", Phone: "9876543210",
		AddressLine1: "12 Lavelle Road", AddressLine2: strp("Flat 4B"), Landmark: strp("Opp. park"),
		City: "Bengaluru", State: "KA", PostalCode: "560002", Country: "IN",
	}
	if err := svc.AddAddress(ctx, addr); err != nil {
		t.Fatalf("AddAddress (ciphertext mode): %v", err)
	}
	var name, phone, line1 string
	var line2, landmark *string
	var enc []byte
	if err := edgePool.QueryRow(ctx, `
		SELECT contact_name, phone, address_line_1, address_line_2, landmark, address_line_1_enc
		  FROM customer_addresses WHERE id=$1`, addr.ID).
		Scan(&name, &phone, &line1, &line2, &landmark, &enc); err != nil {
		t.Fatal(err)
	}
	if name != "" || phone != "" || line1 != "" || line2 != nil || landmark != nil || len(enc) == 0 {
		t.Fatalf("precondition: a ciphertext-mode row must be sealed with empty plaintext; got "+
			"name=%q phone=%q line1=%q line2=%v landmark=%v enc=%d bytes", name, phone, line1, line2, landmark, len(enc))
	}
	return addr.ID
}

type addressBody struct {
	ID           string  `json:"id"`
	ContactName  string  `json:"contact_name"`
	Phone        string  `json:"phone"`
	AddressLine1 string  `json:"address_line_1"`
	AddressLine2 *string `json:"address_line_2"`
	Landmark     *string `json:"landmark"`
	City         string  `json:"city"`
	PostalCode   string  `json:"postal_code"`
}

// ─── The address book ───────────────────────────────────────────────────

func TestCutoverAddressBookOpensASealedRowWithEmptyPlaintext(t *testing.T) {
	r, svc := cutoverEngine(t, pii.ModeCiphertext, stubCourier{chargeMinor: 4000})
	userID := uuid.New()
	id := sealedAddressFor(t, svc, userID)

	w := call(t, r, http.MethodGet, "/v1/commerce/addresses", userID, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("GET /addresses = %d\n%s", w.Code, w.Body.String())
	}
	var env struct {
		Data []addressBody `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v\n%s", err, w.Body.String())
	}
	if len(env.Data) != 1 || env.Data[0].ID != id.String() {
		t.Fatalf("address book = %+v, want the one sealed address", env.Data)
	}
	a := env.Data[0]
	if a.ContactName != "Asha Rao" || a.Phone != "9876543210" || a.AddressLine1 != "12 Lavelle Road" ||
		a.AddressLine2 == nil || *a.AddressLine2 != "Flat 4B" || a.Landmark == nil || *a.Landmark != "Opp. park" {
		t.Fatalf("after the cutover the address book served %+v — the identifying fields were read "+
			"from plaintext columns that ciphertext mode writes empty", a)
	}
	if a.City != "Bengaluru" || a.PostalCode != "560002" {
		t.Fatalf("routing fields lost: %+v", a)
	}
	if bytesContain(w.Body.Bytes(), "_enc") {
		t.Fatalf("the response leaks ciphertext columns:\n%s", w.Body.String())
	}
}

// After cutover a row with no ciphertext is a defect the backfill missed. It
// must surface, not be served from plaintext; during dual-write it is served.
func TestCutoverAddressBookRefusesAnUnsealedRowOnlyAfterCutover(t *testing.T) {
	userID := uuid.New()
	if _, err := edgePool.Exec(context.Background(), `
		INSERT INTO customer_addresses (id,user_id,contact_name,phone,address_line_1,city,state,postal_code)
		VALUES ($1,$2,'Legacy Buyer','9111111111','5 Main St','Bengaluru','KA','560002')`,
		uuid.New(), userID); err != nil {
		t.Fatal(err)
	}

	cut, _ := cutoverEngine(t, pii.ModeCiphertext, stubCourier{chargeMinor: 4000})
	if w := call(t, cut, http.MethodGet, "/v1/commerce/addresses", userID, nil); w.Code == http.StatusOK {
		t.Fatalf("ciphertext mode served a row with no ciphertext from its plaintext; that hides "+
			"exactly the backfill gap the cutover exists to surface\n%s", w.Body.String())
	}

	dual, _ := cutoverEngine(t, pii.ModeDual, stubCourier{chargeMinor: 4000})
	w := call(t, dual, http.MethodGet, "/v1/commerce/addresses", userID, nil)
	if w.Code != http.StatusOK || !bytesContain(w.Body.Bytes(), "Legacy Buyer") {
		t.Fatalf("dual mode must still serve a legacy plaintext row: %d\n%s", w.Code, w.Body.String())
	}
}

// ─── Checkout honours its own quote ─────────────────────────────────────

func TestCutoverCheckoutAcceptsTheQuoteItIssuedForASealedAddress(t *testing.T) {
	const unitMinor, shippingMinor = 100000, 4000
	f := seedJourney(t, 5, unitMinor)
	r, svc := cutoverEngine(t, pii.ModeCiphertext, stubCourier{chargeMinor: shippingMinor})
	f.addressID = sealedAddressFor(t, svc, f.userID)

	qw := f.post(t, r, "/v1/commerce/checkout/quote", "", map[string]any{
		"address_id":     f.addressID.String(),
		"payment_method": "upi",
	})
	if qw.Code != http.StatusOK {
		t.Fatalf("quote returned %d: %s", qw.Code, qw.Body.String())
	}
	q := decode[quoteBody](t, qw)

	cw := f.post(t, r, "/v1/commerce/v2/orders/checkout", "cutover-"+uuid.NewString(), map[string]any{
		"address_id":           f.addressID.String(),
		"quote_id":             q.QuoteID,
		"payment_method":       "upi",
		"expected_total_minor": q.TotalMinor,
	})
	if cw.Code != http.StatusCreated {
		t.Fatalf("checkout returned %d for a quote the server issued a moment ago: %s\n\n"+
			"The quote bound a hash of the DECRYPTED street; a checkout that re-hashes the "+
			"plaintext column hashes '' after the cutover and refuses every order.",
			cw.Code, cw.Body.String())
	}
}

// ─── The courier is sent to the real address ────────────────────────────

func TestCutoverShipmentIsBookedToTheSealedDeliveryAddress(t *testing.T) {
	var got courier.ShipmentRequest
	r, svc := cutoverEngine(t, pii.ModeCiphertext,
		capturingCourier{bookingCourier: bookingCourier{stubCourier{chargeMinor: 4900}}, got: &got})
	f := seedOwnOrder(t, 88000, 4900, 14172)
	addrID := sealedAddressFor(t, svc, f.buyerUserID)
	if _, err := edgePool.Exec(context.Background(),
		`UPDATE orders SET delivery_address_id=$2 WHERE id=$1`, f.orderID, addrID); err != nil {
		t.Fatal(err)
	}

	w := call(t, r, http.MethodPost, "/v1/commerce/orders/"+f.orderID.String()+"/shipment", f.sellerUserID, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("booking = %d\n%s", w.Code, w.Body.String())
	}
	d := got.DropAddress
	if d.Name != "Asha Rao" || d.Phone != "9876543210" || d.Line1 != "12 Lavelle Road" || d.Line2 != "Flat 4B" {
		t.Fatalf("the courier was booked to %+v — a parcel with no recipient, phone or street", d)
	}
	if d.Postal != "560002" || d.City != "Bengaluru" {
		t.Fatalf("routing fields lost: %+v", d)
	}
}

func bytesContain(b []byte, s string) bool { return contains(string(b), s) }
