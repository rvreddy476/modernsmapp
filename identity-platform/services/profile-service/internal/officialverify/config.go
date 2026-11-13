package officialverify

import (
	"fmt"
	"strings"

	"github.com/atpost/identity-profile-service/internal/personverify"
)

// Environment. Both marks are OFF unless their flag is "true": the user
// routes answer 503 and the service routes are not registered.
//
//	ORG_VERIFICATION_ENABLED         true | false (default)
//	ORG_VERIFICATION_REGISTRY_MODE   disabled (default) | mock (refused in production)
//	ORG_VERIFICATION_EMAIL_MODE      disabled (default) | mock (refused in production)
//	GOVERNMENT_VERIFICATION_ENABLED  true | false (default)
//	AUTH_SERVICE_URL, INTERNAL_SERVICE_KEY
//	                                 the admin-role read (reviewers, approvers)
//
// Enabling the Official mark needs a registry provider and code delivery;
// production has neither yet, so production cannot enable it.
const (
	EnvOrgEnabled   = "ORG_VERIFICATION_ENABLED"
	EnvRegistryMode = "ORG_VERIFICATION_REGISTRY_MODE"
	EnvEmailMode    = "ORG_VERIFICATION_EMAIL_MODE"
	EnvGovEnabled   = "GOVERNMENT_VERIFICATION_ENABLED"
	EnvAuthURL      = "AUTH_SERVICE_URL"
	EnvInternalKey  = "INTERNAL_SERVICE_KEY" //nolint:gosec // G101: name of the env variable, not its value

	ModeDisabled = "disabled"
	ModeMock     = "mock"
)

// Config is what the service runs with.
type Config struct {
	OrgEnabled bool
	GovEnabled bool
	Production bool
	Registry   RegistryVerifier
	Mailer     CodeMailer
	// FixedCode, when set, is issued instead of a random code (email mock
	// mode in development only).
	FixedCode string
	Roles     RoleChecker
}

func parseBool(getenv func(string) string, name string) (bool, error) {
	switch v := strings.ToLower(strings.TrimSpace(getenv(name))); v {
	case "", "false", "0", "no", "off":
		return false, nil
	case "true", "1", "yes", "on":
		return true, nil
	default:
		return false, fmt.Errorf("%s must be true or false", name)
	}
}

// RegistryFromEnv selects the registry provider. mock is refused in
// production. No production provider exists yet.
func RegistryFromEnv(getenv func(string) string, production bool) (RegistryVerifier, error) {
	switch mode := strings.ToLower(strings.TrimSpace(getenv(EnvRegistryMode))); mode {
	case "", ModeDisabled:
		return nil, nil
	case ModeMock:
		if production {
			return nil, fmt.Errorf("%s=mock is refused unless the environment is local, dev or development", EnvRegistryMode)
		}
		return &MockRegistry{EchoName: true}, nil
	default:
		return nil, fmt.Errorf("unknown %s %q (want disabled or mock; no production registry provider is built yet)", EnvRegistryMode, mode)
	}
}

// MailerFromEnv selects code delivery. mock is refused in production.
func MailerFromEnv(getenv func(string) string, production bool) (CodeMailer, string, error) {
	switch mode := strings.ToLower(strings.TrimSpace(getenv(EnvEmailMode))); mode {
	case "", ModeDisabled:
		return nil, "", nil
	case ModeMock:
		if production {
			return nil, "", fmt.Errorf("%s=mock is refused unless the environment is local, dev or development", EnvEmailMode)
		}
		return &MockMailer{}, MockEmailCode, nil
	default:
		return nil, "", fmt.Errorf("unknown %s %q (want disabled or mock; no production delivery is wired yet)", EnvEmailMode, mode)
	}
}

// ConfigFromEnv reads the configuration. Errors name variables, never values.
func ConfigFromEnv(getenv func(string) string) (Config, error) {
	cfg := Config{Production: personverify.IsProduction(getenv)}
	var err error
	if cfg.OrgEnabled, err = parseBool(getenv, EnvOrgEnabled); err != nil {
		return Config{}, err
	}
	if cfg.GovEnabled, err = parseBool(getenv, EnvGovEnabled); err != nil {
		return Config{}, err
	}
	if cfg.OrgEnabled {
		if cfg.Registry, err = RegistryFromEnv(getenv, cfg.Production); err != nil {
			return Config{}, err
		}
		if cfg.Registry == nil {
			return Config{}, fmt.Errorf("%s=true needs a registry provider (%s)", EnvOrgEnabled, EnvRegistryMode)
		}
		if cfg.Mailer, cfg.FixedCode, err = MailerFromEnv(getenv, cfg.Production); err != nil {
			return Config{}, err
		}
		if cfg.Mailer == nil {
			return Config{}, fmt.Errorf("%s=true needs code delivery (%s)", EnvOrgEnabled, EnvEmailMode)
		}
	}
	if cfg.OrgEnabled || cfg.GovEnabled {
		roles := NewAuthRoleClient(getenv(EnvAuthURL), getenv(EnvInternalKey))
		if roles == nil {
			return Config{}, fmt.Errorf("official and government marks need %s and %s for the admin-role check", EnvAuthURL, EnvInternalKey)
		}
		cfg.Roles = roles
	}
	return cfg, nil
}
