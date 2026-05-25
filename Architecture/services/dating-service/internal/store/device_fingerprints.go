// Device-fingerprint store — §P0-7 Phase B.
//
// Two signals were left at 0 weight in Phase A because the
// device-fingerprint table + IP/ASN velocity aggregator didn't exist
// yet. This file lands both:
//
//   - UpsertDeviceFingerprint is called from a Gin middleware on every
//     pulse/spark request that carries an X-Device-Fingerprint header.
//     INSERT ... ON CONFLICT(user_id, fingerprint) DO UPDATE keeps the
//     fingerprint→user link unique while refreshing last_seen_at and
//     the IP (rotating mobile IPs are expected).
//
//   - CountUsersByFingerprint feeds the device-reuse signal: if any of
//     the caller's fingerprints maps to > 3 distinct users, the device
//     is being recycled across accounts (multi-account abuse vector).
//
//   - CountDistinctUsersOnIPLastHour feeds the IP/ASN velocity signal:
//     if a single IP shows > 5 distinct users in the last hour, it's
//     an emulator farm / Tor exit / shared-NAT abuse signature.
//
//   - ListFingerprintsForUser returns the caller's fingerprints so the
//     risk-compute job can look up each one's user-count without
//     joining live tables under a single query plan.
//
// All queries are intentionally simple — Phase 3 ML pipelines can later
// roll up these counts into per-device + per-ASN aggregates without
// touching the scoring contract.
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// DeviceFingerprint is one row of dating_device_fingerprints.
type DeviceFingerprint struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Fingerprint string
	IP          string
}

// UpsertDeviceFingerprint inserts (user_id, fingerprint) or refreshes
// last_seen_at + ip when the pair already exists. Empty fingerprint is
// a no-op so the middleware can call this unconditionally — requests
// without an X-Device-Fingerprint header skip the write cheaply.
func (s *Store) UpsertDeviceFingerprint(ctx context.Context, userID uuid.UUID, fingerprint, ip string) error {
	if userID == uuid.Nil || fingerprint == "" {
		return nil
	}
	var ipArg any
	if ip != "" {
		ipArg = ip
	}
	_, err := s.db.Exec(ctx, `
        INSERT INTO dating_device_fingerprints (user_id, fingerprint, ip)
        VALUES ($1, $2, $3)
        ON CONFLICT (user_id, fingerprint) DO UPDATE
            SET last_seen_at = NOW(),
                ip = COALESCE(EXCLUDED.ip, dating_device_fingerprints.ip)`,
		userID, fingerprint, ipArg)
	if err != nil {
		return fmt.Errorf("upsert device fingerprint: %w", err)
	}
	return nil
}

// CountUsersByFingerprint returns the number of DISTINCT user_ids that
// have ever been observed using the supplied fingerprint. Drives the
// device-reuse risk signal.
func (s *Store) CountUsersByFingerprint(ctx context.Context, fingerprint string) (int, error) {
	if fingerprint == "" {
		return 0, nil
	}
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(DISTINCT user_id)
        FROM dating_device_fingerprints
        WHERE fingerprint = $1`, fingerprint).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count users by fingerprint: %w", err)
	}
	return n, nil
}

// CountDistinctUsersOnIPLastHour returns COUNT(DISTINCT user_id) for
// rows seen on `ip` in the last hour. Drives the IP/ASN-velocity risk
// signal. Empty IP returns 0.
func (s *Store) CountDistinctUsersOnIPLastHour(ctx context.Context, ip string) (int, error) {
	if ip == "" {
		return 0, nil
	}
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(DISTINCT user_id)
        FROM dating_device_fingerprints
        WHERE ip = $1
          AND last_seen_at > NOW() - INTERVAL '1 hour'`, ip).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count users on ip last hour: %w", err)
	}
	return n, nil
}

// ListFingerprintsForUser returns the user's recent fingerprint values
// (most-recent first, capped at 16 — plenty for the risk job, keeps
// the row count bounded for users who genuinely rotate hardware).
func (s *Store) ListFingerprintsForUser(ctx context.Context, userID uuid.UUID) ([]*DeviceFingerprint, error) {
	if userID == uuid.Nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, fingerprint, COALESCE(ip, '')
        FROM dating_device_fingerprints
        WHERE user_id = $1
        ORDER BY last_seen_at DESC
        LIMIT 16`, userID)
	if err != nil {
		return nil, fmt.Errorf("list fingerprints: %w", err)
	}
	defer rows.Close()
	out := make([]*DeviceFingerprint, 0, 8)
	for rows.Next() {
		d := &DeviceFingerprint{}
		if err := rows.Scan(&d.ID, &d.UserID, &d.Fingerprint, &d.IP); err != nil {
			return nil, fmt.Errorf("scan fingerprint: %w", err)
		}
		out = append(out, d)
	}
	return out, rows.Err()
}
