package postgres

import (
	"errors"
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/food-service/database"
	"github.com/atpost/food-service/internal/payments"
)

// An order placed with no payment method must be refused, never silently
// turned into cash on delivery.
func TestPlaceOrderPaymentState(t *testing.T) {
	cases := []struct {
		method, status, paymentStatus string
		ok                            bool
	}{
		{"ONLINE", "PAYMENT_PENDING", "PENDING", true},
		{"WALLET", "PAYMENT_PENDING", "PENDING", true},
		{"COD", "CONFIRMED", "NOT_REQUIRED", true},
		{"", "", "", false},
		{"cod", "", "", false},
		{"upi", "", "", false},
	}
	for _, tc := range cases {
		status, paymentStatus, err := placeOrderPaymentState(tc.method)
		if !tc.ok {
			if !errors.Is(err, payments.ErrPaymentMethodInvalid) {
				t.Fatalf("method %q: err = %v, want ErrPaymentMethodInvalid", tc.method, err)
			}
			continue
		}
		if err != nil || status != tc.status || paymentStatus != tc.paymentStatus {
			t.Fatalf("method %q: (%s, %s, %v), want (%s, %s)", tc.method, status, paymentStatus, err, tc.status, tc.paymentStatus)
		}
	}
}

// food's outbox and payment inbox live in the food schema. An unprefixed
// outbox_events in the shared app database collides with the platform's own
// declaration of that table.
func TestSetupSQLDeclaresFoodOutboxAndInbox(t *testing.T) {
	sql := database.SetupSQL
	for _, want := range []string{
		"CREATE TABLE IF NOT EXISTS food.outbox_events",
		"CREATE TABLE IF NOT EXISTS food.payment_event_inbox",
	} {
		if !strings.Contains(sql, want) {
			t.Fatalf("setup.sql is missing %q", want)
		}
	}
	if regexp.MustCompile(`(?i)CREATE TABLE IF NOT EXISTS\s+outbox_events\b`).MatchString(sql) {
		t.Fatal("setup.sql still declares an unprefixed outbox_events table")
	}
}
