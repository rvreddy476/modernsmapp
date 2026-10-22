// Package fieldkeys decides, at boot, which key ring chat field encryption
// at rest runs on, and refuses to start when production would otherwise run
// without one.
//
// The rules (founder decision 22 Oct 2026; commerce's KMS mechanism and env
// naming, commerce-service/cmd/server/main.go buildPIICipher):
//
//   - CHAT_KMS_KEY_ID set: master keys are minted by AWS KMS under that
//     customer-managed key (IRSA credentials) and persisted wrapped in
//     chat.field_key_ring; every version is unwrapped once at boot. This is
//     the production path, and staging/QA use it too.
//   - CHAT_FIELD_KEYS set ("v1:<base64 32 bytes>,v2:<...>"; the newest seals,
//     all open): an env keyring for development and tests. Refused in
//     production — a raw key in an env var is not key management.
//   - neither: plaintext. Allowed outside production only, with ONE warning
//     at boot. In production (APP_ENV / ENVIRONMENT / ENV = prod|production,
//     the same rule as accessauth) the service refuses to boot.
//   - both: refused everywhere (ambiguous).
//
// Whatever the ring, a readiness probe seals and opens one value per scope
// before the service reports ready, so a KMS that wraps but will not unwrap
// fails the boot rather than the first message.
package fieldkeys

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/contentcrypt"
	"github.com/atpost/chat-shared/fieldcrypt"
)

// Env var names.
const (
	EnvKMSKeyID  = "CHAT_KMS_KEY_ID"
	EnvFieldKeys = "CHAT_FIELD_KEYS"
)

// KMS encryption-context labels for the ring's keys.
const (
	KMSPurpose   = "chat-field"
	KMSRingScope = "chat.field"
)

// Mode is the ring the cipher runs on.
type Mode string

const (
	ModeKMS       Mode = "kms"
	ModeEnvKeys   Mode = "env-keys"
	ModePlaintext Mode = "plaintext"
)

// ErrRefused wraps every refusal to boot.
var ErrRefused = errors.New("field encryption: refusing to start")

// Deps are the production collaborators, injected so the decision is
// testable without AWS or a database.
type Deps struct {
	// Getenv reads configuration (os.Getenv in main).
	Getenv func(string) string
	// NewWrapper builds the KMS key wrapper for a key id (kmsclient.New).
	NewWrapper func(ctx context.Context, keyID string) (fieldcrypt.KeyWrapper, error)
	// Store persists wrapped keys (postgres.FieldKeyRingStore).
	Store fieldcrypt.WrappedKeyStore
	// Log receives the one plaintext-mode warning and the ready line.
	Log *slog.Logger
}

// Result is what Load decided.
type Result struct {
	Cipher *fieldcrypt.Cipher
	Mode   Mode
	// Versions are the key versions loaded (never the keys).
	Versions []uint32
	// KMSRing is set in ModeKMS (key rotation).
	KMSRing *fieldcrypt.KMSRing
}

// IsProduction is the platform rule (shared/accessauth): the first of
// APP_ENV, ENVIRONMENT, ENV that is set decides; prod/production is
// production, anything else is not.
func IsProduction(getenv func(string) string) bool {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		switch strings.ToLower(strings.TrimSpace(getenv(key))) {
		case "":
			continue
		case "prod", "production":
			return true
		default:
			return false
		}
	}
	return false
}

// environment is the KMS encryption-context label: the deployment's name,
// canonicalised so "prod" and "production" are one environment.
func environment(getenv func(string) string) string {
	for _, key := range []string{"APP_ENV", "ENVIRONMENT", "ENV"} {
		v := strings.ToLower(strings.TrimSpace(getenv(key)))
		switch v {
		case "":
			continue
		case "prod", "production":
			return "production"
		default:
			return v
		}
	}
	return "development"
}

// Load builds the cipher per the package rules.
func Load(ctx context.Context, d Deps) (*Result, error) {
	if d.Getenv == nil {
		return nil, fmt.Errorf("%w: no configuration source", ErrRefused)
	}
	log := d.Log
	if log == nil {
		log = slog.Default()
	}
	prod := IsProduction(d.Getenv)
	keyID := strings.TrimSpace(d.Getenv(EnvKMSKeyID))
	envKeys := strings.TrimSpace(d.Getenv(EnvFieldKeys))

	switch {
	case keyID != "" && envKeys != "":
		return nil, fmt.Errorf("%w: both %s and %s are set; set exactly one", ErrRefused, EnvKMSKeyID, EnvFieldKeys)
	case prod && envKeys != "":
		return nil, fmt.Errorf("%w: %s is a development keyring and is refused in production; set %s (KMS)", ErrRefused, EnvFieldKeys, EnvKMSKeyID)
	case prod && keyID == "":
		return nil, fmt.Errorf("%w: production needs a key ring for chat message content; set %s", ErrRefused, EnvKMSKeyID)
	}

	var res Result
	switch {
	case keyID != "":
		if d.NewWrapper == nil || d.Store == nil {
			return nil, fmt.Errorf("%w: KMS mode needs a key wrapper and the key-ring store", ErrRefused)
		}
		wrapper, err := d.NewWrapper(ctx, keyID)
		if err != nil {
			return nil, fmt.Errorf("%w: KMS client: %v", ErrRefused, err)
		}
		ring, err := fieldcrypt.OpenKMSRing(ctx, fieldcrypt.KMSRingConfig{
			Wrapper: wrapper, Store: d.Store, KMSKeyID: keyID,
			Purpose: KMSPurpose, RingScope: KMSRingScope, Environment: environment(d.Getenv),
		})
		if err != nil {
			return nil, fmt.Errorf("%w: KMS key ring: %v", ErrRefused, err)
		}
		c, err := fieldcrypt.New(ring)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrRefused, err)
		}
		res = Result{Cipher: c, Mode: ModeKMS, Versions: ring.Versions(), KMSRing: ring}
	case envKeys != "":
		ring, err := fieldcrypt.ParseKeyList(envKeys)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %v", ErrRefused, EnvFieldKeys, err)
		}
		c, err := fieldcrypt.New(ring)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrRefused, err)
		}
		res = Result{Cipher: c, Mode: ModeEnvKeys, Versions: ring.Versions()}
	default:
		log.Warn("field encryption is OFF: message content is stored in plaintext. Set " + EnvFieldKeys +
			" (development) or " + EnvKMSKeyID + " (KMS). Production refuses to boot like this.")
		return &Result{Cipher: fieldcrypt.Plaintext(), Mode: ModePlaintext}, nil
	}

	if err := Probe(ctx, res.Cipher); err != nil {
		return nil, fmt.Errorf("%w: readiness probe: %v", ErrRefused, err)
	}
	log.Info("field encryption ready", "mode", string(res.Mode), "key_versions", res.Versions)
	return &res, nil
}

// Probe seals and opens a fixed, non-identifying value under every scope.
func Probe(ctx context.Context, c *fieldcrypt.Cipher) error {
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	const probe = "fieldcrypt-readiness-probe"
	row := fieldcrypt.Bind("readiness")
	for _, scope := range contentcrypt.AllScopes {
		sealed, err := c.SealString(ctx, scope, row, probe)
		if err != nil {
			return fmt.Errorf("sealing %s: %w", scope, err)
		}
		if !fieldcrypt.IsSealed(sealed) {
			return fmt.Errorf("sealing %s produced no envelope", scope)
		}
		opened, err := c.OpenString(ctx, scope, row, sealed)
		if err != nil {
			return fmt.Errorf("opening %s: %w", scope, err)
		}
		if opened != probe {
			return fmt.Errorf("round trip mismatch for %s", scope)
		}
	}
	return nil
}
