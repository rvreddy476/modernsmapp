// Sprint 6 — soft-launch cohort gate.
//
// We do staged rollout via a deterministic cohort number 0..99 derived from
// (user_id, cohort_salt). The env `PULSE_COHORT_GATE` is the percentage
// allowed in. Users above the threshold get a 200-OK response with an
// empty Pulse + `cohort_gated: true`, so the mobile app can render a
// "coming soon" screen identical to the city gate.
//
// Cohort assignment is per-user-stable: the salt is generated at profile
// creation and never changes (so a 5%-day-1 → 25%-day-4 ramp keeps the
// 5% group inside the 25% group). The salt lives in
// `dating_profiles.cohort_salt`.
package service

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
)

// cohortPercent returns the value of PULSE_COHORT_GATE clamped to [0, 100].
// Returns -1 (uncapped) when the env is unset/empty so the rollout is
// effectively disabled.
func cohortPercent() int {
	raw := strings.TrimSpace(os.Getenv("PULSE_COHORT_GATE"))
	if raw == "" {
		return -1
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		return -1
	}
	if v < 0 {
		return 0
	}
	if v > 100 {
		return 100
	}
	return v
}

// userCohort returns a stable 0..99 bucket for the given user_id + salt.
// SHA-256 is overkill for the use case but it's the standard primitive
// available; we hash and take 8 bytes mod 100. Same input → same bucket
// forever, which is the whole point of having a per-user salt.
func userCohort(userID uuid.UUID, salt string) int {
	h := sha256.New()
	h.Write([]byte(userID.String()))
	h.Write([]byte(":"))
	h.Write([]byte(salt))
	sum := h.Sum(nil)
	n := binary.BigEndian.Uint64(sum[:8])
	return int(n % 100)
}

// isUserGated returns true when the user's cohort is OUTSIDE the allowed
// percentage. When cohort gating is disabled (env unset), returns false
// — i.e. nobody is gated.
func (s *Service) isUserGated(ctx context.Context, userID uuid.UUID) bool {
	pct := cohortPercent()
	if pct < 0 {
		return false
	}
	if pct >= 100 {
		return false
	}
	salt, err := s.store.GetCohortSalt(ctx, userID)
	if err != nil {
		// On read failure, fail-open: don't accidentally cohort-gate the
		// whole user base when the DB is flaky. The runbook for "cohort
		// gate read failure" is the same as for any DB error.
		slog.Warn("cohort: salt fetch failed; allowing user", "user_id", userID, "error", err)
		return false
	}
	if salt == "" {
		// Profile predates the cohort_salt column. Best-effort: backfill
		// a fresh salt and treat the user as IN the rollout (their first
		// computed bucket may shift mid-rollout but only once).
		fresh := newCohortSalt()
		if err := s.store.SetCohortSalt(ctx, userID, fresh); err != nil {
			slog.Warn("cohort: salt backfill failed", "user_id", userID, "error", err)
		}
		salt = fresh
	}
	bucket := userCohort(userID, salt)
	return bucket >= pct
}

// newCohortSalt returns 16 random hex characters. Doesn't need crypto-rand
// (the salt is not a secret); math/rand is fine and avoids an extra import
// of crypto/rand failure plumbing.
func newCohortSalt() string {
	src := rand.New(rand.NewSource(time.Now().UnixNano()))
	buf := make([]byte, 8)
	for i := range buf {
		buf[i] = byte(src.Intn(256))
	}
	return hex.EncodeToString(buf)
}
