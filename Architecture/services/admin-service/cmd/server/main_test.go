package main

import (
	"strings"
	"testing"
)

func TestStaleRefundThresholdWarning(t *testing.T) {
	for _, v := range []string{"", "   "} {
		if msg, stale := staleRefundThresholdWarning(func(string) string { return v }); stale || msg != "" {
			t.Fatalf("%q: warned %q", v, msg)
		}
	}
	// Any value, valid or not, is a warning and never refuses boot.
	for _, v := range []string{"500000", " 1 ", "0", "abc"} {
		msg, stale := staleRefundThresholdWarning(func(string) string { return v })
		if !stale || !strings.Contains(msg, "ADMIN_REFUND_TWO_PERSON_THRESHOLD_PAISE") || !strings.Contains(msg, "no longer read") {
			t.Fatalf("%q: stale=%v %q", v, stale, msg)
		}
	}
}
