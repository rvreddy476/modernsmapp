package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// UpsertKeyBundle stores or replaces a user's E2E public key bundle.
// Only public key material is stored; private keys never leave the client.
func UpsertKeyBundle(ctx context.Context, db *pgxpool.Pool, userID string, identityKey, signedPreKey, signature []byte, oneTimePreKeys [][]byte) error {
	_, err := db.Exec(ctx, `
		INSERT INTO chat.key_bundles (user_id, identity_key, signed_pre_key, pre_key_signature, one_time_pre_keys, updated_at)
		VALUES ($1, $2, $3, $4, $5, NOW())
		ON CONFLICT (user_id) DO UPDATE
		SET identity_key      = EXCLUDED.identity_key,
		    signed_pre_key    = EXCLUDED.signed_pre_key,
		    pre_key_signature = EXCLUDED.pre_key_signature,
		    one_time_pre_keys = EXCLUDED.one_time_pre_keys,
		    updated_at        = NOW()
	`, userID, identityKey, signedPreKey, signature, oneTimePreKeys)
	return err
}

// GetKeyBundle retrieves a user's public key bundle. Returns nil, nil when not found.
func GetKeyBundle(ctx context.Context, db *pgxpool.Pool, userID string) (map[string]interface{}, error) {
	var identityKey, signedPreKey, signature []byte
	var oneTimePreKeys [][]byte
	err := db.QueryRow(ctx, `
		SELECT identity_key, signed_pre_key, pre_key_signature, one_time_pre_keys
		FROM chat.key_bundles WHERE user_id = $1
	`, userID).Scan(&identityKey, &signedPreKey, &signature, &oneTimePreKeys)
	if err != nil {
		return nil, err
	}
	return map[string]interface{}{
		"user_id":           userID,
		"identity_key":      identityKey,
		"signed_pre_key":    signedPreKey,
		"pre_key_signature": signature,
		"one_time_pre_keys": oneTimePreKeys,
	}, nil
}
