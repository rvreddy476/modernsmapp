package realtime

import (
	"testing"
	"time"
)

func TestTokenRoundTrip(t *testing.T) {
	secret := []byte("test-secret")
	signer := NewTokenSigner(secret)
	verifier := NewTokenVerifier(secret)

	want := []string{"food.order.abc", "food.admin.*"}
	tok, err := signer.Sign("user-123", want)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	got, sub, err := verifier.Verify(tok)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if sub != "user-123" {
		t.Fatalf("subject: want user-123, got %q", sub)
	}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("topics mismatch: want %v, got %v", want, got)
	}
}

func TestTokenWrongSecret(t *testing.T) {
	signer := NewTokenSigner([]byte("aaa"))
	verifier := NewTokenVerifier([]byte("bbb"))
	tok, _ := signer.Sign("u", []string{"food.order.x"})
	if _, _, err := verifier.Verify(tok); err == nil {
		t.Fatal("expected verify failure for wrong secret")
	}
}

func TestTokenExpired(t *testing.T) {
	signer := NewTokenSigner([]byte("s")).WithTTL(-time.Minute)
	verifier := NewTokenVerifier([]byte("s"))
	tok, _ := signer.Sign("u", []string{"x"})
	if _, _, err := verifier.Verify(tok); err == nil {
		t.Fatal("expected expiry rejection")
	}
}

func TestTokenTamper(t *testing.T) {
	signer := NewTokenSigner([]byte("s"))
	verifier := NewTokenVerifier([]byte("s"))
	tok, _ := signer.Sign("u", []string{"food.order.x"})
	// flip a character in the payload half
	tampered := "Z" + tok[1:]
	if _, _, err := verifier.Verify(tampered); err == nil {
		t.Fatal("expected tamper rejection")
	}
}

func TestMatchTopic(t *testing.T) {
	allowed := []string{"food.order.123", "food.admin.*"}
	cases := []struct {
		topic string
		ok    bool
	}{
		{"food.order.123", true},
		{"food.order.999", false},
		{"food.admin.live_orders", true},
		{"food.admin.audit_log", true},
		{"rider.ride.x", false},
	}
	for _, c := range cases {
		if got := MatchTopic(allowed, c.topic); got != c.ok {
			t.Errorf("MatchTopic(%q): want %v, got %v", c.topic, c.ok, got)
		}
	}
}

func TestChannelRoundTrip(t *testing.T) {
	topic := "food.order.42"
	if TopicFromChannel(Channel(topic)) != topic {
		t.Fatalf("channel round-trip broken")
	}
}
