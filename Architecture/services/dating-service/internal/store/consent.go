// Consent registry effects (Dating plan lane D9).
//
// dating_consent_log stays append-only (payments.go RecordConsent /
// ListConsentForUser). SetConsent appends an entry and applies what a
// withdrawal means in the same transaction, so the log never says
// "withdrawn" while the data it covered is still held.
package store

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Consent types with an effect at the store.
const (
	ConsentTypeReligion        = "sensitive_religion"
	ConsentTypeCommunity       = "sensitive_community"
	ConsentTypeBiometricSelfie = "biometric_selfie"
	ConsentTypeEchoes          = "echoes"
)

// LatestConsents returns the newest entry per consent type for the user.
func (s *Store) LatestConsents(ctx context.Context, userID uuid.UUID) (map[string]*ConsentEntry, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	rows, err := s.db.Query(ctx, `
        SELECT DISTINCT ON (consent_type) id, user_id, consent_type, granted, policy_version, created_at
        FROM dating_consent_log
        WHERE user_id = $1
        ORDER BY consent_type, created_at DESC, id DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("latest consents: %w", err)
	}
	defer rows.Close()
	out := map[string]*ConsentEntry{}
	for rows.Next() {
		e := &ConsentEntry{}
		if err := rows.Scan(&e.ID, &e.UserID, &e.ConsentType, &e.Granted, &e.PolicyVersion, &e.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan consent: %w", err)
		}
		out[e.ConsentType] = e
	}
	return out, rows.Err()
}

// SetConsent appends a consent entry and applies its effect in one
// transaction:
//
//   - religion / community withdrawn: the field is cleared (sealed and any
//     legacy plaintext);
//   - biometric selfie withdrawn: every unused selfie challenge is spent, so no
//     further check can start from one already issued;
//   - Echoes granted / withdrawn: echoes_consent follows, and a withdrawal
//     drops the Echoes snapshot already pulled in.
func (s *Store) SetConsent(ctx context.Context, userID uuid.UUID, consentType string, granted bool, policyVersion string) (*ConsentEntry, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user_id required")
	}
	if policyVersion == "" {
		return nil, fmt.Errorf("invalid: policy_version required")
	}
	var effects []string
	switch consentType {
	case ConsentTypeReligion:
		if !granted {
			effects = append(effects, `UPDATE dating_profiles SET religion = NULL, religion_sealed = NULL, updated_at = now()
                WHERE user_id = $1 AND (religion IS NOT NULL OR religion_sealed IS NOT NULL)`)
		}
	case ConsentTypeCommunity:
		if !granted {
			effects = append(effects, `UPDATE dating_profiles SET community = NULL, community_sealed = NULL, updated_at = now()
                WHERE user_id = $1 AND (community IS NOT NULL OR community_sealed IS NOT NULL)`)
		}
	case ConsentTypeBiometricSelfie:
		if !granted {
			effects = append(effects, `UPDATE dating_selfie_challenges SET used_at = now()
                WHERE user_id = $1 AND used_at IS NULL`)
		}
	case ConsentTypeEchoes:
		if granted {
			effects = append(effects, `UPDATE dating_profiles SET echoes_consent = true, updated_at = now()
                WHERE user_id = $1 AND deleted_at IS NULL`)
		} else {
			effects = append(effects,
				`UPDATE dating_profiles SET echoes_consent = false, updated_at = now() WHERE user_id = $1`,
				`DELETE FROM dating_echo_cache WHERE user_id = $1`)
		}
	default:
		return nil, fmt.Errorf("invalid: unknown consent type")
	}

	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("set consent: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	e := &ConsentEntry{}
	if err := tx.QueryRow(ctx, `
        INSERT INTO dating_consent_log (user_id, consent_type, granted, policy_version)
        VALUES ($1, $2, $3, $4)
        RETURNING id, user_id, consent_type, granted, policy_version, created_at`,
		userID, consentType, granted, policyVersion).Scan(&e.ID, &e.UserID, &e.ConsentType, &e.Granted, &e.PolicyVersion, &e.CreatedAt); err != nil {
		return nil, fmt.Errorf("set consent: record: %w", err)
	}
	for _, stmt := range effects {
		if _, err := tx.Exec(ctx, stmt, userID); err != nil {
			return nil, fmt.Errorf("set consent: apply %s: %w", consentType, err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("set consent: commit: %w", err)
	}
	return e, nil
}
