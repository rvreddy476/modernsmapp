package pii

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestStaticKeyRingRejects(t *testing.T) {
	cases := []struct {
		name string
		keys []StaticKey
		want error
	}{
		{"wrong_length_31", []StaticKey{{scopeTest, 1, testKey(1)[:31]}}, ErrBadDataKey},
		{"wrong_length_33", []StaticKey{{scopeTest, 1, append(testKey(1), 1)}}, ErrBadDataKey},
		{"wrong_length_16", []StaticKey{{scopeTest, 1, testKey(1)[:16]}}, ErrBadDataKey},
		{"wrong_length_nil", []StaticKey{{scopeTest, 1, nil}}, ErrBadDataKey},
		{"all_zero", []StaticKey{{scopeTest, 1, make([]byte, DataKeySize)}}, ErrBadDataKey},
		{"version_zero", []StaticKey{{scopeTest, 0, testKey(1)}}, ErrInvalidKeyVersion},
		{"duplicate", []StaticKey{{scopeTest, 1, testKey(1)}, {scopeTest, 1, testKey(2)}}, ErrInvalidKeyVersion},
		{"invalid_scope", []StaticKey{{"Not Valid", 1, testKey(1)}}, ErrInvalidScope},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r, err := NewStaticKeyRing(c.keys...)
			if r != nil {
				t.Fatal("ring returned on failure")
			}
			expectErr(t, err, c.want)
			var ke *KeyError
			if !errors.As(err, &ke) {
				t.Fatalf("error %v is not a *KeyError", err)
			}
		})
	}

	rotations := []struct {
		name    string
		version uint32
		key     []byte
		scope   Scope
		want    error
	}{
		// The ring holds v1 and v3, so v2 is lower than active AND absent: only
		// the ordering guard can refuse it.
		{"rotate_to_lower", 2, testKey(9), scopeTest, ErrInvalidKeyVersion},
		{"rotate_to_lower_present", 1, testKey(9), scopeTest, ErrInvalidKeyVersion},
		{"rotate_to_equal", 3, testKey(9), scopeTest, ErrInvalidKeyVersion},
		{"rotate_version_zero", 0, testKey(9), scopeTest, ErrInvalidKeyVersion},
		{"rotate_wrong_length", 4, testKey(9)[:24], scopeTest, ErrBadDataKey},
		{"rotate_all_zero", 4, make([]byte, DataKeySize), scopeTest, ErrBadDataKey},
		{"rotate_invalid_scope", 4, testKey(9), "", ErrInvalidScope},
	}
	for _, c := range rotations {
		t.Run(c.name, func(t *testing.T) {
			ctx := context.Background()
			r := mustRing(t, StaticKey{scopeTest, 1, testKey(1)}, StaticKey{scopeTest, 3, testKey(3)})
			expectErr(t, r.Rotate(c.scope, c.version, c.key), c.want)
			if v, _ := r.ActiveVersion(ctx, scopeTest); v != 3 {
				t.Fatalf("refused rotation changed active version to %d", v)
			}
			if _, err := r.DataKey(ctx, scopeTest, 2); !errors.Is(err, ErrUnknownKeyVersion) {
				t.Fatal("refused rotation added key v2")
			}
			if k, _ := r.DataKey(ctx, scopeTest, 1); !bytes.Equal(k, testKey(1)) {
				t.Fatal("refused rotation changed key v1")
			}
		})
	}
	t.Run("rotate_nil_ring", func(t *testing.T) {
		expectErr(t, (*StaticKeyRing)(nil).Rotate(scopeTest, 1, testKey(1)), ErrNoKeyRing)
	})
}

func TestStaticKeyRingCopiesKeys(t *testing.T) {
	ctx := context.Background()
	in := testKey(1)
	r := mustRing(t, StaticKey{scopeTest, 1, in})
	in[0] ^= 0xFF

	rot := testKey(2)
	if err := r.Rotate(scopeTest, 2, rot); err != nil {
		t.Fatal(err)
	}
	rot[0] ^= 0xFF

	out, err := r.DataKey(ctx, scopeTest, 1)
	if err != nil {
		t.Fatal(err)
	}
	out[1] ^= 0xFF

	for v, want := range map[uint32][]byte{1: testKey(1), 2: testKey(2)} {
		got, err := r.DataKey(ctx, scopeTest, v)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("DataKey(v%d) changed after caller mutation", v)
		}
	}
}

func TestStaticKeyRingActiveIsHighest(t *testing.T) {
	ctx := context.Background()
	r := mustRing(t,
		StaticKey{scopeTest, 3, testKey(3)},
		StaticKey{scopeTest, 1, testKey(1)},
		StaticKey{scopeProfile, 5, testKey(5)},
		StaticKey{scopeTest, 2, testKey(2)},
	)
	if v, err := r.ActiveVersion(ctx, scopeTest); err != nil || v != 3 {
		t.Fatalf("ActiveVersion = %d, %v; want 3", v, err)
	}
	if v, err := r.ActiveVersion(ctx, scopeProfile); err != nil || v != 5 {
		t.Fatalf("ActiveVersion(profile) = %d, %v; want 5", v, err)
	}
	if _, err := r.ActiveVersion(ctx, scopeSnapshot); !errors.Is(err, ErrScopeNotConfigured) {
		t.Fatalf("unconfigured scope error = %v", err)
	}
	var zero StaticKeyRing
	if _, err := zero.ActiveVersion(ctx, scopeTest); !errors.Is(err, ErrScopeNotConfigured) {
		t.Fatalf("zero ring error = %v", err)
	}
	if err := zero.Rotate(scopeTest, 1, testKey(1)); err != nil {
		t.Fatalf("zero ring Rotate: %v", err)
	}
	if v, err := zero.ActiveVersion(ctx, scopeTest); err != nil || v != 1 {
		t.Fatalf("zero ring after Rotate = %d, %v", v, err)
	}
}

func TestDecodeKey(t *testing.T) {
	key := testKey(5)
	for name, in := range map[string]string{
		"hex_lower":      hex.EncodeToString(key),
		"hex_upper":      strings.ToUpper(hex.EncodeToString(key)),
		"base64_std":     base64.StdEncoding.EncodeToString(key),
		"base64_raw":     base64.RawStdEncoding.EncodeToString(key),
		"base64_url":     base64.URLEncoding.EncodeToString(key),
		"base64_raw_url": base64.RawURLEncoding.EncodeToString(key),
		"whitespace":     "  " + hex.EncodeToString(key) + "\n",
	} {
		t.Run(name, func(t *testing.T) {
			got, err := DecodeKey(in)
			if err != nil || !bytes.Equal(got, key) {
				t.Fatalf("DecodeKey = %x, %v", got, err)
			}
		})
	}

	k31, k33 := key[:31], append(append([]byte(nil), key...), 9)
	for name, in := range map[string]string{
		"hex_31":        hex.EncodeToString(k31),
		"hex_33":        hex.EncodeToString(k33),
		"base64_31":     base64.StdEncoding.EncodeToString(k31),
		"base64_33":     base64.StdEncoding.EncodeToString(k33),
		"not_hex_64":    strings.Repeat("zz", 32),
		"garbage":       "definitely-not-a-key!!",
		"empty":         "",
		"all_zero_hex":  strings.Repeat("00", 32),
		"all_zero_b64":  base64.StdEncoding.EncodeToString(make([]byte, 32)),
		"hex_bad_digit": "g" + hex.EncodeToString(key)[1:],
	} {
		t.Run("rejects_"+name, func(t *testing.T) {
			got, err := DecodeKey(in)
			expectErr(t, err, ErrBadDataKey)
			if got != nil {
				t.Fatal("DecodeKey returned bytes on failure")
			}
			msg := err.Error()
			if in != "" && strings.Contains(msg, in) {
				t.Fatalf("error %q echoes the input", msg)
			}
			if len(in) >= 8 && strings.Contains(msg, in[:8]) {
				t.Fatalf("error %q echoes part of the input", msg)
			}
		})
	}
}
