package service

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Chat parity (lane B1) guard tests.

func TestValidateSendInputRichTypes(t *testing.T) {
	id := uuid.New()
	gif := func(mut func(g *GIFRef)) *GIFRef {
		g := &GIFRef{Provider: "giphy", ID: "3o7aD2saalBwwftBIY", URL: "https://media.giphy.com/media/3o7aD2saalBwwftBIY/giphy.gif?cid=x&rid=giphy.gif", Width: 480, Height: 270}
		if mut != nil {
			mut(g)
		}
		return g
	}
	cases := []struct {
		name string
		in   SendInput
		ok   bool
	}{
		{"voice ok", SendInput{Type: MessageTypeVoice, MediaID: &id, DurationMs: 1, Waveform: []int{0, 255}}, true},
		{"voice max", SendInput{Type: MessageTypeVoice, MediaID: &id, DurationMs: VoiceMaxDurationMs}, true},
		{"voice too long", SendInput{Type: MessageTypeVoice, MediaID: &id, DurationMs: VoiceMaxDurationMs + 1}, false},
		{"voice zero", SendInput{Type: MessageTypeVoice, MediaID: &id}, false},
		{"voice no media", SendInput{Type: MessageTypeVoice, DurationMs: 10}, false},
		{"voice waveform too long", SendInput{Type: MessageTypeVoice, MediaID: &id, DurationMs: 10, Waveform: make([]int, VoiceMaxWaveform+1)}, false},
		{"voice waveform value", SendInput{Type: MessageTypeVoice, MediaID: &id, DurationMs: 10, Waveform: []int{256}}, false},
		{"voice negative value", SendInput{Type: MessageTypeVoice, MediaID: &id, DurationMs: 10, Waveform: []int{-1}}, false},
		{"voice with text", SendInput{Type: MessageTypeVoice, MediaID: &id, DurationMs: 10, Text: "hi"}, false},
		{"sticker ok", SendInput{Type: MessageTypeSticker, StickerID: &id}, true},
		{"sticker missing", SendInput{Type: MessageTypeSticker}, false},
		{"sticker plus profile", SendInput{Type: MessageTypeSticker, StickerID: &id, ProfileUserID: &id}, false},
		{"sticker with media", SendInput{Type: MessageTypeSticker, StickerID: &id, MediaID: &id}, false},
		{"gif ok", SendInput{Type: MessageTypeGIF, GIF: gif(nil)}, true},
		{"gif missing", SendInput{Type: MessageTypeGIF}, false},
		{"gif with text", SendInput{Type: MessageTypeGIF, GIF: gif(nil), Text: "lol"}, false},
		{"gif tenor", SendInput{Type: MessageTypeGIF, GIF: gif(func(g *GIFRef) { g.Provider = "tenor" })}, false},
		{"gif bad id", SendInput{Type: MessageTypeGIF, GIF: gif(func(g *GIFRef) { g.ID = "../x" })}, false},
		{"gif long id", SendInput{Type: MessageTypeGIF, GIF: gif(func(g *GIFRef) { g.ID = strings.Repeat("a", 65) })}, false},
		{"gif width", SendInput{Type: MessageTypeGIF, GIF: gif(func(g *GIFRef) { g.Width = 4097 })}, false},
		{"gif height", SendInput{Type: MessageTypeGIF, GIF: gif(func(g *GIFRef) { g.Height = 0 })}, false},
		{"gif host", SendInput{Type: MessageTypeGIF, GIF: gif(func(g *GIFRef) { g.URL = "https://example.com/a.gif" })}, false},
		{"gif still host", SendInput{Type: MessageTypeGIF, GIF: gif(func(g *GIFRef) { g.StillURL = "https://example.com/a.gif" })}, false},
		{"profile ok", SendInput{Type: MessageTypeProfile, ProfileUserID: &id, Text: "meet Dev"}, true},
		{"profile missing", SendInput{Type: MessageTypeProfile}, false},
		{"sound ok", SendInput{Type: MessageTypeSound, SoundID: &id}, true},
		{"live ok", SendInput{Type: MessageTypeLive, StreamID: &id}, true},
		{"live with post", SendInput{Type: MessageTypeLive, StreamID: &id, PostID: &id}, false},
		{"text with sticker", SendInput{Type: MessageTypeText, Text: "hi", StickerID: &id}, false},
		{"post with waveform", SendInput{Type: MessageTypePost, PostID: &id, Waveform: []int{1}}, false},
		{"unknown", SendInput{Type: "hologram"}, false},
	}
	for _, tc := range cases {
		err := validateSendInput(tc.in)
		if tc.ok != (err == nil) {
			t.Errorf("%s: err = %v, want ok=%v", tc.name, err, tc.ok)
		}
		if err != nil && !errors.Is(err, ErrInvalidMessageContent) {
			t.Errorf("%s: %v must wrap ErrInvalidMessageContent", tc.name, err)
		}
	}
}

func TestGiphyURLHosts(t *testing.T) {
	ok := []string{
		"https://i.giphy.com/media/abc/giphy.gif",
		"https://media.giphy.com/media/abc/giphy.gif",
		"https://media0.giphy.com/a.gif",
		"https://media9.giphy.com/a.gif?cid=1&rid=giphy.gif&ct=g",
	}
	bad := []string{
		"http://media.giphy.com/a.gif",
		"https://media10.giphy.com/a.gif",
		"https://giphy.com/a.gif",
		"https://media.giphy.com.evil.test/a.gif",
		"https://evil.test/media.giphy.com/a.gif",
		"https://user@media.giphy.com/a.gif",
		"https://media.giphy.com:8443/a.gif",
		"https://MEDIA.giphy.com/a.gif",
		"javascript:alert(1)",
		"",
	}
	for _, u := range ok {
		if !validGiphyURL(u) {
			t.Errorf("rejected %s", u)
		}
	}
	for _, u := range bad {
		if validGiphyURL(u) {
			t.Errorf("accepted %s", u)
		}
	}
}

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestAdvanceStreak(t *testing.T) {
	a, b := uuid.MustParse("00000000-0000-4000-8000-00000000000a"), uuid.MustParse("00000000-0000-4000-8000-00000000000b")
	st := postgres.StreakState{UserA: a, UserB: b}
	d1, d2, d3, d5 := day(2026, 10, 1), day(2026, 10, 2), day(2026, 10, 3), day(2026, 10, 5)

	st = postgres.AdvanceStreak(st, a, d1)
	if st.Days != 0 || st.LastDay != nil {
		t.Fatalf("one side only must not count: %+v", st)
	}
	st = postgres.AdvanceStreak(st, b, d1)
	if st.Days != 1 || !st.LastDay.Equal(d1) {
		t.Fatalf("both sides on day 1: %+v", st)
	}
	st = postgres.AdvanceStreak(st, b, d1) // replay / second message
	if st.Days != 1 {
		t.Fatalf("a second message the same day must not count twice: %+v", st)
	}
	st = postgres.AdvanceStreak(postgres.AdvanceStreak(st, a, d2), b, d2)
	st = postgres.AdvanceStreak(postgres.AdvanceStreak(st, b, d3), a, d3)
	if st.Days != 3 || !st.LastDay.Equal(d3) {
		t.Fatalf("three consecutive days: %+v", st)
	}
	st = postgres.AdvanceStreak(st, a, d2) // out-of-order replay
	if st.Days != 3 {
		t.Fatalf("an older replay must not change the streak: %+v", st)
	}
	st = postgres.AdvanceStreak(postgres.AdvanceStreak(st, a, d5), b, d5)
	if st.Days != 1 {
		t.Fatalf("a missed day restarts the streak: %+v", st)
	}
	if got := postgres.AdvanceStreak(st, uuid.New(), d5); got.Days != st.Days {
		t.Fatal("a non-member sender changes nothing")
	}
}

func TestStreakDayIsKolkata(t *testing.T) {
	// 20:00 UTC on 1 Oct is 01:30 IST on 2 Oct.
	if got := postgres.StreakDay(time.Date(2026, 10, 1, 20, 0, 0, 0, time.UTC)); !got.Equal(day(2026, 10, 2)) {
		t.Fatalf("StreakDay = %v", got)
	}
}

func TestStreakFor(t *testing.T) {
	ist := postgres.StreakZone
	today := day(2026, 10, 18)
	yesterday := day(2026, 10, 17)
	at := func(h int) time.Time { return time.Date(2026, 10, 18, h, 0, 0, 0, ist) }
	if StreakFor(postgres.StreakState{Days: 2, LastDay: &today}, at(10)) != nil {
		t.Fatal("2 days must not show")
	}
	s := StreakFor(postgres.StreakState{Days: 3, LastDay: &today}, at(10))
	if s == nil || s.Days != 3 || s.AtRisk {
		t.Fatalf("kept today = %+v", s)
	}
	if want := time.Date(2026, 10, 20, 0, 0, 0, 0, ist).UTC(); !s.ExpiresAt.Equal(want) {
		t.Fatalf("expires_at = %v, want %v", s.ExpiresAt, want)
	}
	if s := StreakFor(postgres.StreakState{Days: 4, LastDay: &yesterday}, at(17)); s == nil || s.AtRisk {
		t.Fatalf("yesterday before 18:00 = %+v", s)
	}
	if s := StreakFor(postgres.StreakState{Days: 4, LastDay: &yesterday}, at(18)); s == nil || !s.AtRisk {
		t.Fatalf("yesterday after 18:00 must be at risk: %+v", s)
	}
	old := day(2026, 10, 16)
	if StreakFor(postgres.StreakState{Days: 9, LastDay: &old}, at(10)) != nil {
		t.Fatal("a streak not kept yesterday or today is gone")
	}
}

func TestValidReactionEmoji(t *testing.T) {
	for _, e := range []string{"❤️", "👍", "👍🏽", "👩‍🚀", "🇮🇳", "😂", "a"} {
		if !ValidReactionEmoji(e) {
			t.Errorf("rejected %q", e)
		}
	}
	for _, e := range []string{"", "👍👍", "🇮🇳🇮🇳", "ab", " 👍", "👍\n", strings.Repeat("👨‍👩‍👧", 2),
		"👨‍👩‍👧", // one grapheme, but 18 bytes: over the 16-byte cap
	} {
		if ValidReactionEmoji(e) {
			t.Errorf("accepted %q", e)
		}
	}
}

func TestNoteTextRules(t *testing.T) {
	if graphemeCount("On a train 🚆") != 12 || graphemeCount("👨‍👩‍👧") != 1 || graphemeCount("é") != 1 {
		t.Fatalf("grapheme counts: %d %d %d", graphemeCount("On a train 🚆"), graphemeCount("👨‍👩‍👧"), graphemeCount("é"))
	}
	for _, bad := range []string{"visit spam.example.com", "https://x.test", "call +91 98765 43210", "me@example.com", "send me money now"} {
		if noteTextAllowed(bad) {
			t.Errorf("allowed %q", bad)
		}
	}
	if !noteTextAllowed("Back at 6 ☕") {
		t.Fatal("ordinary note refused")
	}
}

func TestNoteVisibility(t *testing.T) {
	everyone := postgres.Note{Audience: "everyone"}
	friends := postgres.Note{Audience: "friends"}
	only := postgres.Note{Audience: "only_me"}
	mutual := graphRelationship{Follows: true, FollowedBy: true}
	oneWay := graphRelationship{Follows: true}
	blocked := graphRelationship{Follows: true, FollowedBy: true, TargetBlockedViewer: true}
	switch {
	case !noteVisible(everyone, oneWay, true):
		t.Fatal("everyone note hidden")
	case noteVisible(everyone, oneWay, false):
		t.Fatal("an unknown relationship must hide the note")
	case !noteVisible(friends, mutual, true):
		t.Fatal("mutual follow must see a friends note")
	case !noteVisible(friends, graphRelationship{IsConnection: true}, true):
		t.Fatal("a connection must see a friends note")
	case noteVisible(friends, oneWay, true):
		t.Fatal("a one-way follow must not see a friends note")
	case noteVisible(everyone, blocked, true):
		t.Fatal("a blocked pair must never see notes")
	case noteVisible(only, mutual, true):
		t.Fatal("only_me note shown")
	}
}

func TestRequestFiltered(t *testing.T) {
	now := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	fresh := now.Add(-48 * time.Hour)
	old := now.Add(-30 * 24 * time.Hour)
	safe := ChatPolicy{DMSafeMode: true}
	off := ChatPolicy{DMSafeMode: false}
	kw := ChatPolicy{DMSafeMode: false, DMFilteredKeywords: []string{"crypto"}}
	cases := []struct {
		name    string
		preview string
		policy  ChatPolicy
		created *time.Time
		want    bool
	}{
		{"new account, safe mode", "hi", safe, &fresh, true},
		{"new account, safe mode off", "hi", off, &fresh, false},
		{"old account", "hi", safe, &old, false},
		{"unknown age", "hi", safe, nil, false},
		{"link, safe mode", "see example.com", safe, &old, true},
		{"link, safe mode off", "see example.com", off, &old, false},
		{"keyword, safe mode off", "free CRYPTO here", kw, &old, true},
		{"keyword absent", "hello", kw, &old, false},
	}
	for _, tc := range cases {
		if got := requestFiltered(tc.preview, tc.policy, tc.created, now); got != tc.want {
			t.Errorf("%s: filtered = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestForwardOutcome(t *testing.T) {
	cases := map[error]string{
		nil:                                     "sent",
		ErrMessagingNotAllowed:                  "blocked",
		ErrRequestFirstMessageInvalid:           "blocked",
		ErrAwaitingRequestAcceptance:            "blocked",
		ErrMediaNotAllowed:                      "blocked",
		ErrMatchClosed:                          "unavailable",
		errors.New("not a conversation member"): "unavailable",
	}
	for err, want := range cases {
		if got := forwardOutcome(err); got != want {
			t.Errorf("%v: %s, want %s", err, got, want)
		}
	}
}

func TestNormalizeKeywords(t *testing.T) {
	in := []string{" Crypto ", "crypto", "", strings.Repeat("x", 31), "Loan"}
	got := postgres.NormalizeKeywords(in)
	if len(got) != 2 || got[0] != "crypto" || got[1] != "loan" {
		t.Fatalf("NormalizeKeywords = %v", got)
	}
	many := make([]string, 150)
	for i := range many {
		many[i] = "k" + strings.Repeat("a", i%29) + string(rune('a'+i%26)) + string(rune('a'+i/26))
	}
	if n := len(postgres.NormalizeKeywords(many)); n != postgres.MaxFilteredKeywords {
		t.Fatalf("kept %d keywords, want the 100 cap", n)
	}
}

func TestPolicyFetchReadsParityKeysWithDefaults(t *testing.T) {
	bodies := map[string]string{
		"missing":   `{"data":{"chat_availability":"enabled"}}`,
		"present":   `{"data":{"chat_availability":"enabled","dm_safe_mode":false,"dm_sensitive_media_filter":true,"dm_filtered_keywords":["Crypto"," loan "],"chat_bubble_style":"ember"}}`,
		"bad style": `{"data":{"chat_availability":"enabled","chat_bubble_style":"neon"}}`,
	}
	var current string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(bodies[current])) }))
	defer srv.Close()
	s := &Service{identityUserURL: srv.URL, httpClient: srv.Client(), log: slog.Default()}

	current = "missing"
	p, ok := s.fetchPolicyFromIdentity(context.Background(), uuid.New())
	if !ok || !p.DMSafeMode || p.DMSensitiveMediaFilter || len(p.DMFilteredKeywords) != 0 || p.ChatBubbleStyle != "default" {
		t.Fatalf("missing keys must project the defaults: %+v", p)
	}
	current = "present"
	p, _ = s.fetchPolicyFromIdentity(context.Background(), uuid.New())
	if p.DMSafeMode || !p.DMSensitiveMediaFilter || p.ChatBubbleStyle != "ember" || len(p.DMFilteredKeywords) != 2 || p.DMFilteredKeywords[0] != "crypto" || p.DMFilteredKeywords[1] != "loan" {
		t.Fatalf("present keys = %+v", p)
	}
	current = "bad style"
	if p, _ = s.fetchPolicyFromIdentity(context.Background(), uuid.New()); p.ChatBubbleStyle != "default" {
		t.Fatalf("unknown bubble style = %q", p.ChatBubbleStyle)
	}
}
