package fieldcrypt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

const (
	scopeText  Scope = "chat.message.text"
	scopeExtra Scope = "chat.message.extra"
)

func testCipher(t *testing.T) (*Cipher, *VersionedRing) {
	t.Helper()
	ring, err := NewVersionedRing(map[uint32][]byte{1: testKey(1)})
	if err != nil {
		t.Fatal(err)
	}
	c, err := New(ring)
	if err != nil {
		t.Fatal(err)
	}
	return c, ring
}

func TestCipherStringRoundTrip(t *testing.T) {
	ctx := context.Background()
	c, _ := testCipher(t)
	row := Bind("conv-1", "msg-1")
	for _, pt := range []string{"hi", "नमस्ते 👋🏽 family", strings.Repeat("చ", 2000)} {
		sealed, err := c.SealString(ctx, scopeText, row, pt)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(sealed, Prefix) || !IsSealed(sealed) {
			t.Fatalf("sealed value %q is not marked", sealed[:12])
		}
		if strings.Contains(sealed, pt[:2]) && len(pt) > 2 {
			// Base64 of a GCM ciphertext containing a plaintext run is
			// practically impossible; this guards a passthrough bug.
			t.Fatal("sealed value contains plaintext")
		}
		got, err := c.OpenString(ctx, scopeText, row, sealed)
		if err != nil || got != pt {
			t.Fatalf("OpenString = %q, %v", got, err)
		}
	}
}

func TestCipherEmptyStaysEmpty(t *testing.T) {
	ctx := context.Background()
	c, _ := testCipher(t)
	if got, err := c.SealString(ctx, scopeText, Bind("a"), ""); err != nil || got != "" {
		t.Fatalf("SealString(\"\") = %q, %v", got, err)
	}
	if got, err := c.SealJSON(ctx, scopeExtra, Bind("a"), nil); err != nil || got != nil {
		t.Fatalf("SealJSON(nil) = %q, %v", got, err)
	}
}

// The AAD binding: a ciphertext copied into another row (another message, the
// same message under another column) does not open.
func TestCipherBindingRefusesAnotherRow(t *testing.T) {
	ctx := context.Background()
	c, _ := testCipher(t)
	sealed, err := c.SealString(ctx, scopeText, Bind("conv-1", "msg-1"), "secret plan")
	if err != nil {
		t.Fatal(err)
	}
	for name, try := range map[string]func() (string, error){
		"other_message":      func() (string, error) { return c.OpenString(ctx, scopeText, Bind("conv-1", "msg-2"), sealed) },
		"other_conversation": func() (string, error) { return c.OpenString(ctx, scopeText, Bind("conv-2", "msg-1"), sealed) },
		"other_column":       func() (string, error) { return c.OpenString(ctx, scopeExtra, Bind("conv-1", "msg-1"), sealed) },
		"unbound":            func() (string, error) { return c.OpenString(ctx, scopeText, nil, sealed) },
		"split_shift":        func() (string, error) { return c.OpenString(ctx, scopeText, Bind("conv-1m", "sg-1"), sealed) },
	} {
		t.Run(name, func(t *testing.T) {
			got, err := try()
			if !errors.Is(err, ErrBadCiphertext) || got != "" {
				t.Fatalf("moved ciphertext opened: %q, %v", got, err)
			}
		})
	}
}

// Dual read: rows written before encryption carry no prefix and read back as
// stored, including values that merely look similar.
func TestCipherLegacyPlaintextReadsThrough(t *testing.T) {
	ctx := context.Background()
	c, _ := testCipher(t)
	for _, legacy := range []string{
		"", "hello", "cfe1:", "cfe1:hello there", "cfe1:" + strings.Repeat("A", 20),
		"cfe1:" + base64.RawURLEncoding.EncodeToString(make([]byte, 40)), // version 0
		"CFE1:" + strings.Repeat("A", 60), "{\"k\":1}",
	} {
		got, err := c.OpenString(ctx, scopeText, Bind("c", "m"), legacy)
		if err != nil || got != legacy {
			t.Fatalf("legacy %q read as %q, %v", legacy, got, err)
		}
	}
	for _, legacy := range [][]byte{nil, []byte(`{"voice":{"ms":1200}}`), []byte(`"plain string"`), []byte(`null`)} {
		got, err := c.OpenJSON(ctx, scopeExtra, Bind("c", "m"), legacy)
		if err != nil || !bytes.Equal(got, legacy) {
			t.Fatalf("legacy JSON %q read as %q, %v", legacy, got, err)
		}
	}
}

func TestCipherJSONRoundTripIsValidJSON(t *testing.T) {
	ctx := context.Background()
	c, _ := testCipher(t)
	doc := []byte(`{"text":"meet at 6","preview":"meet"}`)
	sealed, err := c.SealJSON(ctx, scopeExtra, Bind("k"), doc)
	if err != nil {
		t.Fatal(err)
	}
	var s string
	if err := json.Unmarshal(sealed, &s); err != nil || !IsSealed(s) || !IsSealedJSON(sealed) {
		t.Fatalf("sealed JSON %q is not a JSON string holding a sealed value: %v", sealed, err)
	}
	if bytes.Contains(sealed, []byte("meet")) {
		t.Fatal("sealed JSON contains plaintext")
	}
	got, err := c.OpenJSON(ctx, scopeExtra, Bind("k"), sealed)
	if err != nil || !bytes.Equal(got, doc) {
		t.Fatalf("OpenJSON = %q, %v", got, err)
	}
	// JSONB may hand the string back with surrounding whitespace.
	if got, err := c.OpenJSON(ctx, scopeExtra, Bind("k"), append([]byte(" "), sealed...)); err != nil || !bytes.Equal(got, doc) {
		t.Fatalf("OpenJSON(padded) = %q, %v", got, err)
	}
}

func TestPlaintextCipherWritesClearAndRefusesSealed(t *testing.T) {
	ctx := context.Background()
	sealer, _ := testCipher(t)
	sealed, _ := sealer.SealString(ctx, scopeText, Bind("c"), "x")
	for name, c := range map[string]*Cipher{"plaintext": Plaintext(), "nil": nil} {
		t.Run(name, func(t *testing.T) {
			if c.Enabled() {
				t.Fatal("Enabled() on a ring-less cipher")
			}
			if got, err := c.SealString(ctx, scopeText, Bind("c"), "hello"); err != nil || got != "hello" {
				t.Fatalf("SealString = %q, %v", got, err)
			}
			if got, err := c.OpenString(ctx, scopeText, Bind("c"), "hello"); err != nil || got != "hello" {
				t.Fatalf("OpenString(plain) = %q, %v", got, err)
			}
			if got, err := c.OpenString(ctx, scopeText, Bind("c"), sealed); !errors.Is(err, ErrPlaintextMode) || got != "" {
				t.Fatalf("OpenString(sealed) = %q, %v; want ErrPlaintextMode", got, err)
			}
		})
	}
	if _, err := New(nil); !errors.Is(err, ErrNoKeyRing) {
		t.Fatalf("New(nil) = %v", err)
	}
}

func TestCipherRotationKeepsOldValuesReadable(t *testing.T) {
	ctx := context.Background()
	ring1, _ := ParseKeyList("v1:" + base64.StdEncoding.EncodeToString(testKey(1)))
	c1, _ := New(ring1)
	old, _ := c1.SealString(ctx, scopeText, Bind("r"), "before")
	ring2, err := ParseKeyList("v1:" + base64.StdEncoding.EncodeToString(testKey(1)) + ", v2:" + base64.RawURLEncoding.EncodeToString(testKey(2)))
	if err != nil {
		t.Fatal(err)
	}
	c2, _ := New(ring2)
	fresh, _ := c2.SealString(ctx, scopeText, Bind("r"), "after")
	if v, ok := SealedKeyVersion(old); !ok || v != 1 {
		t.Fatalf("old version = %d", v)
	}
	if v, ok := SealedKeyVersion(fresh); !ok || v != 2 {
		t.Fatalf("new value sealed under v%d, want the newest (v2)", v)
	}
	for sealed, want := range map[string]string{old: "before", fresh: "after"} {
		if got, err := c2.OpenString(ctx, scopeText, Bind("r"), sealed); err != nil || got != want {
			t.Fatalf("OpenString = %q, %v", got, err)
		}
	}
	// A ring without v2 cannot open the new value and says which version.
	if _, err := c1.OpenString(ctx, scopeText, Bind("r"), fresh); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("old ring on new value: %v", err)
	}
}
