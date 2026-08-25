// Package rollout resolves runtime rollout flags.
//
// These are NOT security controls. They exist so a contract change can be
// enabled, observed, and switched off again without a redeploy — the rollout
// requirement in the platform quality bar (§5.10: "feature flag, expand/
// contract migration, canary/rollback plan").
//
// Security and eligibility decisions must never be routed through here. Those
// fail closed on unknown state (quality bar §4); a rollout flag deliberately
// falls back to its configured default so that a Redis blip cannot change the
// behaviour of a running service in either direction.
package rollout

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// FlagRegisterRequireGender gates whether `gender` is mandatory on
// registration.
//
// Expand/contract: the field has always been accepted and is being promoted to
// required. The expand phase (flag OFF) accepts an absent gender but still
// rejects an invalid one, so clients can start sending it before enforcement
// begins. The contract phase (flag ON) additionally rejects absence.
const FlagRegisterRequireGender = "register_require_gender"

const (
	redisKeyPrefix = "flag:"
	// Bounds how stale a decision can be after an operator flips the switch.
	// Short enough that a rollback takes effect well inside the 60s the
	// runbook promises; long enough that registration does not add a Redis
	// round trip per request.
	cacheTTL = 5 * time.Second
	// A flag lookup must never be the reason registration is slow. If Redis
	// cannot answer within this budget the configured default is used.
	lookupTimeout = 100 * time.Millisecond
)

// Flags resolves rollout flags with a defaults-backed, briefly-cached read.
type Flags struct {
	rdb      *redis.Client
	log      *slog.Logger
	defaults map[string]bool

	mu     sync.RWMutex
	cache  map[string]bool
	expiry map[string]time.Time
}

// New builds a resolver. A nil rdb is valid and means "always use defaults",
// which is what unit tests and any Redis-less deployment get.
func New(rdb *redis.Client, log *slog.Logger, defaults map[string]bool) *Flags {
	if log == nil {
		log = slog.Default()
	}
	if defaults == nil {
		defaults = map[string]bool{}
	}
	return &Flags{
		rdb:      rdb,
		log:      log,
		defaults: defaults,
		cache:    map[string]bool{},
		expiry:   map[string]time.Time{},
	}
}

// Enabled reports whether name is on.
//
// Resolution order: fresh cache entry, then Redis, then the configured
// default. Any Redis failure — unavailable, timeout, unparseable value —
// resolves to the default rather than to false, so an outage cannot silently
// disable a contract that is already live.
func (f *Flags) Enabled(ctx context.Context, name string) bool {
	if v, ok := f.cached(name); ok {
		return v
	}

	value := f.defaults[name]
	if f.rdb != nil {
		lookupCtx, cancel := context.WithTimeout(ctx, lookupTimeout)
		defer cancel()

		raw, err := f.rdb.Get(lookupCtx, redisKeyPrefix+name).Result()
		switch {
		case err == redis.Nil:
			// Unset is the normal state: the default governs.
		case err != nil:
			f.log.Warn("rollout flag lookup failed — using configured default",
				"flag", name, "default", value, "err", err)
		default:
			switch raw {
			case "true", "1", "on":
				value = true
			case "false", "0", "off":
				value = false
			default:
				f.log.Warn("rollout flag has an unrecognised value — using configured default",
					"flag", name, "value", raw, "default", value)
			}
		}
	}

	f.store(name, value)
	return value
}

func (f *Flags) cached(name string) (bool, bool) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	if exp, ok := f.expiry[name]; ok && time.Now().Before(exp) {
		return f.cache[name], true
	}
	return false, false
}

func (f *Flags) store(name string, value bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cache[name] = value
	f.expiry[name] = time.Now().Add(cacheTTL)
}
