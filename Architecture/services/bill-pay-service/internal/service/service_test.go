package service

import "testing"

func TestNormalisePhone_Service(t *testing.T) {
	cases := map[string]string{
		"":                "",
		"9876543210":      "9876543210",
		"+919876543210":   "9876543210",
		"09876543210":     "9876543210",
		"00919876543210":  "9876543210",
		"+91 98765 43210": "9876543210",
		"+91-98765-43210": "9876543210",
	}
	for in, want := range cases {
		if got := normalisePhone(in); got != want {
			t.Errorf("normalisePhone(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIndianMobileRE(t *testing.T) {
	good := []string{"9876543210", "+919876543210", "08123456789", "7234567890"}
	bad := []string{"", "12345", "5876543210", "98765432101"}
	for _, g := range good {
		if !indianMobileRE.MatchString(g) {
			t.Errorf("expected %q to match", g)
		}
	}
	for _, b := range bad {
		if indianMobileRE.MatchString(b) {
			t.Errorf("expected %q to NOT match", b)
		}
	}
}

func TestScheduledIdempotencyKey_Deterministic(t *testing.T) {
	// Re-runs of the cron on the same date MUST produce the same key.
	id := mustUUID(t, "11111111-1111-4111-8111-111111111111")
	day := mustDate(t, "2026-04-30")
	a := scheduledIdempotencyKey(id, day)
	b := scheduledIdempotencyKey(id, day)
	if a != b {
		t.Fatalf("expected deterministic key; got %q vs %q", a, b)
	}
	if len(a) < 10 {
		t.Fatalf("key looks too short: %q", a)
	}
}
