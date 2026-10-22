package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Chat-parity CONTRACT §8: show_message_preview decides whether a chat push
// says what was sent. Off (or unknown) keeps today's generic copy.

func renderBody(t *testing.T, msg ChatMessagePush, previewOn bool, name string) (string, string, map[string]string) {
	t.Helper()
	conv := uuid.New()
	render := ChatMessageRender(msg, conv, previewOn, name)
	title, body, data := buildPushData(uuid.New(), "dm", "conversation", conv, "/messenger?user=x", render)
	if data["conversation_id"] != conv.String() {
		t.Fatalf("conversation_id %q, want %q", data["conversation_id"], conv)
	}
	return title, body, data
}

func TestChatPreviewOnNamesSenderAndText(t *testing.T) {
	title, body, data := renderBody(t, ChatMessagePush{MessageType: "text", Text: "see you at  \n six"}, true, "Asha")
	if body != "Asha: see you at six" {
		t.Fatalf("body %q", body)
	}
	if title != "New Message" || data["body"] != body || data["title"] != title {
		t.Fatalf("title/data mismatch: %q %+v", title, data)
	}
}

func TestChatPreviewOffIsGeneric(t *testing.T) {
	for _, msg := range []ChatMessagePush{
		{MessageType: "text", Text: "secret plans"},
		{MessageType: "media", MediaKind: "image"},
		{MessageType: "voice"},
	} {
		_, body, _ := renderBody(t, msg, false, "Asha")
		if body != "You have a new message" {
			t.Errorf("%s: preview off body %q, want generic", msg.MessageType, body)
		}
	}
	// A shared post keeps its existing generic copy.
	if _, body, _ := renderBody(t, ChatMessagePush{MessageType: "post"}, false, "Asha"); body != DMPostBody {
		t.Errorf("post preview off body %q, want %q", body, DMPostBody)
	}
}

func TestChatPreviewWithoutNameOrTextIsGeneric(t *testing.T) {
	if _, body, _ := renderBody(t, ChatMessagePush{MessageType: "text", Text: "hello"}, true, "  "); body != "You have a new message" {
		t.Errorf("unknown sender: body %q", body)
	}
	if _, body, _ := renderBody(t, ChatMessagePush{MessageType: "text", Text: " \n "}, true, "Asha"); body != "You have a new message" {
		t.Errorf("empty text: body %q", body)
	}
	if _, body, _ := renderBody(t, ChatMessagePush{MessageType: "hologram"}, true, "Asha"); body != "You have a new message" {
		t.Errorf("unknown type: body %q", body)
	}
}

func TestChatPreviewMediaCopy(t *testing.T) {
	cases := []struct {
		msg  ChatMessagePush
		want string
	}{
		{ChatMessagePush{MessageType: "media", MediaKind: "image"}, "Asha sent a photo"},
		{ChatMessagePush{MessageType: "media", MediaKind: "video"}, "Asha sent a video"},
		{ChatMessagePush{MessageType: "media", MediaKind: "audio"}, "Asha sent a voice message"},
		{ChatMessagePush{MessageType: "media"}, "Asha sent an attachment"},
		{ChatMessagePush{MessageType: "voice"}, "Asha sent a voice message"},
		{ChatMessagePush{MessageType: "sticker"}, "Asha sent a sticker"},
		{ChatMessagePush{MessageType: "gif"}, "Asha sent a GIF"},
		{ChatMessagePush{MessageType: "post"}, "Asha shared a post"},
		{ChatMessagePush{MessageType: "profile"}, "Asha shared a profile"},
		{ChatMessagePush{MessageType: "location"}, "Asha shared a location"},
		// A location push never shows text, even if a client sent some.
		{ChatMessagePush{MessageType: "location", Text: "12.97160, 77.59460"}, "Asha shared a location"},
		{ChatMessagePush{MessageType: "sound"}, "Asha shared a sound"},
		{ChatMessagePush{MessageType: "live"}, "Asha shared a LIVE"},
		// Media never leaks its caption, even when one is present.
		{ChatMessagePush{MessageType: "media", MediaKind: "image", Text: "caption"}, "Asha sent a photo"},
	}
	for _, tc := range cases {
		if _, body, _ := renderBody(t, tc.msg, true, "Asha"); body != tc.want {
			t.Errorf("%s/%s: body %q, want %q", tc.msg.MessageType, tc.msg.MediaKind, body, tc.want)
		}
	}
}

func TestChatPreviewTruncatesTo80Graphemes(t *testing.T) {
	long := strings.Repeat("ab", 60) // 120 graphemes
	_, body, _ := renderBody(t, ChatMessagePush{MessageType: "text", Text: long}, true, "A")
	text := strings.TrimPrefix(body, "A: ")
	if n := len(graphemeStarts(text)); n != ChatPreviewMaxGraphemes {
		t.Fatalf("preview has %d graphemes, want %d: %q", n, ChatPreviewMaxGraphemes, text)
	}
	if !strings.HasSuffix(text, "…") {
		t.Fatalf("truncated preview should end with an ellipsis: %q", text)
	}
	// Exactly 80 is untouched.
	exact := strings.Repeat("x", 80)
	if got := truncateGraphemes(exact, 80); got != exact {
		t.Fatalf("80 graphemes changed: %q", got)
	}
}

func TestTruncateGraphemesKeepsClustersWhole(t *testing.T) {
	family := "👨‍👩‍👧" // one cluster, five runes
	flag := "🇮🇳"      // one cluster, two regional indicators
	thumb := "👍🏽"     // emoji + skin tone
	accent := "é"    // e + combining acute
	for _, c := range []string{family, flag, thumb, accent} {
		if n := len(graphemeStarts(c)); n != 1 {
			t.Errorf("%q counted as %d graphemes, want 1", c, n)
		}
	}
	s := strings.Repeat(family, 100)
	got := truncateGraphemes(s, 80)
	if !utf8.ValidString(got) {
		t.Fatal("truncation split a rune")
	}
	if !strings.HasPrefix(got, strings.Repeat(family, 79)) || len(graphemeStarts(got)) != 80 {
		t.Fatalf("cluster split: %d graphemes", len(graphemeStarts(got)))
	}
	if n := len(graphemeStarts(flag + flag + flag)); n != 3 {
		t.Fatalf("three flags counted as %d", n)
	}
}

func TestChatPushCarriesAlertToneAndIDs(t *testing.T) {
	mid := uuid.New()
	for _, tone := range []string{"default", "chime", "pop", "bell", "silent"} {
		_, _, data := renderBody(t, ChatMessagePush{MessageType: "text", MessageID: mid, AlertTone: tone}, false, "")
		if data["alert_tone"] != tone {
			t.Errorf("tone %q: data alert_tone %q", tone, data["alert_tone"])
		}
		if data["message_id"] != mid.String() {
			t.Errorf("message_id %q", data["message_id"])
		}
	}
	// Absent → omitted; unknown → dropped, never forwarded.
	for _, tone := range []string{"", "airhorn"} {
		_, _, data := renderBody(t, ChatMessagePush{MessageType: "text", AlertTone: tone}, true, "Asha")
		if _, ok := data["alert_tone"]; ok {
			t.Errorf("tone %q should be omitted, got %q", tone, data["alert_tone"])
		}
	}
}

func TestRenderDataNeverReplacesRoutingKeys(t *testing.T) {
	conv := uuid.New()
	render := RenderOverride{Data: map[string]string{"type": "evil", "deep_link": "x", "collapse_key": "y", "extra": "ok"}}
	_, _, data := buildPushData(uuid.New(), "dm", "conversation", conv, "/messenger?user=x", render)
	if data["type"] != "dm" || data["deep_link"] != "/messenger?user=x" || data["collapse_key"] == "y" {
		t.Fatalf("routing keys replaced: %+v", data)
	}
	if data["extra"] != "ok" {
		t.Fatalf("extra key dropped: %+v", data)
	}
}

func TestRecipientPreviewFailsClosedAndCaches(t *testing.T) {
	s := New(nil, nil)
	user := uuid.New()
	calls := 0
	answer, known := true, false
	s.SetChatPreviewReaders(func(context.Context, uuid.UUID) (bool, bool) {
		calls++
		return answer, known
	}, nil)

	if s.recipientShowsPreview(context.Background(), user) {
		t.Fatal("unknown setting must answer false (generic copy)")
	}
	known = true
	if !s.recipientShowsPreview(context.Background(), user) {
		t.Fatal("known true answered false")
	}
	if calls != 2 {
		t.Fatalf("failure must not be cached: %d calls", calls)
	}
	answer = false
	if !s.recipientShowsPreview(context.Background(), user) || calls != 2 {
		t.Fatalf("cached answer not reused (calls %d)", calls)
	}
	// user.settings_changed invalidates: the next push re-reads.
	if err := s.InvalidateChatPreview(context.Background(), user); err != nil {
		t.Fatal(err)
	}
	if s.recipientShowsPreview(context.Background(), user) || calls != 3 {
		t.Fatalf("invalidation not honoured (calls %d)", calls)
	}
}

func TestPreviewOffNeverLooksUpTheSender(t *testing.T) {
	s := New(nil, nil)
	nameCalls := 0
	s.SetChatPreviewReaders(
		func(context.Context, uuid.UUID) (bool, bool) { return false, true },
		func(context.Context, uuid.UUID) string { nameCalls++; return "Asha" },
	)
	r := s.chatMessageRenderFor(context.Background(), uuid.New(), uuid.New(), uuid.New(), ChatMessagePush{MessageType: "text", Text: "hi"})
	if r.Body != "" || nameCalls != 0 {
		t.Fatalf("preview off rendered %q with %d name lookups", r.Body, nameCalls)
	}
	s2 := New(nil, nil)
	s2.SetChatPreviewReaders(
		func(context.Context, uuid.UUID) (bool, bool) { return true, true },
		func(context.Context, uuid.UUID) string { return "Asha" },
	)
	r = s2.chatMessageRenderFor(context.Background(), uuid.New(), uuid.New(), uuid.New(), ChatMessagePush{MessageType: "text", Text: "hi"})
	if r.Body != "Asha: hi" {
		t.Fatalf("preview on body %q", r.Body)
	}
}

func TestParseShowMessagePreview(t *testing.T) {
	cases := []struct {
		body      string
		on, known bool
	}{
		{`{"data":{"chat_availability":"enabled","show_message_preview":true}}`, true, true},
		{`{"data":{"chat_availability":"enabled","show_message_preview":false}}`, false, true},
		{`{"data":{"show_message_preview":true}}`, false, false},                // not the authority
		{`{"data":{"chat_availability":"enabled"}}`, false, false},              // field missing → unknown
		{`{"show_message_preview":true,"chat_availability":"x"}`, false, false}, // no envelope
		{`not json`, false, false},
	}
	for _, tc := range cases {
		on, known := parseShowMessagePreview([]byte(tc.body))
		if on != tc.on || known != tc.known {
			t.Errorf("%s: got (%v,%v), want (%v,%v)", tc.body, on, known, tc.on, tc.known)
		}
	}
}

func TestFetchShowMessagePreviewReadsSettingsAuthority(t *testing.T) {
	user := uuid.New()
	t.Setenv("INTERNAL_SERVICE_KEY", "k-test")
	var gotPath, gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotKey = r.URL.Path, r.Header.Get("X-Internal-Service-Key")
		_, _ = w.Write([]byte(`{"data":{"chat_availability":"enabled","show_message_preview":true}}`))
	}))
	defer srv.Close()
	s := New(nil, nil)
	s.SetIdentityUserServiceURL(srv.URL + "/")
	on, known := s.fetchShowMessagePreview(context.Background(), user)
	if !on || !known {
		t.Fatalf("got (%v,%v)", on, known)
	}
	if gotPath != "/v1/users/"+user.String()+"/settings" || gotKey != "k-test" {
		t.Fatalf("path %q key set=%v", gotPath, gotKey != "")
	}

	srv500 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv500.Close()
	s.SetIdentityUserServiceURL(srv500.URL)
	if _, known := s.fetchShowMessagePreview(context.Background(), user); known {
		t.Fatal("a 500 must be unknown")
	}
	s.SetIdentityUserServiceURL("")
	if _, known := s.fetchShowMessagePreview(context.Background(), user); known {
		t.Fatal("no authority configured must be unknown")
	}
}
