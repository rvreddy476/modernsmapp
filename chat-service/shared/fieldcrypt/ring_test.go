package fieldcrypt

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

func TestVersionedRingDerivesPerScopeKeys(t *testing.T) {
	ctx := context.Background()
	master := testKey(5)
	r, err := NewVersionedRing(map[uint32][]byte{1: master})
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.DataKey(ctx, "chat.message.text", 1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := r.DataKey(ctx, "chat.message.extra", 1)
	if bytes.Equal(a, b) || bytes.Equal(a, master) || len(a) != DataKeySize {
		t.Fatal("scopes must derive distinct keys that are not the master key")
	}
	again, _ := r.DataKey(ctx, "chat.message.text", 1)
	if !bytes.Equal(a, again) {
		t.Fatal("derivation is not deterministic")
	}
	again[0] ^= 0xff
	if third, _ := r.DataKey(ctx, "chat.message.text", 1); !bytes.Equal(third, a) {
		t.Fatal("DataKey handed out its cached slice")
	}
	if _, err := r.DataKey(ctx, "chat.message.text", 2); !errors.Is(err, ErrUnknownKeyVersion) {
		t.Fatalf("unknown version: %v", err)
	}
	if _, err := r.DataKey(ctx, "Bad Scope", 1); !errors.Is(err, ErrInvalidScope) {
		t.Fatalf("invalid scope: %v", err)
	}
	if v, err := r.ActiveVersion(ctx, "any.scope"); err != nil || v != 1 {
		t.Fatalf("ActiveVersion = %d, %v", v, err)
	}
	var zero VersionedRing
	if _, err := zero.ActiveVersion(ctx, "any.scope"); !errors.Is(err, ErrScopeNotConfigured) {
		t.Fatalf("zero ring: %v", err)
	}
}

func TestParseKeyList(t *testing.T) {
	k1, k2 := testKey(1), testKey(2)
	good := []string{
		"v1:" + base64.StdEncoding.EncodeToString(k1),
		" v1:" + base64.RawStdEncoding.EncodeToString(k1) + " , V2:" + hex.EncodeToString(k2) + " ",
		EncodeKeyList(map[uint32][]byte{2: k2, 1: k1}),
	}
	for _, spec := range good {
		r, err := ParseKeyList(spec)
		if err != nil {
			t.Fatalf("ParseKeyList: %v", err)
		}
		if v, _ := r.ActiveVersion(context.Background(), "x"); v == 0 {
			t.Fatal("no active version")
		}
	}
	r, _ := ParseKeyList(good[1])
	if v, _ := r.ActiveVersion(context.Background(), "x"); v != 2 {
		t.Fatalf("the newest version must be active, got %d", v)
	}

	secret := base64.StdEncoding.EncodeToString(k1)
	bad := map[string]error{
		"":                               ErrBadDataKey,
		"   ":                            ErrBadDataKey,
		secret:                           ErrInvalidKeyVersion,
		"1:" + secret:                    ErrInvalidKeyVersion,
		"v0:" + secret:                   ErrInvalidKeyVersion,
		"vx:" + secret:                   ErrInvalidKeyVersion,
		"v1:" + secret + ",v1:" + secret: ErrInvalidKeyVersion,
		"v1:" + secret[:20]:              ErrBadDataKey,
		"v1:" + base64.StdEncoding.EncodeToString(make([]byte, 32)): ErrBadDataKey,
	}
	for spec, want := range bad {
		_, err := ParseKeyList(spec)
		if !errors.Is(err, want) {
			t.Fatalf("ParseKeyList(%.8q...) = %v, want %v", spec, err, want)
		}
		if spec != "" && strings.Contains(err.Error(), secret[:8]) {
			t.Fatalf("error %q echoes key material", err)
		}
	}
}
