// Package runmode resolves the monetization service's boot-time flags
// into one Mode and refuses the combinations that must never run. It is
// a package rather than a block in main so the decisions — which
// workers start, whether Kafka and Redis are opened, whether the admin
// routes are the only writes — can be asserted by a test.
//
// Flags (all environment variables, all default false):
//
//	MONETIZATION_WRITES_ENABLED   the financial launch boundary (Module 6)
//	MONETIZATION_PAYOUTS_ENABLED  withdrawals (plan Phase 3C); needs writes
//	MONETIZATION_MAINTENANCE      admin routes only, no worker, no consumer
//	MONETIZATION_TDS_APPLY        deduct TDS at payout (founder: off until
//	                              the tax module; the calculation is kept)
//
// Maintenance mode (reviewer correction B, 12 Sep 2026) exists so an
// operator can run the admin corrections — the January reversals — with
// a guarantee that nothing else in this process can touch the ledger at
// the same time. Enabling plain writes starts the accrual, settlement and
// eligibility workers, any of which could claim a row the operator is
// about to correct. Maintenance mode is the mutual exclusion: every
// non-admin financial write answers 503 MAINTENANCE and no background
// goroutine that moves money is started.
package runmode

import (
	"errors"
	"fmt"
	"strings"
)

// Config is what the environment said.
type Config struct {
	Environment    string
	InternalKey    string
	WritesEnabled  bool
	PayoutsEnabled bool
	Maintenance    bool
	TDSApply       bool
}

// Mode is what the process will do about it.
type Mode struct {
	WritesEnabled  bool
	PayoutsEnabled bool
	Maintenance    bool
	TDSApply       bool

	// StartWorkers: the background workers in workers.StartAll (accrual,
	// settlement, eligibility, renewals, holds, fundraisers, reconciliation,
	// and — only with payouts on — the payout submitter and reconciler).
	StartWorkers bool
	// StartKafka: the event producer. This service has no Kafka consumer;
	// the producer is the only Kafka client and it is opened only when
	// something that publishes can run.
	StartKafka bool
	// RequireRedis: the fraud counters. Needed by the live withdrawal path,
	// not by the admin corrections.
	RequireRedis bool
	// AdminKeyRequired: every /admin/ route must carry the internal
	// service key in addition to the admin scope header.
	AdminKeyRequired bool
}

var (
	ErrPayoutsNeedWrites          = errors.New("MONETIZATION_PAYOUTS_ENABLED=true requires MONETIZATION_WRITES_ENABLED=true")
	ErrMaintenanceNeedsPayoutsOff = errors.New("MONETIZATION_MAINTENANCE=true requires MONETIZATION_PAYOUTS_ENABLED=false")
	ErrMaintenanceNeedsKey        = errors.New("MONETIZATION_MAINTENANCE=true requires INTERNAL_SERVICE_KEY: the admin routes are the only open writes and they must not be callable by any container that merely reaches the port")
	ErrKeyRequiredOutsideDev      = errors.New("INTERNAL_SERVICE_KEY is required outside development")
)

// Resolve turns the flags into a Mode, or says why the process must not
// start. Every refusal is deliberate: a configuration that says two
// contradictory things is a mistake the process refuses to run under.
func Resolve(c Config) (Mode, error) {
	env := strings.ToLower(strings.TrimSpace(c.Environment))
	key := strings.TrimSpace(c.InternalKey)
	if c.PayoutsEnabled && !c.WritesEnabled {
		return Mode{}, ErrPayoutsNeedWrites
	}
	if c.Maintenance && c.PayoutsEnabled {
		return Mode{}, ErrMaintenanceNeedsPayoutsOff
	}
	if c.Maintenance && key == "" {
		return Mode{}, ErrMaintenanceNeedsKey
	}
	if (env == "prod" || env == "production" || env == "staging") && key == "" {
		return Mode{}, ErrKeyRequiredOutsideDev
	}
	m := Mode{
		WritesEnabled:  c.WritesEnabled,
		PayoutsEnabled: c.PayoutsEnabled,
		Maintenance:    c.Maintenance,
		TDSApply:       c.TDSApply,
	}
	// Maintenance wins over writes: even with both flags on, nothing in
	// the background runs and only the admin routes accept a write.
	m.StartWorkers = c.WritesEnabled && !c.Maintenance
	m.StartKafka = c.WritesEnabled && !c.Maintenance
	m.RequireRedis = c.WritesEnabled && !c.Maintenance
	m.AdminKeyRequired = c.Maintenance
	return m, nil
}

// BootLine is the one log line that states the mode. It is deliberately
// flat so an operator can grep the container log for it.
func (m Mode) BootLine() string {
	return fmt.Sprintf("monetization run mode: maintenance=%t writes_enabled=%t payouts_enabled=%t tds_apply=%t workers=%t kafka_producer=%t redis=%t admin_key_required=%t",
		m.Maintenance, m.WritesEnabled, m.PayoutsEnabled, m.TDSApply, m.StartWorkers, m.StartKafka, m.RequireRedis, m.AdminKeyRequired)
}
