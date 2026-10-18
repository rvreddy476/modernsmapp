package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/atpost/chat-message-service/internal/store/postgres"
	"github.com/atpost/chat-message-service/internal/store/scylla"
	sharedEvents "github.com/atpost/chat-shared/events"
	"github.com/google/uuid"
)

// TikTok parity T-25 ("Send to friends"): a "post" message carries only a
// post id (and an optional caption). These tests pin the validation, that a
// post card is gated exactly like a text message, that the request flow
// refuses it as a first message, and every denormalised copy (inbox preview,
// last-message preview, realtime frame, MessageCreated event, history read).

// --- capturing fakes over the dating send fixture ---

type postSendStore struct {
	*datingSendStore
	lastPreview    string
	requestPreview string
	events         map[string]interface{} // event type -> last payload
}

func (s *postSendStore) SetLastMessage(_ context.Context, _, _ uuid.UUID, preview string, _ time.Time) error {
	s.lastPreview = preview
	return nil
}

func (s *postSendStore) SetMessageRequestPreview(_ context.Context, _ uuid.UUID, preview string) error {
	s.requestPreview = preview
	return nil
}

func (s *postSendStore) InsertOutboxEventOnce(_ context.Context, _ string, eventType string, payload interface{}) error {
	if s.events == nil {
		s.events = map[string]interface{}{}
	}
	s.events[eventType] = payload
	return nil
}

type postMsgStore struct {
	*previewMsgStore
	created      []scylla.Message
	inboxPreview string
}

func (m *postMsgStore) CreateMessage(_ context.Context, msg *scylla.Message) error {
	m.created = append(m.created, *msg)
	return nil
}

func (m *postMsgStore) UpsertInbox(_ context.Context, _, _, _ uuid.UUID, text string, _ time.Time) error {
	m.inboxPreview = text
	return nil
}

type postFixture struct {
	svc    *Service
	store  *postSendStore
	msgs   *postMsgStore
	convID uuid.UUID
	userA  uuid.UUID
	userB  uuid.UUID
}

func newPostFixture(t *testing.T) *postFixture {
	t.Helper()
	base := newDatingFixture(t, "", false)
	store := &postSendStore{datingSendStore: base.store}
	msgs := &postMsgStore{previewMsgStore: &previewMsgStore{}}
	base.svc.convStore = store
	base.svc.msgStore = msgs
	return &postFixture{svc: base.svc, store: store, msgs: msgs, convID: base.convID, userA: base.userA, userB: base.userB}
}

func (f *postFixture) sendPost(from uuid.UUID, postID *uuid.UUID, caption, key string) (*MessageResponse, error) {
	return f.svc.SendMessage(context.Background(), from, f.convID, MessageTypePost, caption, nil, postID, nil, key)
}

func (f *postFixture) sendText(from uuid.UUID, text, key string) (*MessageResponse, error) {
	return f.svc.SendMessage(context.Background(), from, f.convID, MessageTypeText, text, nil, nil, nil, key)
}

// markRequest turns the fixture's conversation into a pending message request
// from userA to userB.
func (f *postFixture) markRequest(preview string) {
	f.store.conversations[f.convID].IsRequest = true
	f.store.latestRequest = &postgres.MessageRequest{
		ConversationID: f.convID, SenderID: f.userA, ReceiverID: f.userB,
		Status: "pending", Preview: preview,
	}
}

func uuidPtr(id uuid.UUID) *uuid.UUID { return &id }

// --- validation ---

func TestValidateMessageContent(t *testing.T) {
	post, media := uuid.New(), uuid.New()
	cases := []struct {
		name    string
		msgType string
		text    string
		mediaID *uuid.UUID
		postID  *uuid.UUID
		wantErr bool
	}{
		{"post with id", MessageTypePost, "", nil, &post, false},
		{"post with caption", MessageTypePost, "look at this", nil, &post, false},
		{"post with max caption", MessageTypePost, strings.Repeat("é", MessageTextMaxRunes), nil, &post, false},
		{"post missing id", MessageTypePost, "hi", nil, nil, true},
		{"post nil uuid", MessageTypePost, "", nil, uuidPtr(uuid.Nil), true},
		{"post with media", MessageTypePost, "", &media, &post, true},
		{"post caption too long", MessageTypePost, strings.Repeat("a", MessageTextMaxRunes+1), nil, &post, true},
		{"text with post id", MessageTypeText, "hi", nil, &post, true},
		{"media with post id", MessageTypeMedia, "", &media, &post, true},
		{"plain text", MessageTypeText, "hi", nil, nil, false},
		{"plain media", MessageTypeMedia, "", &media, nil, false},
		{"unknown type", "sticker", "hi", nil, nil, true},
		{"empty type", "", "hi", nil, nil, true},
	}
	for _, tc := range cases {
		err := validateMessageContent(tc.msgType, tc.text, tc.mediaID, tc.postID)
		if tc.wantErr != (err != nil) {
			t.Errorf("%s: err = %v, wantErr %v", tc.name, err, tc.wantErr)
		}
		if err != nil && !errors.Is(err, ErrInvalidMessageContent) {
			t.Errorf("%s: error %v must wrap ErrInvalidMessageContent", tc.name, err)
		}
	}
}

// TestEveryMessageTypeIsHandled is the exhaustiveness guard: every known type
// must validate in its well-formed shape (i.e. not hit the "unknown type"
// default) and produce a preview.
func TestEveryMessageTypeIsHandled(t *testing.T) {
	id := uuid.New()
	gif := &GIFRef{Provider: "giphy", ID: "abc123", URL: "https://media.giphy.com/media/abc123/giphy.gif", Width: 200, Height: 150}
	wellFormed := map[string]func() error{
		MessageTypeText:  func() error { return validateMessageContent(MessageTypeText, "hi", nil, nil) },
		MessageTypeMedia: func() error { return validateMessageContent(MessageTypeMedia, "", &id, nil) },
		MessageTypePost:  func() error { return validateMessageContent(MessageTypePost, "", nil, &id) },
		// Chat parity rich types go through validateSendInput.
		MessageTypeVoice: func() error {
			return validateSendInput(SendInput{Type: MessageTypeVoice, MediaID: &id, DurationMs: 1200})
		},
		MessageTypeSticker: func() error { return validateSendInput(SendInput{Type: MessageTypeSticker, StickerID: &id}) },
		MessageTypeGIF:     func() error { return validateSendInput(SendInput{Type: MessageTypeGIF, GIF: gif}) },
		MessageTypeProfile: func() error { return validateSendInput(SendInput{Type: MessageTypeProfile, ProfileUserID: &id}) },
		MessageTypeSound:   func() error { return validateSendInput(SendInput{Type: MessageTypeSound, SoundID: &id}) },
		MessageTypeLive:    func() error { return validateSendInput(SendInput{Type: MessageTypeLive, StreamID: &id}) },
	}
	for _, typ := range KnownMessageTypes {
		if messagePreview(typ, "") == "" && typ != MessageTypeText && typ != MessageTypeMedia {
			t.Errorf("message type %q has no preview", typ)
		}
	}
	for _, typ := range KnownMessageTypes {
		check, ok := wellFormed[typ]
		if !ok {
			t.Fatalf("message type %q has no well-formed case here: add one, and handle it in validateMessageContent and messagePreview", typ)
		}
		if err := check(); err != nil {
			t.Errorf("well-formed %q rejected: %v", typ, err)
		}
	}
	if len(wellFormed) != len(KnownMessageTypes) {
		t.Fatalf("KnownMessageTypes (%v) and the test's cases disagree", KnownMessageTypes)
	}
}

func TestSendRejectsMalformedPostBeforeAnyGate(t *testing.T) {
	f := newPostFixture(t)
	for name, postID := range map[string]*uuid.UUID{"missing": nil, "nil uuid": uuidPtr(uuid.Nil)} {
		if _, err := f.sendPost(f.userA, postID, "", "bad-"+name); !errors.Is(err, ErrInvalidMessageContent) {
			t.Errorf("%s post_id: err = %v, want ErrInvalidMessageContent", name, err)
		}
	}
	if _, err := f.svc.SendMessage(context.Background(), f.userA, f.convID, "sticker", "hi", nil, nil, nil, "bad-type"); !errors.Is(err, ErrInvalidMessageContent) {
		t.Errorf("unknown type: err = %v, want ErrInvalidMessageContent", err)
	}
	if len(f.store.intents) != 0 {
		t.Fatalf("a rejected send must reserve nothing, got %d intents", len(f.store.intents))
	}
}

// --- delivery: storage, previews, event, frame, read ---

func TestPostMessageIsStoredAndPreviewedAsAPostCard(t *testing.T) {
	f := newPostFixture(t)
	postID := uuid.New()

	resp, err := f.sendPost(f.userA, &postID, "", "post-1")
	if err != nil {
		t.Fatalf("send post: %v", err)
	}
	if resp.Type != MessageTypePost || resp.PostID == nil || *resp.PostID != postID || resp.Text != "" {
		t.Fatalf("response = type %q post %v text %q", resp.Type, resp.PostID, resp.Text)
	}
	intent := f.store.intents["post-1"]
	if intent == nil || intent.PostID == nil || *intent.PostID != postID || intent.MessageType != MessageTypePost {
		t.Fatalf("durable intent must carry the post id for repair replay: %+v", intent)
	}
	if len(f.msgs.created) != 1 || f.msgs.created[0].PostID == nil || *f.msgs.created[0].PostID != postID {
		t.Fatalf("scylla row must carry the post id: %+v", f.msgs.created)
	}
	if f.store.lastPreview != PostMessagePreview || f.msgs.inboxPreview != PostMessagePreview {
		t.Fatalf("last-message preview %q / inbox %q, want %q", f.store.lastPreview, f.msgs.inboxPreview, PostMessagePreview)
	}

	// MessageCreated drives the push. It carries type=post and nothing about
	// the post: notification-service never sees the id or a caption.
	created, ok := f.store.events[sharedEvents.MessageCreated].(sharedEvents.MessageCreatedPayload)
	if !ok {
		t.Fatalf("no MessageCreated event queued: %v", f.store.events)
	}
	if created.Type != MessageTypePost || created.IsRequest {
		t.Fatalf("MessageCreated type=%q is_request=%v", created.Type, created.IsRequest)
	}
	raw, _ := json.Marshal(created)
	if strings.Contains(string(raw), postID.String()) {
		t.Fatalf("MessageCreated must not carry the post id: %s", raw)
	}
}

func TestCaptionedPostPreviewsTheCaption(t *testing.T) {
	f := newPostFixture(t)
	if _, err := f.sendPost(f.userA, uuidPtr(uuid.New()), "you have to see this", "post-cap"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if f.store.lastPreview != "you have to see this" {
		t.Fatalf("captioned post preview = %q", f.store.lastPreview)
	}
	if got := messagePreview(MessageTypePost, "   "); got != PostMessagePreview {
		t.Fatalf("whitespace caption preview = %q", got)
	}
	// Text and media previews are unchanged by the new type.
	if got := messagePreview(MessageTypeText, "hello"); got != "hello" {
		t.Fatalf("text preview = %q", got)
	}
	if got := messagePreview(MessageTypeMedia, ""); got != "" {
		t.Fatalf("uncaptioned media preview must stay empty, got %q", got)
	}
}

func TestPostIDIsPartOfTheIdempotencyHash(t *testing.T) {
	f := newPostFixture(t)
	if _, err := f.sendPost(f.userA, uuidPtr(uuid.New()), "", "same-key"); err != nil {
		t.Fatalf("first send: %v", err)
	}
	if _, err := f.sendPost(f.userA, uuidPtr(uuid.New()), "", "same-key"); !errors.Is(err, ErrIdempotencyConflict) {
		t.Fatalf("same key, different post: err = %v, want ErrIdempotencyConflict", err)
	}
}

func TestRealtimeFrameCarriesPostIDOnlyOnPosts(t *testing.T) {
	conv, msg, sender, post := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	body := messageFrameBody(conv, msg, sender, MessageTypePost, "", nil, &post, time.Now())
	if body["type"] != MessageTypePost || body["post_id"] != &post {
		t.Fatalf("post frame = %v", body)
	}
	text := messageFrameBody(conv, msg, sender, MessageTypeText, "hi", nil, nil, time.Now())
	if _, ok := text["post_id"]; ok {
		t.Fatalf("a text frame must not carry post_id: %v", text)
	}
}

func TestHistoryReturnsThePostIDForClientHydration(t *testing.T) {
	f := newPostFixture(t)
	postID := uuid.New()
	f.msgs.surviving = []scylla.Message{{
		ConversationID: f.convID, Bucket: "202610", Ts: time.Now(), MsgID: uuid.New(),
		SenderID: f.userA, Type: MessageTypePost, PostID: &postID,
	}}
	out, _, err := f.svc.GetMessages(context.Background(), f.userB, f.convID, nil, 30)
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(out) != 1 || out[0].Type != MessageTypePost || out[0].PostID == nil || *out[0].PostID != postID {
		t.Fatalf("history = %+v", out)
	}
	raw, _ := json.Marshal(out[0])
	if !strings.Contains(string(raw), `"post_id":"`+postID.String()+`"`) {
		t.Fatalf("wire shape must carry post_id: %s", raw)
	}
}

// --- permission parity with text ---

func TestPostFollowsTheSameGatesAsText(t *testing.T) {
	t.Run("blocked or removed sender (membership severed)", func(t *testing.T) {
		f := newPostFixture(t)
		delete(f.store.roles[f.convID], f.userA) // a block severs membership
		_, textErr := f.sendText(f.userA, "hi", "t")
		_, postErr := f.sendPost(f.userA, uuidPtr(uuid.New()), "", "p")
		if textErr == nil || postErr == nil || textErr.Error() != postErr.Error() {
			t.Fatalf("severed sender: text err %v, post err %v — must match and refuse", textErr, postErr)
		}
	})
	t.Run("paused recipient", func(t *testing.T) {
		f := newPostFixture(t)
		f.store.freshPolicy(f.userB, true)
		_, textErr := f.sendText(f.userA, "hi", "t")
		_, postErr := f.sendPost(f.userA, uuidPtr(uuid.New()), "", "p")
		if !errors.Is(textErr, ErrMessagingNotAllowed) || !errors.Is(postErr, ErrMessagingNotAllowed) {
			t.Fatalf("paused recipient: text %v, post %v", textErr, postErr)
		}
	})
	t.Run("paused sender", func(t *testing.T) {
		f := newPostFixture(t)
		f.store.freshPolicy(f.userA, true)
		_, postErr := f.sendPost(f.userA, uuidPtr(uuid.New()), "", "p")
		if !errors.Is(postErr, ErrMessagingNotAllowed) {
			t.Fatalf("paused sender post: %v", postErr)
		}
	})
}

func TestPostCannotOpenAMessageRequest(t *testing.T) {
	f := newPostFixture(t)
	f.markRequest("")

	_, err := f.sendPost(f.userA, uuidPtr(uuid.New()), "", "req-post")
	if !errors.Is(err, ErrRequestFirstMessageInvalid) {
		t.Fatalf("post as a request's first message: err = %v, want ErrRequestFirstMessageInvalid", err)
	}
	_, err = f.sendPost(f.userA, uuidPtr(uuid.New()), "hey, saw this", "req-post-cap")
	if !errors.Is(err, ErrRequestFirstMessageInvalid) {
		t.Fatalf("captioned post as a request's first message: err = %v", err)
	}
	if len(f.store.intents) != 0 {
		t.Fatal("a refused request post must not reserve a delivery")
	}

	// Plain text still opens the request, as before.
	if _, err := f.sendText(f.userA, "hi there", "req-text"); err != nil {
		t.Fatalf("text first message: %v", err)
	}
	if f.store.requestPreview != "hi there" {
		t.Fatalf("request preview = %q", f.store.requestPreview)
	}
}

func TestPostAfterTheOneRequestMessageWaitsLikeText(t *testing.T) {
	f := newPostFixture(t)
	f.markRequest("hi there") // the single allowed message was already sent

	_, textErr := f.sendText(f.userA, "again", "t2")
	_, postErr := f.sendPost(f.userA, uuidPtr(uuid.New()), "", "p2")
	if !errors.Is(textErr, ErrAwaitingRequestAcceptance) || !errors.Is(postErr, ErrAwaitingRequestAcceptance) {
		t.Fatalf("follow-up before acceptance: text %v, post %v", textErr, postErr)
	}
}

func TestPostFlowsOnceTheRequestIsAccepted(t *testing.T) {
	f := newPostFixture(t)
	f.markRequest("hi there")
	f.store.conversations[f.convID].IsRequest = false
	f.store.latestRequest.Status = "accepted"

	if _, err := f.sendPost(f.userA, uuidPtr(uuid.New()), "", "after-accept"); err != nil {
		t.Fatalf("post after acceptance: %v", err)
	}
}

// Deleting the newest message re-derives the inbox preview from the survivor;
// a surviving post card must read "Sent a post", not an empty string.
func TestPreviewRepairDescribesASurvivingPostCard(t *testing.T) {
	fx := newPreviewFixture(t)
	postID := uuid.New()
	fx.msgs.surviving = []scylla.Message{{
		ConversationID: fx.convID, SenderID: fx.sender,
		Ts: fx.ts.Add(-time.Minute), Type: MessageTypePost, PostID: &postID,
	}}
	if err := fx.deleteMessage(t); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !fx.conv.applied || fx.conv.lastPreview != PostMessagePreview {
		t.Fatalf("repaired preview = %q (applied=%v), want %q", fx.conv.lastPreview, fx.conv.applied, PostMessagePreview)
	}
}
