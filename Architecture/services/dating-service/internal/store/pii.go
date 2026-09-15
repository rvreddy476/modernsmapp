// PII sealing at rest (Dating plan lane D9).
//
// The store seals religion, community, exact points and device signals with
// internal/datingpii before they reach Postgres and opens them on read. It
// fails closed: with no crypto configured (local/dev without keys only; boot
// refuses elsewhere) a write that would have to seal is refused with
// ErrPIINotConfigured, except where refusing is unsafe or pointless:
//
//   - a panic keeps the incident and drops its point (responders are still
//     paged);
//   - a device fingerprint is simply not recorded (best-effort signal).
//
// Reads open the sealed column and fall back to the legacy plaintext column
// for rows BackfillSealedPII has not reached yet.
package store

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/dating-service/internal/datingpii"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrPIINotConfigured is returned by a write that must seal while no keys are
// configured. The HTTP layer answers 503 PII_NOT_CONFIGURED.
var ErrPIINotConfigured = datingpii.ErrNotConfigured

// SetPII wires the sealing keys. Nil leaves sealing unavailable.
func (s *Store) SetPII(c *datingpii.Crypto) { s.pii = c }

// PII returns the configured crypto (nil when unconfigured).
func (s *Store) PII() *datingpii.Crypto { return s.pii }

// openSensitive returns the opened sealed value, else the legacy plaintext.
func (s *Store) openSensitive(ctx context.Context, sealed []byte, legacy *string, field string) *string {
	if len(sealed) == 0 {
		return legacy
	}
	v, err := s.pii.OpenSensitive(ctx, sealed)
	if err != nil {
		slog.Warn("dating pii: sealed field could not be opened", "field", field, "error", err)
		return nil
	}
	return &v
}

// openPoint sets lat/lng from the sealed point, else keeps the legacy columns.
func (s *Store) openPoint(ctx context.Context, sealed []byte, lat, lng **float64, what string) {
	if len(sealed) == 0 {
		return
	}
	la, lo, err := s.pii.OpenPoint(ctx, sealed)
	if err != nil {
		slog.Warn("dating pii: sealed point could not be opened", "table", what, "error", err)
		*lat, *lng = nil, nil
		return
	}
	*lat, *lng = &la, &lo
}

// PIIBackfillResult counts rows sealed by one BackfillSealedPII run.
type PIIBackfillResult struct {
	Profiles           int64
	PanicIncidents     int64
	LocationShares     int64
	Meets              int64
	DeviceFingerprints int64
}

// Total is the sum of every count.
func (r PIIBackfillResult) Total() int64 {
	return r.Profiles + r.PanicIncidents + r.LocationShares + r.Meets + r.DeviceFingerprints
}

// BackfillSealedPII seals every plaintext value written before D9 and clears
// the plaintext, batch rows per transaction (FOR UPDATE SKIP LOCKED, so
// replicas booting together do not collide). Idempotent: a second run finds
// nothing. Refuses to run without crypto.
func (s *Store) BackfillSealedPII(ctx context.Context, batch int) (PIIBackfillResult, error) {
	var out PIIBackfillResult
	if s.pii == nil {
		return out, ErrPIINotConfigured
	}
	if batch <= 0 {
		batch = 200
	}
	var err error
	if out.Profiles, err = s.backfillLoop(ctx, batch, s.backfillProfilesBatch); err != nil {
		return out, fmt.Errorf("backfill profiles: %w", err)
	}
	for _, t := range []struct {
		table string
		dst   *int64
	}{
		{"dating_panic_incidents", &out.PanicIncidents},
		{"dating_location_shares", &out.LocationShares},
		{"dating_meets", &out.Meets},
	} {
		table := t.table
		n, err := s.backfillLoop(ctx, batch, func(ctx context.Context, after uuid.UUID, limit int) (int, uuid.UUID, int64, error) {
			return s.backfillPointsBatch(ctx, table, after, limit)
		})
		if err != nil {
			return out, fmt.Errorf("backfill %s: %w", table, err)
		}
		*t.dst = n
	}
	if out.DeviceFingerprints, err = s.backfillLoop(ctx, batch, s.backfillFingerprintsBatch); err != nil {
		return out, fmt.Errorf("backfill device fingerprints: %w", err)
	}
	return out, nil
}

type backfillBatch func(ctx context.Context, after uuid.UUID, limit int) (seen int, last uuid.UUID, sealed int64, err error)

// backfillLoop walks the table in key order so a row that cannot be sealed
// is passed over instead of being selected forever.
func (s *Store) backfillLoop(ctx context.Context, batch int, fn backfillBatch) (int64, error) {
	var total int64
	after := uuid.Nil
	for {
		seen, last, sealed, err := fn(ctx, after, batch)
		if err != nil {
			return total, err
		}
		total += sealed
		if seen < batch {
			return total, nil
		}
		after = last
	}
}

func (s *Store) backfillProfilesBatch(ctx context.Context, after uuid.UUID, limit int) (int, uuid.UUID, int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, after, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	type row struct {
		id                  uuid.UUID
		religion, community *string
	}
	rows, err := tx.Query(ctx, `
        SELECT user_id, religion, community FROM dating_profiles
        WHERE (religion IS NOT NULL OR community IS NOT NULL) AND user_id > $1
        ORDER BY user_id LIMIT $2
        FOR UPDATE SKIP LOCKED`, after, limit)
	if err != nil {
		return 0, after, 0, err
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.religion, &r.community); err != nil {
			rows.Close()
			return 0, after, 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, after, 0, err
	}
	var sealed int64
	for _, r := range todo {
		after = r.id
		relBlob, relSet, err := s.sealOptionalSensitive(ctx, r.religion)
		if err != nil {
			slog.Warn("dating pii backfill: religion not sealed", "user_id", r.id, "error", err)
			continue
		}
		comBlob, comSet, err := s.sealOptionalSensitive(ctx, r.community)
		if err != nil {
			slog.Warn("dating pii backfill: community not sealed", "user_id", r.id, "error", err)
			continue
		}
		// A plaintext value is always newer than any sealed one: sealing
		// writers clear the plaintext, so only a pre-D9 writer leaves one.
		if _, err := tx.Exec(ctx, `
            UPDATE dating_profiles
            SET religion_sealed  = CASE WHEN $2 THEN $3::bytea ELSE religion_sealed END,
                community_sealed = CASE WHEN $4 THEN $5::bytea ELSE community_sealed END,
                religion = NULL, community = NULL
            WHERE user_id = $1`, r.id, relSet, relBlob, comSet, comBlob); err != nil {
			return 0, after, 0, err
		}
		sealed++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, after, 0, err
	}
	return len(todo), after, sealed, nil
}

// sealOptionalSensitive seals a present value. set reports whether the column
// is to be written at all; a blank value writes NULL (cleared).
func (s *Store) sealOptionalSensitive(ctx context.Context, v *string) (blob []byte, set bool, err error) {
	if v == nil {
		return nil, false, nil
	}
	if trimmed := strings.TrimSpace(*v); trimmed == "" {
		return nil, true, nil
	}
	blob, err = s.pii.SealSensitive(ctx, *v)
	if err != nil {
		return nil, false, err
	}
	return blob, true, nil
}

func (s *Store) backfillPointsBatch(ctx context.Context, table string, after uuid.UUID, limit int) (int, uuid.UUID, int64, error) {
	switch table {
	case "dating_panic_incidents", "dating_location_shares", "dating_meets":
	default:
		return 0, after, 0, fmt.Errorf("backfill: unexpected table")
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, after, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	type row struct {
		id       uuid.UUID
		lat, lng *float64
	}
	rows, err := tx.Query(ctx, `
        SELECT id, latitude, longitude FROM `+table+`
        WHERE (latitude IS NOT NULL OR longitude IS NOT NULL) AND id > $1
        ORDER BY id LIMIT $2
        FOR UPDATE SKIP LOCKED`, after, limit)
	if err != nil {
		return 0, after, 0, err
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.lat, &r.lng); err != nil {
			rows.Close()
			return 0, after, 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, after, 0, err
	}
	var sealed int64
	for _, r := range todo {
		after = r.id
		var blob []byte
		if r.lat != nil && r.lng != nil {
			if blob, err = s.pii.SealPoint(ctx, *r.lat, *r.lng); err != nil {
				slog.Warn("dating pii backfill: point not sealed", "table", table, "id", r.id, "error", err)
				continue
			}
		}
		// A half point is unusable; it is cleared rather than sealed.
		if _, err := tx.Exec(ctx, `
            UPDATE `+table+`
            SET location_sealed = COALESCE($2::bytea, location_sealed), latitude = NULL, longitude = NULL
            WHERE id = $1`, r.id, blob); err != nil {
			return 0, after, 0, err
		}
		sealed++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, after, 0, err
	}
	return len(todo), after, sealed, nil
}

func (s *Store) backfillFingerprintsBatch(ctx context.Context, after uuid.UUID, limit int) (int, uuid.UUID, int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, after, 0, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	type row struct {
		id, userID  uuid.UUID
		fp, ip      *string
		first, last time.Time
	}
	rows, err := tx.Query(ctx, `
        SELECT id, user_id, fingerprint, ip, first_seen_at, last_seen_at FROM dating_device_fingerprints
        WHERE (fingerprint IS NOT NULL OR ip IS NOT NULL) AND id > $1
        ORDER BY id LIMIT $2
        FOR UPDATE SKIP LOCKED`, after, limit)
	if err != nil {
		return 0, after, 0, err
	}
	var todo []row
	for rows.Next() {
		var r row
		if err := rows.Scan(&r.id, &r.userID, &r.fp, &r.ip, &r.first, &r.last); err != nil {
			rows.Close()
			return 0, after, 0, err
		}
		todo = append(todo, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, after, 0, err
	}
	var sealed int64
	for _, r := range todo {
		after = r.id
		var fp, ip datingpii.DeviceSignal
		if r.fp != nil && strings.TrimSpace(*r.fp) != "" {
			if fp, err = s.pii.SealFingerprint(ctx, *r.fp); err != nil {
				slog.Warn("dating pii backfill: fingerprint not sealed", "id", r.id, "error", err)
				continue
			}
		}
		if r.ip != nil && strings.TrimSpace(*r.ip) != "" {
			if ip, err = s.pii.SealIP(ctx, *r.ip); err != nil {
				slog.Warn("dating pii backfill: ip not sealed", "id", r.id, "error", err)
				continue
			}
		}
		// A sealed row for the same device may already exist (written after
		// D9 shipped): merge the legacy row into it and delete the legacy row.
		var existing uuid.UUID
		err := tx.QueryRow(ctx, `
            SELECT id FROM dating_device_fingerprints
            WHERE user_id = $1 AND fingerprint_lookup = $2 AND id <> $3`, r.userID, nullIfEmpty(fp.Lookup), r.id).Scan(&existing)
		switch {
		case err == nil:
			if _, err := tx.Exec(ctx, `
                UPDATE dating_device_fingerprints
                SET first_seen_at = LEAST(first_seen_at, $2),
                    ip_sealed = COALESCE(ip_sealed, $3::bytea),
                    ip_lookup = COALESCE(ip_lookup, $4)
                WHERE id = $1`, existing, r.first, nullBytes(ip.Blob), nullIfEmpty(ip.Lookup)); err != nil {
				return 0, after, 0, err
			}
			if _, err := tx.Exec(ctx, `DELETE FROM dating_device_fingerprints WHERE id = $1`, r.id); err != nil {
				return 0, after, 0, err
			}
		case errors.Is(err, pgx.ErrNoRows):
			if _, err := tx.Exec(ctx, `
                UPDATE dating_device_fingerprints
                SET fingerprint_sealed = COALESCE($2::bytea, fingerprint_sealed),
                    fingerprint_lookup = COALESCE($3, fingerprint_lookup),
                    ip_sealed = COALESCE($4::bytea, ip_sealed),
                    ip_lookup = COALESCE($5, ip_lookup),
                    fingerprint = NULL, ip = NULL
                WHERE id = $1`, r.id, nullBytes(fp.Blob), nullIfEmpty(fp.Lookup), nullBytes(ip.Blob), nullIfEmpty(ip.Lookup)); err != nil {
				return 0, after, 0, err
			}
		default:
			return 0, after, 0, err
		}
		sealed++
	}
	if err := tx.Commit(ctx); err != nil {
		return 0, after, 0, err
	}
	return len(todo), after, sealed, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func nullBytes(b []byte) any {
	if len(b) == 0 {
		return nil
	}
	return b
}
