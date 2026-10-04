// Package config reads doorstep-service's environment (the names pinned by
// the platform lane: compose, deploy values and the secrets seeder) and
// refuses a production process that is missing a value it cannot run safely
// without, or that selects a development mock.
//
// Production is fail-closed (internal/runtimeenv): anything but an explicit
// local/dev/development environment. Every variable is parsed and validated
// here even when the A1 code does not use it yet, so later lanes only read
// fields.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/doorstep-service/internal/digilocker"
	"github.com/atpost/doorstep-service/internal/runtimeenv"
	"github.com/atpost/doorstep-service/internal/tax"
	"github.com/atpost/shared/pii"
	"github.com/atpost/shared/servicetoken"
)

// Defaults.
const (
	DefaultPort       = "8122"
	DefaultKafkaTopic = "doorstep.events"
	DefaultQuoteTTL   = 15 * time.Minute

	// Development defaults (compose service names). Production must set each.
	devKafkaBrokers   = "redpanda:9092"
	devRedisAddr      = "redis:6379"
	devIdentityAuth   = "http://identity-auth:8081"
	devMediaService   = "http://media-service:8087"
	devPaymentsServic = "http://payments-service:8102"
)

// Verification provider modes.
const (
	DigiLockerMock     = "mock"
	DigiLockerDisabled = "disabled"
	DigiLockerHTTP     = "http"

	FaceCompareMock = "mock"
	FaceCompareHTTP = "http"

	BackgroundCheckMock     = "mock"
	BackgroundCheckUploaded = "uploaded_document"
	BackgroundCheckProvider = "provider"

	// ServiceTokenIssuer is doorstep's name as a payments-service caller.
	ServiceTokenIssuer = "doorstep-service"
)

// PIIKey is one parsed DOORSTEP_PII_KEYS entry.
type PIIKey struct {
	Version uint32
	Key     []byte
}

// Config is the boot configuration.
type Config struct {
	Production bool

	HTTPPort     string
	PostgresDSN  string
	RedisAddr    string
	KafkaBrokers []string
	KafkaTopic   string
	// InternalKey is X-Internal-Service-Key; every /v1/doorstep route but
	// the admin-token family sits behind it.
	InternalKey string
	// RealtimeTokenSecret signs realtime topic tokens (shared/realtime);
	// outside production it falls back to InternalKey, as rider and food do.
	RealtimeTokenSecret string

	IdentityAuthURL    string
	MediaServiceURL    string
	PaymentsServiceURL string

	// ServiceTokenKey/KID are doorstep's PRIVATE ed25519 payments token
	// (application doorstep). Empty outside production: payment routes 503.
	ServiceTokenKey string
	ServiceTokenKID string

	PIIKeys       []PIIKey
	PIILookupSalt []byte

	// PlatformGSTIN is the platform's e-commerce-operator GSTIN (liable under
	// s.9(5) for the _VIA_ECO categories).
	PlatformGSTIN string

	DigiLockerMode         string
	DigiLockerClientID     string
	DigiLockerClientSecret string
	// DigiLockerAuthorizeURL / DigiLockerTokenURL default to DigiLocker's
	// public endpoints (DIGILOCKER_AUTHORIZE_URL / DIGILOCKER_TOKEN_URL).
	DigiLockerAuthorizeURL string
	DigiLockerTokenURL     string
	FaceCompareMode        string
	SelfieMinSimilarity    int
	BackgroundCheckMode    string
	PublicBaseURL          string
	ProAppLinkURL          string

	QuoteTTL time.Duration
	// DevSeed seeds the Hyderabad catalogue at boot. Development only.
	DevSeed bool
}

// FromEnv reads and validates the configuration. Error messages name the
// variable, never its value.
func FromEnv(getenv func(string) string) (Config, error) {
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	prod := runtimeenv.IsProduction(getenv)
	cfg := Config{
		Production:             prod,
		HTTPPort:               orDefault(get("HTTP_PORT"), DefaultPort),
		PostgresDSN:            get("POSTGRES_DSN"),
		KafkaTopic:             orDefault(get("KAFKA_TOPIC"), DefaultKafkaTopic),
		InternalKey:            get("INTERNAL_SERVICE_KEY"),
		RealtimeTokenSecret:    get("REALTIME_TOKEN_SECRET"),
		ServiceTokenKey:        get("DOORSTEP_SERVICE_TOKEN_KEY"),
		ServiceTokenKID:        get("DOORSTEP_SERVICE_TOKEN_KID"),
		PlatformGSTIN:          get("DOORSTEP_PLATFORM_GSTIN"),
		DigiLockerClientID:     get("DIGILOCKER_CLIENT_ID"),
		DigiLockerClientSecret: get("DIGILOCKER_CLIENT_SECRET"),
		DigiLockerAuthorizeURL: orDefault(get("DIGILOCKER_AUTHORIZE_URL"), digilocker.DefaultAuthorizeURL),
		DigiLockerTokenURL:     orDefault(get("DIGILOCKER_TOKEN_URL"), digilocker.DefaultTokenURL),
		PublicBaseURL:          get("DOORSTEP_PUBLIC_BASE_URL"),
		ProAppLinkURL:          get("DOORSTEP_PRO_APP_LINK_URL"),
		QuoteTTL:               DefaultQuoteTTL,
		SelfieMinSimilarity:    80,
	}
	var errs []error
	fail := func(format string, a ...any) { errs = append(errs, fmt.Errorf(format, a...)) }
	// need returns v, the dev default outside production, or records a
	// production refusal.
	need := func(name, v, devDefault, why string) string {
		if v != "" {
			return v
		}
		if prod {
			fail("%s is required in production: %s", name, why)
			return ""
		}
		return devDefault
	}

	if cfg.PostgresDSN == "" {
		fail("POSTGRES_DSN is required")
	}
	brokers := need("KAFKA_BROKERS", get("KAFKA_BROKERS"), devKafkaBrokers, "the outbox would publish nowhere")
	for _, b := range strings.Split(brokers, ",") {
		if b = strings.TrimSpace(b); b != "" {
			cfg.KafkaBrokers = append(cfg.KafkaBrokers, b)
		}
	}
	cfg.RedisAddr = need("REDIS_ADDR", get("REDIS_ADDR"), devRedisAddr, "realtime and presence use Redis")
	if prod && cfg.InternalKey == "" {
		fail("INTERNAL_SERVICE_KEY is required in production: /v1/doorstep trusts gateway identity headers")
	}
	cfg.RealtimeTokenSecret = need("REALTIME_TOKEN_SECRET", cfg.RealtimeTokenSecret, cfg.InternalKey,
		"notification-service verifies doorstep's realtime tokens with it")

	for _, u := range []struct {
		name, devDefault string
		dst              *string
		why              string
	}{
		{"IDENTITY_AUTH_URL", devIdentityAuth, &cfg.IdentityAuthURL, "service_professional roles are granted there"},
		{"MEDIA_SERVICE_URL", devMediaService, &cfg.MediaServiceURL, "photos, selfies and the face compare go there"},
		{"PAYMENTS_SERVICE_URL", devPaymentsServic, &cfg.PaymentsServiceURL, "every payment and refund goes there"},
	} {
		*u.dst = need(u.name, get(u.name), u.devDefault, u.why)
		if *u.dst != "" && !isHTTPURL(*u.dst, false) {
			fail("%s must be an absolute http(s) URL", u.name)
		}
	}

	// Payments token: both halves or neither; production needs it.
	switch {
	case cfg.ServiceTokenKey == "" && cfg.ServiceTokenKID == "":
		if prod {
			fail("DOORSTEP_SERVICE_TOKEN_KEY and DOORSTEP_SERVICE_TOKEN_KID are required in production: bookings are paid through payments-service")
		}
	case cfg.ServiceTokenKey == "" || cfg.ServiceTokenKID == "":
		fail("DOORSTEP_SERVICE_TOKEN_KEY and DOORSTEP_SERVICE_TOKEN_KID must be set together")
	default:
		if _, err := servicetoken.NewSignerFromBase64(ServiceTokenIssuer, cfg.ServiceTokenKID, cfg.ServiceTokenKey); err != nil {
			fail("DOORSTEP_SERVICE_TOKEN_KEY is not a valid base64 ed25519 private key")
		}
	}

	// PII sealing: keys and lookup salt together; production needs both.
	rawKeys, rawSalt := get("DOORSTEP_PII_KEYS"), getenv("DOORSTEP_PII_LOOKUP_SALT")
	switch {
	case rawKeys == "" && rawSalt == "":
		if prod {
			fail("DOORSTEP_PII_KEYS and DOORSTEP_PII_LOOKUP_SALT are required in production: addresses, Aadhaar-derived fields, PAN and payout accounts are sealed")
		}
	case rawKeys == "" || rawSalt == "":
		fail("DOORSTEP_PII_KEYS and DOORSTEP_PII_LOOKUP_SALT must be set together")
	default:
		keys, err := ParsePIIKeys(rawKeys)
		if err != nil {
			errs = append(errs, err)
		}
		cfg.PIIKeys = keys
		if len(rawSalt) < pii.MinLookupSaltSize {
			fail("DOORSTEP_PII_LOOKUP_SALT must be at least %d bytes", pii.MinLookupSaltSize)
		}
		cfg.PIILookupSalt = []byte(rawSalt)
	}

	if cfg.PlatformGSTIN == "" {
		if prod {
			fail("DOORSTEP_PLATFORM_GSTIN is required in production: the platform is liable for GST under s.9(5) on the _VIA_ECO categories")
		}
	} else if _, err := tax.NewGST(nil, cfg.PlatformGSTIN); err != nil {
		fail("DOORSTEP_PLATFORM_GSTIN is not a valid GSTIN")
	}

	// Verification providers. Development defaults are the mocks; production
	// defaults are the safe modes, and a mock is refused there.
	cfg.DigiLockerMode = strings.ToLower(orDefault(get("DIGILOCKER_MODE"), pick(prod, DigiLockerDisabled, DigiLockerMock)))
	switch cfg.DigiLockerMode {
	case DigiLockerMock:
		if prod {
			fail("DIGILOCKER_MODE=mock is refused in production: it verifies every Aadhaar")
		}
	case DigiLockerDisabled:
	case DigiLockerHTTP:
		if cfg.DigiLockerClientID == "" || cfg.DigiLockerClientSecret == "" {
			fail("DIGILOCKER_MODE=http needs DIGILOCKER_CLIENT_ID and DIGILOCKER_CLIENT_SECRET")
		}
		if cfg.PublicBaseURL == "" {
			fail("DIGILOCKER_MODE=http needs DOORSTEP_PUBLIC_BASE_URL for the DigiLocker return")
		}
	default:
		fail("DIGILOCKER_MODE must be mock, disabled or http")
	}

	cfg.FaceCompareMode = strings.ToLower(orDefault(get("DOORSTEP_FACE_COMPARE_MODE"), pick(prod, FaceCompareHTTP, FaceCompareMock)))
	switch cfg.FaceCompareMode {
	case FaceCompareMock:
		if prod {
			fail("DOORSTEP_FACE_COMPARE_MODE=mock is refused in production: it matches every selfie")
		}
	case FaceCompareHTTP:
	default:
		fail("DOORSTEP_FACE_COMPARE_MODE must be mock or http")
	}
	if raw := get("DOORSTEP_SELFIE_MIN_SIMILARITY"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 50 || n > 100 {
			fail("DOORSTEP_SELFIE_MIN_SIMILARITY must be a whole number between 50 and 100")
		} else {
			cfg.SelfieMinSimilarity = n
		}
	}

	cfg.BackgroundCheckMode = strings.ToLower(orDefault(get("DOORSTEP_BACKGROUND_CHECK_MODE"), pick(prod, BackgroundCheckUploaded, BackgroundCheckMock)))
	switch cfg.BackgroundCheckMode {
	case BackgroundCheckMock:
		if prod {
			fail("DOORSTEP_BACKGROUND_CHECK_MODE=mock is refused in production: it clears every professional")
		}
	case BackgroundCheckUploaded:
	case BackgroundCheckProvider:
		fail("DOORSTEP_BACKGROUND_CHECK_MODE=provider has no vendor integrated yet; use uploaded_document")
	default:
		fail("DOORSTEP_BACKGROUND_CHECK_MODE must be mock, uploaded_document or provider")
	}

	for _, u := range []struct{ name, v string }{
		{"DOORSTEP_PUBLIC_BASE_URL", cfg.PublicBaseURL}, {"DOORSTEP_PRO_APP_LINK_URL", cfg.ProAppLinkURL},
		{"DIGILOCKER_AUTHORIZE_URL", cfg.DigiLockerAuthorizeURL}, {"DIGILOCKER_TOKEN_URL", cfg.DigiLockerTokenURL},
	} {
		if u.v != "" && !isHTTPURL(u.v, prod) {
			fail("%s must be an absolute %s URL", u.name, pick(prod, "https", "http(s)"))
		}
	}

	if raw := get("DOORSTEP_QUOTE_TTL_MINUTES"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 120 {
			fail("DOORSTEP_QUOTE_TTL_MINUTES must be a whole number of minutes between 1 and 120")
		} else {
			cfg.QuoteTTL = time.Duration(n) * time.Minute
		}
	}

	if raw := get("DOORSTEP_DEV_SEED"); raw != "" {
		on, err := strconv.ParseBool(raw)
		switch {
		case err != nil:
			fail("DOORSTEP_DEV_SEED must be true or false")
		case on && prod:
			fail("DOORSTEP_DEV_SEED=true is refused outside local/dev/development: the Hyderabad seed is dev data")
		default:
			cfg.DevSeed = on
		}
	}

	if len(errs) > 0 {
		return Config{}, fmt.Errorf("doorstep-service configuration: %w", errors.Join(errs...))
	}
	return cfg, nil
}

// ParsePIIKeys reads "v1:<key>[,v2:<key>...]" (32-byte keys, base64 or hex;
// the food/rider format). Errors name the entry position, never its content.
func ParsePIIKeys(raw string) ([]PIIKey, error) {
	const name = "DOORSTEP_PII_KEYS"
	seen := map[uint32]bool{}
	var out []PIIKey
	for i, entry := range strings.Split(raw, ",") {
		pos := i + 1
		entry = strings.TrimSpace(entry)
		ver, key, ok := strings.Cut(entry, ":")
		if !ok || len(ver) < 2 || ver[0] != 'v' {
			return nil, fmt.Errorf("%s entry %d must look like v<version>:<key>", name, pos)
		}
		n, err := strconv.ParseUint(ver[1:], 10, 32)
		if err != nil || n == 0 {
			return nil, fmt.Errorf("%s entry %d: version must be a whole number from 1", name, pos)
		}
		if seen[uint32(n)] {
			return nil, fmt.Errorf("%s entry %d: version %d appears twice", name, pos, n)
		}
		seen[uint32(n)] = true
		decoded, err := pii.DecodeKey(key)
		if err != nil {
			return nil, fmt.Errorf("%s entry %d: key must be 32 bytes, base64 or hex", name, pos)
		}
		out = append(out, PIIKey{Version: uint32(n), Key: decoded})
	}
	return out, nil
}

func orDefault(v, d string) string {
	if v == "" {
		return d
	}
	return v
}

func pick(cond bool, a, b string) string {
	if cond {
		return a
	}
	return b
}

func isHTTPURL(raw string, httpsOnly bool) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return false
	}
	if httpsOnly {
		return u.Scheme == "https"
	}
	return u.Scheme == "http" || u.Scheme == "https"
}

// DigiLockerRedirectURI is the redirect_uri registered with DigiLocker: the
// pro app's link (DOORSTEP_PRO_APP_LINK_URL) when set, else the public base
// URL's /doorstep-pro/digilocker path, which the pro app claims as an App
// Link. The app reads code and state from it and calls
// POST /v1/doorstep/pro/digilocker/callback.
func (c Config) DigiLockerRedirectURI() string {
	if c.ProAppLinkURL != "" {
		return c.ProAppLinkURL
	}
	if c.PublicBaseURL == "" {
		return ""
	}
	return strings.TrimRight(c.PublicBaseURL, "/") + "/doorstep-pro/digilocker"
}
