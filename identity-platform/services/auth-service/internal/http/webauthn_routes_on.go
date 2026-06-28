//go:build webauthn

// Passkey (WebAuthn) ceremony. Compiled only with `-tags webauthn` after:
//   cd identity-platform/services/auth-service && go get github.com/go-webauthn/webauthn@latest && go mod tidy
// Targets go-webauthn v0.10+; if the API has shifted, the adjustments are
// localized to this file. The default build uses webauthn_routes_off.go (no-op),
// so fetching the library is opt-in.
//
// Routes (under /v1/auth/passkeys):
//   POST /register/begin   (authed)  → creation options
//   POST /register/finish  (authed)  → store the new credential
//   GET  ""                (authed)  → list the caller's passkeys
//   DELETE /:id            (authed)  → remove a passkey
//   POST /login/begin      (public)  → discoverable assertion options
//   POST /login/finish     (public)  → verify + mint a session (passwordless)
package http

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/atpost/identity-auth-service/internal/store"
	"github.com/atpost/identity-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/google/uuid"
)

const passkeySessionTTL = 5 * time.Minute

// ── webauthn.User adapter ──────────────────────────────────────────
type webAuthnUser struct {
	u     *store.User
	creds []store.WebAuthnCredential
}

func (w webAuthnUser) WebAuthnID() []byte {
	b, _ := w.u.ID.MarshalBinary()
	return b
}
func (w webAuthnUser) WebAuthnName() string        { return userLabel(w.u) }
func (w webAuthnUser) WebAuthnDisplayName() string { return userLabel(w.u) }
func (w webAuthnUser) WebAuthnIcon() string        { return "" }
func (w webAuthnUser) WebAuthnCredentials() []webauthn.Credential {
	out := make([]webauthn.Credential, 0, len(w.creds))
	for _, c := range w.creds {
		out = append(out, toWebauthnCredential(c))
	}
	return out
}

func userLabel(u *store.User) string {
	if u.Email != nil && *u.Email != "" {
		return *u.Email
	}
	if u.Phone != "" {
		return u.Phone
	}
	return u.ID.String()
}

// ── credential mapping ─────────────────────────────────────────────
func toWebauthnCredential(c store.WebAuthnCredential) webauthn.Credential {
	transports := make([]protocol.AuthenticatorTransport, 0, len(c.Transports))
	for _, t := range c.Transports {
		transports = append(transports, protocol.AuthenticatorTransport(t))
	}
	return webauthn.Credential{
		ID:              c.CredentialID,
		PublicKey:       c.PublicKey,
		AttestationType: c.AttestationType,
		Transport:       transports,
		Authenticator: webauthn.Authenticator{
			AAGUID:       c.AAGUID,
			SignCount:    c.SignCount,
			CloneWarning: c.CloneWarning,
		},
	}
}

func fromWebauthnCredential(userID uuid.UUID, cred *webauthn.Credential) *store.WebAuthnCredential {
	transports := make([]string, 0, len(cred.Transport))
	for _, t := range cred.Transport {
		transports = append(transports, string(t))
	}
	return &store.WebAuthnCredential{
		UserID:          userID,
		CredentialID:    cred.ID,
		PublicKey:       cred.PublicKey,
		AttestationType: cred.AttestationType,
		AAGUID:          cred.Authenticator.AAGUID,
		SignCount:       cred.Authenticator.SignCount,
		Transports:      transports,
		Name:            "passkey",
	}
}

// ── session (challenge) persistence in Redis ───────────────────────
func (h *Handler) saveWAStore(ctx context.Context, key string, sd *webauthn.SessionData) error {
	b, err := json.Marshal(sd)
	if err != nil {
		return err
	}
	return h.rdb.Set(ctx, key, b, passkeySessionTTL).Err()
}

func (h *Handler) loadWASession(ctx context.Context, key string) (*webauthn.SessionData, error) {
	b, err := h.rdb.Get(ctx, key).Bytes()
	if err != nil {
		return nil, err
	}
	h.rdb.Del(ctx, key) // single-use
	var sd webauthn.SessionData
	if err := json.Unmarshal(b, &sd); err != nil {
		return nil, err
	}
	return &sd, nil
}

func (h *Handler) loadUser(ctx context.Context, id uuid.UUID) (webAuthnUser, error) {
	u, err := h.waStore.GetUserByID(ctx, id)
	if err != nil {
		return webAuthnUser{}, err
	}
	creds, err := h.waStore.ListWebAuthnCredentials(ctx, id)
	if err != nil {
		return webAuthnUser{}, err
	}
	return webAuthnUser{u: u, creds: creds}, nil
}

// ── routes ─────────────────────────────────────────────────────────
func (h *Handler) RegisterWebAuthnRoutes(r *gin.Engine, authMW, csrfMW gin.HandlerFunc) {
	wa, err := webauthn.New(&webauthn.Config{
		RPDisplayName: h.cfg.WebAuthnRPDisplayName,
		RPID:          h.cfg.WebAuthnRPID,
		RPOrigins:     h.cfg.WebAuthnRPOrigins,
	})
	if err != nil {
		h.log.Error("webauthn init failed", "err", err)
		return
	}
	g := r.Group("/v1/auth/passkeys")
	authed := g.Group("", authMW, csrfMW)
	authed.POST("/register/begin", h.passkeyRegisterBegin(wa))
	authed.POST("/register/finish", h.passkeyRegisterFinish(wa))
	authed.GET("", h.passkeyList)
	authed.DELETE("/:id", h.passkeyDelete)
	g.POST("/login/begin", h.passkeyLoginBegin(wa))
	g.POST("/login/finish", h.passkeyLoginFinish(wa))
}

func callerUUID(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user ID", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

func (h *Handler) passkeyRegisterBegin(wa *webauthn.WebAuthn) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := callerUUID(c)
		if !ok {
			return
		}
		user, err := h.loadUser(c.Request.Context(), uid)
		if err != nil {
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "load user", nil, nil)
			return
		}
		options, session, err := wa.BeginRegistration(user)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "WEBAUTHN", err.Error(), nil, nil)
			return
		}
		if err := h.saveWAStore(c.Request.Context(), "wa:reg:"+uid.String(), session); err != nil {
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "save session", nil, nil)
			return
		}
		api.JSON(c.Writer, http.StatusOK, options, nil)
	}
}

func (h *Handler) passkeyRegisterFinish(wa *webauthn.WebAuthn) gin.HandlerFunc {
	return func(c *gin.Context) {
		uid, ok := callerUUID(c)
		if !ok {
			return
		}
		user, err := h.loadUser(c.Request.Context(), uid)
		if err != nil {
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "load user", nil, nil)
			return
		}
		session, err := h.loadWASession(c.Request.Context(), "wa:reg:"+uid.String())
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "WEBAUTHN", "no registration in progress", nil, nil)
			return
		}
		parsed, err := protocol.ParseCredentialCreationResponseBody(c.Request.Body)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "WEBAUTHN", err.Error(), nil, nil)
			return
		}
		cred, err := wa.CreateCredential(user, *session, parsed)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "WEBAUTHN", err.Error(), nil, nil)
			return
		}
		rec := fromWebauthnCredential(uid, cred)
		if name := c.Query("name"); name != "" {
			rec.Name = name
		}
		if err := h.waStore.CreateWebAuthnCredential(c.Request.Context(), rec); err != nil {
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "store credential", nil, nil)
			return
		}
		api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "registered"}, nil)
	}
}

func (h *Handler) passkeyList(c *gin.Context) {
	uid, ok := callerUUID(c)
	if !ok {
		return
	}
	creds, err := h.waStore.ListWebAuthnCredentials(c.Request.Context(), uid)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "list", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"passkeys": creds}, nil)
}

func (h *Handler) passkeyDelete(c *gin.Context) {
	uid, ok := callerUUID(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "invalid id", nil, nil)
		return
	}
	if err := h.waStore.DeleteWebAuthnCredential(c.Request.Context(), uid, id); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "delete", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}

func (h *Handler) passkeyLoginBegin(wa *webauthn.WebAuthn) gin.HandlerFunc {
	return func(c *gin.Context) {
		options, session, err := wa.BeginDiscoverableLogin()
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "WEBAUTHN", err.Error(), nil, nil)
			return
		}
		// Challenge keys the single-use session; client echoes it on finish.
		key := "wa:login:" + session.Challenge.String()
		if err := h.saveWAStore(c.Request.Context(), key, session); err != nil {
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "save session", nil, nil)
			return
		}
		api.JSON(c.Writer, http.StatusOK, gin.H{"options": options, "session": session.Challenge.String()}, nil)
	}
}

func (h *Handler) passkeyLoginFinish(wa *webauthn.WebAuthn) gin.HandlerFunc {
	return func(c *gin.Context) {
		sessionKey := c.Query("session")
		if sessionKey == "" {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "missing session", nil, nil)
			return
		}
		session, err := h.loadWASession(c.Request.Context(), "wa:login:"+sessionKey)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "WEBAUTHN", "no login in progress", nil, nil)
			return
		}
		parsed, err := protocol.ParseCredentialRequestResponseBody(c.Request.Body)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "WEBAUTHN", err.Error(), nil, nil)
			return
		}
		// Resolve the user from the authenticator's userHandle (= our user id).
		discover := func(_, userHandle []byte) (webauthn.User, error) {
			uid, err := uuid.FromBytes(userHandle)
			if err != nil {
				return nil, err
			}
			user, err := h.loadUser(c.Request.Context(), uid)
			if err != nil {
				return nil, err
			}
			return user, nil
		}
		cred, err := wa.ValidateDiscoverableLogin(discover, *session, parsed)
		if err != nil {
			api.Error(c.Writer, http.StatusUnauthorized, "WEBAUTHN", err.Error(), nil, nil)
			return
		}
		// Advance the signature counter (clone/replay defense).
		_ = h.waStore.UpdateWebAuthnSignCount(c.Request.Context(), cred.ID, cred.Authenticator.SignCount, cred.Authenticator.CloneWarning)

		// Map credential → user, then mint a real session (passwordless login).
		stored, err := h.waStore.GetWebAuthnCredentialByID(c.Request.Context(), cred.ID)
		if err != nil || stored == nil {
			api.Error(c.Writer, http.StatusUnauthorized, "WEBAUTHN", "unknown credential", nil, nil)
			return
		}
		resp, err := h.svc.IssueSessionForUser(c.Request.Context(), stored.UserID, "web", "web",
			c.ClientIP(), c.Request.UserAgent())
		if err != nil {
			api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "issue session", nil, nil)
			return
		}
		api.JSON(c.Writer, http.StatusOK, resp, nil)
	}
}
