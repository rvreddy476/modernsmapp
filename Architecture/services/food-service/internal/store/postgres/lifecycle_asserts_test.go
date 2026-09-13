package postgres

import (
	"errors"
	"testing"
)

// Sentinel assertions for the lifecycle tests. (Phase 1 of the TDD run used
// plain err != nil checks here, before the sentinels existed.)

func assertIs(t *testing.T, err, target error) {
	t.Helper()
	if !errors.Is(err, target) {
		t.Fatalf("want %v, got %v", target, err)
	}
}

func assertDeliveryOTPRequired(t *testing.T, err error) { t.Helper(); assertIs(t, err, ErrDeliveryOTPRequired) }
func assertDeliveryPartnerNotActive(t *testing.T, err error) {
	t.Helper()
	assertIs(t, err, ErrDeliveryPartnerNotActive)
}
func assertAddressOutOfRange(t *testing.T, err error)       { t.Helper(); assertIs(t, err, ErrAddressOutOfRange) }
func assertOutsideHours(t *testing.T, err error)            { t.Helper(); assertIs(t, err, ErrRestaurantOutsideHours) }
func assertAddressLocationRequired(t *testing.T, err error) { t.Helper(); assertIs(t, err, ErrAddressLocationRequired) }
func assertRestaurantLocationMissing(t *testing.T, err error) {
	t.Helper()
	assertIs(t, err, ErrRestaurantLocationMissing)
}
