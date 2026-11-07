// auth.sessions, auth.login_anomalies and auth.trusted_devices: creating,
// reading, listing and revoking sessions, recording and acknowledging login
// anomalies, and trusted devices. Refresh rotation lives in
// sessions_rotation.go. Pure move out of users.go; behaviour is unchanged.

package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// --- auth.sessions ---

func (s *Store) CreateSession(ctx context.Context, sess *Session) error {
	kind := sess.Kind
	if kind == "" {
		kind = SessionKindConsumer
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.sessions (session_id, user_id, refresh_token_hash, device_id, platform, ip, user_agent, is_active, created_at, expires_at, auth_time, amr, kind, family_id)
		VALUES ($1, $2, $3, $4, $5, $6, $7, TRUE, $8, $9, $10, $11, $12, $1)
	`, sess.ID, sess.UserID, sess.RefreshToken, sess.DeviceID, sess.Platform, sess.IP, sess.UserAgent, sess.CreatedAt, sess.ExpiresAt, sess.AuthTime, sess.AMR, kind)
	return err
}

func scanSession(row pgx.Row) (*Session, error) {
	var sess Session
	var familyID *uuid.UUID
	var lastRefreshIP *string
	err := row.Scan(
		&sess.ID, &sess.UserID, &sess.RefreshToken,
		&sess.DeviceID, &sess.Platform, &sess.IP, &sess.UserAgent,
		&sess.IsActive, &sess.CreatedAt, &sess.ExpiresAt, &sess.RevokedAt,
		&familyID, &sess.anomalyFlagged, &sess.LastRefreshAt, &lastRefreshIP,
		&sess.AuthTime, &sess.AMR, &sess.Kind,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil
		}
		return nil, err
	}
	sess.FamilyID = familyID
	if lastRefreshIP != nil {
		sess.LastRefreshIP = *lastRefreshIP
	}
	return &sess, nil
}

const allSessionCols = `session_id, user_id, refresh_token_hash, device_id, platform, ip, user_agent, is_active, created_at, expires_at, revoked_at, family_id, anomaly_flagged, last_refresh_at, last_refresh_ip, auth_time, amr, kind`

func (s *Store) GetSessionByRefreshTokenHash(ctx context.Context, refreshTokenHash string) (*Session, error) {
	row := s.db.QueryRow(ctx, `SELECT `+allSessionCols+` FROM auth.sessions WHERE refresh_token_hash = $1`, refreshTokenHash)
	return scanSession(row)
}

func (s *Store) GetSessionByID(ctx context.Context, sessionID uuid.UUID) (*Session, error) {
	row := s.db.QueryRow(ctx, `SELECT `+allSessionCols+` FROM auth.sessions WHERE session_id = $1`, sessionID)
	return scanSession(row)
}

func (s *Store) ListActiveSessions(ctx context.Context, userID uuid.UUID) ([]Session, error) {
	rows, err := s.db.Query(ctx, `
		SELECT `+allSessionCols+`
		FROM auth.sessions
		WHERE user_id = $1 AND is_active = TRUE AND revoked_at IS NULL
		ORDER BY created_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []Session
	for rows.Next() {
		var sess Session
		var familyID *uuid.UUID
		var lastRefreshIP *string
		if err := rows.Scan(
			&sess.ID, &sess.UserID, &sess.RefreshToken,
			&sess.DeviceID, &sess.Platform, &sess.IP, &sess.UserAgent,
			&sess.IsActive, &sess.CreatedAt, &sess.ExpiresAt, &sess.RevokedAt,
			&familyID, &sess.anomalyFlagged, &sess.LastRefreshAt, &lastRefreshIP,
			&sess.AuthTime, &sess.AMR, &sess.Kind,
		); err != nil {
			return nil, err
		}
		sess.FamilyID = familyID
		if lastRefreshIP != nil {
			sess.LastRefreshIP = *lastRefreshIP
		}
		sessions = append(sessions, sess)
	}
	return sessions, rows.Err()
}

// RotateSessionWithFingerprint swaps the session's refresh token from oldHash
// to newHash, also updating last_refresh_ip and anomaly_flagged. It lives in
// sessions_rotation.go with the reuse-detection queries it pairs with.

// RecordLoginAnomaly persists one anomaly detection. anomalyType must
// match the auth.login_anomalies CHECK constraint; metadata is opaque
// JSON used by the security UI to render context.
func (s *Store) RecordLoginAnomaly(ctx context.Context, userID uuid.UUID, anomalyType, ip, userAgent, deviceID, countryCode string, riskScore int, challenged bool, metadata map[string]any) error {
	metaBytes, _ := json.Marshal(metadata)
	if len(metaBytes) == 0 {
		metaBytes = []byte("{}")
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.login_anomalies
			(user_id, anomaly_type, ip, user_agent, device_id, country_code, metadata, risk_score, challenged)
		VALUES ($1, $2, NULLIF($3,''), NULLIF($4,''), NULLIF($5,''), NULLIF($6,''), $7, $8, $9)
	`, userID, anomalyType, ip, userAgent, deviceID, countryCode, metaBytes, riskScore, challenged)
	return err
}

// ListLoginAnomalies returns the most recent anomaly events for a user
// so the in-app security screen can render "where you've signed in
// from". Limit caps at 50 to bound the response.
func (s *Store) ListLoginAnomalies(ctx context.Context, userID uuid.UUID, limit int) ([]LoginAnomaly, error) {
	if limit <= 0 || limit > 50 {
		limit = 20
	}
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, anomaly_type, COALESCE(ip,''), COALESCE(user_agent,''),
		       COALESCE(device_id,''), COALESCE(country_code,''), metadata, risk_score,
		       challenged, acknowledged_at, occurred_at
		FROM auth.login_anomalies
		WHERE user_id = $1
		ORDER BY occurred_at DESC
		LIMIT $2
	`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []LoginAnomaly
	for rows.Next() {
		var a LoginAnomaly
		var metaBytes []byte
		if err := rows.Scan(&a.ID, &a.UserID, &a.AnomalyType, &a.IP, &a.UserAgent,
			&a.DeviceID, &a.CountryCode, &metaBytes, &a.RiskScore,
			&a.Challenged, &a.AcknowledgedAt, &a.OccurredAt); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(metaBytes, &a.Metadata)
		out = append(out, a)
	}
	return out, rows.Err()
}

// AcknowledgeAnomaly flips acknowledged_at on a user's own anomaly so
// the security inbox can be cleared. Returns the affected row count.
func (s *Store) AcknowledgeAnomaly(ctx context.Context, userID, anomalyID uuid.UUID) (int64, error) {
	cmd, err := s.db.Exec(ctx, `
		UPDATE auth.login_anomalies
		SET acknowledged_at = NOW()
		WHERE id = $1 AND user_id = $2 AND acknowledged_at IS NULL
	`, anomalyID, userID)
	if err != nil {
		return 0, err
	}
	return cmd.RowsAffected(), nil
}

func (s *Store) RevokeSession(ctx context.Context, sessionID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.sessions
		SET revoked_at = NOW(), is_active = FALSE
		WHERE session_id = $1 AND revoked_at IS NULL
	`, sessionID)
	return err
}

func (s *Store) RevokeAllSessions(ctx context.Context, userID uuid.UUID) (int64, error) {
	res, err := s.db.Exec(ctx, `
		UPDATE auth.sessions
		SET revoked_at = NOW(), is_active = FALSE
		WHERE user_id = $1 AND is_active = TRUE AND revoked_at IS NULL
	`, userID)
	if err != nil {
		return 0, err
	}
	return res.RowsAffected(), nil
}

// --- auth.trusted_devices ---

func (s *Store) UpsertTrustedDevice(ctx context.Context, d *TrustedDevice) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.trusted_devices (id, user_id, device_fingerprint, device_name, last_used_at, trusted_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (user_id, device_fingerprint) DO UPDATE
		SET last_used_at = NOW(), device_name = COALESCE(EXCLUDED.device_name, auth.trusted_devices.device_name)
	`, d.ID, d.UserID, d.DeviceFingerprint, d.DeviceName)
	return err
}

func (s *Store) ListTrustedDevices(ctx context.Context, userID uuid.UUID) ([]TrustedDevice, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, device_fingerprint, device_name, last_used_at, trusted_at
		FROM auth.trusted_devices
		WHERE user_id = $1
		ORDER BY last_used_at DESC
	`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var devices []TrustedDevice
	for rows.Next() {
		var d TrustedDevice
		if err := rows.Scan(&d.ID, &d.UserID, &d.DeviceFingerprint, &d.DeviceName, &d.LastUsedAt, &d.TrustedAt); err != nil {
			return nil, err
		}
		devices = append(devices, d)
	}
	return devices, rows.Err()
}

func (s *Store) DeleteTrustedDevice(ctx context.Context, userID, deviceID uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM auth.trusted_devices WHERE id = $1 AND user_id = $2
	`, deviceID, userID)
	return err
}
