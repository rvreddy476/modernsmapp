package propii

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/atpost/doorstep-service/internal/config"
)

func key(b byte) []byte { return bytes.Repeat([]byte{b}, 32) }

func TestSealing(t *testing.T) {
	ctx := context.Background()
	var none *Crypto
	if _, err := none.SealAccountNumber(ctx, "123456789"); !errors.Is(err, ErrNotConfigured) {
		t.Fatal("nil crypto sealed")
	}
	if c, err := New(ctx, nil); c != nil || err != nil {
		t.Fatal("no keys must be (nil, nil)")
	}
	c, err := New(ctx, []config.PIIKey{{Version: 1, Key: key(7)}, {Version: 2, Key: key(9)}})
	if err != nil {
		t.Fatal(err)
	}
	s, err := c.SealAccountNumber(ctx, "000123456789")
	if err != nil || s.KeyVersion != 2 || bytes.Contains(s.Blob, []byte("123456789")) {
		t.Fatalf("seal %v v%d", err, s.KeyVersion)
	}
	if got, err := c.OpenAccountNumber(ctx, s.Blob); err != nil || got != "000123456789" {
		t.Fatalf("open %q %v", got, err)
	}
	// Scope binding: an account blob does not open as a verifier.
	if _, err := c.OpenVerifier(ctx, s.Blob); err == nil {
		t.Fatal("cross-scope open succeeded")
	}
	v, _ := c.SealVerifier(ctx, "verifier")
	if got, err := c.OpenVerifier(ctx, v.Blob); err != nil || got != "verifier" {
		t.Fatalf("verifier %q %v", got, err)
	}
}
