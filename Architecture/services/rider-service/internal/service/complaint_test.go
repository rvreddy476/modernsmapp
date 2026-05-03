package service

import (
	"testing"

	"github.com/atpost/rider-service/internal/store"
)

// TestComplaintCategory_Validation — store.IsValidComplaintCategory matches
// the spec category set; everything else rejected.
func TestComplaintCategory_Validation(t *testing.T) {
	valid := []string{"driver_behavior", "vehicle_condition", "route_deviation", "fare_dispute", "safety", "other"}
	for _, v := range valid {
		if !store.IsValidComplaintCategory(v) {
			t.Errorf("expected %q to be valid", v)
		}
	}
	invalid := []string{"", "DRIVER_BEHAVIOR", "spam", "abuse"}
	for _, v := range invalid {
		if store.IsValidComplaintCategory(v) {
			t.Errorf("expected %q to be invalid", v)
		}
	}
}

// TestComplaintStatus_Validation — store.IsValidComplaintStatus matches the
// CHECK constraint set.
func TestComplaintStatus_Validation(t *testing.T) {
	valid := []string{"open", "under_review", "resolved", "dismissed"}
	for _, v := range valid {
		if !store.IsValidComplaintStatus(v) {
			t.Errorf("expected status %q to be valid", v)
		}
	}
	invalid := []string{"", "OPEN", "closed", "in_progress"}
	for _, v := range invalid {
		if store.IsValidComplaintStatus(v) {
			t.Errorf("expected status %q to be invalid", v)
		}
	}
}

// TestSafetyKindValidation — store kind whitelist matches the spec.
func TestSafetyKindValidation(t *testing.T) {
	valid := []string{"sos_triggered", "route_anomaly", "partner_no_show", "long_idle_in_progress"}
	for _, v := range valid {
		if !store.IsValidSafetyKind(v) {
			t.Errorf("expected kind %q to be valid", v)
		}
	}
	if store.IsValidSafetyKind("foo") {
		t.Error("expected kind foo to be invalid")
	}
}

// TestSafetySeverityValidation — store severity whitelist matches CHECK.
func TestSafetySeverityValidation(t *testing.T) {
	valid := []string{"low", "medium", "high", "critical"}
	for _, v := range valid {
		if !store.IsValidSafetySeverity(v) {
			t.Errorf("expected severity %q to be valid", v)
		}
	}
	if store.IsValidSafetySeverity("urgent") {
		t.Error("expected severity 'urgent' to be invalid")
	}
}
