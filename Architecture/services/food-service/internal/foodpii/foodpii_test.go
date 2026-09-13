package foodpii

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

// Test-only key material: deterministic, obviously synthetic, never deployed.
func testKey(fill byte) string {
	k := bytes.Repeat([]byte{fill}, 32)
	return base64.StdEncoding.EncodeToString(k)
}

const testSalt = "food-service-unit-test-salt-0001"

func envOf(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func TestParseKeys(t *testing.T) {
	good := "v1:" + testKey(1)
	cases := []struct {
		name       string
		raw        string
		wantErr    bool
		wantActive uint32
	}{
		{"one version", good, false, 1},
		{"two versions", good + ", v2:" + testKey(2), false, 2},
		{"out of order", "v3:" + testKey(3) + ",v1:" + testKey(1), false, 3},
		{"empty", "", true, 0},
		{"no version prefix", testKey(1), true, 0},
		{"version zero", "v0:" + testKey(1), true, 0},
		{"non-numeric version", "vx:" + testKey(1), true, 0},
		{"duplicate version", good + ",v1:" + testKey(2), true, 0},
		{"short key", "v1:" + base64.StdEncoding.EncodeToString([]byte("short")), true, 0},
		{"empty entry", good + ",", true, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			keys, err := ParseKeys(tc.raw)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if err != nil {
				if strings.Contains(err.Error(), testKey(1)) || strings.Contains(err.Error(), testKey(2)) {
					t.Fatalf("error echoes key material")
				}
				return
			}
			var max uint32
			for _, k := range keys {
				if k.Version > max {
					max = k.Version
				}
			}
			if max != tc.wantActive {
				t.Fatalf("highest version = %d, want %d", max, tc.wantActive)
			}
		})
	}
}

func TestFromEnv(t *testing.T) {
	ctx := context.Background()
	full := map[string]string{"FOOD_PII_KEYS": "v1:" + testKey(7), "FOOD_PII_LOOKUP_SALT": testSalt}
	cases := []struct {
		name       string
		env        map[string]string
		wantErr    bool
		wantCrypto bool
	}{
		{"production without keys refuses", map[string]string{"ENV": "production"}, true, false},
		{"blank ENV is production", map[string]string{}, true, false},
		{"staging is production", map[string]string{"ENV": "staging"}, true, false},
		{"local without keys boots unconfigured", map[string]string{"ENV": "local"}, false, false},
		{"dev without keys boots unconfigured", map[string]string{"ENV": "DEV"}, false, false},
		{"development without keys boots unconfigured", map[string]string{"ENV": "development"}, false, false},
		{"local with keys but no salt refuses", map[string]string{"ENV": "local", "FOOD_PII_KEYS": full["FOOD_PII_KEYS"]}, true, false},
		{"local with salt but no keys refuses", map[string]string{"ENV": "local", "FOOD_PII_LOOKUP_SALT": testSalt}, true, false},
		{"short salt refuses", map[string]string{"ENV": "local", "FOOD_PII_KEYS": full["FOOD_PII_KEYS"], "FOOD_PII_LOOKUP_SALT": "short"}, true, false},
		{"malformed keys refuse in local", map[string]string{"ENV": "local", "FOOD_PII_KEYS": "garbage", "FOOD_PII_LOOKUP_SALT": testSalt}, true, false},
		{"production configured", map[string]string{"ENV": "production", "FOOD_PII_KEYS": full["FOOD_PII_KEYS"], "FOOD_PII_LOOKUP_SALT": testSalt}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := FromEnv(ctx, envOf(tc.env))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if (c != nil) != tc.wantCrypto {
				t.Fatalf("crypto present = %v, want %v", c != nil, tc.wantCrypto)
			}
			if err != nil && strings.Contains(err.Error(), testKey(7)) {
				t.Fatalf("error echoes key material")
			}
		})
	}
}

func testCrypto(t *testing.T) *Crypto {
	t.Helper()
	c, err := FromEnv(context.Background(), envOf(map[string]string{
		"ENV": "local", "FOOD_PII_KEYS": "v1:" + testKey(7) + ",v2:" + testKey(8), "FOOD_PII_LOOKUP_SALT": testSalt,
	}))
	if err != nil || c == nil {
		t.Fatalf("crypto: %v", err)
	}
	return c
}

func TestSealPANRoundTripAndLookup(t *testing.T) {
	ctx := context.Background()
	c := testCrypto(t)
	const pan = "ZZZPZ0000Z"
	sealed, err := c.SealPAN(ctx, pan)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if sealed.KeyVersion != 2 {
		t.Fatalf("key version = %d, want the active version 2", sealed.KeyVersion)
	}
	if bytes.Contains(sealed.Blob, []byte(pan)) || strings.Contains(sealed.Lookup, pan) {
		t.Fatalf("sealed output carries the plaintext")
	}
	opened, err := c.OpenPAN(ctx, sealed.Blob)
	if err != nil || opened != pan {
		t.Fatalf("open = %q, %v", opened, err)
	}
	again, _ := c.SealPAN(ctx, "zzzpz0000z")
	if again.Lookup != sealed.Lookup {
		t.Fatalf("lookup hash must be stable across case")
	}
	if bytes.Equal(again.Blob, sealed.Blob) {
		t.Fatalf("seal must be randomised")
	}
	if _, err := c.OpenAccountNumber(ctx, sealed.Blob); err == nil {
		t.Fatalf("a PAN blob must not open under the payout-account scope")
	}
}

func TestSealAccountNumber(t *testing.T) {
	ctx := context.Background()
	c := testCrypto(t)
	const acct = "000123456789"
	sealed, err := c.SealAccountNumber(ctx, acct)
	if err != nil {
		t.Fatalf("seal: %v", err)
	}
	if bytes.Contains(sealed.Blob, []byte(acct)) || strings.Contains(sealed.Lookup, acct) {
		t.Fatalf("sealed output carries the plaintext")
	}
	opened, err := c.OpenAccountNumber(ctx, sealed.Blob)
	if err != nil || opened != acct {
		t.Fatalf("open = %q, %v", opened, err)
	}
	panSealed, _ := c.SealPAN(ctx, "ZZZPZ0000Z")
	if panSealed.Lookup == sealed.Lookup {
		t.Fatalf("domains must separate lookup hashes")
	}
	if _, err := c.SealAccountNumber(ctx, "12AB"); err == nil {
		t.Fatalf("an invalid account number must not be hashed")
	}
}

func TestNilCryptoFailsClosed(t *testing.T) {
	var c *Crypto
	ctx := context.Background()
	if _, err := c.SealPAN(ctx, "ZZZPZ0000Z"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("SealPAN on nil = %v", err)
	}
	if _, err := c.SealAccountNumber(ctx, "000123456789"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("SealAccountNumber on nil = %v", err)
	}
	if c.Configured() {
		t.Fatalf("nil crypto must report unconfigured")
	}
}
