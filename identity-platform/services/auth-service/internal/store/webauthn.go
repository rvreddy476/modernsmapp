package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// WebAuthnCredential is a registered passkey/authenticator. Persistence only —
// the spec verification (CBOR/COSE/attestation/assertion) lives in the service
// behind the `webauthn` build tag, which maps these rows to go-webauthn types.
type WebAuthnCredential struct {
	ID              uuid.UUID  `json:"id"`
	UserID          uuid.UUID  `json:"user_id"`
	CredentialID    []byte     `json:"-"`
	PublicKey       []byte     `json:"-"`
	AttestationType string     `json:"attestation_type"`
	AAGUID          []byte     `json:"-"`
	SignCount       uint32     `json:"sign_count"`
	Transports      []string   `json:"transports"`
	CloneWarning    bool       `json:"clone_warning"`
	Name            string     `json:"name"`
	CreatedAt       time.Time  `json:"created_at"`
	LastUsedAt      *time.Time `json:"last_used_at,omitempty"`
}

// CreateWebAuthnCredential persists a newly registered authenticator.
func (s *Store) CreateWebAuthnCredential(ctx context.Context, c *WebAuthnCredential) error {
	if c.Name == "" {
		c.Name = "passkey"
	}
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.webauthn_credentials
		  (user_id, credential_id, public_key, attestation_type, aaguid, sign_count, transports, clone_warning, name)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
	`, c.UserID, c.CredentialID, c.PublicKey, c.AttestationType, c.AAGUID,
		int64(c.SignCount), c.Transports, c.CloneWarning, c.Name)
	if err != nil {
		return fmt.Errorf("create webauthn credential: %w", err)
	}
	return nil
}

// ListWebAuthnCredentials returns a user's registered authenticators.
func (s *Store) ListWebAuthnCredentials(ctx context.Context, userID uuid.UUID) ([]WebAuthnCredential, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, credential_id, public_key, attestation_type, aaguid,
		       sign_count, transports, clone_warning, name, created_at, last_used_at
		FROM auth.webauthn_credentials WHERE user_id = $1 ORDER BY created_at
	`, userID)
	if err != nil {
		return nil, fmt.Errorf("list webauthn credentials: %w", err)
	}
	defer rows.Close()
	var out []WebAuthnCredential
	for rows.Next() {
		c, err := scanWebAuthnCredential(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetWebAuthnCredentialByID looks up a credential by its (authenticator) handle.
func (s *Store) GetWebAuthnCredentialByID(ctx context.Context, credentialID []byte) (*WebAuthnCredential, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, credential_id, public_key, attestation_type, aaguid,
		       sign_count, transports, clone_warning, name, created_at, last_used_at
		FROM auth.webauthn_credentials WHERE credential_id = $1
	`, credentialID)
	if err != nil {
		return nil, fmt.Errorf("get webauthn credential: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	c, err := scanWebAuthnCredential(rows)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// UpdateWebAuthnSignCount advances the stored signature counter after a
// successful assertion (replay/clone defense) and stamps last_used_at.
func (s *Store) UpdateWebAuthnSignCount(ctx context.Context, credentialID []byte, signCount uint32, cloneWarning bool) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.webauthn_credentials
		SET sign_count = $2, clone_warning = $3, last_used_at = NOW()
		WHERE credential_id = $1
	`, credentialID, int64(signCount), cloneWarning)
	if err != nil {
		return fmt.Errorf("update webauthn sign count: %w", err)
	}
	return nil
}

// DeleteWebAuthnCredential removes one of the caller's authenticators.
func (s *Store) DeleteWebAuthnCredential(ctx context.Context, userID, id uuid.UUID) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth.webauthn_credentials WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return fmt.Errorf("delete webauthn credential: %w", err)
	}
	return nil
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanWebAuthnCredential(r rowScanner) (WebAuthnCredential, error) {
	var c WebAuthnCredential
	var signCount int64
	if err := r.Scan(&c.ID, &c.UserID, &c.CredentialID, &c.PublicKey, &c.AttestationType,
		&c.AAGUID, &signCount, &c.Transports, &c.CloneWarning, &c.Name, &c.CreatedAt, &c.LastUsedAt); err != nil {
		return c, fmt.Errorf("scan webauthn credential: %w", err)
	}
	c.SignCount = uint32(signCount)
	return c, nil
}
