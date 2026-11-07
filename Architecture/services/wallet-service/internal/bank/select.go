package bank

import (
	"fmt"
	"strings"
)

// envNames are the variables that name the deployment environment, in the
// order other services read them (ENV is what the deploy values set).
var envNames = []string{"ENV", "APP_ENV", "DEPLOY_ENV", "ENVIRONMENT"}

// devEnvironments are the only environments the mock partner may run in.
var devEnvironments = map[string]bool{"local": true, "dev": true, "development": true, "test": true}

// FromEnv selects the partner-bank client from BANK_PARTNER. It accepts
// exactly "icici" or "mock" and refuses anything else, so a typo can never
// fall through to the mock, which accepts any UPI reference as paid.
//
// The mock (also what an empty BANK_PARTNER means) is allowed only when the
// environment is local, dev, development or test: at least one of ENV,
// APP_ENV, DEPLOY_ENV, ENVIRONMENT must be set and every one that is set must
// name one of those. A blank environment counts as production. icici needs
// ICICI_BASE_URL. The returned name is for the start-up log.
func FromEnv(getenv func(string) string) (client BankClient, name string, err error) {
	partner := strings.ToLower(strings.TrimSpace(getenv("BANK_PARTNER")))
	switch partner {
	case "icici":
		base := strings.TrimSpace(getenv("ICICI_BASE_URL"))
		if base == "" {
			return nil, "", fmt.Errorf("BANK_PARTNER=icici needs ICICI_BASE_URL")
		}
		return NewHTTPClient(base, getenv("ICICI_API_KEY"), getenv("ICICI_BC_ID")), "icici", nil
	case "mock", "":
		if env, ok := devEnvironment(getenv); !ok {
			return nil, "", fmt.Errorf("BANK_PARTNER=%q selects the mock partner bank, which accepts any UPI reference as paid; it is allowed only when ENV is local, dev, development or test (got %q). Set BANK_PARTNER=icici", partner, env)
		}
		return NewMockClient(), "mock", nil
	default:
		return nil, "", fmt.Errorf("unknown BANK_PARTNER %q (want icici, or mock on a dev environment)", partner)
	}
}

// devEnvironment reports whether the environment variables name a dev
// environment, and the values it saw (for the error message).
func devEnvironment(getenv func(string) string) (seen string, ok bool) {
	var vals []string
	for _, name := range envNames {
		v := strings.ToLower(strings.TrimSpace(getenv(name)))
		if v == "" {
			continue
		}
		vals = append(vals, name+"="+v)
		if !devEnvironments[v] {
			return strings.Join(vals, ","), false
		}
	}
	return strings.Join(vals, ","), len(vals) > 0
}
