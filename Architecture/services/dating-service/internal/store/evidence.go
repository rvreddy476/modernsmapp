// Evidence retention (Dating plan lane D8).
//
// An account purge erases the user's dating data, but some of it is evidence
// other people depend on: reports they made or received, panic incidents they
// raised, and the risk signals that stop a banned person simply signing up
// again. That evidence is kept for a bounded window under a stable anonymised
// subject token instead of the user id:
//
//   - SubjectToken is an HMAC of the user id under the evidence key, shaped as
//     a UUID (version 8) so it fits the existing UUID columns. It is stable,
//     so every retained row about one person links together, and it cannot be
//     reversed without the key.
//   - HashSignal is an HMAC of a device fingerprint or IP, so a retained
//     signal can still be matched against a new account's raw value.
//   - DeleteExpiredEvidence removes retained evidence once retain_until has
//     passed, and clears expired live-location points.
package store

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// DefaultEvidenceRetention is how long evidence about a purged account is kept.
const DefaultEvidenceRetention = 180 * 24 * time.Hour

// Retained signal kinds (dating_retained_risk_signals.kind).
const (
	RetainedSignalAccount           = "account"
	RetainedSignalDeviceFingerprint = "device_fingerprint"
	RetainedSignalIP                = "ip"
)

// devEvidenceKey is used only when no key was configured: main refuses to
// boot without DATING_EVIDENCE_HMAC_KEY outside local/dev, so this reaches
// local stacks and tests only.
var devEvidenceKey = []byte("dating-local-dev-evidence-hmac-key-not-for-deployed-envs")

// SetEvidenceKey installs the HMAC key for subject tokens and signal hashes.
// An empty key keeps the local/dev key.
func (s *Store) SetEvidenceKey(key []byte) {
	if len(key) > 0 {
		s.evidenceKey = append([]byte(nil), key...)
	}
}

// SetEvidenceRetention sets how long evidence about a purged account is
// kept. Non-positive keeps the current value.
func (s *Store) SetEvidenceRetention(d time.Duration) {
	if d > 0 {
		s.evidenceRetention = d
	}
}

// EvidenceRetention returns the retention window in force.
func (s *Store) EvidenceRetention() time.Duration {
	if s.evidenceRetention <= 0 {
		return DefaultEvidenceRetention
	}
	return s.evidenceRetention
}

func (s *Store) evidenceMAC(domain, value string) []byte {
	key := s.evidenceKey
	if len(key) == 0 {
		key = devEvidenceKey
	}
	m := hmac.New(sha256.New, key)
	m.Write([]byte(domain))
	m.Write([]byte{0})
	m.Write([]byte(value))
	return m.Sum(nil)
}

// SubjectToken is the stable anonymised stand-in for userID in retained
// evidence. Version 8 (custom) never collides with the v4/v7 ids the
// platform issues.
func (s *Store) SubjectToken(userID uuid.UUID) uuid.UUID {
	sum := s.evidenceMAC("dating-subject", userID.String())
	var id uuid.UUID
	copy(id[:], sum[:16])
	id[6] = (id[6] & 0x0f) | 0x80 // version 8
	id[8] = (id[8] & 0x3f) | 0x80 // RFC 4122 variant
	return id
}

// HashSignal is the retained form of a raw risk signal value.
func (s *Store) HashSignal(kind, value string) string {
	return hex.EncodeToString(s.evidenceMAC("dating-signal:"+kind, value))
}

// retainRiskSignalsTx copies the user's account risk row, profile status and
// device fingerprints / IPs into dating_retained_risk_signals, hashed, before
// the purge deletes them. Idempotent: a redelivered purge finds nothing left
// to copy and the unique key absorbs repeats.
func (s *Store) retainRiskSignalsTx(ctx context.Context, tx pgx.Tx, userID, token uuid.UUID) (int64, error) {
	secs := s.EvidenceRetention().Seconds()
	var n int64
	tag, err := tx.Exec(ctx, `
        INSERT INTO dating_retained_risk_signals
            (subject_token, kind, value_hash, risk_score, risk_level, profile_status,
             reports_against, retain_until)
        SELECT $2, 'account', $3, r.risk_score, r.risk_level, p.profile_status,
               (SELECT COUNT(*)::int FROM dating_reports WHERE target_id = $1),
               now() + make_interval(secs => $4)
        FROM (SELECT $1::uuid AS uid) u
        LEFT JOIN dating_account_risk r ON r.user_id = u.uid
        LEFT JOIN dating_profiles p ON p.user_id = u.uid
        WHERE r.user_id IS NOT NULL OR p.user_id IS NOT NULL
        ON CONFLICT (subject_token, kind, value_hash) DO UPDATE
            SET risk_score      = COALESCE(EXCLUDED.risk_score, dating_retained_risk_signals.risk_score),
                risk_level      = COALESCE(EXCLUDED.risk_level, dating_retained_risk_signals.risk_level),
                profile_status  = COALESCE(EXCLUDED.profile_status, dating_retained_risk_signals.profile_status),
                reports_against = GREATEST(EXCLUDED.reports_against, dating_retained_risk_signals.reports_against),
                retain_until    = GREATEST(EXCLUDED.retain_until, dating_retained_risk_signals.retain_until)`,
		userID, token, token.String(), secs)
	if err != nil {
		return 0, fmt.Errorf("retain account risk: %w", err)
	}
	n += tag.RowsAffected()

	type fpRow struct {
		fingerprint, ip string
		first, last     time.Time
	}
	// Lane D9: the raw values are sealed; they are opened here only to derive
	// the same retained hash CountUsersByFingerprint computes from a live
	// request. A sealed row that cannot be opened fails the purge rather
	// than losing the ban-evasion signal.
	rows, err := tx.Query(ctx, `
        SELECT fingerprint, fingerprint_sealed, ip, ip_sealed, first_seen_at, last_seen_at
        FROM dating_device_fingerprints WHERE user_id = $1`, userID)
	if err != nil {
		return 0, fmt.Errorf("read fingerprints to retain: %w", err)
	}
	var fps []fpRow
	for rows.Next() {
		var r fpRow
		var fpPlain, ipPlain *string
		var fpSealed, ipSealed []byte
		if err := rows.Scan(&fpPlain, &fpSealed, &ipPlain, &ipSealed, &r.first, &r.last); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan fingerprint to retain: %w", err)
		}
		if r.fingerprint, err = s.openDeviceSignal(ctx, fpSealed, fpPlain); err != nil {
			rows.Close()
			return 0, fmt.Errorf("open fingerprint to retain: %w", err)
		}
		if r.ip, err = s.openDeviceSignal(ctx, ipSealed, ipPlain); err != nil {
			rows.Close()
			return 0, fmt.Errorf("open ip to retain: %w", err)
		}
		fps = append(fps, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, fmt.Errorf("read fingerprints to retain: %w", err)
	}
	insert := func(kind, value string, first, last time.Time) error {
		tag, err := tx.Exec(ctx, `
            INSERT INTO dating_retained_risk_signals
                (subject_token, kind, value_hash, first_seen_at, last_seen_at, retain_until)
            VALUES ($1, $2, $3, $4, $5, now() + make_interval(secs => $6))
            ON CONFLICT (subject_token, kind, value_hash) DO UPDATE
                SET first_seen_at = LEAST(dating_retained_risk_signals.first_seen_at, EXCLUDED.first_seen_at),
                    last_seen_at  = GREATEST(dating_retained_risk_signals.last_seen_at, EXCLUDED.last_seen_at),
                    retain_until  = GREATEST(dating_retained_risk_signals.retain_until, EXCLUDED.retain_until)`,
			token, kind, s.HashSignal(kind, value), first, last, secs)
		if err != nil {
			return fmt.Errorf("retain %s signal: %w", kind, err)
		}
		n += tag.RowsAffected()
		return nil
	}
	for _, r := range fps {
		if r.fingerprint != "" {
			if err := insert(RetainedSignalDeviceFingerprint, r.fingerprint, r.first, r.last); err != nil {
				return 0, err
			}
		}
		if r.ip != "" {
			if err := insert(RetainedSignalIP, r.ip, r.first, r.last); err != nil {
				return 0, err
			}
		}
	}
	return n, nil
}

// EvidenceSweepResult counts what one retention sweep removed.
type EvidenceSweepResult struct {
	Reports              int64
	PanicIncidents       int64
	RiskSignals          int64
	LocationPointsExpired int64
	LocationSharesDeleted int64
}

// Total is the sum of every count.
func (r EvidenceSweepResult) Total() int64 {
	return r.Reports + r.PanicIncidents + r.RiskSignals + r.LocationPointsExpired + r.LocationSharesDeleted
}

// locationShareRowRetention is how long an expired or stopped share row (no
// point left) is kept for the sharer's history before deletion.
const locationShareRowRetention = 7 * 24 * time.Hour

// DeleteExpiredEvidence deletes retained reports, panic incidents and risk
// signals whose retain_until has passed, clears the point of every expired
// location share and deletes share rows a week after expiry. Each statement
// is bounded by limit so a backlog drains over several sweeps.
func (s *Store) DeleteExpiredEvidence(ctx context.Context, limit int) (EvidenceSweepResult, error) {
	if limit <= 0 {
		limit = 500
	}
	var out EvidenceSweepResult
	run := func(dst *int64, stmt string, args ...any) error {
		tag, err := s.db.Exec(ctx, stmt, args...)
		if err != nil {
			return err
		}
		*dst = tag.RowsAffected()
		return nil
	}
	if err := run(&out.Reports, `
        DELETE FROM dating_reports WHERE id IN (
            SELECT id FROM dating_reports
            WHERE retain_until IS NOT NULL AND retain_until < now() LIMIT $1)`, limit); err != nil {
		return out, fmt.Errorf("sweep retained reports: %w", err)
	}
	if err := run(&out.PanicIncidents, `
        DELETE FROM dating_panic_incidents WHERE id IN (
            SELECT id FROM dating_panic_incidents
            WHERE retain_until IS NOT NULL AND retain_until < now() LIMIT $1)`, limit); err != nil {
		return out, fmt.Errorf("sweep retained panic incidents: %w", err)
	}
	if err := run(&out.RiskSignals, `
        DELETE FROM dating_retained_risk_signals WHERE id IN (
            SELECT id FROM dating_retained_risk_signals
            WHERE retain_until < now() LIMIT $1)`, limit); err != nil {
		return out, fmt.Errorf("sweep retained risk signals: %w", err)
	}
	if err := run(&out.LocationPointsExpired, `
        UPDATE dating_location_shares SET latitude = NULL, longitude = NULL, location_sealed = NULL
        WHERE id IN (
            SELECT id FROM dating_location_shares
            WHERE expires_at <= now() AND (latitude IS NOT NULL OR location_sealed IS NOT NULL) LIMIT $1)`, limit); err != nil {
		return out, fmt.Errorf("sweep expired location points: %w", err)
	}
	if err := run(&out.LocationSharesDeleted, `
        DELETE FROM dating_location_shares WHERE id IN (
            SELECT id FROM dating_location_shares
            WHERE expires_at < now() - make_interval(secs => $2) LIMIT $1)`,
		limit, locationShareRowRetention.Seconds()); err != nil {
		return out, fmt.Errorf("sweep old location shares: %w", err)
	}
	return out, nil
}
