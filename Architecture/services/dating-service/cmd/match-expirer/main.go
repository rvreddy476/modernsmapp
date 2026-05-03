// match-expirer — periodic cron that:
//
//  1. Marks 7-day-stale matches (no first reply) as 'expired' and emits
//     dating.match.expired per row.
//  2. Marks 14-day-idle matches as 'quiet' and emits dating.match.quiet.
//
// Run modes:
//   - Daemon (default): loops every MATCH_EXPIRER_INTERVAL (default 1h).
//   - One-shot: set MATCH_EXPIRER_ONCE=true and the binary exits after one pass.
//
// NOTE: Sprint 3/4 stub. Full implementation lands in S5 — see the tracker.
package main

import (
	"log/slog"
	"os"
)

func main() {
	slog.Info("match-expirer stub; full implementation pending S5",
		"once", os.Getenv("MATCH_EXPIRER_ONCE"))
}
