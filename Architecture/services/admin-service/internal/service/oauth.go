package service

import (
	"context"
	"errors"

	"github.com/atpost/admin-service/internal/secrethash"
	"github.com/atpost/admin-service/internal/store/postgres"
)

// CreateOAuthClient registers a new OAuth client for a developer. Only an
// argon2id hash of secret is stored; the plaintext never reaches the database.
func (s *Service) CreateOAuthClient(ctx context.Context, client *postgres.OAuthClient, secret string) error {
	hashed, err := secrethash.Hash(secret)
	if err != nil {
		return err
	}
	client.ClientSecretHash = hashed
	return s.store.CreateOAuthClient(ctx, client)
}

// GetOAuthClientByClientID returns an OAuth client by its client_id.
func (s *Service) GetOAuthClientByClientID(ctx context.Context, clientID string) (*postgres.OAuthClient, error) {
	return s.store.GetOAuthClientByClientID(ctx, clientID)
}

// VerifyOAuthClientSecret reports whether secret is the registered secret of an
// active client. An unknown, inactive, or unhashed client never verifies.
func (s *Service) VerifyOAuthClientSecret(ctx context.Context, clientID, secret string) (bool, error) {
	client, err := s.store.GetOAuthClientByClientID(ctx, clientID)
	if err != nil {
		return false, err
	}
	if client == nil || !client.IsActive || secret == "" {
		return false, nil
	}
	switch err := secrethash.Verify(client.ClientSecretHash, secret); {
	case err == nil:
		return true, nil
	case errors.Is(err, secrethash.ErrMismatch), errors.Is(err, secrethash.ErrInvalidHash):
		return false, nil
	default:
		return false, err
	}
}

// HashPlaintextOAuthSecrets rewrites any client secret still stored in plaintext
// as an argon2id hash. Safe to run on every boot.
func (s *Service) HashPlaintextOAuthSecrets(ctx context.Context) (int, error) {
	return s.store.HashPlaintextOAuthSecrets(ctx, secrethash.IsHash, secrethash.Hash)
}
