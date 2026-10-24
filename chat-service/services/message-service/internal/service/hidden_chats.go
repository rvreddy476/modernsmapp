package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/chat-message-service/internal/hiddenchat"
	"github.com/atpost/chat-message-service/internal/ratelimit"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Hidden chats (contract pinned 24 Oct 2026).
//
// A member hides individual conversations for themself (conversation_members
// .locked). Two account secrets, both stored only as Argon2id hashes:
//
//   - the SECRET KEY (4-12 letters/digits) reveals the hidden list — POST
//     /hidden/reveal returns a reveal token;
//   - the CHAT PASSWORD (6-64 characters, never equal to the key) opens a
//     hidden chat — POST /hidden/open (with the reveal token) returns an open
//     token, required on every message / media / socket path of a locked
//     conversation.
//
// Tokens: 32 random bytes, stored as SHA-256, bound to the user and the
// device (the access token's session id), 15 minutes sliding. Wrong attempts:
// 5 lock the secret for 15 min, then 1 h, then 24 h — counted in Postgres so a
// restart cannot reset them — and every attempt is also rate limited per
// account, device and IP.
//
// A wrong secret key answers exactly like "not configured" (same error, same
// Argon2 cost): nobody can learn from the reveal route whether an account
// hides anything.

// Hidden-chat tuning (contract).
const (
	HiddenTokenTTL      = 15 * time.Minute
	HiddenLockThreshold = 5
)

// HiddenLockLadder is the escalating lockout (contract: 15 min, 1 h, 24 h;
// the last step repeats).
var HiddenLockLadder = []time.Duration{15 * time.Minute, time.Hour, 24 * time.Hour}

// Per-attempt rate limits on top of the Postgres lockout. A Redis outage
// fails these OPEN (logged): the durable lockout still holds.
var (
	hiddenAttemptAccountLimit = ratelimit.Limit{Action: "hidden_attempt_acct", Max: 30, Window: 15 * time.Minute}
	hiddenAttemptDeviceLimit  = ratelimit.Limit{Action: "hidden_attempt_dev", Max: 20, Window: 15 * time.Minute}
	hiddenAttemptIPLimit      = ratelimit.Limit{Action: "hidden_attempt_ip", Max: 60, Window: 15 * time.Minute}
)

var (
	// ErrHiddenChatsDisabled: HIDDEN_CHATS_ENABLED is off. Existing locks
	// still hold (lists stay filtered, locked chats still need a token); only
	// new setup / hide / reveal / open is refused.
	ErrHiddenChatsDisabled = errors.New("hidden chats are not available")
	// ErrHiddenNotFound is the one answer for a wrong secret key, an account
	// without secrets, and a missing/expired reveal token.
	ErrHiddenNotFound = errors.New("not found")
	// ErrHiddenNotConfigured: hide before the secrets were set.
	ErrHiddenNotConfigured = errors.New("set up a secret key and chat password first")
	// ErrHiddenSecretsEqual: the chat password equals the secret key.
	ErrHiddenSecretsEqual = errors.New("the chat password must be different from the secret key")
	// ErrHiddenSecretsRequired: the first setup (or a reset) needs both.
	ErrHiddenSecretsRequired = errors.New("secret_key and chat_password are required")
	// ErrHiddenShortcutInvalid: shortcut longer than 64 characters.
	ErrHiddenShortcutInvalid = errors.New("shortcut must be at most 64 characters")
	// ErrStepUpRequired: reset without a fresh re-auth from identity.
	ErrStepUpRequired = errors.New("sign in again or complete a step-up within 5 minutes, then retry")
	// ErrChatLocked: a locked conversation without a valid open token.
	ErrChatLocked = errors.New("this chat is hidden; open it with your chat password")
)

// HiddenLockedError is 429 LOCKED.
type HiddenLockedError struct{ RetryAfter time.Duration }

func (e *HiddenLockedError) Error() string { return "too many attempts; try again later" }

// HiddenWrongPasswordError is 401 WRONG_PASSWORD.
type HiddenWrongPasswordError struct{ AttemptsLeft int }

func (e *HiddenWrongPasswordError) Error() string { return "wrong chat password" }

// hiddenChatStore is the Postgres surface (checked type assertion: older
// fakes do not implement it, and every hidden-chat route then answers
// ErrHiddenChatsDisabled).
type hiddenChatStore interface {
	GetHiddenChatSettings(ctx context.Context, userID uuid.UUID) (*postgres.HiddenChatSettings, error)
	SaveHiddenChatSettings(ctx context.Context, hs postgres.HiddenChatSettings) error
	GetHiddenChatAttempts(ctx context.Context, userID uuid.UUID, kind string) (postgres.AttemptState, error)
	RecordHiddenChatFailure(ctx context.Context, userID uuid.UUID, kind string, now time.Time, threshold int, ladder []time.Duration) (postgres.AttemptState, error)
	ClearHiddenChatFailures(ctx context.Context, userID uuid.UUID, kind string) error
	InsertHiddenChatToken(ctx context.Context, tokenHash []byte, userID uuid.UUID, deviceKey, kind string, expiresAt time.Time) error
	TouchHiddenChatToken(ctx context.Context, tokenHash []byte, userID uuid.UUID, deviceKey, kind string, now, newExpiry time.Time) (time.Time, bool, error)
	DeleteHiddenChatTokens(ctx context.Context, userID uuid.UUID, deviceKey string) error
	LockConversationForMember(ctx context.Context, conversationID, userID uuid.UUID) (bool, int64, error)
	UnlockConversationForMember(ctx context.Context, conversationID, userID uuid.UUID) error
	IsConversationLockedFor(ctx context.Context, conversationID, userID uuid.UUID) (bool, error)
	LockedConversationsAmong(ctx context.Context, userID uuid.UUID, convIDs []uuid.UUID) (map[uuid.UUID]bool, error)
	LockedMemberIDs(ctx context.Context, conversationID uuid.UUID) ([]uuid.UUID, error)
	ListHiddenConversations(ctx context.Context, userID uuid.UUID, limit int, cursorTs *time.Time, cursorID *uuid.UUID) ([]postgres.Conversation, error)
	ResetHiddenChats(ctx context.Context, r postgres.HiddenChatsReset) ([]uuid.UUID, error)
	ViewerMayAccessLockedChatMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error)
}

func (s *Service) hiddenStore() (hiddenChatStore, bool) {
	hs, ok := s.convStore.(hiddenChatStore)
	return hs, ok
}

// SetHiddenChatsEnabled is the HIDDEN_CHATS_ENABLED kill switch (default on).
func (s *Service) SetHiddenChatsEnabled(on bool) { s.hiddenChatsDisabled = !on }

func (s *Service) hiddenFeature() (hiddenChatStore, error) {
	hs, ok := s.hiddenStore()
	if !ok || s.hiddenChatsDisabled {
		return nil, ErrHiddenChatsDisabled
	}
	return hs, nil
}

// HiddenChatSettingsView is GET /v1/chat/hidden/settings. Never secrets.
type HiddenChatSettingsView struct {
	Configured bool    `json:"configured"`
	Strict     bool    `json:"strict"`
	Shortcut   *string `json:"shortcut"`
}

func viewOf(hs *postgres.HiddenChatSettings) *HiddenChatSettingsView {
	if hs == nil {
		return &HiddenChatSettingsView{}
	}
	return &HiddenChatSettingsView{Configured: true, Strict: hs.Strict, Shortcut: hs.Shortcut}
}

// GetHiddenChatSettings is GET /v1/chat/hidden/settings.
func (s *Service) GetHiddenChatSettings(ctx context.Context, userID uuid.UUID) (*HiddenChatSettingsView, error) {
	hs, err := s.hiddenFeature()
	if err != nil {
		return nil, err
	}
	row, err := hs.GetHiddenChatSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	return viewOf(row), nil
}

// HiddenSettingsInput is PUT /v1/chat/hidden/settings. Nil means "keep".
// Shortcut "" restores the client default (stored NULL).
type HiddenSettingsInput struct {
	SecretKey           *string
	ChatPassword        *string
	CurrentChatPassword *string
	Strict              *bool
	Shortcut            *string
}

// HiddenAttempt identifies who is trying a secret, for the rate limits.
type HiddenAttempt struct {
	UserID    uuid.UUID
	DeviceKey string
	IP        string
}

// PutHiddenChatSettings is PUT /v1/chat/hidden/settings: the first setup
// needs both secrets and no current password; any later change needs
// current_chat_password (a wrong one counts toward the password lockout).
// Changing either secret drops every reveal/open token of the account, so
// every device re-locks.
func (s *Service) PutHiddenChatSettings(ctx context.Context, who HiddenAttempt, in HiddenSettingsInput) (*HiddenChatSettingsView, error) {
	hs, err := s.hiddenFeature()
	if err != nil {
		return nil, err
	}
	userID := who.UserID
	row, err := hs.GetHiddenChatSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	shortcut, err := normalizeShortcut(in.Shortcut, row)
	if err != nil {
		return nil, err
	}

	if row == nil {
		if in.SecretKey == nil || in.ChatPassword == nil {
			return nil, ErrHiddenSecretsRequired
		}
		next, err := newSecrets(userID, *in.SecretKey, *in.ChatPassword)
		if err != nil {
			return nil, err
		}
		next.Strict = in.Strict != nil && *in.Strict
		next.Shortcut = shortcut
		if err := hs.SaveHiddenChatSettings(ctx, *next); err != nil {
			return nil, err
		}
		return viewOf(next), nil
	}

	// Configured: the current chat password authorises every change.
	if in.CurrentChatPassword == nil {
		return nil, &HiddenWrongPasswordError{AttemptsLeft: s.passwordAttemptsLeft(ctx, hs, userID)}
	}
	if err := s.checkSecret(ctx, hs, who, postgres.SecretKindPassword, *in.CurrentChatPassword, row.ChatPasswordHash, false); err != nil {
		return nil, err
	}

	next := *row
	secretsChanged := false
	switch {
	case in.SecretKey != nil && in.ChatPassword != nil:
		fresh, err := newSecrets(userID, *in.SecretKey, *in.ChatPassword)
		if err != nil {
			return nil, err
		}
		next.SecretKeyHash, next.ChatPasswordHash = fresh.SecretKeyHash, fresh.ChatPasswordHash
		secretsChanged = true
	case in.SecretKey != nil:
		if err := hiddenchat.ValidateSecretKey(*in.SecretKey); err != nil {
			return nil, err
		}
		if hiddenchat.Verify(*in.SecretKey, row.ChatPasswordHash) {
			return nil, ErrHiddenSecretsEqual
		}
		if next.SecretKeyHash, err = hiddenchat.Hash(*in.SecretKey); err != nil {
			return nil, err
		}
		secretsChanged = true
	case in.ChatPassword != nil:
		if err := hiddenchat.ValidateChatPassword(*in.ChatPassword); err != nil {
			return nil, err
		}
		if hiddenchat.Verify(*in.ChatPassword, row.SecretKeyHash) {
			return nil, ErrHiddenSecretsEqual
		}
		if next.ChatPasswordHash, err = hiddenchat.Hash(*in.ChatPassword); err != nil {
			return nil, err
		}
		secretsChanged = true
	}
	if in.Strict != nil {
		next.Strict = *in.Strict
	}
	next.Shortcut = shortcut
	if err := hs.SaveHiddenChatSettings(ctx, next); err != nil {
		return nil, err
	}
	if secretsChanged {
		if err := hs.DeleteHiddenChatTokens(ctx, userID, ""); err != nil {
			return nil, err
		}
	}
	return viewOf(&next), nil
}

// newSecrets validates and hashes a fresh pair.
func newSecrets(userID uuid.UUID, key, password string) (*postgres.HiddenChatSettings, error) {
	if err := hiddenchat.ValidateSecretKey(key); err != nil {
		return nil, err
	}
	if err := hiddenchat.ValidateChatPassword(password); err != nil {
		return nil, err
	}
	if key == password {
		return nil, ErrHiddenSecretsEqual
	}
	kh, err := hiddenchat.Hash(key)
	if err != nil {
		return nil, err
	}
	ph, err := hiddenchat.Hash(password)
	if err != nil {
		return nil, err
	}
	return &postgres.HiddenChatSettings{UserID: userID, SecretKeyHash: kh, ChatPasswordHash: ph}, nil
}

func normalizeShortcut(in *string, row *postgres.HiddenChatSettings) (*string, error) {
	if in == nil {
		if row != nil {
			return row.Shortcut, nil
		}
		return nil, nil
	}
	v := strings.TrimSpace(*in)
	if v == "" {
		return nil, nil
	}
	if utf8.RuneCountInString(v) > 64 {
		return nil, ErrHiddenShortcutInvalid
	}
	return &v, nil
}

// HiddenResetInput is POST /v1/chat/hidden/reset.
type HiddenResetInput struct {
	SecretKey    string
	ChatPassword string
	Strict       *bool
	Shortcut     *string
}

// HiddenResetResult is the reset response.
type HiddenResetResult struct {
	HiddenChatSettingsView
	// WipedConversations counts the locked conversations a strict reset
	// cleared for this user.
	WipedConversations int `json:"wiped_conversations"`
}

// ResetHiddenChats is POST /v1/chat/hidden/reset: for a user who forgot the
// secrets. freshReauth must say identity re-authenticated the user (full
// sign-in or step-up) within 5 minutes. Sets new secrets, clears every
// lockout and token; when the account was in strict mode, every locked
// conversation is wiped for THIS user only (clear-for-me + unhide) instead of
// being kept behind the new secrets. The other members are untouched.
func (s *Service) ResetHiddenChats(ctx context.Context, userID uuid.UUID, freshReauth bool, in HiddenResetInput) (*HiddenResetResult, error) {
	hs, err := s.hiddenFeature()
	if err != nil {
		return nil, err
	}
	if !freshReauth {
		return nil, ErrStepUpRequired
	}
	next, err := newSecrets(userID, in.SecretKey, in.ChatPassword)
	if err != nil {
		return nil, err
	}
	row, err := hs.GetHiddenChatSettings(ctx, userID)
	if err != nil {
		return nil, err
	}
	shortcut, err := normalizeShortcut(in.Shortcut, row)
	if err != nil {
		return nil, err
	}
	wipe := row != nil && row.Strict
	next.Strict = wipe
	if in.Strict != nil {
		next.Strict = *in.Strict
	}
	next.Shortcut = shortcut
	wiped, err := hs.ResetHiddenChats(ctx, postgres.HiddenChatsReset{Settings: *next, Wipe: wipe, At: s.clock().UTC()})
	if err != nil {
		return nil, err
	}
	return &HiddenResetResult{HiddenChatSettingsView: *viewOf(next), WipedConversations: len(wiped)}, nil
}

// HiddenToken is a reveal or open token response.
type HiddenToken struct {
	Token     string    `json:"-"`
	ExpiresAt time.Time `json:"expires_at"`
}

// RevealHiddenChats is POST /v1/chat/hidden/reveal. ErrHiddenNotFound for a
// wrong key AND for an account without secrets (one Argon2 verification
// either way); *HiddenLockedError while the key is locked out.
func (s *Service) RevealHiddenChats(ctx context.Context, who HiddenAttempt, key string) (*HiddenToken, error) {
	hs, err := s.hiddenFeature()
	if err != nil {
		return nil, err
	}
	row, err := hs.GetHiddenChatSettings(ctx, who.UserID)
	if err != nil {
		return nil, err
	}
	stored := ""
	if row != nil {
		stored = row.SecretKeyHash
	}
	if err := s.checkSecret(ctx, hs, who, postgres.SecretKindKey, key, stored, true); err != nil {
		return nil, err
	}
	return s.issueHiddenToken(ctx, hs, who, postgres.TokenKindReveal)
}

// OpenHiddenChats is POST /v1/chat/hidden/open: needs a valid reveal token
// (ErrHiddenNotFound otherwise) and the chat password.
func (s *Service) OpenHiddenChats(ctx context.Context, who HiddenAttempt, revealToken, password string) (*HiddenToken, error) {
	hs, err := s.hiddenFeature()
	if err != nil {
		return nil, err
	}
	if _, ok, err := s.touchToken(ctx, hs, who.UserID, who.DeviceKey, postgres.TokenKindReveal, revealToken); err != nil {
		return nil, err
	} else if !ok {
		return nil, ErrHiddenNotFound
	}
	row, err := hs.GetHiddenChatSettings(ctx, who.UserID)
	if err != nil {
		return nil, err
	}
	if row == nil {
		return nil, ErrHiddenNotFound
	}
	if err := s.checkSecret(ctx, hs, who, postgres.SecretKindPassword, password, row.ChatPasswordHash, false); err != nil {
		return nil, err
	}
	return s.issueHiddenToken(ctx, hs, who, postgres.TokenKindOpen)
}

// LockHiddenChats drops this device's reveal and open tokens (an explicit
// re-lock; clients also simply forget them).
func (s *Service) LockHiddenChats(ctx context.Context, userID uuid.UUID, deviceKey string) error {
	hs, ok := s.hiddenStore()
	if !ok {
		return ErrHiddenChatsDisabled
	}
	if deviceKey == "" {
		return nil
	}
	return hs.DeleteHiddenChatTokens(ctx, userID, deviceKey)
}

// checkSecret is the one attempt path for both secrets: rate limits, the
// durable lockout, one Argon2 verification (a dummy when stored is empty),
// then the failure count. notFound selects the reveal wording (every failure
// is ErrHiddenNotFound); otherwise a failure is *HiddenWrongPasswordError,
// or *HiddenLockedError when it triggered the lockout.
func (s *Service) checkSecret(ctx context.Context, hs hiddenChatStore, who HiddenAttempt, kind, candidate, stored string, notFound bool) error {
	if !s.hiddenAttemptAllowed(ctx, who) {
		return &HiddenLockedError{RetryAfter: hiddenAttemptAccountLimit.Window}
	}
	now := s.clock()
	st, err := hs.GetHiddenChatAttempts(ctx, who.UserID, kind)
	if err != nil {
		return err
	}
	if st.LockedUntil != nil && st.LockedUntil.After(now) {
		return &HiddenLockedError{RetryAfter: st.LockedUntil.Sub(now)}
	}
	if hiddenchat.VerifyOrDummy(candidate, stored) {
		if err := hs.ClearHiddenChatFailures(ctx, who.UserID, kind); err != nil {
			return err
		}
		return nil
	}
	after, err := hs.RecordHiddenChatFailure(ctx, who.UserID, kind, now, HiddenLockThreshold, HiddenLockLadder)
	if err != nil {
		return err
	}
	if notFound {
		return ErrHiddenNotFound
	}
	if after.LockedUntil != nil && after.LockedUntil.After(now) {
		return &HiddenLockedError{RetryAfter: after.LockedUntil.Sub(now)}
	}
	return &HiddenWrongPasswordError{AttemptsLeft: HiddenLockThreshold - after.Failures}
}

func (s *Service) passwordAttemptsLeft(ctx context.Context, hs hiddenChatStore, userID uuid.UUID) int {
	st, err := hs.GetHiddenChatAttempts(ctx, userID, postgres.SecretKindPassword)
	if err != nil {
		return 0
	}
	left := HiddenLockThreshold - st.Failures
	if left < 0 {
		return 0
	}
	return left
}

// hiddenAttemptAllowed applies the per-account, per-device and per-IP rate
// limits. A limiter error is logged and allowed: the Postgres lockout is the
// durable guard.
func (s *Service) hiddenAttemptAllowed(ctx context.Context, who HiddenAttempt) bool {
	if s.rateLimiter == nil {
		return true
	}
	keys := []struct {
		limit ratelimit.Limit
		key   string
	}{
		{hiddenAttemptAccountLimit, who.UserID.String()},
		{hiddenAttemptDeviceLimit, who.UserID.String() + ":" + who.DeviceKey},
		{hiddenAttemptIPLimit, who.IP},
	}
	for _, k := range keys {
		if k.key == "" {
			continue
		}
		ok, err := s.rateLimiter.Allow(ctx, k.limit, k.key)
		if err != nil {
			s.log.Warn("hidden-chat rate limiter unavailable; the durable lockout still applies", "err", err, "action", k.limit.Action)
			continue
		}
		if !ok {
			return false
		}
	}
	return true
}

func (s *Service) issueHiddenToken(ctx context.Context, hs hiddenChatStore, who HiddenAttempt, kind string) (*HiddenToken, error) {
	if who.DeviceKey == "" {
		return nil, fmt.Errorf("hidden chats: no device binding for the token")
	}
	token, hash, err := hiddenchat.NewToken()
	if err != nil {
		return nil, err
	}
	exp := s.clock().Add(HiddenTokenTTL).UTC()
	if err := hs.InsertHiddenChatToken(ctx, hash, who.UserID, who.DeviceKey, kind, exp); err != nil {
		return nil, err
	}
	return &HiddenToken{Token: token, ExpiresAt: exp}, nil
}

// touchToken validates a token (user + device + kind) and slides it.
func (s *Service) touchToken(ctx context.Context, hs hiddenChatStore, userID uuid.UUID, deviceKey, kind, token string) (time.Time, bool, error) {
	if deviceKey == "" || !hiddenchat.WellFormedToken(token) {
		return time.Time{}, false, nil
	}
	now := s.clock()
	return hs.TouchHiddenChatToken(ctx, hiddenchat.TokenHash(token), userID, deviceKey, kind, now, now.Add(HiddenTokenTTL).UTC())
}

// ValidateOpenToken reports whether token is a live open token of this user
// on this device, sliding it 15 minutes. Works with the feature switched off
// (existing locks keep needing it).
func (s *Service) ValidateOpenToken(ctx context.Context, userID uuid.UUID, deviceKey, token string) (time.Time, bool, error) {
	hs, ok := s.hiddenStore()
	if !ok {
		return time.Time{}, false, nil
	}
	return s.touchToken(ctx, hs, userID, deviceKey, postgres.TokenKindOpen, token)
}

// IsConversationLockedFor reports whether userID hid conversationID. Works
// with the feature off. A store without the surface reports false.
func (s *Service) IsConversationLockedFor(ctx context.Context, userID, conversationID uuid.UUID) (bool, error) {
	hs, ok := s.hiddenStore()
	if !ok {
		return false, nil
	}
	return hs.IsConversationLockedFor(ctx, conversationID, userID)
}

// LockedConversationsAmong reports which of ids userID hid.
func (s *Service) LockedConversationsAmong(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	hs, ok := s.hiddenStore()
	if !ok {
		return map[uuid.UUID]bool{}, nil
	}
	return hs.LockedConversationsAmong(ctx, userID, ids)
}

// HideConversation is POST /v1/chat/conversations/:id/hide. Needs the
// account's secrets (ErrHiddenNotConfigured) and an active membership
// (ErrNotMember). Idempotent. On the first hide it revokes every
// conversation-room entitlement the member held (generation bump + deny
// marker) and tells the member's own devices to drop the room and the row.
func (s *Service) HideConversation(ctx context.Context, userID, convID uuid.UUID) error {
	hs, err := s.hiddenFeature()
	if err != nil {
		return err
	}
	row, err := hs.GetHiddenChatSettings(ctx, userID)
	if err != nil {
		return err
	}
	if row == nil {
		return ErrHiddenNotConfigured
	}
	changed, oldGen, err := hs.LockConversationForMember(ctx, convID, userID)
	if err != nil {
		if errors.Is(err, postgres.ErrNotActiveMember) {
			return ErrNotMember
		}
		return err
	}
	if !changed {
		return nil
	}
	if oldGen > 0 {
		// Same contract as a sever: the marker must be written, or the
		// durable intent stays for the repair worker. The hide itself has
		// committed; a marker failure only delays the room revocation (the
		// 5-minute entitlement expiry is the backstop), so it is logged.
		if err := s.armRevocation(ctx, convID, userID, oldGen); err != nil {
			s.log.Warn("hidden chat: entitlement revocation deferred to the repair worker", "err", err, "conversation_id", convID)
		} else if gs, ok := s.convStore.(groupStore); ok {
			if err := gs.DeleteRevocationIntent(ctx, convID, userID, oldGen); err != nil {
				s.log.Warn("hidden chat: revocation intent cleanup failed", "err", err, "conversation_id", convID)
			}
		}
	}
	s.publishControlFrame(ctx, userID, map[string]any{"type": "subscription_revoked", "conversation_id": convID.String()})
	s.publishControlFrame(ctx, userID, map[string]any{"type": "conversation_hidden", "conversation_id": convID.String()})
	return nil
}

// UnhideConversation is POST /v1/chat/conversations/:id/unhide. The HTTP
// gate already required a valid open token for a locked conversation.
func (s *Service) UnhideConversation(ctx context.Context, userID, convID uuid.UUID) error {
	hs, ok := s.hiddenStore()
	if !ok {
		return ErrHiddenChatsDisabled
	}
	if err := hs.UnlockConversationForMember(ctx, convID, userID); err != nil {
		if errors.Is(err, postgres.ErrNotActiveMember) {
			return ErrNotMember
		}
		return err
	}
	return nil
}

// ListHiddenConversations is GET /v1/chat/hidden/conversations: the hidden
// list in the normal row shape, only with a valid reveal token
// (ErrHiddenNotFound otherwise — the same answer as a wrong key).
func (s *Service) ListHiddenConversations(ctx context.Context, userID uuid.UUID, deviceKey, revealToken string, limit int, cursor *ConversationCursor) ([]ConversationResponse, *ConversationCursor, time.Time, error) {
	hs, ok := s.hiddenStore()
	if !ok {
		return nil, nil, time.Time{}, ErrHiddenNotFound
	}
	exp, valid, err := s.touchToken(ctx, hs, userID, deviceKey, postgres.TokenKindReveal, revealToken)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	if !valid {
		return nil, nil, time.Time{}, ErrHiddenNotFound
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var cursorTs *time.Time
	var cursorID *uuid.UUID
	if cursor != nil {
		cursorTs, cursorID = &cursor.UpdatedAt, &cursor.ID
	}
	convs, err := hs.ListHiddenConversations(ctx, userID, limit, cursorTs, cursorID)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	out, err := s.inboxRows(ctx, userID, convs)
	if err != nil {
		return nil, nil, time.Time{}, err
	}
	for i := range out {
		out[i].Locked = true
	}
	var next *ConversationCursor
	if len(convs) == limit {
		last := convs[len(convs)-1]
		next = &ConversationCursor{UpdatedAt: last.SortAt, ID: last.ID}
	}
	return out, next, exp, nil
}

// RedactLockedConversation strips what a locked conversation must not show
// outside an open session (another entry point — open-a-DM, join by invite —
// returned it): no preview, no last message, no unread state. Members and
// title stay: the caller already named the person or group.
func RedactLockedConversation(c *ConversationResponse) {
	if c == nil {
		return
	}
	c.Locked = true
	c.LastMessagePreview = ""
	c.LastMessageSender = nil
	c.LastMessage = nil
	c.LastMessageAt = nil
	c.HasUnread = false
	c.UnreadCount = 0
	c.MarkedUnread = false
	c.Receipts = nil
	c.Streak = nil
}

// ViewerMayAccessLockedChatMedia: media only reachable through conversations
// the viewer hid. The internal media route grants it with a valid open token.
func (s *Service) ViewerMayAccessLockedChatMedia(ctx context.Context, viewerID, mediaID uuid.UUID) (bool, error) {
	hs, ok := s.hiddenStore()
	if !ok {
		return false, nil
	}
	return hs.ViewerMayAccessLockedChatMedia(ctx, viewerID, mediaID)
}

// lockedRecipients is the subset of recipients who hid conversationID —
// MessageCreated.locked_recipient_ids. A lookup error is returned (delivery
// retries) rather than guessed: a push must never preview a hidden chat.
func (s *Service) lockedRecipients(ctx context.Context, conversationID uuid.UUID, recipients []string) ([]string, error) {
	hs, ok := s.hiddenStore()
	if !ok || len(recipients) == 0 {
		return nil, nil
	}
	locked, err := hs.LockedMemberIDs(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("locked-member lookup: %w", err)
	}
	if len(locked) == 0 {
		return nil, nil
	}
	set := make(map[string]bool, len(locked))
	for _, id := range locked {
		set[id.String()] = true
	}
	var out []string
	for _, r := range recipients {
		if set[r] {
			out = append(out, r)
		}
	}
	return out, nil
}

// personalChannel is the ONLY way a frame about a conversation reaches a
// member's personal channel (chat:<user>): a member who hid the conversation
// gets nothing there — their open session receives it on the conversation
// room, whose entitlement needed an open token. hidden_chats_inventory_test
// fails on any other personal-channel publish of a member list.
func personalChannel(m postgres.Member) (string, bool) {
	if m.Locked {
		return "", false
	}
	return "chat:" + m.UserID.String(), true
}

// redisPublisher is the PUBLISH half of *redis.Client and redis.Pipeliner.
type redisPublisher interface {
	Publish(ctx context.Context, channel string, message interface{}) *redis.IntCmd
}

// publishPersonal publishes payload to m's personal channel unless m hid the
// conversation (personalChannel).
func publishPersonal(ctx context.Context, p redisPublisher, m postgres.Member, payload interface{}) {
	if ch, ok := personalChannel(m); ok {
		p.Publish(ctx, ch, payload)
	}
}

// personalChannelFor is personalChannel for a frame addressed to one user
// about one conversation (no member row at hand). A lookup failure skips the
// frame: it is an acceleration, never the truth.
func (s *Service) personalChannelFor(ctx context.Context, userID, conversationID uuid.UUID) (string, bool) {
	if hs, ok := s.hiddenStore(); ok {
		locked, err := hs.IsConversationLockedFor(ctx, conversationID, userID)
		if err != nil || locked {
			return "", false
		}
	}
	return "chat:" + userID.String(), true
}
