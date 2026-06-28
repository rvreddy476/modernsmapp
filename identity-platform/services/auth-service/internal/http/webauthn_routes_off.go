//go:build !webauthn

package http

import "github.com/gin-gonic/gin"

// RegisterWebAuthnRoutes is a no-op in the default build. The real
// implementation (passkey registration/authentication via go-webauthn) is in
// webauthn_routes_on.go, compiled only with `-tags webauthn` once the library
// is vendored (`go get github.com/go-webauthn/webauthn`). Keeping it tag-gated
// means the default build never depends on the not-yet-fetched library.
func (h *Handler) RegisterWebAuthnRoutes(_ *gin.Engine, _, _ gin.HandlerFunc) {}
