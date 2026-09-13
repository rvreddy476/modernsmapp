//go:build integration

package postgres

// Checkout's address binding after the PII cutover moved the hash out of the
// transaction.
//
// The quote is bound to a hash of the DECRYPTED street, which the store can no
// longer compute (the plaintext columns are '' after the cutover), so the
// service supplies it. What must still hold is B7: the content the order ships
// to is the content that was quoted. The fingerprint of the row the service
// decrypted, re-checked against the row locked FOR SHARE, is what keeps it.
//
//	COMMERCE_TEST_DSN=.../commerce_it_test go test -tags=integration ./internal/store/postgres/ -run AddressBinding -v

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestAddressBindingRefusesAnAddressEditedAfterItWasRead(t *testing.T) {
	f := newFixture(t, 5, 50000, "18")
	f.addToCart(1, 50000)
	quoteID := f.quote(4000)

	// The service has read and decrypted the row (params captures its
	// fingerprint). Then the row is edited before the transaction runs.
	p := f.params(quoteID, "binding-edit-"+uuid.NewString())
	if _, err := testPool.Exec(context.Background(),
		`UPDATE customer_addresses SET address_line_1='99 Remote Rd' WHERE id=$1`, f.addressID); err != nil {
		t.Fatal(err)
	}

	_, err := f.store.Checkout(context.Background(), p)
	if !errors.Is(err, ErrQuoteMismatch) {
		t.Fatalf("checkout of an address edited after it was decrypted: err = %v, want ErrQuoteMismatch — "+
			"the order would ship to the new street at the old street's quote", err)
	}
	if f.orderCount() != 0 {
		t.Fatal("an order was written for an address that changed under the checkout")
	}
}

func TestAddressBindingRefusesAHashTheQuoteWasNotBoundTo(t *testing.T) {
	f := newFixture(t, 5, 50000, "18")
	f.addToCart(1, 50000)
	quoteID := f.quote(4000)

	p := f.params(quoteID, "binding-hash-"+uuid.NewString())
	p.AddressHash = HashAddress("1 Elsewhere", "", "Bengaluru", "KA", "560002")
	if _, err := f.store.Checkout(context.Background(), p); !errors.Is(err, ErrQuoteMismatch) {
		t.Fatalf("a checkout whose decrypted address hashes differently from the quote: err = %v, want ErrQuoteMismatch", err)
	}
}

func TestAddressBindingIsRequired(t *testing.T) {
	f := newFixture(t, 5, 50000, "18")
	f.addToCart(1, 50000)
	quoteID := f.quote(4000)

	for name, mutate := range map[string]func(*CheckoutParams){
		"no hash":        func(p *CheckoutParams) { p.AddressHash = "" },
		"no fingerprint": func(p *CheckoutParams) { p.AddressFingerprint = "" },
	} {
		p := f.params(quoteID, "binding-req-"+uuid.NewString())
		mutate(&p)
		_, err := f.store.Checkout(context.Background(), p)
		if err == nil || !strings.Contains(err.Error(), "address binding") {
			t.Fatalf("%s: err = %v, want a refusal naming the address binding", name, err)
		}
	}
	if f.orderCount() != 0 {
		t.Fatal("an order was written without an address binding")
	}
}
