package http

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/service"
	"github.com/atpost/dating-service/internal/store"
)

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
