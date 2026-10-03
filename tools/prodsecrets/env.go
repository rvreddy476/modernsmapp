package main

import (
	"fmt"
	"strings"
)

// DefaultEnv keeps the tool's original behaviour: production.
const DefaultEnv = "prod"

// KnownEnvs are the environments --env accepts.
var KnownEnvs = []string{"prod", "qa"}

func knownEnv(env string) bool {
	for _, e := range KnownEnvs {
		if e == env {
			return true
		}
	}
	return false
}

// wrapperCmd is how the report names scripts/prodsecrets.sh for env (the
// production wording is unchanged).
func wrapperCmd(env string) string {
	if env == DefaultEnv || env == "" {
		return "scripts/prodsecrets.sh"
	}
	return "scripts/prodsecrets.sh --env " + env
}

// requiredPrefix is the prefix a value must carry in env, for the few keys
// whose mode is visible in the value. Razorpay key ids say whether they are
// live (rzp_live_) or test mode (rzp_test_): production takes only live keys
// and every other environment only test-mode keys, so a QA cluster can never
// hold a key that moves real money, and production never runs on a test key.
// The rule is code, not manifest data, so editing the manifest cannot relax it.
func requiredPrefix(env, name string) (string, bool) {
	if name != "razorpay_key_id" {
		return "", false
	}
	if env == "prod" {
		return "rzp_live_", true
	}
	return "rzp_test_", true
}

// checkPrefix reports whether value is acceptable for the key name in env.
// The error never contains the value.
func checkPrefix(env, name, value string) error {
	want, ok := requiredPrefix(env, name)
	if !ok || value == "" {
		return nil
	}
	if !strings.HasPrefix(value, want) {
		return fmt.Errorf("%s: the %s environment accepts only %s key ids (value not shown)", name, env, want)
	}
	return nil
}

// keyRuleName is the name the prefix rule is keyed by for a manifest key:
// the shared value's name when the key copies one, else the key's own name.
func keyRuleName(k KeyResult) string {
	switch k.Spec.Kind {
	case KindShared, KindSharedPrivate, KindSharedPublic:
		if _, ok := requiredPrefix(DefaultEnv, k.Spec.Arg); ok {
			return k.Spec.Arg
		}
	}
	return k.Key
}
