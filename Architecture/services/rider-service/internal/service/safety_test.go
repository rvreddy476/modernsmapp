package service

import (
	"strings"
	"testing"
)

// TestRedactPhone — last 4 digits visible, prefix masked, non-digits dropped.
func TestRedactPhone(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"+919876543210", "********3210"},
		{"9876543210", "******3210"},
		{"123", "123"},
		{"", ""},
		{"abc-9876", "9876"},
		{"+91 98765 43210", "********3210"},
	}
	for _, c := range cases {
		got := redactPhone(c.in)
		if got != c.out {
			t.Errorf("redactPhone(%q) = %q; want %q", c.in, got, c.out)
		}
	}
}

// TestFirstChunk — comma split keeps only the first chunk.
func TestFirstChunk(t *testing.T) {
	cases := []struct {
		in, out string
	}{
		{"123 MG Road, Bengaluru, KA", "123 MG Road"},
		{"Whitefield", "Whitefield"},
		{"", ""},
		{",foo", ""},
	}
	for _, c := range cases {
		got := firstChunk(c.in)
		if got != c.out {
			t.Errorf("firstChunk(%q) = %q; want %q", c.in, got, c.out)
		}
	}
}

// TestFirstName — first space-separated chunk.
func TestFirstName(t *testing.T) {
	cases := []struct{ in, out string }{
		{"Asha Rao", "Asha"},
		{"Asha", "Asha"},
		{"", ""},
		{"  Asha", ""},
	}
	for _, c := range cases {
		got := firstName(c.in)
		if got != c.out {
			t.Errorf("firstName(%q) = %q; want %q", c.in, got, c.out)
		}
	}
}

// TestLastN — keeps last n chars or whole string when shorter.
func TestLastN(t *testing.T) {
	cases := []struct {
		in   string
		n    int
		want string
	}{
		{"KA01AB1234", 4, "1234"},
		{"AB", 4, "AB"},
		{"", 4, ""},
		{"hello", 0, "hello"},
		{"hello", -1, "hello"},
	}
	for _, c := range cases {
		got := lastN(c.in, c.n)
		if got != c.want {
			t.Errorf("lastN(%q,%d) = %q; want %q", c.in, c.n, got, c.want)
		}
	}
}

// TestIsTerminalRideStatus — terminal vs non-terminal classification.
func TestIsTerminalRideStatus(t *testing.T) {
	terminal := []string{"completed", "expired", "failed", "cancelled_by_customer", "cancelled_by_partner", "cancelled_by_admin"}
	for _, s := range terminal {
		if !isTerminalRideStatus(s) {
			t.Errorf("isTerminalRideStatus(%q) = false; want true", s)
		}
	}
	nonTerminal := []string{"requested", "searching_partner", "in_progress", "partner_assigned", "arrived", "otp_verified"}
	for _, s := range nonTerminal {
		if isTerminalRideStatus(s) {
			t.Errorf("isTerminalRideStatus(%q) = true; want false", s)
		}
	}
}

// TestGenerateShareToken — hex output, exactly 32 chars, no two calls equal.
func TestGenerateShareToken(t *testing.T) {
	a, err := generateShareToken()
	if err != nil {
		t.Fatalf("generateShareToken: %v", err)
	}
	if len(a) != 32 {
		t.Errorf("token length = %d; want 32", len(a))
	}
	for _, ch := range a {
		if !strings.ContainsRune("0123456789abcdef", ch) {
			t.Errorf("token contains non-hex rune %q", ch)
		}
	}
	b, _ := generateShareToken()
	if a == b {
		t.Error("two calls produced identical tokens — randomness broken")
	}
}

// TestLooksLikeWKTPolygon — basic shape recognition.
func TestLooksLikeWKTPolygon(t *testing.T) {
	cases := []struct {
		in string
		ok bool
	}{
		{"POLYGON((77.45 12.83, 77.78 12.83, 77.78 13.14, 77.45 13.14, 77.45 12.83))", true},
		{"polygon((1 1,2 2,3 3,1 1))", true},
		{"POINT(1 1)", false},
		{"", false},
		{"POLYGON()", false},
	}
	for _, c := range cases {
		if got := looksLikeWKTPolygon(c.in); got != c.ok {
			t.Errorf("looksLikeWKTPolygon(%q) = %v; want %v", c.in, got, c.ok)
		}
	}
}
