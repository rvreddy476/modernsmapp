package config

// The caller → application allowlist (migration 010).
//
//	SERVICE_CALLERS=commerce-service,food-service
//	SERVICE_CALLER_COMMERCE_SERVICE_APPLICATIONS=mstore
//	SERVICE_CALLER_FOOD_SERVICE_APPLICATIONS=feast
//
// A service token caller may open intents only for the applications listed for
// it. The list is validated at boot against the registry
// (payments.applications): in production an empty list, or a key the registry
// does not hold, refuses to start; elsewhere each is a WARN. A key that exists
// but is DISABLED is a WARN in every environment: disabling is a runtime switch
// an operator flips through the registry route, and it must not turn the next
// pod restart into a crash loop that takes every other application's payments
// down with it. Requests for a disabled application are refused either way.
//
// Legacy internal-key callers carry no identity to hang a list on; outside
// production they may name any active application.

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// ErrCallerApplications refuses a production boot whose caller application
// allowlist is empty or names an application the registry does not hold.
var ErrCallerApplications = errors.New("SERVICE_CALLER_<NAME>_APPLICATIONS is invalid")

// applicationKeyPattern is the registry key rule (chk_applications_key).
var applicationKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{1,31}$`)

// CallerEnvPrefix is the env prefix for one caller: SERVICE_CALLER_<NAME>, with
// the name upper-cased and dashes as underscores.
func CallerEnvPrefix(name string) string {
	return "SERVICE_CALLER_" + strings.ToUpper(strings.ReplaceAll(name, "-", "_"))
}

// AdminServiceCaller is the admin console's backend. It opens no payments, so
// it has no application allowlist: it is registered with admin permissions
// only (http.AdminPermissions) and is confined per request by the
// application_id it passes for an app-scoped admin.
const AdminServiceCaller = "admin-service"

// CallerApplications reads SERVICE_CALLER_<NAME>_APPLICATIONS for every caller
// in SERVICE_CALLERS. A caller with no list maps to an empty slice. The admin
// console's backend is not a payment opener and is left out.
func CallerApplications(getenv func(string) string) map[string][]string {
	out := map[string][]string{}
	for _, name := range splitCSV(getenv("SERVICE_CALLERS")) {
		if name == AdminServiceCaller {
			continue
		}
		out[name] = splitCSV(getenv(CallerEnvPrefix(name) + "_APPLICATIONS"))
	}
	return out
}

// ValidateCallerApplications checks each caller's allowlist against the
// registry, given as key → status. It returns the warnings to log, and in
// production an error wrapping ErrCallerApplications for an empty list or an
// unknown key.
func ValidateCallerApplications(allow map[string][]string, registry map[string]string, production bool) ([]string, error) {
	callers := make([]string, 0, len(allow))
	for name := range allow {
		callers = append(callers, name)
	}
	sort.Strings(callers)

	var warnings, problems []string
	for _, name := range callers {
		apps := allow[name]
		env := CallerEnvPrefix(name) + "_APPLICATIONS"
		if len(apps) == 0 {
			problems = append(problems, fmt.Sprintf("caller %q has no %s; it can open no payments", name, env))
			continue
		}
		for _, key := range apps {
			status, ok := registry[key]
			switch {
			case !applicationKeyPattern.MatchString(key) || !ok:
				problems = append(problems, fmt.Sprintf("caller %q: %s names %q, which is not a registered application", name, env, key))
			case status != "active":
				warnings = append(warnings, fmt.Sprintf("caller %q: application %q is %s; its payments are refused until it is re-enabled", name, key, status))
			}
		}
	}
	if len(problems) == 0 {
		return warnings, nil
	}
	if production {
		return warnings, fmt.Errorf("%w: %s", ErrCallerApplications, strings.Join(problems, "; "))
	}
	return append(warnings, problems...), nil
}

// Allows reports whether apps contains key.
func Allows(apps []string, key string) bool {
	for _, a := range apps {
		if a == key {
			return true
		}
	}
	return false
}

func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
