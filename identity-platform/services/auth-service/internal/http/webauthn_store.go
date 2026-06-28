package http

import (
	"context"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/google/uuid"
)

// WebAuthnStore is the persistence the passkey ceremony needs. *store.Store
// satisfies it; wired in via SetWebAuthnStore so the default build doesn't
// depend on the webauthn-tagged code. Defined untagged so the Handler struct is
// identical in both builds.
type WebAuthnStore interface {
	CreateWebAuthnCredential(ctx context.Context, c *store.WebAuthnCredential) error
	ListWebAuthnCredentials(ctx context.Context, userID uuid.UUID) ([]store.WebAuthnCredential, error)
	GetWebAuthnCredentialByID(ctx context.Context, credentialID []byte) (*store.WebAuthnCredential, error)
	UpdateWebAuthnSignCount(ctx context.Context, credentialID []byte, signCount uint32, cloneWarning bool) error
	DeleteWebAuthnCredential(ctx context.Context, userID, id uuid.UUID) error
	GetUserByID(ctx context.Context, userID uuid.UUID) (*store.User, error)
}

// SetWebAuthnStore wires the credential store (call from main with *store.Store).
func (h *Handler) SetWebAuthnStore(s WebAuthnStore) { h.waStore = s }
