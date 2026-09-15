package http

import (
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
)

// ResolveSelfieConfig reads the lane D5 selfie verification bars:
//
//	DATING_SELFIE_PASS_THRESHOLD        1-100, default 90
//	DATING_SELFIE_REVIEW_THRESHOLD      1-100, default 80, below the pass bar
//	DATING_SELFIE_MAX_ATTEMPTS_PER_DAY  1-50,  default 5 (rolling 24h)
//	DATING_SELFIE_REQUIRED_BLINKS       1-5,   default 2
//	DATING_SELFIE_MAX_VIDEO_MS          1000-10000, default 4000 (told to the
//	                                    client in the challenge; keep equal to
//	                                    media-service MEDIA_LIVENESS_MAX_DURATION_MS)
//
// Any malformed or inconsistent value is an error, on which main refuses to
// start (never a silently weaker bar).
func ResolveSelfieConfig(getenv func(string) string) (service.SelfieConfig, error) {
	cfg := service.DefaultSelfieConfig()
	num := func(key string, lo, hi float64, dst *float64) error {
		raw := strings.TrimSpace(getenv(key))
		if raw == "" {
			return nil
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f < lo || f > hi {
			return fmt.Errorf("%s must be a number from %g to %g, got %q", key, lo, hi, raw)
		}
		*dst = f
		return nil
	}
	if err := num("DATING_SELFIE_PASS_THRESHOLD", 1, 100, &cfg.PassThreshold); err != nil {
		return cfg, err
	}
	if err := num("DATING_SELFIE_REVIEW_THRESHOLD", 1, 100, &cfg.ReviewThreshold); err != nil {
		return cfg, err
	}
	if cfg.ReviewThreshold >= cfg.PassThreshold {
		return cfg, fmt.Errorf("DATING_SELFIE_REVIEW_THRESHOLD (%g) must be below DATING_SELFIE_PASS_THRESHOLD (%g)",
			cfg.ReviewThreshold, cfg.PassThreshold)
	}
	if raw := strings.TrimSpace(getenv("DATING_SELFIE_MAX_ATTEMPTS_PER_DAY")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 50 {
			return cfg, fmt.Errorf("DATING_SELFIE_MAX_ATTEMPTS_PER_DAY must be a whole number from 1 to 50, got %q", raw)
		}
		cfg.MaxAttemptsPerDay = n
	}
	if raw := strings.TrimSpace(getenv("DATING_SELFIE_REQUIRED_BLINKS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 5 {
			return cfg, fmt.Errorf("DATING_SELFIE_REQUIRED_BLINKS must be a whole number from 1 to 5, got %q", raw)
		}
		cfg.RequiredBlinks = n
	}
	if raw := strings.TrimSpace(getenv("DATING_SELFIE_MAX_VIDEO_MS")); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1000 || n > 10000 {
			return cfg, fmt.Errorf("DATING_SELFIE_MAX_VIDEO_MS must be a whole number from 1000 to 10000, got %q", raw)
		}
		cfg.MaxVideoDurationMs = n
	}
	return cfg, nil
}

// ResolvePhotoSafetyConfig reads the lane D6 photo safety bars:
//
//	DATING_PHOTO_MAX_PER_PROFILE         1-12, default 6 (rejected photos do not count)
//	DATING_PHOTO_EXPLICIT_LABELS         comma list of labels/parent categories that
//	                                     auto-reject (default service.DefaultExplicitPhotoLabels)
//	DATING_PHOTO_EXPLICIT_MIN_CONFIDENCE 50-100, default 80
//	DATING_PHOTO_REVIEW_LABELS           comma list sent to pending_review
//	                                     (default service.DefaultReviewPhotoLabels)
//	DATING_PHOTO_REVIEW_MIN_CONFIDENCE   50-100, default 80. media-service stores only
//	                                     labels at or above its scanner's MinConfidence
//	                                     (80), so a lower bar here has no effect alone.
//	DATING_PHOTO_REQUIRE_FACE            true|false, default true (primary with no face → review)
//	DATING_PHOTO_RECHECK_ENABLED         true|false, default true
//	DATING_PHOTO_RECHECK_INTERVAL_HOURS  1-168, default 24
//
// Any malformed value is an error, on which main refuses to start.
func ResolvePhotoSafetyConfig(getenv func(string) string) (service.PhotoSafetyConfig, error) {
	cfg := service.DefaultPhotoSafetyConfig()
	intIn := func(key string, lo, hi int, dst *int) error {
		raw := strings.TrimSpace(getenv(key))
		if raw == "" {
			return nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < lo || n > hi {
			return fmt.Errorf("%s must be a whole number from %d to %d, got %q", key, lo, hi, raw)
		}
		*dst = n
		return nil
	}
	confidence := func(key string, dst *float64) error {
		raw := strings.TrimSpace(getenv(key))
		if raw == "" {
			return nil
		}
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f < 50 || f > 100 {
			return fmt.Errorf("%s must be a number from 50 to 100, got %q", key, raw)
		}
		*dst = f
		return nil
	}
	labels := func(key string, dst *[]string) error {
		raw := strings.TrimSpace(getenv(key))
		if raw == "" {
			return nil
		}
		var out []string
		for _, part := range strings.Split(raw, ",") {
			if p := strings.TrimSpace(part); p != "" {
				out = append(out, p)
			}
		}
		if len(out) == 0 {
			return fmt.Errorf("%s must name at least one label, got %q", key, raw)
		}
		*dst = out
		return nil
	}
	boolean := func(key string, dst *bool) error {
		raw := strings.TrimSpace(getenv(key))
		if raw == "" {
			return nil
		}
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("%s must be true or false, got %q", key, raw)
		}
		*dst = b
		return nil
	}
	hours := int(cfg.RecheckInterval / time.Hour)
	for _, step := range []error{
		intIn("DATING_PHOTO_MAX_PER_PROFILE", 1, 12, &cfg.MaxPhotos),
		labels("DATING_PHOTO_EXPLICIT_LABELS", &cfg.ExplicitLabels),
		confidence("DATING_PHOTO_EXPLICIT_MIN_CONFIDENCE", &cfg.ExplicitMinConfidence),
		labels("DATING_PHOTO_REVIEW_LABELS", &cfg.ReviewLabels),
		confidence("DATING_PHOTO_REVIEW_MIN_CONFIDENCE", &cfg.ReviewMinConfidence),
		boolean("DATING_PHOTO_REQUIRE_FACE", &cfg.RequireFaceOnPrimary),
		boolean("DATING_PHOTO_RECHECK_ENABLED", &cfg.RecheckEnabled),
		intIn("DATING_PHOTO_RECHECK_INTERVAL_HOURS", 1, 168, &hours),
	} {
		if step != nil {
			return cfg, step
		}
	}
	cfg.RecheckInterval = time.Duration(hours) * time.Hour
	return cfg, nil
}

// DefaultMediaServiceURL is media-service's in-cluster address (port 8087).
const DefaultMediaServiceURL = "http://media-service:8087"

// ResolveMediaServiceURL reads MEDIA_SERVICE_URL (default
// DefaultMediaServiceURL). A set but malformed URL is refused.
func ResolveMediaServiceURL(getenv func(string) string) (string, error) {
	raw := strings.TrimSpace(getenv("MEDIA_SERVICE_URL"))
	if raw == "" {
		return DefaultMediaServiceURL, nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", fmt.Errorf("MEDIA_SERVICE_URL must be an absolute http(s) URL")
	}
	return strings.TrimRight(raw, "/"), nil
}

// DIGILOCKER_MODE values.
const (
	DigiLockerModeHTTP     = "http"
	DigiLockerModeMock     = "mock"
	DigiLockerModeDisabled = "disabled"
)

// ResolveDigiLockerMode applies food-service's DIGILOCKER_MODE rule to the
// optional Aadhaar step: http | mock | disabled. Unset means mock in
// local/dev (with a warning) and is refused elsewhere; mock is refused unless
// ENV is local/dev/development (a blank ENV is not local); http needs
// DIGILOCKER_BASE_URL and DIGILOCKER_API_KEY. disabled answers the Aadhaar
// routes with 503 AADHAAR_DISABLED.
func ResolveDigiLockerMode(getenv func(string) string) (mode, warning string, err error) {
	env := strings.TrimSpace(getenv("ENV"))
	mode = strings.ToLower(strings.TrimSpace(getenv("DIGILOCKER_MODE")))
	switch mode {
	case "":
		if !IsLocalEnv(env) {
			return "", "", fmt.Errorf("DIGILOCKER_MODE is required unless ENV is local or dev (ENV=%q): set http or disabled", env)
		}
		return DigiLockerModeMock, "dating-service: DIGILOCKER_MODE not set (ENV=" + env + ") — using the DigiLocker mock. Local/dev only.", nil
	case DigiLockerModeMock:
		if !IsLocalEnv(env) {
			return "", "", fmt.Errorf("DIGILOCKER_MODE=mock is refused unless ENV is local, dev or development (ENV=%q)", env)
		}
		return mode, "dating-service: DigiLocker mock client active (ENV=" + env + "). Local/dev only.", nil
	case DigiLockerModeHTTP:
		var missing []string
		for _, k := range []string{"DIGILOCKER_BASE_URL", "DIGILOCKER_API_KEY"} {
			if strings.TrimSpace(getenv(k)) == "" {
				missing = append(missing, k)
			}
		}
		if len(missing) > 0 {
			return "", "", fmt.Errorf("DIGILOCKER_MODE=http needs %s", strings.Join(missing, ", "))
		}
		return mode, "", nil
	case DigiLockerModeDisabled:
		return mode, "", nil
	}
	return "", "", fmt.Errorf("unknown DIGILOCKER_MODE %q (want http, mock or disabled)", mode)
}

// ResolveOpenMatchDedupePolicy reads DATING_DEDUPE_OPEN_MATCHES and ENV for
// the boot cleanup of duplicate open matches. Local/dev (IsLocalEnv) always
// closes duplicates; elsewhere only DATING_DEDUPE_OPEN_MATCHES=true does. A
// blank flag is false; any value strconv.ParseBool rejects is an error, on
// which main refuses to start.
func ResolveOpenMatchDedupePolicy(getenv func(string) string) (service.OpenMatchDedupePolicy, error) {
	env := strings.TrimSpace(getenv("ENV"))
	policy := service.OpenMatchDedupePolicy{Env: env, LocalEnv: IsLocalEnv(env)}
	raw := strings.TrimSpace(getenv("DATING_DEDUPE_OPEN_MATCHES"))
	if raw == "" {
		return policy, nil
	}
	enabled, err := strconv.ParseBool(raw)
	if err != nil {
		return policy, fmt.Errorf("DATING_DEDUPE_OPEN_MATCHES must be true or false, got %q", raw)
	}
	policy.FlagEnabled = enabled
	return policy, nil
}

// Decline cooldown bounds for DATING_DECLINE_COOLDOWN_DAYS.
const (
	MinDeclineCooldownDays = 1
	MaxDeclineCooldownDays = 365
)

// ResolveDeclineCooldown reads DATING_DECLINE_COOLDOWN_DAYS. Blank means
// store.DeclineCooldown (30 days); otherwise a whole number of days from 1 to
// 365, else an error on which main refuses to start.
func ResolveDeclineCooldown(getenv func(string) string) (time.Duration, error) {
	raw := strings.TrimSpace(getenv("DATING_DECLINE_COOLDOWN_DAYS"))
	if raw == "" {
		return store.DeclineCooldown, nil
	}
	days, err := strconv.Atoi(raw)
	if err != nil || days < MinDeclineCooldownDays || days > MaxDeclineCooldownDays {
		return 0, fmt.Errorf("DATING_DECLINE_COOLDOWN_DAYS must be a whole number of days from %d to %d, got %q",
			MinDeclineCooldownDays, MaxDeclineCooldownDays, raw)
	}
	return time.Duration(days) * 24 * time.Hour, nil
}

// ResolveLocationPrivacyConfig reads the lane D7 limits:
//
//	DATING_LOCATION_CHANGE_MIN_INTERVAL_MINUTES  1-1440, default 15
//	DATING_LOCATION_MAX_CHANGES_PER_DAY          1-100,  default 10 (rolling 24h)
//	DATING_EXPLAIN_DAILY_LIMIT                   1-1000, default 60 (rolling 24h)
//
// Any malformed value is an error, on which main refuses to start (never a
// silently weaker limit).
func ResolveLocationPrivacyConfig(getenv func(string) string) (service.LocationPrivacyConfig, error) {
	cfg := service.DefaultLocationPrivacyConfig()
	intIn := func(key string, lo, hi int, dst *int) error {
		raw := strings.TrimSpace(getenv(key))
		if raw == "" {
			return nil
		}
		n, err := strconv.Atoi(raw)
		if err != nil || n < lo || n > hi {
			return fmt.Errorf("%s must be a whole number from %d to %d, got %q", key, lo, hi, raw)
		}
		*dst = n
		return nil
	}
	minutes := int(cfg.LocationChangeMinInterval / time.Minute)
	if err := intIn("DATING_LOCATION_CHANGE_MIN_INTERVAL_MINUTES", 1, 1440, &minutes); err != nil {
		return cfg, err
	}
	cfg.LocationChangeMinInterval = time.Duration(minutes) * time.Minute
	if err := intIn("DATING_LOCATION_MAX_CHANGES_PER_DAY", 1, 100, &cfg.LocationChangesPerDay); err != nil {
		return cfg, err
	}
	if err := intIn("DATING_EXPLAIN_DAILY_LIMIT", 1, 1000, &cfg.ExplainDailyLimit); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// envIntIn reads a whole number in [lo, hi]; blank keeps *dst.
func envIntIn(getenv func(string) string, key string, lo, hi int, dst *int) error {
	raw := strings.TrimSpace(getenv(key))
	if raw == "" {
		return nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < lo || n > hi {
		return fmt.Errorf("%s must be a whole number from %d to %d, got %q", key, lo, hi, raw)
	}
	*dst = n
	return nil
}

// ResolveSafetyConfig reads the lane D8 limits:
//
//	DATING_PANIC_DEDUPE_SECONDS        30-900, default 120 (same incident)
//	DATING_PANIC_DAILY_LIMIT           1-50,   default 5 (rolling 24h; above it
//	                                   an incident is suspected abuse, not paged)
//	DATING_REPORT_DAILY_LIMIT          1-100,  default 10 per reporter (rolling 24h)
//	DATING_LOCATION_SHARE_MAX_MINUTES  15-480, default 120
//
// Any malformed value is an error, on which main refuses to start.
func ResolveSafetyConfig(getenv func(string) string) (service.SafetyConfig, error) {
	cfg := service.DefaultSafetyConfig()
	secs := int(cfg.PanicDedupeWindow / time.Second)
	if err := envIntIn(getenv, "DATING_PANIC_DEDUPE_SECONDS", 30, 900, &secs); err != nil {
		return cfg, err
	}
	cfg.PanicDedupeWindow = time.Duration(secs) * time.Second
	if err := envIntIn(getenv, "DATING_PANIC_DAILY_LIMIT", 1, 50, &cfg.PanicDailyLimit); err != nil {
		return cfg, err
	}
	if err := envIntIn(getenv, "DATING_REPORT_DAILY_LIMIT", 1, 100, &cfg.ReportDailyLimit); err != nil {
		return cfg, err
	}
	maxMinutes := int(cfg.LocationShareMax / time.Minute)
	if err := envIntIn(getenv, "DATING_LOCATION_SHARE_MAX_MINUTES", 15, 480, &maxMinutes); err != nil {
		return cfg, err
	}
	cfg.LocationShareMax = time.Duration(maxMinutes) * time.Minute
	if cfg.LocationShareDefault > cfg.LocationShareMax {
		cfg.LocationShareDefault = cfg.LocationShareMax
	}
	return cfg, nil
}

// MinEvidenceKeyBytes is the shortest DATING_EVIDENCE_HMAC_KEY accepted.
const MinEvidenceKeyBytes = 32

// Evidence retention bounds for DATING_EVIDENCE_RETENTION_DAYS.
const (
	MinEvidenceRetentionDays     = 30
	MaxEvidenceRetentionDays     = 3650
	DefaultEvidenceRetentionDays = 180
)

// ResolveEvidenceConfig reads the lane D8 evidence settings:
//
//	DATING_EVIDENCE_HMAC_KEY        secret, at least 32 bytes. Keys the stable
//	                                subject token that replaces a purged user's
//	                                id in retained evidence and the hashes of
//	                                retained device/IP signals. Required unless
//	                                ENV is local/dev; changing it breaks the
//	                                link between old and new retained rows.
//	DATING_EVIDENCE_RETENTION_DAYS  30-3650, default 180.
//
// In local/dev an unset key returns a nil key and a warning (the store's
// development key applies). A set but short key is refused everywhere.
func ResolveEvidenceConfig(getenv func(string) string) (key []byte, retention time.Duration, warning string, err error) {
	days := DefaultEvidenceRetentionDays
	if err := envIntIn(getenv, "DATING_EVIDENCE_RETENTION_DAYS", MinEvidenceRetentionDays, MaxEvidenceRetentionDays, &days); err != nil {
		return nil, 0, "", err
	}
	retention = time.Duration(days) * 24 * time.Hour
	raw := strings.TrimSpace(getenv("DATING_EVIDENCE_HMAC_KEY"))
	if raw != "" {
		if len(raw) < MinEvidenceKeyBytes {
			return nil, 0, "", fmt.Errorf("DATING_EVIDENCE_HMAC_KEY must be at least %d bytes", MinEvidenceKeyBytes)
		}
		return []byte(raw), retention, "", nil
	}
	env := strings.TrimSpace(getenv("ENV"))
	if !IsLocalEnv(env) {
		return nil, 0, "", fmt.Errorf("DATING_EVIDENCE_HMAC_KEY is required unless ENV is local or dev (ENV=%q): "+
			"without it retained evidence is keyed by a public development key", env)
	}
	return nil, retention, "dating-service: DATING_EVIDENCE_HMAC_KEY not set (ENV=" + env + ") — " +
		"retained evidence uses the development key. Local/dev only.", nil
}

// ResolveTrustSafetyURL reads TRUST_SAFETY_SERVICE_URL, the base of the
// internal grievance route every report links through. Required unless ENV
// is local/dev (where unset leaves reports pending for the retry worker);
// a set but malformed URL is refused everywhere.
func ResolveTrustSafetyURL(getenv func(string) string) (baseURL, warning string, err error) {
	raw := strings.TrimSpace(getenv("TRUST_SAFETY_SERVICE_URL"))
	if raw == "" {
		env := strings.TrimSpace(getenv("ENV"))
		if !IsLocalEnv(env) {
			return "", "", fmt.Errorf("TRUST_SAFETY_SERVICE_URL is required unless ENV is local or dev (ENV=%q): "+
				"without it no dating report gets a grievance", env)
		}
		return "", "dating-service: TRUST_SAFETY_SERVICE_URL not set (ENV=" + env + ") — reports stay pending for the grievance retry worker. Local/dev only.", nil
	}
	u, perr := url.Parse(raw)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return "", "", fmt.Errorf("TRUST_SAFETY_SERVICE_URL must be an absolute http(s) URL")
	}
	return strings.TrimRight(raw, "/"), "", nil
}
