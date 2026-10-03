package service

// Chat authors and chat text (chat_author.go): `author` on the POST answer,
// the chat.message frame, GET /chat and the pinned message; a failed profile
// lookup; and any Unicode in a message — emoji, joined emoji, flags, Telugu,
// Hindi — counted in characters and kept exactly.

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
	"github.com/atpost/live-service-v2/internal/storetest"
)

// Text the chat must take as it is.
const (
	txtEmoji  = "great stream 😀🔥"
	txtFamily = "us 👨‍👩‍👧‍👦 watching"           // one family: 4 people joined by 3 zero-width joiners
	txtFlags  = "🇮🇳 🇯🇵 🇧🇷"                      // regional-indicator pairs
	txtTelugu = "నమస్తే, ఈ లైవ్ చాలా బాగుంది"   // Telugu
	txtHindi  = "नमस्ते, यह लाइव बहुत अच्छा है" // Hindi (Devanagari)
	txtMixed  = "వావ్ 😍 बहुत बढ़िया 👍🏽 ❤️ café ñ 日本語 🏳️‍🌈"
)

var unicodeTexts = map[string]string{
	"emoji": txtEmoji, "zwj family": txtFamily, "flags": txtFlags,
	"telugu": txtTelugu, "hindi": txtHindi, "mixed": txtMixed,
}

func authorOf(t *testing.T, m *postgres.ChatMessage) postgres.ChatAuthor {
	t.Helper()
	if m.Author == nil {
		t.Fatalf("message %s has no author", m.ID)
	}
	return *m.Author
}

func TestChatAuthorOnPostFrameAndList(t *testing.T) {
	host, mod, fan, ghost := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := r.live(host, 0, "landscape", "")
	if _, err := r.svc.SetModerators(ctx, st.ID, host, []uuid.UUID{mod}); err != nil {
		t.Fatal(err)
	}
	r.profiles.m[host] = Profile{Name: "Asha Rao", Handle: "asha", AvatarURL: "/v1/media/a/serve/avatar"}
	r.profiles.m[mod] = Profile{Name: "Ben", Handle: "ben"}
	r.profiles.m[fan] = Profile{Name: "కిరణ్", Handle: "kiran"}
	// ghost has no profile (a deleted or hidden account).
	r.store.Badges[host] = &storetest.BadgeRow{Badge: postgres.BadgeFoundingCreator, GrantedAt: r.clock.Now()}

	want := map[uuid.UUID]postgres.ChatAuthor{
		host:  {UserID: host, Name: "Asha Rao", Handle: "asha", AvatarURL: "/v1/media/a/serve/avatar", Badges: []string{postgres.BadgeFoundingCreator}, Role: "host"},
		mod:   {UserID: mod, Name: "Ben", Handle: "ben", Badges: []string{}, Role: "moderator"},
		fan:   {UserID: fan, Name: "కిరణ్", Handle: "kiran", Badges: []string{}, Role: "viewer"},
		ghost: {UserID: ghost, Badges: []string{}, Role: "viewer"},
	}
	order := []uuid.UUID{host, mod, fan, ghost}
	for i, u := range order {
		r.clock.Advance(time.Second)
		msg, err := r.svc.SendChat(ctx, st.ID, u, "hello")
		if err != nil {
			t.Fatalf("send by %d: %v", i, err)
		}
		// The POST answer.
		if got := authorOf(t, msg); !reflect.DeepEqual(got, want[u]) {
			t.Fatalf("POST author %d: %+v, want %+v", i, got, want[u])
		}
		if msg.UserID != u {
			t.Fatalf("the top-level user_id is gone")
		}
		// The frame: the same row, author at the top level and under payload.
		frames := r.ev.ofType(EventChatMessage)
		frame := frames[len(frames)-1]
		wantJSON, _ := json.Marshal(want[u])
		for where, raw := range map[string]any{"top level": frame["author"], "payload": frame["payload"].(map[string]any)["author"]} {
			gotJSON, _ := json.Marshal(raw)
			var a, b any
			_ = json.Unmarshal(gotJSON, &a)
			_ = json.Unmarshal(wantJSON, &b)
			if !reflect.DeepEqual(a, b) {
				t.Fatalf("frame author (%s) %d: %s, want %s", where, i, gotJSON, wantJSON)
			}
		}
		if frame["user_id"] != u.String() {
			t.Fatalf("frame user_id: %v", frame["user_id"])
		}
	}

	// GET /chat, newest first, for a signed-in viewer and a signed-out one.
	for _, reader := range []uuid.UUID{fan, uuid.Nil} {
		list, err := r.svc.ListChat(ctx, st.ID, reader, 50)
		if err != nil || len(list) != 4 {
			t.Fatalf("list: %d rows, %v", len(list), err)
		}
		for i, m := range list {
			u := order[len(order)-1-i]
			if m.UserID != u || !reflect.DeepEqual(authorOf(t, m), want[u]) {
				t.Fatalf("list row %d: %+v", i, m.Author)
			}
		}
	}

	// The role is the role NOW: a moderator who is removed reads as a viewer.
	if _, err := r.svc.SetModerators(ctx, st.ID, host, nil); err != nil {
		t.Fatal(err)
	}
	list, _ := r.svc.ListChat(ctx, st.ID, fan, 50)
	for _, m := range list {
		if m.UserID == mod && m.Author.Role != "viewer" {
			t.Fatalf("a removed moderator still reads as %s", m.Author.Role)
		}
	}

	// The pinned message carries its author too.
	if err := r.svc.PinMessage(ctx, st.ID, host, list[0].ID); err != nil {
		t.Fatal(err)
	}
	pinned, err := r.svc.GetPinnedMessage(ctx, st.ID)
	if err != nil || pinned == nil || !reflect.DeepEqual(authorOf(t, pinned), want[ghost]) {
		t.Fatalf("pinned: %+v %v", pinned, err)
	}
}

// TestChatAuthorJSONShape pins the wire names, and that an author without a
// profile is exactly user_id, badges and role.
func TestChatAuthorJSONShape(t *testing.T) {
	host, ghost := uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := r.live(host, 0, "landscape", "")
	msg, err := r.svc.SendChat(ctx, st.ID, ghost, "hi")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(msg)
	var row map[string]any
	_ = json.Unmarshal(raw, &row)
	author, _ := row["author"].(map[string]any)
	want := map[string]any{"user_id": ghost.String(), "badges": []any{}, "role": "viewer"}
	if !reflect.DeepEqual(author, want) {
		t.Fatalf("author of a user without a profile: %s", raw)
	}
	if row["user_id"] != ghost.String() || row["text"] != "hi" {
		t.Fatalf("row: %s", raw)
	}
}

// TestChatAuthorLookupFailureNeverFailsChat: identity-profile down — the
// send and the list still work, authors are user_id and role, and the
// directory is not asked again for every message.
func TestChatAuthorLookupFailureNeverFailsChat(t *testing.T) {
	host, fan := uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := r.live(host, 0, "landscape", "")
	r.profiles.m[fan] = Profile{Name: "Kiran", Handle: "kiran"}
	r.profiles.err = errors.New("identity-profile is down")

	msg, err := r.svc.SendChat(ctx, st.ID, fan, "still here")
	if err != nil {
		t.Fatalf("a failed author lookup failed the send: %v", err)
	}
	if got := authorOf(t, msg); !reflect.DeepEqual(got, postgres.ChatAuthor{UserID: fan, Badges: []string{}, Role: "viewer"}) {
		t.Fatalf("author after a failed lookup: %+v", got)
	}
	if frames := r.ev.ofType(EventChatMessage); len(frames) != 1 || frames[0]["author"].(map[string]any)["role"] != "viewer" {
		t.Fatalf("the frame did not go out with an author: %+v", frames)
	}
	if got, err := r.svc.SendChat(ctx, st.ID, host, "host here"); err != nil || got.Author.Role != "host" {
		t.Fatalf("host send: %+v %v", got, err)
	}
	list, err := r.svc.ListChat(ctx, st.ID, uuid.Nil, 50)
	if err != nil || len(list) != 2 || list[0].Author == nil || list[1].Author == nil {
		t.Fatalf("list with the directory down: %d rows, %v", len(list), err)
	}
	// One failed call, then the directory is left alone for a while.
	if r.profiles.calls != 1 {
		t.Fatalf("the failing directory was asked %d times for 3 chat reads", r.profiles.calls)
	}

	// It is back: after the pause the next message has the name (a failure
	// is not remembered as "no profile").
	r.profiles.err = nil
	r.clock.Advance(cardRetryAfter)
	msg, err = r.svc.SendChat(ctx, st.ID, fan, "back")
	if err != nil || msg.Author.Name != "Kiran" || msg.Author.Handle != "kiran" {
		t.Fatalf("after the directory came back: %+v %v", msg.Author, err)
	}

	// A failed moderator read makes nobody a moderator, and fails nothing.
	mod := uuid.New()
	if _, err := r.svc.SetModerators(ctx, st.ID, host, []uuid.UUID{mod}); err != nil {
		t.Fatal(err)
	}
	if got, _ := r.svc.SendChat(ctx, st.ID, mod, "mod"); got.Author.Role != "moderator" {
		t.Fatalf("moderator role: %+v", got.Author)
	}
}

// TestChatAcceptsAnyUnicode: the text is stored, answered, framed and listed
// byte for byte.
func TestChatAcceptsAnyUnicode(t *testing.T) {
	host, fan := uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := r.live(host, 0, "landscape", "")
	// A host with ordinary blocked words: none of them is in the texts.
	for _, w := range []string{"spam", "buy now", "http://"} {
		if err := r.svc.AddWordFilter(ctx, st.ID, host, w); err != nil {
			t.Fatal(err)
		}
	}
	for name, text := range unicodeTexts {
		if !utf8.ValidString(text) {
			t.Fatalf("%s: the test text is not UTF-8", name)
		}
		msg, err := r.svc.SendChat(ctx, st.ID, fan, text)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if msg.Text != text {
			t.Fatalf("%s: answered %q, sent %q", name, msg.Text, text)
		}
		if stored := r.store.Messages[msg.ID].Text; stored != text {
			t.Fatalf("%s: stored %q", name, stored)
		}
		frames := r.ev.ofType(EventChatMessage)
		frame := frames[len(frames)-1]
		if frame["text"] != text || frame["payload"].(map[string]any)["text"] != text {
			t.Fatalf("%s: framed %q", name, frame["text"])
		}
	}
	list, err := r.svc.ListChat(ctx, st.ID, fan, 50)
	if err != nil || len(list) != len(unicodeTexts) {
		t.Fatalf("list: %d %v", len(list), err)
	}
	seen := map[string]bool{}
	for _, m := range list {
		seen[m.Text] = true
	}
	for name, text := range unicodeTexts {
		if !seen[text] {
			t.Fatalf("%s is not in the list as sent", name)
		}
	}
}

// TestChatLengthIsCharactersNotBytes: 500 characters, whatever they weigh.
func TestChatLengthIsCharactersNotBytes(t *testing.T) {
	host, fan := uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := r.live(host, 0, "landscape", "")
	send := func(text string) error {
		r.clock.Advance(time.Minute)
		_, err := r.svc.SendChat(ctx, st.ID, fan, text)
		return err
	}
	for name, unit := range map[string]string{"emoji (4 bytes)": "😀", "telugu (3 bytes)": "న", "hindi (3 bytes)": "म", "ascii": "a"} {
		if err := send(strings.Repeat(unit, 500)); err != nil {
			t.Fatalf("500 × %s (%d bytes) refused: %v", name, 500*len(unit), err)
		}
		err := send(strings.Repeat(unit, 501))
		if err == nil || !strings.HasPrefix(err.Error(), "invalid:") {
			t.Fatalf("501 × %s: %v", name, err)
		}
	}
	// 71 families are 497 code points (7 each); 72 are 504.
	if err := send(strings.Repeat("👨‍👩‍👧‍👦", 71)); err != nil {
		t.Fatalf("71 family emoji: %v", err)
	}
	if err := send(strings.Repeat("👨‍👩‍👧‍👦", 72)); err == nil {
		t.Fatalf("72 family emoji (504 code points) accepted")
	}
	// Only emoji, only a flag, only a joiner sequence: not "empty".
	for _, text := range []string{"😀", "🇮🇳", "👨‍👩‍👧‍👦", "❤️", "న"} {
		if err := send(text); err != nil {
			t.Fatalf("%q alone: %v", text, err)
		}
	}
	// Blank stays refused, whatever kind of space it is made of.
	for _, text := range []string{"", "   ", "  　", "\x00", " \x00 "} {
		if err := send(text); err == nil || !strings.HasPrefix(err.Error(), "invalid:") {
			t.Fatalf("blank %q: %v", text, err)
		}
	}
}

// TestChatDropsNUL: the one character PostgreSQL cannot store is dropped and
// the rest of the message is kept.
func TestChatDropsNUL(t *testing.T) {
	host, fan := uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := r.live(host, 0, "landscape", "")
	msg, err := r.svc.SendChat(ctx, st.ID, fan, "hi\x00 😀\x00")
	if err != nil || msg.Text != "hi 😀" {
		t.Fatalf("%q %v", msg.Text, err)
	}
	if got := cleanChatText("  👨‍👩‍👧‍👦 ❤️  "); got != "👨‍👩‍👧‍👦 ❤️" {
		t.Fatalf("cleaning touched joiners or variation selectors: %q", got)
	}
}

// TestWordFilterWithUnicode: blocked words in any script, counted in
// characters, matched literally.
func TestWordFilterWithUnicode(t *testing.T) {
	host, fan := uuid.New(), uuid.New()
	r := newSurfRig(host)
	st := r.live(host, 0, "landscape", "")
	send := func(text string) error {
		r.clock.Advance(time.Minute)
		_, err := r.svc.SendChat(ctx, st.ID, fan, text)
		return err
	}
	add := func(w string) {
		t.Helper()
		if err := r.svc.AddWordFilter(ctx, st.ID, host, w); err != nil {
			t.Fatalf("add %q: %v", w, err)
		}
	}
	clear := func() {
		words, _ := r.svc.ListWordFilters(ctx, st.ID, host)
		for _, w := range words {
			_ = r.svc.RemoveWordFilter(ctx, st.ID, host, w)
		}
	}

	// LIKE's wildcards are ordinary characters in a blocked word: a host who
	// blocks "_" or "%" blocks those characters, not every message.
	add("_")
	add("%")
	add("100%")
	for name, text := range unicodeTexts {
		if err := send(text); err != nil {
			t.Fatalf("with \"_\" and \"%%\" blocked, %s was refused: %v", name, err)
		}
	}
	if err := send("plain ascii too"); err != nil {
		t.Fatalf("plain text refused: %v", err)
	}
	for _, text := range []string{"snake_case", "50% off", "100% sure"} {
		if err := send(text); !errors.Is(err, ErrChatBlockedWord) {
			t.Fatalf("%q with its character blocked: %v", text, err)
		}
	}
	clear()

	// Words in Telugu and Hindi, an emoji as a word, and case folded beyond
	// ASCII.
	add("చెత్త")
	add("बकवास")
	add("🖕")
	add("ÉCOLE")
	for _, text := range []string{"ఇది చెత్త లైవ్", "यह बकवास है", "take this 🖕", "vive l'école", "L'ÉCOLE"} {
		if err := send(text); !errors.Is(err, ErrChatBlockedWord) {
			t.Fatalf("%q: %v", text, err)
		}
	}
	for name, text := range unicodeTexts {
		if err := send(text); err != nil {
			t.Fatalf("%s refused by unrelated blocked words: %v", name, err)
		}
	}
	clear()

	// 100 characters, not 100 bytes.
	long := strings.Repeat("న", 100) // 300 bytes
	add(long)
	if err := r.svc.AddWordFilter(ctx, st.ID, host, long+"న"); !errors.Is(err, ErrInvalidWord) {
		t.Fatalf("a 101-character word: %v", err)
	}
	if err := r.svc.AddWordFilter(ctx, st.ID, host, strings.Repeat("😀", 100)); err != nil {
		t.Fatalf("100 emoji (400 bytes) as a word: %v", err)
	}
}
