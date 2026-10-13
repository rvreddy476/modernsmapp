package ownedmedia

import "github.com/google/uuid"

// Reference is resolved by a product's permission-checked admin route, never
// supplied by the browser. It binds private evidence to its original uploader.
type Reference struct {
	MediaID uuid.UUID `json:"media_id"`
	OwnerID uuid.UUID `json:"owner_id"`
	Kind    string    `json:"kind"`
}
