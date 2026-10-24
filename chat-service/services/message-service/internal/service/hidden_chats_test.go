package service

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/hiddenchat"
	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Hidden chats (contract pinned 24 Oct 2026) — service rules.

type hiddenTokenRow struct {
	user   uuid.UUID
	device string
	kind   string
	exp    time.Time
}

// hiddenFake is governanceFake plus an in-memory hiddenChatStore that keeps
// exactly the state the Postgres tables keep. It outlives a Service, so a
// "restart" is a new Service over the same fake.
type hiddenFake struct {
	*governanceFake
	settings map[uuid.UUID]postgres.HiddenChatSettings
	attempts map[string]postgres.AttemptState
	tokens   map[string]hiddenTokenRow
	locked   map[string]bool // conv|user
	saves    []postgres.HiddenChatSettings
	resets   []postgres.HiddenChatsReset
	lockErr  error
}

func newHiddenFake() *hiddenFake {
	return &hiddenFake{
		governanceFake: newGovernanceFake(),
		settings:       map[uuid.UUID]postgres.HiddenChatSettings{},
		attempts:       map[string]postgres.AttemptState{},
		tokens:         map[string]hiddenTokenRow{},
		locked:         map[string]bool{},
	}
}

func (h *hiddenFake) GetMembers(ctx context.Context, convID uuid.UUID) ([]postgres.Member, error) {
	ms, _ := h.governanceFake.GetMembers(ctx, convID)
	for i := range ms {
		ms[i].Locked = h.locked[intentKey(convID, ms[i].UserID)]
	}
	return ms, nil
}

func (h *hiddenFake) GetHiddenChatSettings(_ context.Context, u uuid.UUID) (*postgres.HiddenChatSettings, error) {
	if s, ok := h.settings[u]; ok {
		return &s, nil
	}
	return nil, nil
}
func (h *hiddenFake) SaveHiddenChatSettings(_ context.Context, s postgres.HiddenChatSettings) error {
	if !strings.HasPrefix(s.SecretKeyHash, hiddenchat.HashPrefix) || !strings.HasPrefix(s.ChatPasswordHash, hiddenchat.HashPrefix) {
		return errors.New("CHECK hidden_chat_settings_hashes_only violated")
	}
	h.settings[s.UserID] = s
	h.saves = append(h.saves, s)
	return nil
}
func (h *hiddenFake) GetHiddenChatAttempts(_ context.Context, u uuid.UUID, kind string) (postgres.AttemptState, error) {
	return h.attempts[u.String()+kind], nil
}
func (h *hiddenFake) RecordHiddenChatFailure(_ context.Context, u uuid.UUID, kind string, now time.Time, threshold int, ladder []time.Duration) (postgres.AttemptState, error) {
	st := postgres.NextAttemptState(h.attempts[u.String()+kind], now, threshold, ladder)
	h.attempts[u.String()+kind] = st
	return st, nil
}
func (h *hiddenFake) ClearHiddenChatFailures(_ context.Context, u uuid.UUID, kind string) error {
	st := h.attempts[u.String()+kind]
	st.Failures = 0
	h.attempts[u.String()+kind] = st
	return nil
}
func (h *hiddenFake) InsertHiddenChatToken(_ context.Context, hash []byte, u uuid.UUID, device, kind string, exp time.Time) error {
	h.tokens[string(hash)] = hiddenTokenRow{u, device, kind, exp}
	return nil
}
func (h *hiddenFake) TouchHiddenChatToken(_ context.Context, hash []byte, u uuid.UUID, device, kind string, now, next time.Time) (time.Time, bool, error) {
	row, ok := h.tokens[string(hash)]
	if !ok || row.user != u || row.device != device || row.kind != kind || !row.exp.After(now) {
		return time.Time{}, false, nil
	}
	row.exp = next
	h.tokens[string(hash)] = row
	return next, true, nil
}
func (h *hiddenFake) DeleteHiddenChatTokens(_ context.Context, u uuid.UUID, device string) error {
	for k, row := range h.tokens {
		if row.user == u && (device == "" || row.device == device) {
			delete(h.tokens, k)
		}
	}
	return nil
}
func (h *hiddenFake) LockConversationForMember(_ context.Context, conv, u uuid.UUID) (bool, int64, error) {
	if h.lockErr != nil {
		return false, 0, h.lockErr
	}
	if _, ok := h.roles[conv][u]; !ok {
		return false, 0, postgres.ErrNotActiveMember
	}
	if h.locked[intentKey(conv, u)] {
		return false, 0, nil
	}
	h.locked[intentKey(conv, u)] = true
	return true, 7, nil
}
func (h *hiddenFake) UnlockConversationForMember(_ context.Context, conv, u uuid.UUID) error {
	if _, ok := h.roles[conv][u]; !ok {
		return postgres.ErrNotActiveMember
	}
	delete(h.locked, intentKey(conv, u))
	return nil
}
func (h *hiddenFake) IsConversationLockedFor(_ context.Context, conv, u uuid.UUID) (bool, error) {
	return h.locked[intentKey(conv, u)], nil
}
func (h *hiddenFake) LockedConversationsAmong(_ context.Context, u uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		if h.locked[intentKey(id, u)] {
			out[id] = true
		}
	}
	return out, nil
}
func (h *hiddenFake) LockedMemberIDs(_ context.Context, conv uuid.UUID) ([]uuid.UUID, error) {
	var out []uuid.UUID
	for u := range h.roles[conv] {
		if h.locked[intentKey(conv, u)] {
			out = append(out, u)
		}
	}
	return out, nil
}
func (h *hiddenFake) ListHiddenConversations(_ context.Context, u uuid.UUID, _ int, _ *time.Time, _ *uuid.UUID) ([]postgres.Conversation, error) {
	var out []postgres.Conversation
	for id, c := range h.conversations {
		if h.locked[intentKey(id, u)] {
			out = append(out, *c)
		}
	}
	return out, nil
}
func (h *hiddenFake) ResetHiddenChats(_ context.Context, r postgres.HiddenChatsReset) ([]uuid.UUID, error) {
	h.resets = append(h.resets, r)
	u := r.Settings.UserID
	var wiped []uuid.UUID
	if r.Wipe {
		for k := range h.locked {
			if strings.HasSuffix(k, "|"+u.String()) {
				conv := uuid.MustParse(strings.SplitN(k, "|", 2)[0])
				delete(h.locked, k)
				wiped = append(wiped, conv)
			}
		}
	}
	h.settings[u] = r.Settings
	for k := range h.attempts {
		if strings.HasPrefix(k, u.String()) {
			delete(h.attempts, k)
		}
	}
	_ = h.DeleteHiddenChatTokens(context.Background(), u, "")
	return wiped, nil
}
func (h *hiddenFake) ViewerMayAccessLockedChatMedia(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
	return true, nil
}

type hiddenClock struct{ now time.Time }

func (c *hiddenClock) Now() time.Time { return c.now }

func newHiddenService(f *hiddenFake) (*Service, *hiddenClock) {
	clk := &hiddenClock{now: time.Date(2026, 10, 24, 12, 0, 0, 0, time.UTC)}
	s := &Service{convStore: f, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	s.SetClock(clk.Now)
	return s, clk
}

func sptr(s string) *string { return &s }

func who(u uuid.UUID, device string) HiddenAttempt {
	return HiddenAttempt{UserID: u, DeviceKey: device, IP: "203.0.113.9"}
}

func setUp(t *testing.T, s *Service, u uuid.UUID, key, pw string) {
	t.Helper()
	if _, err := s.PutHiddenChatSettings(context.Background(), who(u, "sid:a"), HiddenSettingsInput{SecretKey: sptr(key), ChatPassword: sptr(pw)}); err != nil {
		t.Fatalf("setup: %v", err)
	}
}

func TestHiddenSecretsAreStoredOnlyAsArgon2Hashes(t *testing.T) {
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u := uuid.New()
	setUp(t, s, u, "Mango42", "river-stone")
	if len(f.saves) != 1 {
		t.Fatalf("saves = %d", len(f.saves))
	}
	row := f.saves[0]
	for _, h := range []string{row.SecretKeyHash, row.ChatPasswordHash} {
		if !strings.HasPrefix(h, hiddenchat.HashPrefix) || strings.Contains(h, "Mango42") || strings.Contains(h, "river-stone") {
			t.Fatalf("stored value is not a bare Argon2id hash: %q", h)
		}
	}
	if !hiddenchat.Verify("Mango42", row.SecretKeyHash) || !hiddenchat.Verify("river-stone", row.ChatPasswordHash) {
		t.Fatal("stored hashes do not verify their secrets")
	}
	view, _ := s.GetHiddenChatSettings(context.Background(), u)
	if !view.Configured || view.Strict || view.Shortcut != nil {
		t.Fatalf("view = %+v", view)
	}
}

func TestEqualSecretKeyAndChatPasswordAreRefused(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u := uuid.New()
	if _, err := s.PutHiddenChatSettings(ctx, who(u, "sid:a"), HiddenSettingsInput{SecretKey: sptr("same1234"), ChatPassword: sptr("same1234")}); !errors.Is(err, ErrHiddenSecretsEqual) {
		t.Fatalf("first set with equal secrets: %v", err)
	}
	setUp(t, s, u, "Mango42", "river-stone")
	// New password equal to the CURRENT key.
	if _, err := s.PutHiddenChatSettings(ctx, who(u, "sid:a"), HiddenSettingsInput{ChatPassword: sptr("Mango42"), CurrentChatPassword: sptr("river-stone")}); !errors.Is(err, ErrHiddenSecretsEqual) {
		t.Fatalf("password = current key: %v", err)
	}
	// New key equal to the CURRENT password (letters only so it is key-shaped).
	setUp2 := newHiddenFake()
	s2, _ := newHiddenService(setUp2)
	setUp(t, s2, u, "Mango42", "riverstone")
	if _, err := s2.PutHiddenChatSettings(ctx, who(u, "sid:a"), HiddenSettingsInput{SecretKey: sptr("riverstone"), CurrentChatPassword: sptr("riverstone")}); !errors.Is(err, ErrHiddenSecretsEqual) {
		t.Fatalf("key = current password: %v", err)
	}
	// Reset refuses them too.
	if _, err := s.ResetHiddenChats(ctx, u, true, HiddenResetInput{SecretKey: "abcd1234", ChatPassword: "abcd1234"}); !errors.Is(err, ErrHiddenSecretsEqual) {
		t.Fatalf("reset with equal secrets: %v", err)
	}
}

func TestChangingSettingsNeedsTheCurrentChatPassword(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u := uuid.New()
	setUp(t, s, u, "Mango42", "river-stone")
	var wrong *HiddenWrongPasswordError
	if _, err := s.PutHiddenChatSettings(ctx, who(u, "sid:a"), HiddenSettingsInput{Strict: boolPtr(true)}); !errors.As(err, &wrong) {
		t.Fatalf("no current password: %v", err)
	}
	if _, err := s.PutHiddenChatSettings(ctx, who(u, "sid:a"), HiddenSettingsInput{Strict: boolPtr(true), CurrentChatPassword: sptr("nope-nope")}); !errors.As(err, &wrong) || wrong.AttemptsLeft != 4 {
		t.Fatalf("wrong current password must count: %v", err)
	}
	if v, err := s.PutHiddenChatSettings(ctx, who(u, "sid:a"), HiddenSettingsInput{Strict: boolPtr(true), Shortcut: sptr("Ctrl+Alt+K"), CurrentChatPassword: sptr("river-stone")}); err != nil || !v.Strict || v.Shortcut == nil || *v.Shortcut != "Ctrl+Alt+K" {
		t.Fatalf("partial update: %+v %v", v, err)
	}
	// shortcut "" (null on the wire) restores the default.
	if v, _ := s.PutHiddenChatSettings(ctx, who(u, "sid:a"), HiddenSettingsInput{Shortcut: sptr(""), CurrentChatPassword: sptr("river-stone")}); v.Shortcut != nil || !v.Strict {
		t.Fatalf("shortcut reset kept strict? %+v", v)
	}
}

func boolPtr(b bool) *bool { return &b }

func TestWrongKeyIsIndistinguishableFromNotConfigured(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	configured, bare := uuid.New(), uuid.New()
	setUp(t, s, configured, "Mango42", "river-stone")

	_, errWrong := s.RevealHiddenChats(ctx, who(configured, "sid:a"), "Papaya99")
	_, errBare := s.RevealHiddenChats(ctx, who(bare, "sid:b"), "Papaya99")
	if errWrong != ErrHiddenNotFound || errBare != ErrHiddenNotFound {
		t.Fatalf("wrong key %v vs not configured %v: must be the same sentinel", errWrong, errBare)
	}
	// The lockout behaves identically too: five tries lock either account.
	for i := 0; i < 4; i++ {
		_, _ = s.RevealHiddenChats(ctx, who(configured, "sid:a"), "Papaya99")
		_, _ = s.RevealHiddenChats(ctx, who(bare, "sid:b"), "Papaya99")
	}
	var l1, l2 *HiddenLockedError
	_, e1 := s.RevealHiddenChats(ctx, who(configured, "sid:a"), "Mango42")
	_, e2 := s.RevealHiddenChats(ctx, who(bare, "sid:b"), "Mango42")
	if !errors.As(e1, &l1) || !errors.As(e2, &l2) || l1.RetryAfter != l2.RetryAfter {
		t.Fatalf("lockout differs between configured (%v) and not configured (%v)", e1, e2)
	}
}

func TestLockoutEscalatesAndSurvivesARestart(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, clk := newHiddenService(f)
	u := uuid.New()
	setUp(t, s, u, "Mango42", "river-stone")
	reveal, err := s.RevealHiddenChats(ctx, who(u, "sid:a"), "Mango42")
	if err != nil {
		t.Fatal(err)
	}
	want := []time.Duration{15 * time.Minute, time.Hour, 24 * time.Hour, 24 * time.Hour}
	for round, d := range want {
		var last error
		for i := 0; i < HiddenLockThreshold; i++ {
			_, last = s.OpenHiddenChats(ctx, who(u, "sid:a"), reveal.Token, "wrong-pass")
		}
		var locked *HiddenLockedError
		if !errors.As(last, &locked) || locked.RetryAfter != d {
			t.Fatalf("round %d: want lock %v, got %v", round, d, last)
		}
		// A RESTART: a new service over the same durable state is still locked,
		// even for the right password.
		restarted, _ := newHiddenService(f)
		restarted.SetClock(clk.Now)
		if _, err := restarted.OpenHiddenChats(ctx, who(u, "sid:a"), reveal.Token, "river-stone"); !errors.As(err, &locked) {
			t.Fatalf("round %d: lockout did not survive a restart: %v", round, err)
		}
		clk.now = clk.now.Add(d + time.Second)
		// keep the reveal token alive across the wait
		reveal, _ = s.RevealHiddenChats(ctx, who(u, "sid:a"), "Mango42")
	}
	if _, err := s.OpenHiddenChats(ctx, who(u, "sid:a"), reveal.Token, "river-stone"); err != nil {
		t.Fatalf("after the last lock expires the right password opens: %v", err)
	}
}

func TestRevealTokenIsRequiredForTheHiddenList(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, clk := newHiddenService(f)
	u, peer := uuid.New(), uuid.New()
	conv := uuid.New()
	f.addGroup(conv, u, map[uuid.UUID]string{peer: "member"})
	setUp(t, s, u, "Mango42", "river-stone")
	if err := s.HideConversation(ctx, u, conv); err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ListHiddenConversations(ctx, u, "sid:a", "", 20, nil); err != ErrHiddenNotFound {
		t.Fatalf("no token: %v", err)
	}
	tok, err := s.RevealHiddenChats(ctx, who(u, "sid:a"), "Mango42")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ListHiddenConversations(ctx, u, "sid:OTHER", tok.Token, 20, nil); err != ErrHiddenNotFound {
		t.Fatalf("token from another device: %v", err)
	}
	if _, _, _, err := s.ListHiddenConversations(ctx, peer, "sid:a", tok.Token, 20, nil); err != ErrHiddenNotFound {
		t.Fatalf("token of another user: %v", err)
	}
	rows, _, _, err := s.ListHiddenConversations(ctx, u, "sid:a", tok.Token, 20, nil)
	if err != nil || len(rows) != 1 || rows[0].ID != conv || !rows[0].Locked {
		t.Fatalf("hidden list: %+v %v", rows, err)
	}
	// An open token is not a reveal token.
	open, err := s.OpenHiddenChats(ctx, who(u, "sid:a"), tok.Token, "river-stone")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, err := s.ListHiddenConversations(ctx, u, "sid:a", open.Token, 20, nil); err != ErrHiddenNotFound {
		t.Fatalf("open token accepted as reveal: %v", err)
	}
	// Sliding expiry: used within 15 min it lives on; idle 15 min it dies.
	clk.now = clk.now.Add(14 * time.Minute)
	if _, _, _, err := s.ListHiddenConversations(ctx, u, "sid:a", tok.Token, 20, nil); err != nil {
		t.Fatalf("slid token: %v", err)
	}
	clk.now = clk.now.Add(HiddenTokenTTL + time.Second)
	if _, _, _, err := s.ListHiddenConversations(ctx, u, "sid:a", tok.Token, 20, nil); err != ErrHiddenNotFound {
		t.Fatalf("idle token still valid: %v", err)
	}
}

func TestOpenNeedsRevealTokenAndPassword(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u := uuid.New()
	setUp(t, s, u, "Mango42", "river-stone")
	if _, err := s.OpenHiddenChats(ctx, who(u, "sid:a"), "", "river-stone"); err != ErrHiddenNotFound {
		t.Fatalf("open without reveal: %v", err)
	}
	tok, _ := s.RevealHiddenChats(ctx, who(u, "sid:a"), "Mango42")
	var wrong *HiddenWrongPasswordError
	if _, err := s.OpenHiddenChats(ctx, who(u, "sid:a"), tok.Token, "wrong-pass"); !errors.As(err, &wrong) || wrong.AttemptsLeft != 4 {
		t.Fatalf("wrong password: %v", err)
	}
	open, err := s.OpenHiddenChats(ctx, who(u, "sid:a"), tok.Token, "river-stone")
	if err != nil || open.Token == "" {
		t.Fatalf("open: %v", err)
	}
	if _, ok, _ := s.ValidateOpenToken(ctx, u, "sid:a", open.Token); !ok {
		t.Fatal("issued open token does not validate")
	}
	if _, ok, _ := s.ValidateOpenToken(ctx, u, "sid:b", open.Token); ok {
		t.Fatal("open token valid on another device")
	}
	if _, ok, _ := s.ValidateOpenToken(ctx, u, "sid:a", tok.Token); ok {
		t.Fatal("reveal token accepted as an open token")
	}
	// Changing a secret re-locks every device.
	if _, err := s.PutHiddenChatSettings(ctx, who(u, "sid:a"), HiddenSettingsInput{ChatPassword: sptr("new-river"), CurrentChatPassword: sptr("river-stone")}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := s.ValidateOpenToken(ctx, u, "sid:a", open.Token); ok {
		t.Fatal("open token survived a password change")
	}
}

func TestResetNeedsAFreshReauth(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u := uuid.New()
	setUp(t, s, u, "Mango42", "river-stone")
	if _, err := s.ResetHiddenChats(ctx, u, false, HiddenResetInput{SecretKey: "Kiwi7777", ChatPassword: "new-secret"}); !errors.Is(err, ErrStepUpRequired) {
		t.Fatalf("reset without step-up: %v", err)
	}
	if len(f.resets) != 0 {
		t.Fatal("a refused reset reached the store")
	}
	if _, err := s.ResetHiddenChats(ctx, u, true, HiddenResetInput{SecretKey: "Kiwi7777", ChatPassword: "new-secret"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RevealHiddenChats(ctx, who(u, "sid:a"), "Kiwi7777"); err != nil {
		t.Fatalf("new key after reset: %v", err)
	}
}

func TestStrictResetWipesOnlyThatUsersLockedConversations(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u, peer := uuid.New(), uuid.New()
	mine, theirs, open := uuid.New(), uuid.New(), uuid.New()
	f.addGroup(mine, u, map[uuid.UUID]string{peer: "member"})
	f.addGroup(theirs, peer, map[uuid.UUID]string{u: "member"})
	f.addGroup(open, u, map[uuid.UUID]string{peer: "member"})
	setUp(t, s, u, "Mango42", "river-stone")
	setUp(t, s, peer, "Peach11", "peer-password")
	_ = s.HideConversation(ctx, u, mine)
	_ = s.HideConversation(ctx, peer, theirs)

	// Non-strict reset keeps the locks.
	res, err := s.ResetHiddenChats(ctx, u, true, HiddenResetInput{SecretKey: "Kiwi7777", ChatPassword: "new-secret"})
	if err != nil || res.WipedConversations != 0 || !f.locked[intentKey(mine, u)] || f.resets[0].Wipe {
		t.Fatalf("non-strict reset wiped: %+v %v", res, err)
	}
	// Turn strict on, then reset again: only u's locked conversation goes.
	if _, err := s.PutHiddenChatSettings(ctx, who(u, "sid:a"), HiddenSettingsInput{Strict: boolPtr(true), CurrentChatPassword: sptr("new-secret")}); err != nil {
		t.Fatal(err)
	}
	res, err = s.ResetHiddenChats(ctx, u, true, HiddenResetInput{SecretKey: "Lime8888", ChatPassword: "newer-secret"})
	if err != nil || res.WipedConversations != 1 || !f.resets[1].Wipe {
		t.Fatalf("strict reset: %+v %v", res, err)
	}
	if f.locked[intentKey(mine, u)] {
		t.Fatal("strict reset kept the user's hidden chat")
	}
	if !f.locked[intentKey(theirs, peer)] {
		t.Fatal("strict reset touched ANOTHER user's hidden chat")
	}
	if !res.Strict {
		t.Fatal("strict mode is kept across the reset unless the body changes it")
	}
}

func TestHideNeedsSecretsAndMembershipAndIsForMeOnly(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u, peer, stranger := uuid.New(), uuid.New(), uuid.New()
	conv := uuid.New()
	f.addGroup(conv, u, map[uuid.UUID]string{peer: "member"})
	if err := s.HideConversation(ctx, u, conv); !errors.Is(err, ErrHiddenNotConfigured) {
		t.Fatalf("hide before setup: %v", err)
	}
	setUp(t, s, u, "Mango42", "river-stone")
	setUp(t, s, stranger, "Plum5555", "stranger-pass")
	if err := s.HideConversation(ctx, stranger, conv); !errors.Is(err, ErrNotMember) {
		t.Fatalf("non-member hide: %v", err)
	}
	if err := s.HideConversation(ctx, u, conv); err != nil {
		t.Fatal(err)
	}
	if err := s.HideConversation(ctx, u, conv); err != nil {
		t.Fatalf("hide is idempotent: %v", err)
	}
	if l, _ := s.IsConversationLockedFor(ctx, peer, conv); l {
		t.Fatal("hiding locked the conversation for the OTHER member")
	}
	if l, _ := s.IsConversationLockedFor(ctx, u, conv); !l {
		t.Fatal("hide did not lock it for the hider")
	}
}

func TestPersonalChannelSkipsAMemberWhoHidTheChat(t *testing.T) {
	if _, ok := personalChannel(postgres.Member{UserID: uuid.New(), Locked: true}); ok {
		t.Fatal("a locked member must get nothing on the personal channel")
	}
	id := uuid.New()
	if ch, ok := personalChannel(postgres.Member{UserID: id}); !ok || ch != "chat:"+id.String() {
		t.Fatalf("unlocked member channel: %q %v", ch, ok)
	}
}

func TestLockedRecipientsRideTheEventAndFailClosed(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u, peer, other := uuid.New(), uuid.New(), uuid.New()
	conv := uuid.New()
	f.addGroup(conv, u, map[uuid.UUID]string{peer: "member", other: "member"})
	f.locked[intentKey(conv, peer)] = true
	got, err := s.lockedRecipients(ctx, conv, []string{peer.String(), other.String()})
	if err != nil || len(got) != 1 || got[0] != peer.String() {
		t.Fatalf("locked recipients: %v %v", got, err)
	}
	failing := &lockedLookupFails{hiddenFake: f}
	s2 := &Service{convStore: failing, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if _, err := s2.lockedRecipients(ctx, conv, []string{peer.String()}); err == nil {
		t.Fatal("a failed lookup must fail the delivery (retry), never guess 'not hidden'")
	}
}

type lockedLookupFails struct{ *hiddenFake }

func (l *lockedLookupFails) LockedMemberIDs(context.Context, uuid.UUID) ([]uuid.UUID, error) {
	return nil, errors.New("db down")
}

func TestKillSwitchKeepsExistingLocks(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u := uuid.New()
	conv := uuid.New()
	f.addGroup(conv, u, nil)
	setUp(t, s, u, "Mango42", "river-stone")
	_ = s.HideConversation(ctx, u, conv)
	s.SetHiddenChatsEnabled(false)
	if _, err := s.RevealHiddenChats(ctx, who(u, "sid:a"), "Mango42"); !errors.Is(err, ErrHiddenChatsDisabled) {
		t.Fatalf("reveal while off: %v", err)
	}
	if l, _ := s.IsConversationLockedFor(ctx, u, conv); !l {
		t.Fatal("switching the feature off exposed a hidden chat")
	}
}

// --- source scans ---

// TestEveryPersonalChannelPublishGoesThroughTheLockFilter: a frame about a
// conversation reaches chat:<user> only via publishPersonal /
// personalChannelFor, which skip a member who hid it. A raw
// fmt.Sprintf("chat:%s", …) publish — the shape every one of these sites had
// before — fails this test.
func TestEveryPersonalChannelPublishGoesThroughTheLockFilter(t *testing.T) {
	// Allowed raw personal-channel writers, with the reason.
	allowed := map[string]string{
		"publishControlFrame": "membership control frames (subscription_revoked, removed, joined) the gateway and the member's own devices must get; no message content",
		"personalChannel":     "the filter itself",
		"personalChannelFor":  "the filter itself",
	}
	raw := regexp.MustCompile(`"chat:%s"|"chat:"\s*\+`)
	files, _ := filepath.Glob("*.go")
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			body := string(src[fset.Position(fn.Body.Pos()).Offset:fset.Position(fn.Body.End()).Offset])
			if raw.MatchString(body) {
				if _, ok := allowed[fn.Name.Name]; !ok {
					t.Errorf("%s: %s publishes to a personal channel without the hidden-chat filter (use publishPersonal / personalChannelFor)", name, fn.Name.Name)
				}
			}
		}
	}
}

// TestMessageCreatedCarriesLockedRecipients pins that the delivery completion
// fills locked_recipient_ids (notification-service blanks those pushes).
func TestMessageCreatedCarriesLockedRecipients(t *testing.T) {
	src, err := os.ReadFile("delivery.go")
	if err != nil {
		t.Fatal(err)
	}
	s := string(src)
	if !strings.Contains(s, "s.lockedRecipients(ctx, intent.ConversationID, recipients)") ||
		!strings.Contains(s, "LockedRecipientIDs: locked") {
		t.Fatal("MessageCreated no longer carries the locked recipients")
	}
	if !strings.Contains(s, "payload.HiddenChat = true") {
		t.Fatal("the dating per-recipient event no longer flags hidden chats")
	}
}

// The secret key may be typed into chat search: a failed search-service call
// must not log the query through the transport error's URL.
func TestSearchTransportErrorsDropTheQuery(t *testing.T) {
	s := &Service{searchServiceURL: "http://127.0.0.1:1", httpClient: &http.Client{Timeout: time.Second}, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	_, err := s.searchGet(context.Background(), "/v1/search/users", url.Values{"q": {"Mango42"}}, nil)
	if err == nil {
		t.Fatal("expected a transport error")
	}
	if strings.Contains(err.Error(), "Mango42") {
		t.Fatalf("search error leaks the query: %v", err)
	}
}

func TestEveryAttemptIsRateLimitedPerAccountDeviceAndIP(t *testing.T) {
	ctx := context.Background()
	f := newHiddenFake()
	s, _ := newHiddenService(f)
	u := uuid.New()
	setUp(t, s, u, "Mango42", "river-stone")
	lim := &fakeLimiter{allowed: false}
	s.rateLimiter = lim
	var locked *HiddenLockedError
	if _, err := s.RevealHiddenChats(ctx, who(u, "sid:a"), "Mango42"); !errors.As(err, &locked) {
		t.Fatalf("rate-limited reveal: %v", err)
	}
	if f.attempts[u.String()+postgres.SecretKindKey].Failures != 0 {
		t.Fatal("a rate-limited attempt must not even be tried")
	}
	// Redis down: the limiter fails open, the durable lockout still guards.
	s.rateLimiter = &fakeLimiter{allowed: false, err: errors.New("redis down")}
	if _, err := s.RevealHiddenChats(ctx, who(u, "sid:a"), "Mango42"); err != nil {
		t.Fatalf("limiter outage blocked a correct key: %v", err)
	}
	s.rateLimiter = &fakeLimiter{allowed: true}
	lim2 := s.rateLimiter.(*fakeLimiter)
	_, _ = s.RevealHiddenChats(ctx, who(u, "sid:a"), "Mango42")
	if lim2.calls != 3 {
		t.Fatalf("limits checked = %d, want account + device + IP", lim2.calls)
	}
}
