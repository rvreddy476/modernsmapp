// Package crypto provides the interface contract for end-to-end encrypted messaging.
// Full implementation requires the Signal Protocol:
//   - X3DH (Extended Triple Diffie-Hellman) for key agreement
//   - Double Ratchet Algorithm for message encryption
//   - Client-side key generation; server stores only ciphertext and public key bundles
package crypto

import "context"

// KeyBundle holds a user's public key material for E2E key exchange.
// The server stores only public keys; private keys never leave the client.
type KeyBundle struct {
	UserID          string   `json:"user_id"`
	IdentityKey     []byte   `json:"identity_key"`      // Ed25519 public key
	SignedPreKey    []byte   `json:"signed_pre_key"`    // Curve25519 signed pre-key
	PreKeySignature []byte   `json:"pre_key_signature"` // Ed25519 signature of pre-key
	OneTimePreKeys  [][]byte `json:"one_time_pre_keys"` // Batch of ephemeral pre-keys
}

// E2EService defines operations for managing E2E key bundles.
// The server is a "blind" intermediary: it routes ciphertext without decrypting.
type E2EService interface {
	// PublishKeyBundle stores a user's public key bundle for session establishment.
	// Called on device registration or key rotation.
	PublishKeyBundle(ctx context.Context, bundle KeyBundle) error

	// GetKeyBundle returns the recipient's key bundle for session establishment.
	// Clients use this to perform X3DH key agreement before sending the first message.
	GetKeyBundle(ctx context.Context, userID string) (*KeyBundle, error)

	// ConsumeOneTimePreKey removes and returns one one-time pre-key for the given user.
	// One-time pre-keys provide forward secrecy per session.
	ConsumeOneTimePreKey(ctx context.Context, userID string) ([]byte, error)
}

// StubE2EService is a non-encrypting stub for development.
// Replace with a full Signal Protocol implementation for production E2E encryption.
type StubE2EService struct {
	bundles map[string]*KeyBundle
}

func NewStubE2EService() *StubE2EService {
	return &StubE2EService{bundles: make(map[string]*KeyBundle)}
}

func (s *StubE2EService) PublishKeyBundle(_ context.Context, bundle KeyBundle) error {
	s.bundles[bundle.UserID] = &bundle
	return nil
}

func (s *StubE2EService) GetKeyBundle(_ context.Context, userID string) (*KeyBundle, error) {
	if b, ok := s.bundles[userID]; ok {
		return b, nil
	}
	return nil, nil
}

func (s *StubE2EService) ConsumeOneTimePreKey(_ context.Context, userID string) ([]byte, error) {
	b, ok := s.bundles[userID]
	if !ok || len(b.OneTimePreKeys) == 0 {
		return nil, nil
	}
	key := b.OneTimePreKeys[0]
	b.OneTimePreKeys = b.OneTimePreKeys[1:]
	return key, nil
}
