package datingpii

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

const testSalt = "dating-unit-test-lookup-salt-01"

func testKey(b byte) string { return base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{b}, 32)) }

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestFromEnv_ProductionRefusesWithoutKeys(t *testing.T) {
	ctx := context.Background()
	full := map[string]string{EnvKeys: "v1:" + testKey(7), EnvLookupSalt: testSalt}
	cases := []struct {
		name       string
		env        map[string]string
		wantErr    bool
		wantCrypto bool
	}{
		{"blank ENV is production and refuses", map[string]string{}, true, false},
		{"staging refuses", map[string]string{"ENV": "staging"}, true, false},
		{"local without keys boots unconfigured", map[string]string{"ENV": "local"}, false, false},
		{"dev without keys boots unconfigured", map[string]string{"ENV": "dev"}, false, false},
		{"keys without salt refuses", map[string]string{"ENV": "dev", EnvKeys: full[EnvKeys]}, true, false},
		{"salt without keys refuses", map[string]string{"ENV": "dev", EnvLookupSalt: testSalt}, true, false},
		{"short salt refuses", map[string]string{"ENV": "dev", EnvKeys: full[EnvKeys], EnvLookupSalt: "short"}, true, false},
		{"malformed keys refuse", map[string]string{"ENV": "dev", EnvKeys: "garbage", EnvLookupSalt: testSalt}, true, false},
		{"production configured", map[string]string{"ENV": "prod", EnvKeys: full[EnvKeys], EnvLookupSalt: testSalt}, false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c, err := FromEnv(ctx, env(tc.env))
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if c.Configured() != tc.wantCrypto {
				t.Fatalf("configured = %v, want %v", c.Configured(), tc.wantCrypto)
			}
			if err != nil && (strings.Contains(err.Error(), testKey(7)) || strings.Contains(err.Error(), testSalt)) {
				t.Fatalf("error echoes secret material")
			}
		})
	}
}

func testCrypto(t *testing.T) *Crypto {
	t.Helper()
	c, err := FromEnv(context.Background(), env(map[string]string{
		"ENV": "dev", EnvKeys: "v1:" + testKey(7) + ",v2:" + testKey(8), EnvLookupSalt: testSalt,
	}))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestSealRoundTripsAndNeverHoldsPlaintext(t *testing.T) {
	ctx := context.Background()
	c := testCrypto(t)
	blob, err := c.SealSensitive(ctx, "Hindu")
	if err != nil || bytes.Contains(blob, []byte("Hindu")) {
		t.Fatalf("seal = %x err=%v", blob, err)
	}
	if got, err := c.OpenSensitive(ctx, blob); err != nil || got != "Hindu" {
		t.Fatalf("open = %q err=%v", got, err)
	}
	// Scope binding: a religion blob never opens as a location or export.
	if _, _, err := c.OpenPoint(ctx, blob); err == nil {
		t.Fatalf("a sensitive blob opened under the location scope")
	}
	p, err := c.SealPoint(ctx, 12.971599, 77.594566)
	if err != nil {
		t.Fatal(err)
	}
	lat, lng, err := c.OpenPoint(ctx, p)
	if err != nil || lat != 12.971599 || lng != 77.594566 {
		t.Fatalf("point = %v,%v err=%v", lat, lng, err)
	}
}

func TestDeviceSignalLookupIsStableAndDomainSeparated(t *testing.T) {
	ctx := context.Background()
	c := testCrypto(t)
	a, err := c.SealFingerprint(ctx, "fp-1")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := c.SealFingerprint(ctx, " fp-1 ")
	if a.Lookup != b.Lookup || bytes.Equal(a.Blob, b.Blob) {
		t.Fatalf("lookup must be stable and blobs randomised")
	}
	ip, _ := c.SealIP(ctx, "fp-1")
	if ip.Lookup == a.Lookup {
		t.Fatalf("fingerprint and IP lookups share a domain")
	}
	if l, _ := c.IPLookup("2001:DB8::1"); l != mustLookup(t, c, "2001:db8::1") {
		t.Fatalf("IP lookup is case sensitive")
	}
	if got, err := c.OpenDeviceSignal(ctx, a.Blob); err != nil || got != "fp-1" {
		t.Fatalf("open fingerprint = %q err=%v", got, err)
	}
}

func mustLookup(t *testing.T, c *Crypto, ip string) string {
	t.Helper()
	l, err := c.IPLookup(ip)
	if err != nil {
		t.Fatal(err)
	}
	return l
}

func TestNilCryptoFailsClosed(t *testing.T) {
	ctx := context.Background()
	var c *Crypto
	if _, err := c.SealSensitive(ctx, "x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil seal err = %v", err)
	}
	if _, err := c.SealPoint(ctx, 1, 2); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil point err = %v", err)
	}
	if _, err := c.SealFingerprint(ctx, "x"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil fingerprint err = %v", err)
	}
}
