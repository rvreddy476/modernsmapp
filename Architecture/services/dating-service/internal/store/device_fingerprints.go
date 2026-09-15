// Device-fingerprint store — §P0-7 Phase B, sealed in lane D9.
//
// Two risk signals come from this table:
//
//   - UpsertDeviceFingerprint is called from a Gin middleware on every
//     pulse/spark request that carries an X-Device-Fingerprint header.
//     The fingerprint and IP are sealed (internal/datingpii) and stored with
//     a salted lookup hash; INSERT ... ON CONFLICT(user_id, fingerprint_lookup)
//     keeps one row per device per user while refreshing last_seen_at and
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
//   - ListFingerprintsForUser returns the caller's fingerprints, opened, so
//     the risk-compute job can count each one.
//
// Counting compares lookup hashes, never plaintext. Rows written before D9
// keep their plaintext until BackfillSealedPII reaches them, and the counts
// match those rows by plaintext meanwhile.
package store

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// DeviceFingerprint is one row of dating_device_fingerprints, opened.
type DeviceFingerprint struct {
	ID          uuid.UUID
	UserID      uuid.UUID
	Fingerprint string
	IP          string
}

// UpsertDeviceFingerprint records (user_id, fingerprint) sealed, or refreshes
// last_seen_at + ip when the pair already exists. An empty fingerprint is a
// no-op so the middleware can call this unconditionally. Without sealing keys
// (local/dev only) nothing is recorded: the signal is best-effort and is
// never stored in plaintext.
func (s *Store) UpsertDeviceFingerprint(ctx context.Context, userID uuid.UUID, fingerprint, ip string) error {
	if userID == uuid.Nil || strings.TrimSpace(fingerprint) == "" {
		return nil
	}
	if s.pii == nil {
		slog.Debug("device fingerprint not recorded: PII sealing is not configured")
		return nil
	}
	fp, err := s.pii.SealFingerprint(ctx, fingerprint)
	if err != nil {
		return fmt.Errorf("seal device fingerprint: %w", err)
	}
	var ipBlob, ipLookup any
	if strings.TrimSpace(ip) != "" {
		sealedIP, err := s.pii.SealIP(ctx, ip)
		if err != nil {
			return fmt.Errorf("seal device ip: %w", err)
		}
		ipBlob, ipLookup = sealedIP.Blob, sealedIP.Lookup
	}
	_, err = s.db.Exec(ctx, `
        INSERT INTO dating_device_fingerprints (user_id, fingerprint_sealed, fingerprint_lookup, ip_sealed, ip_lookup)
        VALUES ($1, $2, $3, $4, $5)
        ON CONFLICT (user_id, fingerprint_lookup) DO UPDATE
            SET last_seen_at = NOW(),
                ip_sealed = COALESCE(EXCLUDED.ip_sealed, dating_device_fingerprints.ip_sealed),
                ip_lookup = COALESCE(EXCLUDED.ip_lookup, dating_device_fingerprints.ip_lookup)`,
		userID, fp.Blob, fp.Lookup, ipBlob, ipLookup)
	if err != nil {
		return fmt.Errorf("upsert device fingerprint: %w", err)
	}
	return nil
}

// CountUsersByFingerprint returns the number of DISTINCT accounts that
// have ever been observed using the supplied fingerprint: live user_ids
// plus purged accounts whose hashed fingerprint was retained (lane D8), so
// deleting an account and signing up again on the same device still counts
// as reuse. Drives the device-reuse risk signal.
func (s *Store) CountUsersByFingerprint(ctx context.Context, fingerprint string) (int, error) {
	if fingerprint == "" {
		return 0, nil
	}
	lookup := s.fingerprintLookup(fingerprint)
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT (SELECT COUNT(DISTINCT user_id)
                FROM dating_device_fingerprints
                WHERE fingerprint_lookup = $3 OR fingerprint = $1)
             + (SELECT COUNT(DISTINCT subject_token)
                FROM dating_retained_risk_signals
                WHERE kind = 'device_fingerprint' AND value_hash = $2)`,
		fingerprint, s.HashSignal(RetainedSignalDeviceFingerprint, fingerprint), lookup).Scan(&n)
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
	var lookup string
	if s.pii != nil {
		lookup, _ = s.pii.IPLookup(ip)
	}
	var n int
	err := s.db.QueryRow(ctx, `
        SELECT COUNT(DISTINCT user_id)
        FROM dating_device_fingerprints
        WHERE (ip_lookup = $2 OR ip = $1)
          AND last_seen_at > NOW() - INTERVAL '1 hour'`, ip, lookup).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count users on ip last hour: %w", err)
	}
	return n, nil
}

// ListFingerprintsForUser returns the user's recent fingerprint values,
// opened (most-recent first, capped at 16 — plenty for the risk job, keeps
// the row count bounded for users who genuinely rotate hardware). A row that
// cannot be opened is skipped.
func (s *Store) ListFingerprintsForUser(ctx context.Context, userID uuid.UUID) ([]*DeviceFingerprint, error) {
	if userID == uuid.Nil {
		return nil, nil
	}
	rows, err := s.db.Query(ctx, `
        SELECT id, user_id, fingerprint, fingerprint_sealed, ip, ip_sealed
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
		var fpPlain, ipPlain *string
		var fpSealed, ipSealed []byte
		if err := rows.Scan(&d.ID, &d.UserID, &fpPlain, &fpSealed, &ipPlain, &ipSealed); err != nil {
			return nil, fmt.Errorf("scan fingerprint: %w", err)
		}
		if d.Fingerprint, err = s.openDeviceSignal(ctx, fpSealed, fpPlain); err != nil || d.Fingerprint == "" {
			if err != nil {
				slog.Warn("device fingerprint could not be opened", "user_id", userID, "error", err)
			}
			continue
		}
		if d.IP, err = s.openDeviceSignal(ctx, ipSealed, ipPlain); err != nil {
			slog.Warn("device ip could not be opened", "user_id", userID, "error", err)
			d.IP = ""
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

// fingerprintLookup is the lookup hash for counting, or "" without keys
// (which matches no sealed row).
func (s *Store) fingerprintLookup(fp string) string {
	if s.pii == nil {
		return ""
	}
	l, err := s.pii.FingerprintLookup(fp)
	if err != nil {
		return ""
	}
	return l
}

// openDeviceSignal opens a sealed fingerprint/IP, else returns the legacy
// plaintext (or "").
func (s *Store) openDeviceSignal(ctx context.Context, sealed []byte, legacy *string) (string, error) {
	if len(sealed) > 0 {
		return s.pii.OpenDeviceSignal(ctx, sealed)
	}
	if legacy == nil {
		return "", nil
	}
	return *legacy, nil
}
