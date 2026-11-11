package personverify

import (
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/atpost/identity-shared/crypto"
	"github.com/atpost/identity-shared/digilocker"
)

// Environment. Person verification is OFF unless
// PERSON_VERIFICATION_ENABLED is "true": every route answers 503 and no
// provider setting is read or required.
const (
	EnvEnabled = "PERSON_VERIFICATION_ENABLED"
	// EnvHashKey is the HMAC key for the identity and name hashes: 64 hex
	// characters (32 bytes). Rotating it un-links every verified person
	// from their hash, so it is set once per environment and kept.
	EnvHashKey = "PERSON_VERIFICATION_HASH_KEY" //nolint:gosec // G101: name of the env variable, not its value
	// EnvSealKey is the AES-256-GCM key sealing the PKCE verifier and the
	// DigiLocker photo while an attempt is open: 64 hex characters.
	EnvSealKey = "PERSON_VERIFICATION_SEAL_KEY" //nolint:gosec // G101: name of the env variable, not its value
	// EnvRedirectURI is the URL registered with DigiLocker. It is a client
	// page (web or the Android App Link) that posts code and state to the
	// signed-in callback route; no server route is public.
	EnvRedirectURI = "PERSON_VERIFICATION_REDIRECT_URI"
)

// envModeVars matches internal/http's boot rule: the first non-blank one
// decides, and only local, dev and development are non-production.
var envModeVars = []string{"APP_ENV", "ENVIRONMENT", "ENV"}

// IsProduction is true unless the environment is local, dev or development.
// A blank environment is production (fails closed).
func IsProduction(getenv func(string) string) bool {
	for _, k := range envModeVars {
		if v := strings.ToLower(strings.TrimSpace(getenv(k))); v != "" {
			return v != "local" && v != "dev" && v != "development"
		}
	}
	return true
}

// Config is what the service runs with.
type Config struct {
	Enabled       bool
	Production    bool
	HashKey       []byte
	Seal          *crypto.SecretBox
	DigiLocker    digilocker.Settings
	Face          FaceVerifier
	MinSimilarity float64
}

// ConfigFromEnv reads the configuration. Disabled, only the hash key is
// read (when set), so a verified account renamed while the feature is off
// can still keep a tick its new name matches. Enabled, every dependency
// must be present: the hash and seal keys, a DigiLocker client and a face
// provider. Errors name variables, never values.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{Production: IsProduction(getenv), MinSimilarity: DefaultMinSimilarity}
	if raw := strings.TrimSpace(getenv(EnvHashKey)); raw != "" {
		key, err := parseKey(raw)
		if err != nil {
			return Config{}, fmt.Errorf("%s %w", EnvHashKey, err)
		}
		cfg.HashKey = key
	}
	switch v := strings.ToLower(strings.TrimSpace(getenv(EnvEnabled))); v {
	case "", "false", "0", "no", "off":
		return cfg, nil
	case "true", "1", "yes", "on":
		cfg.Enabled = true
	default:
		return Config{}, fmt.Errorf("%s must be true or false", EnvEnabled)
	}

	if cfg.HashKey == nil {
		return Config{}, fmt.Errorf("%s=true needs %s", EnvEnabled, EnvHashKey)
	}
	rawSeal := strings.TrimSpace(getenv(EnvSealKey))
	if _, err := parseKey(rawSeal); err != nil {
		return Config{}, fmt.Errorf("%s=true needs %s: %w", EnvEnabled, EnvSealKey, err)
	}
	box, err := crypto.NewSecretBox(strings.ToLower(rawSeal))
	if err != nil {
		return Config{}, fmt.Errorf("%s is not a usable key", EnvSealKey)
	}
	cfg.Seal = box

	dl, err := digilocker.SettingsFromEnv(getenv, cfg.Production, getenv(EnvRedirectURI))
	if err != nil {
		return Config{}, err
	}
	if dl.Client == nil {
		return Config{}, fmt.Errorf("%s=true needs %s set to http (or mock outside production)", EnvEnabled, digilocker.EnvMode)
	}
	cfg.DigiLocker = dl

	face, min, err := FaceFromEnv(getenv, cfg.Production)
	if err != nil {
		return Config{}, err
	}
	if face == nil {
		return Config{}, fmt.Errorf("%s=true needs a face provider (%s)", EnvEnabled, EnvFaceMode)
	}
	cfg.Face, cfg.MinSimilarity = face, min
	return cfg, nil
}

// parseKey accepts exactly 64 hex characters.
func parseKey(raw string) ([]byte, error) {
	if len(raw) != 64 {
		return nil, fmt.Errorf("must be 64 hex characters")
	}
	key, err := hex.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("must be 64 hex characters")
	}
	return key, nil
}
