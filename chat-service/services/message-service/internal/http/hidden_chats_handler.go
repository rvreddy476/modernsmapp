package http

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/chat-message-service/internal/hiddenchat"
	"github.com/atpost/chat-message-service/internal/service"
	"github.com/atpost/chat-shared/accessauth"
	"github.com/atpost/chat-shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Hidden chats (contract pinned 24 Oct 2026). Routes under /v1/chat:
//
//	GET  /hidden/settings              {configured, strict, shortcut}
//	PUT  /hidden/settings              partial {secret_key?, chat_password?, shortcut? (string|null), strict?, current_chat_password?}
//	POST /hidden/reset                 {secret_key, chat_password, strict?, shortcut?} — fresh re-auth required
//	POST /hidden/reveal                {secret_key} -> {reveal_token, expires_at} | 404 NOT_FOUND | 429 LOCKED
//	GET  /hidden/conversations         X-Chat-Reveal -> rows | 404
//	POST /hidden/open                  {chat_password} + X-Chat-Reveal -> {open_token, expires_at} | 401 | 429
//	POST /hidden/lock                  (additive) drops this device's reveal/open tokens
//	POST /conversations/:id/hide       (needs secrets set up)
//	POST /conversations/:id/unhide     (needs X-Chat-Open when locked: the gate)
//
// Every other /conversations/:id route of a conversation the caller hid
// needs X-Chat-Open (hiddenChatGate) — 403 CHAT_LOCKED without it.
//
// Nothing here logs a secret, a token or a request body.

const (
	revealHeader      = "X-Chat-Reveal"
	openHeader        = "X-Chat-Open"
	openExpiresHeader = "X-Chat-Open-Expires-At"
	revealExpHeader   = "X-Chat-Reveal-Expires-At"
	deviceIDHeader    = "X-Device-Id"
)

// HiddenChatsService is the hidden-chat surface (the real *service.Service
// implements it; older stubs do not, and the gate then stands aside).
type HiddenChatsService interface {
	GetHiddenChatSettings(ctx context.Context, userID uuid.UUID) (*service.HiddenChatSettingsView, error)
	PutHiddenChatSettings(ctx context.Context, who service.HiddenAttempt, in service.HiddenSettingsInput) (*service.HiddenChatSettingsView, error)
	ResetHiddenChats(ctx context.Context, userID uuid.UUID, freshReauth bool, in service.HiddenResetInput) (*service.HiddenResetResult, error)
	RevealHiddenChats(ctx context.Context, who service.HiddenAttempt, key string) (*service.HiddenToken, error)
	OpenHiddenChats(ctx context.Context, who service.HiddenAttempt, revealToken, password string) (*service.HiddenToken, error)
	LockHiddenChats(ctx context.Context, userID uuid.UUID, deviceKey string) error
	ListHiddenConversations(ctx context.Context, userID uuid.UUID, deviceKey, revealToken string, limit int, cursor *service.ConversationCursor) ([]service.ConversationResponse, *service.ConversationCursor, time.Time, error)
	HideConversation(ctx context.Context, userID, convID uuid.UUID) error
	UnhideConversation(ctx context.Context, userID, convID uuid.UUID) error
	IsConversationLockedFor(ctx context.Context, userID, conversationID uuid.UUID) (bool, error)
	LockedConversationsAmong(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	ValidateOpenToken(ctx context.Context, userID uuid.UUID, deviceKey, token string) (time.Time, bool, error)
	ViewerMayAccessLockedChatMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error)
}

func (h *Handler) hidden() (HiddenChatsService, bool) {
	hs, ok := h.svc.(HiddenChatsService)
	return hs, ok
}

func (h *Handler) registerHiddenChatRoutes(v1 *gin.RouterGroup) {
	v1.GET("/hidden/settings", h.GetHiddenChatSettings)
	v1.PUT("/hidden/settings", h.PutHiddenChatSettings)
	v1.POST("/hidden/reset", h.ResetHiddenChats)
	v1.POST("/hidden/reveal", h.RevealHiddenChats)
	v1.GET("/hidden/conversations", h.ListHiddenConversations)
	v1.POST("/hidden/open", h.OpenHiddenChats)
	v1.POST("/hidden/lock", h.LockHiddenChats)
	v1.POST("/conversations/:id/hide", h.HideConversation)
	v1.POST("/conversations/:id/unhide", h.UnhideConversation)
}

// hiddenGateExempt lists the /conversations/:id routes that do NOT need an
// open token on a locked conversation. Only hide: hiding an already hidden
// chat is a no-op and must not need the password.
var hiddenGateExempt = map[string]bool{
	http.MethodPost + " /v1/chat/conversations/:id/hide": true,
}

// hiddenChatGate requires a valid X-Chat-Open for every /conversations/:id
// route of a conversation the caller hid. A lookup failure refuses (503):
// a hidden chat never opens because the database hiccupped.
func (h *Handler) hiddenChatGate() gin.HandlerFunc {
	return func(c *gin.Context) {
		hs, ok := h.hidden()
		route := c.FullPath()
		if !ok || !strings.HasPrefix(route, "/v1/chat/conversations/:id") || hiddenGateExempt[c.Request.Method+" "+route] {
			c.Next()
			return
		}
		convID, err := uuid.Parse(c.Param("id"))
		if err != nil {
			c.Next() // the handler answers the bad id
			return
		}
		userID, ok := gateUserID(c)
		if !ok {
			c.Next() // the handler answers the missing identity
			return
		}
		if !h.requireOpen(c, hs, userID, []uuid.UUID{convID}) {
			c.Abort()
			return
		}
		c.Next()
	}
}

func gateUserID(c *gin.Context) (uuid.UUID, bool) {
	raw, ok := c.Get(userIDKey)
	if !ok {
		return uuid.Nil, false
	}
	s, _ := raw.(string)
	id, err := uuid.Parse(s)
	return id, err == nil
}

// requireOpen passes when none of convIDs is locked for userID, or when the
// request carries a valid open token (which it slides). Otherwise it writes
// 403 CHAT_LOCKED (503 on a lookup failure) and returns false.
func (h *Handler) requireOpen(c *gin.Context, hs HiddenChatsService, userID uuid.UUID, convIDs []uuid.UUID) bool {
	ctx := c.Request.Context()
	locked, err := hs.LockedConversationsAmong(ctx, userID, convIDs)
	if err != nil {
		h.log.Error("hidden chat lock lookup failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "Try again shortly", nil, nil)
		return false
	}
	if len(locked) == 0 {
		return true
	}
	exp, valid, err := hs.ValidateOpenToken(ctx, userID, deviceKeyOf(c), strings.TrimSpace(c.GetHeader(openHeader)))
	if err != nil {
		h.log.Error("hidden chat open-token check failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "Try again shortly", nil, nil)
		return false
	}
	if !valid {
		writeHiddenError(c, h, service.ErrChatLocked)
		return false
	}
	c.Writer.Header().Set(openExpiresHeader, exp.UTC().Format(time.RFC3339))
	return true
}

// hasValidOpen reports (without writing a response) whether the request's
// open token is valid — for entry points that redact instead of refuse.
func (h *Handler) hasValidOpen(c *gin.Context, hs HiddenChatsService, userID uuid.UUID) bool {
	_, ok, err := hs.ValidateOpenToken(c.Request.Context(), userID, deviceKeyOf(c), strings.TrimSpace(c.GetHeader(openHeader)))
	return err == nil && ok
}

// redactIfLocked strips a locked conversation returned by a non-gated entry
// point (open-a-DM, join by invite) unless the request holds an open token.
func (h *Handler) redactIfLocked(c *gin.Context, userID uuid.UUID, conv *service.ConversationResponse) bool {
	hs, ok := h.hidden()
	if !ok || conv == nil {
		return true
	}
	locked, err := hs.IsConversationLockedFor(c.Request.Context(), userID, conv.ID)
	if err != nil {
		h.log.Error("hidden chat lock lookup failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusServiceUnavailable, "UNAVAILABLE", "Try again shortly", nil, nil)
		return false
	}
	if !locked {
		return true
	}
	if h.hasValidOpen(c, hs, userID) {
		conv.Locked = true
		return true
	}
	service.RedactLockedConversation(conv)
	return true
}

// lockedChatMediaAllowed: media reachable only through conversations the
// viewer hid is allowed with the viewer's valid open token, bound to the
// device media-service names (session_id, else device_id).
func (h *Handler) lockedChatMediaAllowed(c *gin.Context, viewerID, mediaID uuid.UUID, body chatMediaAccessRequest) (bool, error) {
	hs, ok := h.hidden()
	token := strings.TrimSpace(body.OpenToken)
	if !ok || token == "" {
		return false, nil
	}
	reachable, err := hs.ViewerMayAccessLockedChatMedia(c.Request.Context(), viewerID, mediaID)
	if err != nil || !reachable {
		return false, err
	}
	device := deviceKeyFromHeader(body.DeviceID)
	if sid := strings.TrimSpace(body.SessionID); sid != "" {
		device = "sid:" + sid
	}
	_, valid, err := hs.ValidateOpenToken(c.Request.Context(), viewerID, device, token)
	return valid, err
}

// deviceKeyOf binds reveal/open tokens: the access token's session id (sid)
// — every signed-in device has its own session — else a client X-Device-Id
// (Android, later), else one shared key for session-less development tokens.
func deviceKeyOf(c *gin.Context) string {
	if sid := strings.TrimSpace(accessIdentityFrom(c).SessionID); sid != "" {
		return "sid:" + sid
	}
	return deviceKeyFromHeader(c.GetHeader(deviceIDHeader))
}

func deviceKeyFromHeader(raw string) string {
	if v := strings.TrimSpace(raw); v != "" && len(v) <= 128 {
		return "dev:" + v
	}
	return "nodevice"
}

func (h *Handler) hiddenWho(c *gin.Context, userID uuid.UUID) service.HiddenAttempt {
	return service.HiddenAttempt{UserID: userID, DeviceKey: deviceKeyOf(c), IP: c.ClientIP()}
}

// writeHiddenError maps the hidden-chat errors. Messages never echo input.
func writeHiddenError(c *gin.Context, h *Handler, err error) {
	var locked *service.HiddenLockedError
	var wrong *service.HiddenWrongPasswordError
	switch {
	case errors.As(err, &locked):
		secs := int(math.Ceil(locked.RetryAfter.Seconds()))
		if secs < 1 {
			secs = 1
		}
		c.Writer.Header().Set("Retry-After", strconv.Itoa(secs))
		api.Error(c.Writer, http.StatusTooManyRequests, "LOCKED", "Too many attempts. Try again later.", gin.H{"retry_after": secs}, nil)
	case errors.As(err, &wrong):
		api.Error(c.Writer, http.StatusUnauthorized, "WRONG_PASSWORD", "Wrong chat password", gin.H{"attempts_left": wrong.AttemptsLeft}, nil)
	case errors.Is(err, service.ErrHiddenNotFound), errors.Is(err, service.ErrNotMember):
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "Not found", nil, nil)
	case errors.Is(err, service.ErrChatLocked):
		api.Error(c.Writer, http.StatusForbidden, "CHAT_LOCKED", service.ErrChatLocked.Error(), nil, nil)
	case errors.Is(err, service.ErrStepUpRequired):
		api.Error(c.Writer, http.StatusForbidden, "STEP_UP_REQUIRED", service.ErrStepUpRequired.Error(), nil, nil)
	case errors.Is(err, service.ErrHiddenSecretsEqual):
		api.Error(c.Writer, http.StatusBadRequest, "SECRETS_MUST_DIFFER", service.ErrHiddenSecretsEqual.Error(), nil, nil)
	case errors.Is(err, hiddenchat.ErrSecretKeyInvalid):
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_SECRET_KEY", hiddenchat.ErrSecretKeyInvalid.Error(), nil, nil)
	case errors.Is(err, hiddenchat.ErrChatPasswordInvalid):
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_CHAT_PASSWORD", hiddenchat.ErrChatPasswordInvalid.Error(), nil, nil)
	case errors.Is(err, service.ErrHiddenSecretsRequired):
		api.Error(c.Writer, http.StatusBadRequest, "SECRETS_REQUIRED", service.ErrHiddenSecretsRequired.Error(), nil, nil)
	case errors.Is(err, service.ErrHiddenShortcutInvalid):
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_SHORTCUT", service.ErrHiddenShortcutInvalid.Error(), nil, nil)
	case errors.Is(err, service.ErrHiddenNotConfigured):
		api.Error(c.Writer, http.StatusConflict, "HIDDEN_CHATS_NOT_CONFIGURED", service.ErrHiddenNotConfigured.Error(), nil, nil)
	case errors.Is(err, service.ErrHiddenChatsDisabled):
		api.Error(c.Writer, http.StatusServiceUnavailable, "HIDDEN_CHATS_UNAVAILABLE", service.ErrHiddenChatsDisabled.Error(), nil, nil)
	default:
		h.log.Error("hidden chats request failed", "err", err, "request_id", RequestIDFromContext(c))
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Something went wrong", nil, nil)
	}
}

func (h *Handler) hiddenPrologue(c *gin.Context) (HiddenChatsService, uuid.UUID, bool) {
	userID, ok := getUserID(c, h.log)
	if !ok {
		return nil, uuid.Nil, false
	}
	hs, ok := h.hidden()
	if !ok {
		writeHiddenError(c, h, service.ErrHiddenChatsDisabled)
		return nil, uuid.Nil, false
	}
	return hs, userID, true
}

// GetHiddenChatSettings — GET /v1/chat/hidden/settings.
func (h *Handler) GetHiddenChatSettings(c *gin.Context) {
	hs, userID, ok := h.hiddenPrologue(c)
	if !ok {
		return
	}
	view, err := hs.GetHiddenChatSettings(c.Request.Context(), userID)
	if err != nil {
		writeHiddenError(c, h, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// readJSONObject reads a small JSON object body into raw fields, so absent
// and null can be told apart (shortcut: null = client default).
func readJSONObject(c *gin.Context) (map[string]json.RawMessage, bool) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 8<<10))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", nil, nil)
		return nil, false
	}
	fields := map[string]json.RawMessage{}
	if len(strings.TrimSpace(string(body))) == 0 {
		return fields, true
	}
	if err := json.Unmarshal(body, &fields); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", nil, nil)
		return nil, false
	}
	return fields, true
}

func optString(fields map[string]json.RawMessage, key string) (*string, bool) {
	raw, present := fields[key]
	if !present || string(raw) == "null" {
		return nil, true
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	return &v, true
}

func optBool(fields map[string]json.RawMessage, key string) (*bool, bool) {
	raw, present := fields[key]
	if !present || string(raw) == "null" {
		return nil, true
	}
	var v bool
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	return &v, true
}

// optShortcut: absent = keep; null or "" = client default; string = set.
func optShortcut(fields map[string]json.RawMessage) (*string, bool) {
	raw, present := fields["shortcut"]
	if !present {
		return nil, true
	}
	if string(raw) == "null" {
		empty := ""
		return &empty, true
	}
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return nil, false
	}
	return &v, true
}

// PutHiddenChatSettings — PUT /v1/chat/hidden/settings (partial body).
func (h *Handler) PutHiddenChatSettings(c *gin.Context) {
	hs, userID, ok := h.hiddenPrologue(c)
	if !ok {
		return
	}
	fields, ok := readJSONObject(c)
	if !ok {
		return
	}
	var in service.HiddenSettingsInput
	var good [5]bool
	in.SecretKey, good[0] = optString(fields, "secret_key")
	in.ChatPassword, good[1] = optString(fields, "chat_password")
	in.CurrentChatPassword, good[2] = optString(fields, "current_chat_password")
	in.Strict, good[3] = optBool(fields, "strict")
	in.Shortcut, good[4] = optShortcut(fields)
	for _, g := range good {
		if !g {
			api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", nil, nil)
			return
		}
	}
	view, err := hs.PutHiddenChatSettings(c.Request.Context(), h.hiddenWho(c, userID), in)
	if err != nil {
		writeHiddenError(c, h, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// ResetHiddenChats — POST /v1/chat/hidden/reset. The bearer (or an
// X-Step-Up-Token from the same user) must prove a re-auth within 5 minutes:
// step_up_at (POST /v1/auth/step-up) or auth_time (a fresh sign-in, which
// covers accounts without 2FA and Google sign-in).
func (h *Handler) ResetHiddenChats(c *gin.Context) {
	hs, userID, ok := h.hiddenPrologue(c)
	if !ok {
		return
	}
	fields, ok := readJSONObject(c)
	if !ok {
		return
	}
	key, ok1 := optString(fields, "secret_key")
	pw, ok2 := optString(fields, "chat_password")
	strict, ok3 := optBool(fields, "strict")
	shortcut, ok4 := optShortcut(fields)
	if !ok1 || !ok2 || !ok3 || !ok4 {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "Invalid request body", nil, nil)
		return
	}
	in := service.HiddenResetInput{Strict: strict, Shortcut: shortcut}
	if key != nil {
		in.SecretKey = *key
	}
	if pw != nil {
		in.ChatPassword = *pw
	}
	if key == nil || pw == nil {
		// Step-up is checked first so a missing re-auth is the answer even
		// for an incomplete body.
		if !freshReauth(c) {
			writeHiddenError(c, h, service.ErrStepUpRequired)
			return
		}
		writeHiddenError(c, h, service.ErrHiddenSecretsRequired)
		return
	}
	res, err := hs.ResetHiddenChats(c.Request.Context(), userID, freshReauth(c), in)
	if err != nil {
		writeHiddenError(c, h, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// freshReauth: the verified bearer, or a verified X-Step-Up-Token of the
// same user, carries step_up_at or auth_time within 5 minutes.
func freshReauth(c *gin.Context) bool {
	now := time.Now()
	if accessIdentityFrom(c).FreshReauth(now) {
		return true
	}
	if v, ok := c.Get(stepUpIdentityKey); ok {
		if id, ok := v.(accessauth.Identity); ok && id.FreshReauth(now) {
			return true
		}
	}
	return false
}

type hiddenTokenResponse struct {
	RevealToken string    `json:"reveal_token,omitempty"`
	OpenToken   string    `json:"open_token,omitempty"`
	ExpiresAt   time.Time `json:"expires_at"`
}

// RevealHiddenChats — POST /v1/chat/hidden/reveal {secret_key}.
func (h *Handler) RevealHiddenChats(c *gin.Context) {
	hs, userID, ok := h.hiddenPrologue(c)
	if !ok {
		return
	}
	fields, ok := readJSONObject(c)
	if !ok {
		return
	}
	key, good := optString(fields, "secret_key")
	// Not key-shaped (or absent): it cannot match any key, so the answer is
	// the wrong-key answer — without an attempt counted (a search for a
	// sentence is not a guess).
	if !good || key == nil || hiddenchat.ValidateSecretKey(*key) != nil {
		writeHiddenError(c, h, service.ErrHiddenNotFound)
		return
	}
	tok, err := hs.RevealHiddenChats(c.Request.Context(), h.hiddenWho(c, userID), *key)
	if err != nil {
		writeHiddenError(c, h, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, hiddenTokenResponse{RevealToken: tok.Token, ExpiresAt: tok.ExpiresAt}, nil)
}

// ListHiddenConversations — GET /v1/chat/hidden/conversations (X-Chat-Reveal).
func (h *Handler) ListHiddenConversations(c *gin.Context) {
	hs, userID, ok := h.hiddenPrologue(c)
	if !ok {
		return
	}
	limit := 20
	if l := c.Query("limit"); l != "" {
		if n, err := strconv.Atoi(l); err == nil && n > 0 && n <= 100 {
			limit = n
		}
	}
	var cursor *service.ConversationCursor
	if raw := c.Query("cursor"); raw != "" {
		cur, err := decodeConvCursor(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil, nil)
			return
		}
		cursor = cur
	}
	rows, next, exp, err := hs.ListHiddenConversations(c.Request.Context(), userID, deviceKeyOf(c),
		strings.TrimSpace(c.GetHeader(revealHeader)), limit, cursor)
	if err != nil {
		writeHiddenError(c, h, err)
		return
	}
	c.Writer.Header().Set(revealExpHeader, exp.UTC().Format(time.RFC3339))
	var meta *api.Meta
	if next != nil {
		if encoded, err := encodeConvCursor(*next); err == nil {
			meta = &api.Meta{NextCursor: encoded}
		}
	}
	if rows == nil {
		rows = []service.ConversationResponse{}
	}
	api.JSON(c.Writer, http.StatusOK, rows, meta)
}

// OpenHiddenChats — POST /v1/chat/hidden/open {chat_password} + X-Chat-Reveal.
func (h *Handler) OpenHiddenChats(c *gin.Context) {
	hs, userID, ok := h.hiddenPrologue(c)
	if !ok {
		return
	}
	fields, ok := readJSONObject(c)
	if !ok {
		return
	}
	pw, good := optString(fields, "chat_password")
	if !good || pw == nil {
		api.Error(c.Writer, http.StatusBadRequest, "BAD_REQUEST", "chat_password is required", nil, nil)
		return
	}
	tok, err := hs.OpenHiddenChats(c.Request.Context(), h.hiddenWho(c, userID), strings.TrimSpace(c.GetHeader(revealHeader)), *pw)
	if err != nil {
		writeHiddenError(c, h, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, hiddenTokenResponse{OpenToken: tok.Token, ExpiresAt: tok.ExpiresAt}, nil)
}

// LockHiddenChats — POST /v1/chat/hidden/lock: re-lock this device now.
func (h *Handler) LockHiddenChats(c *gin.Context) {
	hs, userID, ok := h.hiddenPrologue(c)
	if !ok {
		return
	}
	if err := hs.LockHiddenChats(c.Request.Context(), userID, deviceKeyOf(c)); err != nil {
		writeHiddenError(c, h, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"locked": true}, nil)
}

// HideConversation — POST /v1/chat/conversations/:id/hide.
func (h *Handler) HideConversation(c *gin.Context) {
	hs, userID, ok := h.hiddenPrologue(c)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	if err := hs.HideConversation(c.Request.Context(), userID, convID); err != nil {
		writeHiddenError(c, h, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"conversation_id": convID, "locked": true}, nil)
}

// UnhideConversation — POST /v1/chat/conversations/:id/unhide. The gate has
// already demanded X-Chat-Open when the conversation is locked.
func (h *Handler) UnhideConversation(c *gin.Context) {
	hs, userID, ok := h.hiddenPrologue(c)
	if !ok {
		return
	}
	convID, ok := parseConvID(c)
	if !ok {
		return
	}
	if err := hs.UnhideConversation(c.Request.Context(), userID, convID); err != nil {
		writeHiddenError(c, h, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"conversation_id": convID, "locked": false}, nil)
}
