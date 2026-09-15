// Package datingpii wires shared/pii for dating-service (Dating plan lane D9):
// the sealing scopes for sensitive profile fields, precise locations, device
// signals and data-export blobs, the lookup hashes for device signals, and the
// DATING_PII_KEYS / DATING_PII_LOOKUP_SALT configuration.
//
// It is a copy of food-service's internal/foodpii wiring and fails closed the
// same way: a nil *Crypto is valid and answers ErrNotConfigured from every
// seal and open, so a write path can never fall back to plaintext. Production
// (ENV other than local/dev/development, blank included) refuses to start
// without keys.
//
// What is sealed, and what deliberately is not:
//
//   - religion, community (profile): sealed, no lookup hash (nothing looks
//     them up by value; the deck's same-community cap opens them in Go).
//   - exact points: panic incidents, live location shares, meet venues. No
//     SQL computes on them, so they are sealed as "lat,lng".
//   - device fingerprint and IP: sealed plus a lookup hash, because ban
//     evasion and IP velocity count distinct users by exact value in SQL.
//   - data-export JSON: sealed while it waits for the owner to download it.
//   - NOT sealed: the profile's D7 point (snapped to 0.01 degree, used by SQL
//     distance and geohash queries) and the discovery gender preference (a
//     SQL filter). Retained D8 risk signals are already HMAC-hashed.
package datingpii

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/atpost/shared/pii"
)

const (
	ScopeProfileSensitive pii.Scope = "dating.profile_sensitive"
	ScopePreciseLocation  pii.Scope = "dating.precise_location"
	ScopeDeviceSignal     pii.Scope = "dating.device_signal"
	ScopeDataExport       pii.Scope = "dating.data_export"

	LookupDomainDeviceFingerprint = "dating_device_fingerprint"
	LookupDomainIP                = "dating_ip"

	EnvKeys       = "DATING_PII_KEYS"
	EnvLookupSalt = "DATING_PII_LOOKUP_SALT"
)

// ErrNotConfigured is what a sealing route answers (503 PII_NOT_CONFIGURED)
// when the keys are absent.
var ErrNotConfigured = errors.New("PII_NOT_CONFIGURED")

// VersionedKey is one parsed DATING_PII_KEYS entry.
type VersionedKey struct {
	Version uint32
	Key     []byte
}

// ParseKeys reads "v1:<key>[,v2:<key>...]". Each key is 32 bytes as base64 or
// hex (pii.DecodeKey). The highest version seals; every version opens. Errors
// name the entry position, never its content.
func ParseKeys(raw string) ([]VersionedKey, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, fmt.Errorf("%s is empty", EnvKeys)
	}
	seen := map[uint32]bool{}
	var out []VersionedKey
	for i, entry := range strings.Split(raw, ",") {
		pos := i + 1
		entry = strings.TrimSpace(entry)
		if entry == "" {
			return nil, fmt.Errorf("%s entry %d is empty", EnvKeys, pos)
		}
		ver, key, ok := strings.Cut(entry, ":")
		if !ok || len(ver) < 2 || ver[0] != 'v' {
			return nil, fmt.Errorf("%s entry %d must look like v<version>:<base64 key>", EnvKeys, pos)
		}
		n, err := strconv.ParseUint(ver[1:], 10, 32)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%s entry %d: version must be a whole number from 1", EnvKeys, pos)
		}
		if seen[uint32(n)] {
			return nil, fmt.Errorf("%s entry %d: version %d appears twice", EnvKeys, pos, n)
		}
		seen[uint32(n)] = true
		decoded, err := pii.DecodeKey(key)
		if err != nil {
			return nil, fmt.Errorf("%s entry %d: key must be 32 bytes, base64 or hex", EnvKeys, pos)
		}
		out = append(out, VersionedKey{Version: uint32(n), Key: decoded})
	}
	return out, nil
}

// IsProduction is true for every ENV except local, dev and development — a
// blank ENV included.
func IsProduction(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "local", "dev", "development":
		return false
	}
	return true
}

// Crypto holds one sealer per scope and the device-signal lookup hashers.
type Crypto struct {
	sensitive *pii.Sealer
	location  *pii.Sealer
	device    *pii.Sealer
	export    *pii.Sealer
	fpLookup  *pii.LookupHasher
	ipLookup  *pii.LookupHasher
}

// New builds the sealers and hashers. Every version is registered for every
// scope; the envelope binds the scope as additional data, so a blob sealed
// for one scope does not open under another.
func New(ctx context.Context, keys []VersionedKey, salt []byte) (*Crypto, error) {
	if len(keys) == 0 {
		return nil, fmt.Errorf("%s: at least one key is required", EnvKeys)
	}
	scopes := []pii.Scope{ScopeProfileSensitive, ScopePreciseLocation, ScopeDeviceSignal, ScopeDataExport}
	var static []pii.StaticKey
	for _, k := range keys {
		for _, sc := range scopes {
			static = append(static, pii.StaticKey{Scope: sc, Version: k.Version, Key: k.Key})
		}
	}
	ring, err := pii.NewStaticKeyRing(static...)
	if err != nil {
		return nil, fmt.Errorf("%s rejected: %w", EnvKeys, err)
	}
	c := &Crypto{}
	for _, want := range []struct {
		scope pii.Scope
		dst   **pii.Sealer
	}{
		{ScopeProfileSensitive, &c.sensitive},
		{ScopePreciseLocation, &c.location},
		{ScopeDeviceSignal, &c.device},
		{ScopeDataExport, &c.export},
	} {
		s, err := pii.NewSealer(ctx, ring, want.scope)
		if err != nil {
			return nil, fmt.Errorf("%s sealer: %w", want.scope, err)
		}
		*want.dst = s
	}
	if c.fpLookup, err = pii.NewLookupHasher(salt, LookupDomainDeviceFingerprint, trimOnly); err != nil {
		return nil, fmt.Errorf("%s rejected: %w", EnvLookupSalt, err)
	}
	if c.ipLookup, err = pii.NewLookupHasher(salt, LookupDomainIP, lowerTrim); err != nil {
		return nil, fmt.Errorf("%s rejected: %w", EnvLookupSalt, err)
	}
	return c, nil
}

// FromEnv returns (nil, nil) only in local/dev with BOTH variables unset.
// Production without them, either one set alone, or a malformed value is an
// error, so a typo cannot silently disable sealing.
func FromEnv(ctx context.Context, getenv func(string) string) (*Crypto, error) {
	keysRaw := strings.TrimSpace(getenv(EnvKeys))
	saltRaw := strings.TrimSpace(getenv(EnvLookupSalt))
	if keysRaw == "" && saltRaw == "" {
		if IsProduction(getenv("ENV")) {
			return nil, fmt.Errorf("%s and %s are required unless ENV is local, dev or development", EnvKeys, EnvLookupSalt)
		}
		return nil, nil
	}
	if keysRaw == "" || saltRaw == "" {
		return nil, fmt.Errorf("set both %s and %s, or (local/dev only) neither", EnvKeys, EnvLookupSalt)
	}
	keys, err := ParseKeys(keysRaw)
	if err != nil {
		return nil, err
	}
	return New(ctx, keys, []byte(saltRaw))
}

// Configured reports whether sealing is available.
func (c *Crypto) Configured() bool { return c != nil }

// SealSensitive seals a religion or community value.
func (c *Crypto) SealSensitive(ctx context.Context, v string) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	return sealBlob(ctx, c.sensitive, v)
}

// OpenSensitive opens a blob sealed by SealSensitive.
func (c *Crypto) OpenSensitive(ctx context.Context, blob []byte) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.sensitive.Open(ctx, blob)
}

// SealPoint seals an exact latitude/longitude as "lat,lng" at full precision.
func (c *Crypto) SealPoint(ctx context.Context, lat, lng float64) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	return sealBlob(ctx, c.location, FormatPoint(lat, lng))
}

// OpenPoint opens a blob sealed by SealPoint.
func (c *Crypto) OpenPoint(ctx context.Context, blob []byte) (lat, lng float64, err error) {
	if c == nil {
		return 0, 0, ErrNotConfigured
	}
	raw, err := c.location.Open(ctx, blob)
	if err != nil {
		return 0, 0, err
	}
	return ParsePoint(raw)
}

// DeviceSignal is a sealed device fingerprint or IP with its lookup hash.
type DeviceSignal struct {
	Blob   []byte
	Lookup string
}

// SealFingerprint seals a device fingerprint and hashes it for exact counts.
func (c *Crypto) SealFingerprint(ctx context.Context, fp string) (DeviceSignal, error) {
	if c == nil {
		return DeviceSignal{}, ErrNotConfigured
	}
	return sealSignal(ctx, c.device, c.fpLookup, fp)
}

// SealIP seals a client IP and hashes it for exact counts.
func (c *Crypto) SealIP(ctx context.Context, ip string) (DeviceSignal, error) {
	if c == nil {
		return DeviceSignal{}, ErrNotConfigured
	}
	return sealSignal(ctx, c.device, c.ipLookup, ip)
}

// FingerprintLookup is the lookup hash of a fingerprint (for counting).
func (c *Crypto) FingerprintLookup(fp string) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.fpLookup.Hash(fp)
}

// IPLookup is the lookup hash of an IP (for counting).
func (c *Crypto) IPLookup(ip string) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.ipLookup.Hash(ip)
}

// OpenDeviceSignal opens a fingerprint or IP blob.
func (c *Crypto) OpenDeviceSignal(ctx context.Context, blob []byte) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.device.Open(ctx, blob)
}

// SealExport seals a data-export JSON document.
func (c *Crypto) SealExport(ctx context.Context, doc []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	return sealBlob(ctx, c.export, string(doc))
}

// OpenExport opens a blob sealed by SealExport.
func (c *Crypto) OpenExport(ctx context.Context, blob []byte) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	raw, err := c.export.Open(ctx, blob)
	if err != nil {
		return nil, err
	}
	return []byte(raw), nil
}

// FormatPoint renders a point at full float precision.
func FormatPoint(lat, lng float64) string {
	return strconv.FormatFloat(lat, 'f', -1, 64) + "," + strconv.FormatFloat(lng, 'f', -1, 64)
}

// ParsePoint parses FormatPoint's output. Errors never echo the input.
func ParsePoint(raw string) (float64, float64, error) {
	a, b, ok := strings.Cut(raw, ",")
	if !ok {
		return 0, 0, errors.New("datingpii: sealed point is malformed")
	}
	lat, err1 := strconv.ParseFloat(a, 64)
	lng, err2 := strconv.ParseFloat(b, 64)
	if err1 != nil || err2 != nil {
		return 0, 0, errors.New("datingpii: sealed point is malformed")
	}
	return lat, lng, nil
}

func sealBlob(ctx context.Context, s *pii.Sealer, v string) ([]byte, error) {
	blob, _, err := s.Seal(ctx, v)
	if err != nil {
		return nil, fmt.Errorf("datingpii: seal: %w", err)
	}
	return blob, nil
}

func sealSignal(ctx context.Context, s *pii.Sealer, h *pii.LookupHasher, v string) (DeviceSignal, error) {
	lookup, err := h.Hash(v)
	if err != nil {
		return DeviceSignal{}, fmt.Errorf("datingpii: lookup hash: %w", err)
	}
	blob, err := sealBlob(ctx, s, v)
	if err != nil {
		return DeviceSignal{}, err
	}
	return DeviceSignal{Blob: blob, Lookup: lookup}, nil
}

// trimOnly keeps a fingerprint byte-exact apart from surrounding space.
func trimOnly(s string) (string, error) { return strings.TrimSpace(s), nil }

// lowerTrim canonicalises an IP string (IPv6 hex case).
func lowerTrim(s string) (string, error) { return strings.ToLower(strings.TrimSpace(s)), nil }
