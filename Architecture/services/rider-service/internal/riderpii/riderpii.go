// Package riderpii wires shared/pii for rider-service: the ride OTP is sealed
// at rest under scope rider.ride_otp with a static key ring read from
// RIDER_PII_KEYS, the same "v1:<key>[,v2:<key>]" format food-service's
// FOOD_PII_KEYS uses (32 bytes each, base64 or hex; the highest version
// seals, every version opens).
//
// It fails closed. A nil *Crypto answers ErrNotConfigured from every seal and
// open, so a ride can never fall back to storing a plaintext OTP. Production
// (runtimeenv.IsProduction) refuses to start without keys; local/dev without
// keys boots, and offer acceptance then fails with ErrNotConfigured.
//
// This replaces the AWS KMS envelope (internal/otp/kms.go) and the
// MOPEDU_OTP_ENCRYPTION_KEY master key (internal/otp/envelope.go). The
// rider_rides.kms_data_key_encrypted column is no longer written.
package riderpii

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/atpost/rider-service/internal/runtimeenv"
	"github.com/atpost/shared/pii"
)

const (
	// ScopeRideOTP seals the 4-digit ride OTP between assignment and start.
	ScopeRideOTP pii.Scope = "rider.ride_otp"

	// EnvKeys is the key-ring variable.
	EnvKeys = "RIDER_PII_KEYS"
)

// ErrNotConfigured is answered when the keys are absent.
var ErrNotConfigured = errors.New("OTP_SEALING_NOT_CONFIGURED: " + EnvKeys + " is not set")

// VersionedKey is one parsed RIDER_PII_KEYS entry.
type VersionedKey struct {
	Version uint32
	Key     []byte
}

// ParseKeys reads "v1:<key>[,v2:<key>...]". Errors name the entry position,
// never its content.
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

// Crypto seals and opens ride OTPs.
type Crypto struct {
	otp *pii.Sealer
}

// New builds the sealer from parsed keys.
func New(ctx context.Context, keys []VersionedKey) (*Crypto, error) {
	var static []pii.StaticKey
	for _, k := range keys {
		static = append(static, pii.StaticKey{Scope: ScopeRideOTP, Version: k.Version, Key: k.Key})
	}
	ring, err := pii.NewStaticKeyRing(static...)
	if err != nil {
		return nil, fmt.Errorf("%s rejected: %w", EnvKeys, err)
	}
	sealer, err := pii.NewSealer(ctx, ring, ScopeRideOTP)
	if err != nil {
		return nil, fmt.Errorf("ride otp sealer: %w", err)
	}
	return &Crypto{otp: sealer}, nil
}

// FromEnv returns (nil, nil) only outside production with RIDER_PII_KEYS
// unset. Production without it, or a malformed value anywhere, is an error,
// so a typo cannot silently disable sealing.
func FromEnv(ctx context.Context, getenv func(string) string) (*Crypto, error) {
	raw := strings.TrimSpace(getenv(EnvKeys))
	if raw == "" {
		if runtimeenv.IsProduction(getenv) {
			return nil, fmt.Errorf("%s is required in production: ride OTPs are sealed at rest", EnvKeys)
		}
		return nil, nil
	}
	keys, err := ParseKeys(raw)
	if err != nil {
		return nil, err
	}
	return New(ctx, keys)
}

// Configured reports whether sealing is available.
func (c *Crypto) Configured() bool { return c != nil }

// SealOTP seals a plaintext OTP; the blob carries the key version.
func (c *Crypto) SealOTP(ctx context.Context, otp string) ([]byte, error) {
	if c == nil {
		return nil, ErrNotConfigured
	}
	blob, _, err := c.otp.Seal(ctx, otp)
	if err != nil {
		return nil, fmt.Errorf("riderpii: seal otp: %w", err)
	}
	return blob, nil
}

// OpenOTP opens a blob produced by SealOTP.
func (c *Crypto) OpenOTP(ctx context.Context, blob []byte) (string, error) {
	if c == nil {
		return "", ErrNotConfigured
	}
	return c.otp.Open(ctx, blob)
}
