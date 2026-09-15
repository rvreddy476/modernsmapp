package processing

import (
	"fmt"
	"strconv"
	"strings"
)

// Face comparison configuration (lane D5).
//
//	MEDIA_FACE_COMPARE_ENABLED   true|false (default false). Off: the
//	                             internal compare route is not registered.
//	MEDIA_FACE_COMPARE_BACKEND   rekognition | mock. Required when enabled.
//	MEDIA_FACE_MATCH_THRESHOLD   1-100 (default 90). Only sets the `match`
//	                             flag in the response; callers apply their
//	                             own pass/review bars to `similarity`.
//
// Boot refuses (the error from ResolveFaceCompareSettings) when enabled and:
//   - no backend is named, or it is unknown;
//   - the backend is mock and ENV is not local/dev/development (a blank ENV
//     is not local), or any of DEPLOY_ENV/APP_ENV/ENVIRONMENT names a
//     non-local environment;
//   - the backend is rekognition without AWS_REGION, with static AWS keys in
//     the environment, or in production without IRSA (AWS_ROLE_ARN +
//     AWS_WEB_IDENTITY_TOKEN_FILE) — the same rules as the content scanner;
//   - INTERNAL_SERVICE_KEY is empty (the route could not be authenticated).
const (
	EnvFaceCompareEnabled  = "MEDIA_FACE_COMPARE_ENABLED"
	EnvFaceCompareBackend  = "MEDIA_FACE_COMPARE_BACKEND"
	EnvFaceMatchThreshold  = "MEDIA_FACE_MATCH_THRESHOLD"
	FaceBackendRekognition = "rekognition"
	FaceBackendMock        = "mock"

	DefaultFaceMatchThreshold = 90.0
)

// FaceCompareSettings is the resolved configuration. Liveness shares the
// enable flag and backend: the same switch turns on both internal routes.
type FaceCompareSettings struct {
	Enabled        bool
	Backend        string
	Region         string
	MatchThreshold float64
	Liveness       LivenessConfig
}

// Blink liveness tuning (all optional; defaults in DefaultLivenessConfig).
const (
	EnvLivenessSampleFPS       = "MEDIA_LIVENESS_SAMPLE_FPS"       // 1-30, default 8
	EnvLivenessMaxFrames       = "MEDIA_LIVENESS_MAX_FRAMES"       // 4-60, default 40
	EnvLivenessMaxDurationMs   = "MEDIA_LIVENESS_MAX_DURATION_MS"  // 1000-10000, default 4000
	EnvLivenessEyesConfidence  = "MEDIA_LIVENESS_EYES_CONFIDENCE"  // 50-100, default 80
	EnvLivenessRequiredBlinks  = "MEDIA_LIVENESS_REQUIRED_BLINKS"  // 1-5, default 2
	EnvLivenessMinFrames       = "MEDIA_LIVENESS_MIN_FRAMES"       // 2-60, default 8
	EnvLivenessFaceConsistency = "MEDIA_LIVENESS_FACE_CONSISTENCY" // 50-100, default 90
)

func envIntInRange(getenv func(string) string, key string, lo, hi int, dst *int) error {
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

func envFloatInRange(getenv func(string) string, key string, lo, hi float64, dst *float64) error {
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

// resolveLivenessConfig reads MEDIA_LIVENESS_*.
func resolveLivenessConfig(getenv func(string) string) (LivenessConfig, error) {
	c := DefaultLivenessConfig()
	for _, step := range []error{
		envIntInRange(getenv, EnvLivenessSampleFPS, 1, 30, &c.SampleFPS),
		envIntInRange(getenv, EnvLivenessMaxFrames, 4, 60, &c.MaxFrames),
		envIntInRange(getenv, EnvLivenessMaxDurationMs, 1000, 10000, &c.MaxDurationMs),
		envFloatInRange(getenv, EnvLivenessEyesConfidence, 50, 100, &c.EyesOpenConfidence),
		envIntInRange(getenv, EnvLivenessRequiredBlinks, 1, 5, &c.RequiredBlinks),
		envIntInRange(getenv, EnvLivenessMinFrames, 2, 60, &c.MinConfidentFrames),
		envFloatInRange(getenv, EnvLivenessFaceConsistency, 50, 100, &c.FaceConsistency),
	} {
		if step != nil {
			return c, step
		}
	}
	if c.MinConfidentFrames > c.MaxFrames {
		return c, fmt.Errorf("%s (%d) cannot exceed %s (%d)", EnvLivenessMinFrames, c.MinConfidentFrames, EnvLivenessMaxFrames, c.MaxFrames)
	}
	return c, nil
}

// isLocalDevValue is true only for local, dev and development.
func isLocalDevValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "local", "dev", "development":
		return true
	}
	return false
}

// IsLocalDevEnv reports whether the environment is local/dev: ENV must be
// local|dev|development, and DEPLOY_ENV / APP_ENV / ENVIRONMENT must each be
// blank or local too. A blank ENV is NOT local, so a deployment that forgets
// ENV fails closed.
func IsLocalDevEnv(getenv func(string) string) bool {
	if !isLocalDevValue(getenv("ENV")) {
		return false
	}
	for _, k := range []string{"DEPLOY_ENV", "APP_ENV", "ENVIRONMENT"} {
		if v := strings.TrimSpace(getenv(k)); v != "" && !isLocalDevValue(v) {
			return false
		}
	}
	return true
}

func isProductionValue(getenv func(string) string) bool {
	for _, k := range []string{"DEPLOY_ENV", "APP_ENV", "ENVIRONMENT", "ENV"} {
		switch strings.ToLower(strings.TrimSpace(getenv(k))) {
		case "production", "prod":
			return true
		}
	}
	return false
}

// ResolveFaceCompareSettings applies the rules above. Any error means main
// must refuse to start.
func ResolveFaceCompareSettings(getenv func(string) string) (FaceCompareSettings, error) {
	out := FaceCompareSettings{MatchThreshold: DefaultFaceMatchThreshold}
	liveness, err := resolveLivenessConfig(getenv)
	if err != nil {
		return out, err
	}
	out.Liveness = liveness
	if raw := strings.TrimSpace(getenv(EnvFaceMatchThreshold)); raw != "" {
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || f < 1 || f > 100 {
			return out, fmt.Errorf("%s must be a number from 1 to 100, got %q", EnvFaceMatchThreshold, raw)
		}
		out.MatchThreshold = f
	}
	if raw := strings.TrimSpace(getenv(EnvFaceCompareEnabled)); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return out, fmt.Errorf("%s must be true or false, got %q", EnvFaceCompareEnabled, raw)
		}
		out.Enabled = enabled
	}
	if !out.Enabled {
		return out, nil
	}
	if strings.TrimSpace(getenv("INTERNAL_SERVICE_KEY")) == "" {
		return out, fmt.Errorf("%s=true requires INTERNAL_SERVICE_KEY: the compare route is internal-key authenticated", EnvFaceCompareEnabled)
	}
	out.Backend = strings.ToLower(strings.TrimSpace(getenv(EnvFaceCompareBackend)))
	switch out.Backend {
	case FaceBackendMock:
		if !IsLocalDevEnv(getenv) {
			return out, fmt.Errorf("%s=mock is refused unless ENV is local, dev or development (ENV=%q); use rekognition",
				EnvFaceCompareBackend, strings.TrimSpace(getenv("ENV")))
		}
	case FaceBackendRekognition:
		out.Region = strings.TrimSpace(getenv("AWS_REGION"))
		if out.Region == "" {
			return out, fmt.Errorf("%s=rekognition requires AWS_REGION", EnvFaceCompareBackend)
		}
		if getenv("AWS_ACCESS_KEY_ID") != "" || getenv("AWS_SECRET_ACCESS_KEY") != "" {
			return out, fmt.Errorf("%s=rekognition: static AWS credentials are forbidden; use IRSA", EnvFaceCompareBackend)
		}
		if isProductionValue(getenv) && (getenv("AWS_WEB_IDENTITY_TOKEN_FILE") == "" || getenv("AWS_ROLE_ARN") == "") {
			return out, fmt.Errorf("production Rekognition face comparison requires AWS_WEB_IDENTITY_TOKEN_FILE and AWS_ROLE_ARN; node-role fallback is forbidden")
		}
	case "":
		return out, fmt.Errorf("%s=true requires %s (rekognition, or mock in local/dev)", EnvFaceCompareEnabled, EnvFaceCompareBackend)
	default:
		return out, fmt.Errorf("unknown %s %q (want rekognition or mock)", EnvFaceCompareBackend, out.Backend)
	}
	return out, nil
}
