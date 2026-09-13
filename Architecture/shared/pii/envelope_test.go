package pii

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
)

const (
	scopeProfile  Scope = "profile"
	scopeSnapshot Scope = "order_snapshot"
	scopeTest     Scope = "test.payout_account"
)

func testKey(seed byte) []byte {
	k := make([]byte, DataKeySize)
	for i := range k {
		k[i] = seed + byte(i)*7 + 1
	}
	return k
}

func mustRing(t *testing.T, keys ...StaticKey) *StaticKeyRing {
	t.Helper()
	r, err := NewStaticKeyRing(keys...)
	if err != nil {
		t.Fatalf("NewStaticKeyRing: %v", err)
	}
	return r
}

func mustSealer(t *testing.T, ring KeyRing, scope Scope) *Sealer {
	t.Helper()
	s, err := NewSealer(context.Background(), ring, scope)
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

func mustSeal(t *testing.T, s *Sealer, pt string) ([]byte, uint32) {
	t.Helper()
	blob, v, err := s.Seal(context.Background(), pt)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	return blob, v
}

// fakeRing lets a test control exactly what the ring answers.
type fakeRing struct {
	active func(Scope) (uint32, error)
	key    func(Scope, uint32) ([]byte, error)
}

func (f fakeRing) ActiveVersion(_ context.Context, s Scope) (uint32, error) { return f.active(s) }
func (f fakeRing) DataKey(_ context.Context, s Scope, v uint32) ([]byte, error) {
	return f.key(s, v)
}

func expectErr(t *testing.T, err, want error) {
	t.Helper()
	if !errors.Is(err, want) {
		t.Fatalf("error = %v, want errors.Is %v", err, want)
	}
}

func TestSealOpenRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := mustSealer(t, mustRing(t, StaticKey{scopeTest, 1, testKey(1)}), scopeTest)
	if s.Scope() != scopeTest {
		t.Fatalf("Scope() = %q", s.Scope())
	}
	for name, pt := range map[string]string{
		"ascii":   "ABCDE1234F",
		"unicode": "राजेश कुमार · Ñandú · 张伟",
		"4kb":     strings.Repeat("0123456789abcdef", 256),
	} {
		t.Run(name, func(t *testing.T) {
			blob, v := mustSeal(t, s, pt)
			if v != 1 {
				t.Fatalf("version = %d, want 1", v)
			}
			if len(blob) != HeaderSize+len(pt)+tagSize {
				t.Fatalf("len = %d, want %d", len(blob), HeaderSize+len(pt)+tagSize)
			}
			got, err := s.Open(ctx, blob)
			if err != nil {
				t.Fatalf("Open: %v", err)
			}
			if got != pt {
				t.Fatalf("Open = %q, want %q", got, pt)
			}
		})
	}
}

func TestSealRefusesEmptyPlaintext(t *testing.T) {
	s := mustSealer(t, mustRing(t, StaticKey{scopeTest, 1, testKey(1)}), scopeTest)
	blob, v, err := s.Seal(context.Background(), "")
	expectErr(t, err, ErrEmptyPlaintext)
	if blob != nil || v != 0 {
		t.Fatalf("Seal(\"\") = %x, %d", blob, v)
	}
}

func TestSealUsesFreshNonce(t *testing.T) {
	s := mustSealer(t, mustRing(t, StaticKey{scopeTest, 1, testKey(1)}), scopeTest)
	a, _ := mustSeal(t, s, "same value")
	b, _ := mustSeal(t, s, "same value")
	if bytes.Equal(a, b) {
		t.Fatal("two seals of the same plaintext are identical")
	}
	if bytes.Equal(a[4:HeaderSize], b[4:HeaderSize]) {
		t.Fatal("two seals reused the same nonce")
	}
	if bytes.Equal(a[4:HeaderSize], make([]byte, NonceSize)) {
		t.Fatal("nonce is all zeros")
	}
}

func TestTamperDetection(t *testing.T) {
	ctx := context.Background()
	const pt = "1234567890123456 account"
	ring := mustRing(t, StaticKey{scopeTest, 1, testKey(1)}, StaticKey{scopeTest, 2, testKey(2)})
	s := mustSealer(t, ring, scopeTest)
	blob, v := mustSeal(t, s, pt)
	if v != 2 {
		t.Fatalf("active version = %d, want 2", v)
	}
	if got, err := s.Open(ctx, blob); err != nil || got != pt {
		t.Fatalf("untampered Open = %q, %v", got, err)
	}
	mutate := func(f func(b []byte) []byte) []byte {
		return f(append([]byte(nil), blob...))
	}
	withVersion := func(ver uint32) []byte {
		return mutate(func(b []byte) []byte { binary.BigEndian.PutUint32(b[:4], ver); return b })
	}

	cases := []struct {
		name string
		blob []byte
		want error
	}{
		{"ciphertext_bit", mutate(func(b []byte) []byte { b[HeaderSize] ^= 0x01; return b }), ErrBadCiphertext},
		{"tag_bit", mutate(func(b []byte) []byte { b[len(b)-1] ^= 0x80; return b }), ErrBadCiphertext},
		{"nonce_bit", mutate(func(b []byte) []byte { b[4] ^= 0x01; return b }), ErrBadCiphertext},
		{"version_absent", withVersion(9), ErrUnknownKeyVersion},
		{"version_present_other_key", withVersion(1), ErrBadCiphertext},
		{"version_zero", withVersion(0), ErrBadCiphertext},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := s.Open(ctx, c.blob)
			expectErr(t, err, c.want)
			if got != "" {
				t.Fatalf("Open returned plaintext %q on failure", got)
			}
		})
	}
	t.Run("truncated", func(t *testing.T) {
		for _, n := range []int{1, 3, 4, 15, 16, 17, 31} {
			_, err := s.Open(ctx, blob[:n])
			if !errors.Is(err, ErrBadCiphertext) {
				t.Fatalf("len %d: error = %v, want ErrBadCiphertext", n, err)
			}
			if _, err := KeyVersion(blob[:n]); !errors.Is(err, ErrBadCiphertext) {
				t.Fatalf("KeyVersion len %d: error = %v", n, err)
			}
		}
	})
	t.Run("empty", func(t *testing.T) {
		for _, b := range [][]byte{nil, {}} {
			got, err := s.Open(ctx, b)
			expectErr(t, err, ErrBadCiphertext)
			if got != "" {
				t.Fatalf("Open(empty) = %q", got)
			}
		}
	})
}

func TestOpenUnderWrongScopeFails(t *testing.T) {
	ctx := context.Background()
	key := testKey(9)
	ring := mustRing(t, StaticKey{"scope.one", 1, key}, StaticKey{"scope.two", 1, key})
	one := mustSealer(t, ring, "scope.one")
	two := mustSealer(t, ring, "scope.two")
	blob, _ := mustSeal(t, one, "bound to scope one")
	if got, err := one.Open(ctx, blob); err != nil || got != "bound to scope one" {
		t.Fatalf("same-scope Open = %q, %v", got, err)
	}
	got, err := two.Open(ctx, blob)
	expectErr(t, err, ErrBadCiphertext)
	if got != "" {
		t.Fatalf("cross-scope Open returned %q", got)
	}
}

func TestRotation(t *testing.T) {
	ctx := context.Background()
	ring := mustRing(t, StaticKey{scopeTest, 1, testKey(1)})
	s := mustSealer(t, ring, scopeTest)
	blob1, v1 := mustSeal(t, s, "sealed before rotation")
	if v1 != 1 {
		t.Fatalf("v1 = %d", v1)
	}
	if err := ring.Rotate(scopeTest, 2, testKey(2)); err != nil {
		t.Fatalf("Rotate: %v", err)
	}
	blob2, v2 := mustSeal(t, s, "sealed after rotation")
	if v2 != 2 {
		t.Fatalf("seal after rotation reported version %d, want 2", v2)
	}
	if h := binary.BigEndian.Uint32(blob2[:4]); h != 2 {
		t.Fatalf("header version = %d, want 2", h)
	}
	for blob, want := range map[*[]byte]string{&blob1: "sealed before rotation", &blob2: "sealed after rotation"} {
		got, err := s.Open(ctx, *blob)
		if err != nil || got != want {
			t.Fatalf("Open = %q, %v; want %q", got, err, want)
		}
	}
	if v, err := KeyVersion(blob1); err != nil || v != 1 {
		t.Fatalf("KeyVersion(blob1) = %d, %v", v, err)
	}
	if v, err := KeyVersion(blob2); err != nil || v != 2 {
		t.Fatalf("KeyVersion(blob2) = %d, %v", v, err)
	}
}

func TestOpenUnknownVersionFails(t *testing.T) {
	ctx := context.Background()
	ring := mustRing(t, StaticKey{scopeTest, 1, testKey(1)})
	s := mustSealer(t, ring, scopeTest)
	blob, _ := mustSeal(t, s, "value")
	binary.BigEndian.PutUint32(blob[:4], 7)

	got, err := s.Open(ctx, blob)
	expectErr(t, err, ErrUnknownKeyVersion)
	var ke *KeyError
	if !errors.As(err, &ke) || ke.Version != 7 || ke.Scope != scopeTest {
		t.Fatalf("error = %#v, want *KeyError{scope, 7}", err)
	}
	if got != "" {
		t.Fatalf("Open returned %q", got)
	}
	for _, v := range []uint32{0, 2, 7} {
		if k, err := ring.DataKey(ctx, scopeTest, v); !errors.Is(err, ErrUnknownKeyVersion) || k != nil {
			t.Fatalf("DataKey(v%d) = %d bytes, %v; want ErrUnknownKeyVersion", v, len(k), err)
		}
	}
	if _, err := ring.DataKey(ctx, "other.scope", 1); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("DataKey(other scope) error = %v", err)
	}
}

func TestNewSealerFailsClosed(t *testing.T) {
	ctx := context.Background()
	errRing := errors.New("ring backend unavailable")
	good := func(Scope, uint32) ([]byte, error) { return testKey(1), nil }
	active1 := func(Scope) (uint32, error) { return 1, nil }

	cases := []struct {
		name  string
		ring  KeyRing
		scope Scope
		want  error
	}{
		{"nil_ring", nil, scopeTest, ErrNoKeyRing},
		{"nil_static_ring", (*StaticKeyRing)(nil), scopeTest, ErrScopeNotConfigured},
		{"invalid_scope_empty", mustRing(t, StaticKey{scopeTest, 1, testKey(1)}), "", ErrInvalidScope},
		{"invalid_scope_upper", mustRing(t, StaticKey{scopeTest, 1, testKey(1)}), "Profile", ErrInvalidScope},
		{"invalid_scope_space", mustRing(t, StaticKey{scopeTest, 1, testKey(1)}), "a b", ErrInvalidScope},
		{"invalid_scope_long", mustRing(t, StaticKey{scopeTest, 1, testKey(1)}), Scope(strings.Repeat("a", 65)), ErrInvalidScope},
		{"no_key_for_scope", mustRing(t, StaticKey{scopeTest, 1, testKey(1)}), "other.scope", ErrScopeNotConfigured},
		{"zero_value_static_ring", &StaticKeyRing{}, scopeTest, ErrScopeNotConfigured},
		{"short_key", fakeRing{active1, func(Scope, uint32) ([]byte, error) { return testKey(1)[:16], nil }}, scopeTest, ErrBadDataKey},
		{"active_version_errors", fakeRing{func(Scope) (uint32, error) { return 0, errRing }, good}, scopeTest, errRing},
		{"active_version_zero", fakeRing{func(Scope) (uint32, error) { return 0, nil }, good}, scopeTest, ErrScopeNotConfigured},
		{"data_key_errors", fakeRing{active1, func(Scope, uint32) ([]byte, error) { return nil, errRing }}, scopeTest, errRing},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s, err := NewSealer(ctx, c.ring, c.scope)
			if s != nil {
				t.Fatal("NewSealer returned a sealer on failure")
			}
			expectErr(t, err, c.want)
			var ke *KeyError
			if !errors.As(err, &ke) {
				t.Fatalf("error %v is not a *KeyError", err)
			}
		})
	}
	if _, err := NewSealer(ctx, mustRing(t, StaticKey{"a-b_c.9", 1, testKey(1)}), "a-b_c.9"); err != nil {
		t.Fatalf("valid scope refused: %v", err)
	}
}

func TestSealRechecksKeySize(t *testing.T) {
	ctx := context.Background()
	var short atomic.Bool
	ring := fakeRing{
		active: func(Scope) (uint32, error) { return 1, nil },
		key: func(Scope, uint32) ([]byte, error) {
			if short.Load() {
				return testKey(1)[:16], nil
			}
			return testKey(1), nil
		},
	}
	s := mustSealer(t, ring, scopeTest)
	blob, _ := mustSeal(t, s, "value")
	short.Store(true)
	if out, _, err := s.Seal(ctx, "value"); !errors.Is(err, ErrBadDataKey) || out != nil {
		t.Fatalf("Seal with a 16-byte key = %x, %v; want ErrBadDataKey", out, err)
	}
	if got, err := s.Open(ctx, blob); !errors.Is(err, ErrBadDataKey) || got != "" {
		t.Fatalf("Open with a 16-byte key = %q, %v; want ErrBadDataKey", got, err)
	}
}

func TestWireFormatMatchesCommerceLayout(t *testing.T) {
	ctx := context.Background()
	key := testKey(42)
	const pt = "Flat 4B, Residency Road"

	commerceSeal := func(scope Scope, version uint32, nonce []byte) []byte {
		block, err := aes.NewCipher(key)
		if err != nil {
			t.Fatal(err)
		}
		aead, err := cipher.NewGCM(block)
		if err != nil {
			t.Fatal(err)
		}
		hdr := make([]byte, 4+len(nonce))
		binary.BigEndian.PutUint32(hdr[:4], version)
		copy(hdr[4:], nonce)
		return aead.Seal(hdr, nonce, []byte(pt), []byte(scope))
	}

	t.Run("commerce_blob_opens", func(t *testing.T) {
		for _, c := range []struct {
			scope   Scope
			version uint32
		}{{scopeProfile, 1}, {scopeSnapshot, 3}} {
			nonce := bytes.Repeat([]byte{0xA5}, NonceSize)
			blob := commerceSeal(c.scope, c.version, nonce)
			s := mustSealer(t, mustRing(t, StaticKey{c.scope, c.version, key}), c.scope)
			got, err := s.Open(ctx, blob)
			if err != nil || got != pt {
				t.Fatalf("%s: Open(commerce blob) = %q, %v", c.scope, got, err)
			}
		}
	})

	t.Run("seal_output_parses_by_hand", func(t *testing.T) {
		s := mustSealer(t, mustRing(t, StaticKey{scopeSnapshot, 3, key}), scopeSnapshot)
		blob, v := mustSeal(t, s, pt)
		if len(blob) != 4+NonceSize+len(pt)+16 {
			t.Fatalf("len = %d", len(blob))
		}
		if hv := binary.BigEndian.Uint32(blob[:4]); hv != 3 || v != 3 {
			t.Fatalf("header version %d, reported %d; want 3", hv, v)
		}
		block, _ := aes.NewCipher(key)
		aead, _ := cipher.NewGCM(block)
		got, err := aead.Open(nil, blob[4:16], blob[16:], []byte(scopeSnapshot))
		if err != nil || string(got) != pt {
			t.Fatalf("hand Open = %q, %v", got, err)
		}
		// And the exact bytes: re-sealing by hand with the same nonce reproduces the blob.
		if !bytes.Equal(commerceSeal(scopeSnapshot, 3, blob[4:16]), blob) {
			t.Fatal("Seal output is not byte-identical to the commerce layout")
		}
	})
}

func TestErrorsDoNotLeak(t *testing.T) {
	ctx := context.Background()
	const secret = "PLAINTEXT-ABCDE1234F-SECRET"
	key := testKey(77)
	forbidden := []string{
		secret,
		string(key),
		hex.EncodeToString(key),
		strings.ToUpper(hex.EncodeToString(key)),
		base64.StdEncoding.EncodeToString(key),
		base64.RawStdEncoding.EncodeToString(key),
		base64.URLEncoding.EncodeToString(key),
		base64.RawURLEncoding.EncodeToString(key),
	}

	var errs []error
	collect := func(err error) {
		if err == nil {
			t.Helper()
			t.Fatal("expected an error to inspect")
		}
		errs = append(errs, err)
	}

	ring := mustRing(t, StaticKey{scopeTest, 1, key}, StaticKey{scopeTest, 2, testKey(78)})
	s := mustSealer(t, ring, scopeTest)
	blob, _ := mustSeal(t, s, secret)

	_, err := NewSealer(ctx, nil, scopeTest)
	collect(err)
	_, err = NewSealer(ctx, ring, "BAD SCOPE")
	collect(err)
	_, err = NewSealer(ctx, ring, "missing")
	collect(err)
	_, err = NewSealer(ctx, fakeRing{func(Scope) (uint32, error) { return 1, nil },
		func(Scope, uint32) ([]byte, error) { return key[:16], nil }}, scopeTest)
	collect(err)
	_, _, err = s.Seal(ctx, "")
	collect(err)

	tampered := append([]byte(nil), blob...)
	tampered[len(tampered)-1] ^= 1
	_, err = s.Open(ctx, tampered)
	collect(err)
	other := append([]byte(nil), blob...)
	binary.BigEndian.PutUint32(other[:4], 1)
	_, err = s.Open(ctx, other)
	collect(err)
	binary.BigEndian.PutUint32(other[:4], 99)
	_, err = s.Open(ctx, other)
	collect(err)
	_, err = s.Open(ctx, blob[:10])
	collect(err)

	_, err = NewStaticKeyRing(StaticKey{scopeTest, 1, append(append([]byte(nil), key...), 0x01)})
	collect(err)
	_, err = NewStaticKeyRing(StaticKey{scopeTest, 1, key}, StaticKey{scopeTest, 1, key})
	collect(err)
	collect(ring.Rotate(scopeTest, 1, key))
	_, err = DecodeKey(hex.EncodeToString(key)[:62])
	collect(err)
	_, err = DecodeKey(base64.StdEncoding.EncodeToString(append(append([]byte(nil), key...), 0x02)))
	collect(err)

	h, herr := NewLookupHasher(bytes.Repeat([]byte{1}, 16), "pan", func(string) (string, error) {
		return "", errors.New("normaliser refused the value")
	})
	if herr != nil {
		t.Fatal(herr)
	}
	_, err = h.Hash(secret)
	collect(err)
	h2, _ := NewLookupHasher(bytes.Repeat([]byte{1}, 16), "pan", CompactUpper)
	_, err = h2.Hash(" - ")
	collect(err)
	_, err = NewLookupHasher(key[:8], "pan", CompactUpper)
	collect(err)

	for _, e := range errs {
		msg := e.Error()
		if msg == "" {
			t.Fatalf("empty error message for %#v", e)
		}
		for _, f := range forbidden {
			if strings.Contains(msg, f) {
				t.Fatalf("error %q leaks forbidden material", msg)
			}
		}
		// No 8-character run of the key's hex or base64 either.
		for _, f := range forbidden[2:] {
			for i := 0; i+8 <= len(f); i++ {
				if strings.Contains(msg, f[i:i+8]) {
					t.Fatalf("error %q contains a fragment of key encoding", msg)
				}
			}
		}
	}
}
