package http

import (
	"errors"
	"strings"
)

// Access-token policy from the environment (reels-15).
//
// media-service verifies the user's access token itself: the gateway forwards
// it and does not stamp the internal key on /v1/media, /v1/audio,
// /v1/audio-library or /v1/subtitles. The verifier used to accept HS256 with
// the shared JWT_SECRET in production, treat a token without `exp` as never
// expiring and check neither `iss` nor `aud`, so any pod holding the secret
// could mint any user for this service, for ever.
//
// The rules now mirror the api-gateway (pkg/tokenpolicy) and chat-service
// (shared/accessauth) cutover, with the same variable names:
//
//   - production (DEPLOY_ENV, APP_ENV, ENVIRONMENT or ENV = prod/production,
//     the service's existing rule) accepts RS256 only, requires
//     JWT_PUBLIC_KEY_PEM, JWT_ISSUER and JWT_AUDIENCE, and refuses to start
//     while JWT_SECRET or JWT_SECRET_PREVIOUS is present, because a verifier
//     holding the symmetric secret can also mint;
//   - JWT_DISABLE_HS256=true applies the RS256-only rule outside production;
//   - otherwise (local, dev) HS256 stays accepted beside RS256 and
//     JWT_SECRET is required, as before. JWT_ISSUER / JWT_AUDIENCE are
//     checked whenever they are set.

// ErrJWTConfig wraps every refusal LoadJWTKeySet returns.
var ErrJWTConfig = errors.New("media-service access-token configuration refused")

// productionEnvVars are read in this order; any one naming production makes
// the process production (the same rule as cmd/server isProductionEnv).
var productionEnvVars = []string{"DEPLOY_ENV", "APP_ENV", "ENVIRONMENT", "ENV"}

func isProduction(getenv func(string) string) bool {
	for _, key := range productionEnvVars {
		switch strings.ToLower(strings.TrimSpace(getenv(key))) {
		case "production", "prod":
			return true
		}
	}
	return false
}

// LoadJWTKeySet builds the access-token key set and policy from getenv and
// refuses a configuration production must not run with.
func LoadJWTKeySet(getenv func(string) string) (JWTKeySet, error) {
	production := isProduction(getenv)
	keys := JWTKeySet{
		ActiveKID:      strings.TrimSpace(getenv("JWT_KID")),
		ActiveSecret:   getenv("JWT_SECRET"),
		PreviousKID:    strings.TrimSpace(getenv("JWT_KID_PREVIOUS")),
		PreviousSecret: getenv("JWT_SECRET_PREVIOUS"),
		DisableHS256:   production || strings.EqualFold(strings.TrimSpace(getenv("JWT_DISABLE_HS256")), "true"),
		Audience:       strings.TrimSpace(getenv("JWT_AUDIENCE")),
	}
	if keys.ActiveKID == "" {
		keys.ActiveKID = "v1"
	}
	for _, iss := range strings.Split(getenv("JWT_ISSUER"), ",") {
		if iss = strings.TrimSpace(iss); iss != "" {
			keys.Issuers = append(keys.Issuers, iss)
		}
	}
	rsaKeys, err := RSAKeysFromEnv(getenv)
	if err != nil {
		return keys, errors.Join(ErrJWTConfig, err)
	}
	keys.RSAKeys = rsaKeys

	if !keys.DisableHS256 {
		if keys.ActiveSecret == "" {
			return keys, errors.Join(ErrJWTConfig, errors.New("JWT_SECRET is required while HS256 is accepted (set JWT_DISABLE_HS256=true and JWT_PUBLIC_KEY_PEM for RS256 only)"))
		}
		return keys, nil
	}
	// RS256 only from here: the symmetric secrets are never used.
	keys.ActiveSecret, keys.PreviousSecret = "", ""
	if len(keys.RSAKeys) == 0 {
		return keys, errors.Join(ErrJWTConfig, errors.New("JWT_PUBLIC_KEY_PEM is required when HS256 is refused"))
	}
	if production {
		switch {
		case len(keys.Issuers) == 0:
			return keys, errors.Join(ErrJWTConfig, errors.New("JWT_ISSUER is required in production"))
		case keys.Audience == "":
			return keys, errors.Join(ErrJWTConfig, errors.New("JWT_AUDIENCE is required in production"))
		case getenv("JWT_SECRET") != "" || getenv("JWT_SECRET_PREVIOUS") != "":
			return keys, errors.Join(ErrJWTConfig, errors.New("JWT_SECRET / JWT_SECRET_PREVIOUS must not be set in production: RS256 is the only accepted algorithm and the verifier must not hold a signing secret"))
		}
	}
	return keys, nil
}
