package postgres

import (
	"fmt"
	"strings"
)

// Audit M6 (2026-10-30): an approved rider could PATCH their name, phone and
// vehicle number with no review, so customers saw (and called) a person and
// vehicle ops never checked. Like a restaurant's onboarding forms, which are
// editable only before review (ErrRestaurantNotDraft), a rider's reviewed
// identity is now frozen once review has passed: a change is refused with
// 409 FOOD_DELIVERY_PROFILE_LOCKED and goes through support. Email and city
// stay self-service.

// CodeDeliveryProfileLocked is the HTTP code for RiderIdentityLockedError.
const CodeDeliveryProfileLocked = "FOOD_DELIVERY_PROFILE_LOCKED"

// riderIdentityLockedStatuses are the statuses that have passed review.
// SUSPENDED is included: a suspended rider must not rewrite the identity a
// reinstatement would be judged on.
var riderIdentityLockedStatuses = map[string]bool{
	"APPROVED":  true,
	"ACTIVE":    true,
	"OFFLINE":   true,
	"SUSPENDED": true,
}

// riderIdentity is the reviewed part of a rider profile.
type riderIdentity struct {
	FullName      string
	Phone         string
	VehicleType   string
	VehicleNumber string
}

// RiderIdentityLockedError lists the reviewed fields a save tried to change.
type RiderIdentityLockedError struct {
	Status string
	Fields []string
}

func (e *RiderIdentityLockedError) Error() string {
	return fmt.Sprintf("a reviewed delivery profile (%s) cannot change %s; contact support to update it",
		e.Status, strings.Join(e.Fields, ", "))
}

// sameName compares names ignoring case and surrounding or repeated spaces.
func sameName(a, b string) bool {
	return strings.EqualFold(strings.Join(strings.Fields(a), " "), strings.Join(strings.Fields(b), " "))
}

// samePlate compares vehicle numbers ignoring case, spaces and hyphens
// ("KA01 AB-1234" is "KA01AB1234").
func samePlate(a, b string) bool {
	norm := func(s string) string {
		return strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(s))
	}
	return norm(a) == norm(b)
}

// checkRiderIdentityChange refuses a change to a reviewed rider's name,
// phone or vehicle. Before review (DRAFT, PENDING_REVIEW, REJECTED, CLOSED)
// every field is editable, as onboarding is.
func checkRiderIdentityChange(status string, current, next riderIdentity) error {
	if !riderIdentityLockedStatuses[status] {
		return nil
	}
	var changed []string
	if !sameName(current.FullName, next.FullName) {
		changed = append(changed, "full_name")
	}
	if strings.TrimSpace(current.Phone) != strings.TrimSpace(next.Phone) {
		changed = append(changed, "phone")
	}
	if !strings.EqualFold(strings.TrimSpace(current.VehicleType), strings.TrimSpace(next.VehicleType)) {
		changed = append(changed, "vehicle_type")
	}
	if !samePlate(current.VehicleNumber, next.VehicleNumber) {
		changed = append(changed, "vehicle_number")
	}
	if len(changed) == 0 {
		return nil
	}
	return &RiderIdentityLockedError{Status: status, Fields: changed}
}
