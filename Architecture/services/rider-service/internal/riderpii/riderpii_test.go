package riderpii

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"testing"

	"github.com/atpost/shared/pii"
)

// TestKeyV1 is a fixed 32-byte test key (never used outside tests).
var TestKeyV1 = base64.StdEncoding.EncodeToString([]byte("rider-otp-test-key-32-bytes-long"))

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestParseKeys(t *testing.T) {
	keys, err := ParseKeys("v1:" + TestKeyV1)
	if err != nil || len(keys) != 1 || keys[0].Version != 1 || len(keys[0].Key) != 32 {
		t.Fatalf("parse: %+v %v", keys, err)
	}
	for _, bad := range []string{"", "x1:abc", "v0:" + TestKeyV1, "v1:short", "v1:" + TestKeyV1 + ",v1:" + TestKeyV1} {
		if _, err := ParseKeys(bad); err == nil {
			t.Errorf("%q accepted", bad)
		} else if strings.Contains(err.Error(), TestKeyV1) {
			t.Errorf("error leaks key material: %v", err)
		}
	}
}

func TestSealOpenRoundTripAndScope(t *testing.T) {
	c, err := FromEnv(context.Background(), env(map[string]string{EnvKeys: "v1:" + TestKeyV1}))
	if err != nil || !c.Configured() {
		t.Fatalf("from env: %v", err)
	}
	blob, err := c.SealOTP(context.Background(), "4829")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(blob), "4829") {
		t.Fatal("plaintext in the blob")
	}
	got, err := c.OpenOTP(context.Background(), blob)
	if err != nil || got != "4829" {
		t.Fatalf("open: %q %v", got, err)
	}
	if _, err := c.OpenOTP(context.Background(), append([]byte{}, blob[:len(blob)-1]...)); !errors.Is(err, pii.ErrBadCiphertext) {
		t.Fatalf("tampered blob: %v", err)
	}
	if _, err := c.SealOTP(context.Background(), ""); err == nil {
		t.Fatal("empty otp sealed")
	}
}

func TestFromEnv_FailsClosed(t *testing.T) {
	c, err := FromEnv(context.Background(), env(map[string]string{"ENV": "dev"}))
	if err != nil || c != nil {
		t.Fatalf("dev without keys: %v %v", c, err)
	}
	if _, err := c.SealOTP(context.Background(), "1234"); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil crypto must refuse: %v", err)
	}
	if _, err := c.OpenOTP(context.Background(), []byte("x")); !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("nil crypto must refuse open: %v", err)
	}
	for _, e := range []string{"prod", "production"} {
		if _, err := FromEnv(context.Background(), env(map[string]string{"ENV": e})); err == nil {
			t.Errorf("ENV=%s without keys booted", e)
		}
	}
	if _, err := FromEnv(context.Background(), env(map[string]string{"ENV": "dev", EnvKeys: "garbage"})); err == nil {
		t.Fatal("malformed keys accepted")
	}
}

func TestRotation_OldVersionStillOpens(t *testing.T) {
	v2 := base64.StdEncoding.EncodeToString([]byte("rider-otp-test-key-v2-32-bytes!!"))
	old, err := FromEnv(context.Background(), env(map[string]string{EnvKeys: "v1:" + TestKeyV1}))
	if err != nil {
		t.Fatal(err)
	}
	blob, _ := old.SealOTP(context.Background(), "0001")
	rotated, err := FromEnv(context.Background(), env(map[string]string{EnvKeys: "v1:" + TestKeyV1 + ",v2:" + v2}))
	if err != nil {
		t.Fatal(err)
	}
	if got, err := rotated.OpenOTP(context.Background(), blob); err != nil || got != "0001" {
		t.Fatalf("v1 blob after rotation: %q %v", got, err)
	}
	fresh, _ := rotated.SealOTP(context.Background(), "0002")
	if v, _ := pii.KeyVersion(fresh); v != 2 {
		t.Fatalf("fresh seal uses version %d, want 2", v)
	}
}
