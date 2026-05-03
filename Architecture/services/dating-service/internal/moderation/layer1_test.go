// Layer 1 regex scanner tests.
//
// SHADOW MODE FOR v1 (CRITICAL RULES #5):
// These tests verify the matcher itself. The shadow-mode contract that
// the *service* layer overwrites action_taken to "shadow" lives in
// service/moderation_test.go.
package moderation

import (
	"strings"
	"testing"
)

func TestScanMessage_DetectsPhoneNumbers(t *testing.T) {
	cases := []string{
		"call me at 9876543210",
		"my number is +91 98765 43210",
		"reach out: (415) 555-1234",
	}
	for _, msg := range cases {
		r := ScanMessage(msg)
		if !containsString(r.Patterns, PatternPhone) {
			t.Errorf("expected phone match for %q, got %v", msg, r.Patterns)
		}
		if r.Confidence < 0.5 {
			t.Errorf("expected confidence >= 0.5 for %q, got %f", msg, r.Confidence)
		}
	}
}

func TestScanMessage_DetectsEmail(t *testing.T) {
	r := ScanMessage("dm me at hello@example.com")
	if !containsString(r.Patterns, PatternEmail) {
		t.Fatalf("expected email match, got %v", r.Patterns)
	}
}

func TestScanMessage_DetectsMoneyRequest(t *testing.T) {
	cases := []string{
		"can you transfer money to me?",
		"send me money via paytm",
		"i need a UPI",
		"paise bhejo na yaar",
	}
	for _, msg := range cases {
		r := ScanMessage(msg)
		if !containsString(r.Patterns, PatternMoneyRequest) {
			t.Errorf("expected money_request for %q, got %v", msg, r.Patterns)
		}
		if r.Confidence < 0.5 {
			t.Errorf("expected confidence >= 0.5 for %q, got %f", msg, r.Confidence)
		}
	}
}

func TestScanMessage_DetectsExternalURLs(t *testing.T) {
	r := ScanMessage("visit https://shady.example/promo for free coins")
	if !containsString(r.Patterns, PatternURL) {
		t.Fatalf("expected url match, got %v", r.Patterns)
	}
}

func TestScanMessage_WhitelistsAtPostDomains(t *testing.T) {
	r := ScanMessage("my profile https://atpost.com/u/me")
	if containsString(r.Patterns, PatternURL) {
		t.Fatalf("expected atpost.com to be whitelisted, got hit: %v", r.Patterns)
	}
}

func TestScanMessage_ConfidenceBuckets(t *testing.T) {
	// Empty → ok.
	if r := ScanMessage(""); r.ActionTaken != "ok" {
		t.Fatalf("empty msg should be ok, got %s", r.ActionTaken)
	}
	// Strong abuse → block.
	if r := ScanMessage("you are a slut"); r.ActionTaken != "block" {
		t.Fatalf("strong abuse should be block, got %s (conf=%f)", r.ActionTaken, r.Confidence)
	}
	// Mild ambiguity → warn (single phone match weight 0.6 → warn).
	if r := ScanMessage("call me 9876543210"); r.ActionTaken != "warn" {
		t.Fatalf("phone-only should warn, got %s (conf=%f)", r.ActionTaken, r.Confidence)
	}
}

func TestScanMessage_ConfidenceCappedAtOne(t *testing.T) {
	// Many hits → confidence stays <= 1.
	bigMsg := "send me money via paytm 9876543210 hello@x.com https://shady.example slut"
	r := ScanMessage(bigMsg)
	if r.Confidence > 1.0 {
		t.Fatalf("confidence must be <= 1.0, got %f", r.Confidence)
	}
}

func TestScanMessage_ShadowModeInvariant(t *testing.T) {
	// The matcher itself returns "ok"|"warn"|"block" — never "shadow".
	// Shadow rewriting happens in the service layer. This test guards
	// against an accidental change to that contract.
	r := ScanMessage("send me money")
	if r.ActionTaken == "shadow" {
		t.Fatalf("matcher must not return shadow; that is service-layer responsibility")
	}
}

func TestScanMessage_SuggestionForMoneyRequest(t *testing.T) {
	r := ScanMessage("send me money plz")
	if !strings.Contains(strings.ToLower(r.Suggestion), "money") {
		t.Fatalf("expected money-themed suggestion, got %q", r.Suggestion)
	}
}

func containsString(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
