package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/google/uuid"
)

// Chat message pushes (chat-parity CONTRACT §8).
//
// The recipient's identity setting `show_message_preview` decides whether a
// "dm" push says what was sent. On: the body names the sender and carries the
// text (≤80 graphemes) or a short action ("sent a photo"). Off, unknown, or
// any lookup failure: the generic copy, exactly as before — a failed read
// must never leak message text onto a lock screen.
//
// Message requests (a stranger's first message) never reach this path: the
// MessageCreated event with is_request=true is dropped by the chat consumer,
// and the MessageRequestCreated row is always generic copy.

// Further chat message types (CONTRACT §2.1), mirrored from chat-service.
const (
	ChatMessageTypeVoice   = "voice"
	ChatMessageTypeSticker = "sticker"
	ChatMessageTypeGIF     = "gif"
	ChatMessageTypeProfile = "profile"
	ChatMessageTypeSound   = "sound"
	ChatMessageTypeLive    = "live"
)

// ChatPreviewMaxGraphemes bounds the text preview in a push body.
const ChatPreviewMaxGraphemes = 80

// Alert tones a conversation may carry (CONTRACT §1.1). Anything else is
// dropped rather than forwarded to a client that would not know it.
var chatAlertTones = map[string]bool{
	"default": true, "chime": true, "pop": true, "bell": true, "silent": true,
}

// ChatMessagePush is the push-relevant content of one chat message, as
// chat-service ships it on MessageCreated. Every field is optional: an older
// publisher sends only the type, and the copy stays generic.
type ChatMessagePush struct {
	MessageID   uuid.UUID
	MessageType string
	// MediaKind is image|video|audio for a "media" message.
	MediaKind string
	// Text is the message text (type "text").
	Text string
	// AlertTone is the recipient's per-conversation tone; empty = omit.
	AlertTone string
}

// chatPreviewAction is the action phrase for a non-text message, or "" when
// the type has no preview copy (unknown types stay generic).
func chatPreviewAction(messageType, mediaKind string) string {
	switch messageType {
	case ChatMessageTypeMedia:
		switch strings.ToLower(mediaKind) {
		case "image", "photo":
			return "sent a photo"
		case "video":
			return "sent a video"
		case "audio":
			return "sent a voice message"
		default:
			return "sent an attachment"
		}
	case ChatMessageTypeVoice:
		return "sent a voice message"
	case ChatMessageTypeSticker:
		return "sent a sticker"
	case ChatMessageTypeGIF:
		return "sent a GIF"
	case ChatMessageTypePost:
		return "shared a post"
	case ChatMessageTypeProfile:
		return "shared a profile"
	case ChatMessageTypeSound:
		return "shared a sound"
	case ChatMessageTypeLive:
		return "shared a LIVE"
	}
	return ""
}

// ChatMessageRender is the push copy and extra data for one chat message.
// previewOn is the RECIPIENT's show_message_preview (false when unknown);
// senderName is the sender's display name ("" when unknown).
func ChatMessageRender(msg ChatMessagePush, conversationID uuid.UUID, previewOn bool, senderName string) RenderOverride {
	render := DMRender(msg.MessageType)
	render.Data = chatPushData(msg, conversationID)
	if !previewOn {
		return render
	}
	name := strings.TrimSpace(collapseWhitespace(senderName))
	if name == "" {
		return render
	}
	var body string
	if msg.MessageType == ChatMessageTypeText || msg.MessageType == "" {
		text := truncateGraphemes(collapseWhitespace(msg.Text), ChatPreviewMaxGraphemes)
		if text == "" {
			return render
		}
		body = name + ": " + text
	} else if action := chatPreviewAction(msg.MessageType, msg.MediaKind); action != "" {
		body = name + " " + action
	} else {
		return render
	}
	title, _ := notifTitleBody("dm")
	render.Title = title
	render.Body = body
	return render
}

// chatPushData is the deep-link and tone data a chat push carries on top of
// the generic keys.
func chatPushData(msg ChatMessagePush, conversationID uuid.UUID) map[string]string {
	data := map[string]string{"conversation_id": conversationID.String()}
	if msg.MessageID != uuid.Nil {
		data["message_id"] = msg.MessageID.String()
	}
	if tone := strings.ToLower(strings.TrimSpace(msg.AlertTone)); chatAlertTones[tone] {
		data["alert_tone"] = tone
	}
	return data
}

func collapseWhitespace(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// truncateGraphemes keeps at most max user-perceived characters, ending in
// "…" (which counts) when it cut. Clusters are approximated: combining marks,
// variation selectors, emoji skin-tone modifiers, tag characters and anything
// joined by ZWJ extend the previous character, and regional-indicator pairs
// (flags) count once. That covers what chat text actually contains without a
// new dependency; it never splits a rune.
func truncateGraphemes(s string, max int) string {
	if max <= 0 || s == "" {
		return ""
	}
	clusters := graphemeStarts(s)
	if len(clusters) <= max {
		return s
	}
	return strings.TrimRight(s[:clusters[max-1]], " ") + "…"
}

// graphemeStarts returns the byte offset at which each cluster starts.
func graphemeStarts(s string) []int {
	var starts []int
	prevZWJ := false
	riOpen := false // an unpaired regional indicator started the current cluster
	for i, r := range s {
		extend := false
		switch {
		case len(starts) == 0:
		case prevZWJ:
			extend = true
		case r == 0x200D,
			unicode.Is(unicode.Mn, r), unicode.Is(unicode.Me, r), unicode.Is(unicode.Mc, r),
			r >= 0xFE00 && r <= 0xFE0F,
			r >= 0x1F3FB && r <= 0x1F3FF,
			r >= 0xE0020 && r <= 0xE007F:
			extend = true
		case isRegionalIndicator(r) && riOpen:
			extend = true
			riOpen = false
		}
		if !extend {
			starts = append(starts, i)
			riOpen = isRegionalIndicator(r)
		}
		prevZWJ = r == 0x200D
	}
	return starts
}

func isRegionalIndicator(r rune) bool { return r >= 0x1F1E6 && r <= 0x1F1FF }

// --- recipient preview setting and sender name (cached reads) ---

// chatPreviewTTL bounds how long a show_message_preview answer is reused.
// Turning previews OFF therefore takes effect within a minute.
const chatPreviewTTL = 60 * time.Second

// chatSenderNameTTL bounds how long a sender's display name is reused.
const chatSenderNameTTL = 5 * time.Minute

const chatLookupTimeout = 2 * time.Second

// chatLookupCacheMax caps each cache; past it the cache is simply reset.
const chatLookupCacheMax = 20000

type cachedBool struct {
	v  bool
	at time.Time
}

type cachedString struct {
	v  string
	at time.Time
}

// chatLookups holds the two small caches the chat push path reads.
type chatLookups struct {
	mu      sync.Mutex
	preview map[uuid.UUID]cachedBool
	names   map[uuid.UUID]cachedString
}

// PreviewSettingReader answers a recipient's show_message_preview. ok=false
// means unknown — the caller then renders generic copy.
type PreviewSettingReader func(ctx context.Context, userID uuid.UUID) (on bool, ok bool)

// SenderNameReader answers a user's display name ("" when unknown).
type SenderNameReader func(ctx context.Context, userID uuid.UUID) string

// SetIdentityUserServiceURL configures identity user-service, the settings
// authority read for show_message_preview (GET /v1/users/:id/settings,
// internal-key gated — the same read message-service's policy path uses).
// Empty disables previews: every chat push stays generic.
func (s *Service) SetIdentityUserServiceURL(url string) {
	s.identityUserURL = strings.TrimRight(url, "/")
}

// SetChatPreviewReaders replaces the preview-setting and sender-name reads
// (tests). Either may be nil to keep the HTTP default.
func (s *Service) SetChatPreviewReaders(preview PreviewSettingReader, name SenderNameReader) {
	s.previewReader = preview
	s.nameReader = name
}

func (s *Service) chatCache() *chatLookups {
	s.chatOnce.Do(func() {
		s.chatLookups = &chatLookups{
			preview: make(map[uuid.UUID]cachedBool),
			names:   make(map[uuid.UUID]cachedString),
		}
	})
	return s.chatLookups
}

// chatPreviewKey is the shared (Redis) cache key for one recipient's
// show_message_preview. Shared so that one user.settings_changed delivery —
// which reaches a single replica of the consumer group — invalidates the
// answer for every replica.
func chatPreviewKey(userID uuid.UUID) string { return "notif:chat_preview:" + userID.String() }

// recipientShowsPreview is the cached show_message_preview of userID. Any
// failure answers false (generic copy) and is not cached.
func (s *Service) recipientShowsPreview(ctx context.Context, userID uuid.UUID) bool {
	if v, ok := s.cachedPreview(ctx, userID); ok {
		return v
	}
	reader := s.previewReader
	if reader == nil {
		reader = s.fetchShowMessagePreview
	}
	on, ok := reader(ctx, userID)
	if !ok {
		return false
	}
	s.storePreview(ctx, userID, on)
	return on
}

func (s *Service) cachedPreview(ctx context.Context, userID uuid.UUID) (bool, bool) {
	if s.rdb != nil {
		v, err := s.rdb.Get(ctx, chatPreviewKey(userID)).Result()
		if err == nil && (v == "1" || v == "0") {
			return v == "1", true
		}
		return false, false
	}
	c := s.chatCache()
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.preview[userID]; ok && time.Since(e.at) < chatPreviewTTL {
		return e.v, true
	}
	return false, false
}

func (s *Service) storePreview(ctx context.Context, userID uuid.UUID, on bool) {
	if s.rdb != nil {
		v := "0"
		if on {
			v = "1"
		}
		_ = s.rdb.Set(ctx, chatPreviewKey(userID), v, chatPreviewTTL).Err()
		return
	}
	c := s.chatCache()
	c.mu.Lock()
	if len(c.preview) >= chatLookupCacheMax {
		c.preview = make(map[uuid.UUID]cachedBool)
	}
	c.preview[userID] = cachedBool{v: on, at: time.Now()}
	c.mu.Unlock()
}

// InvalidateChatPreview drops the cached show_message_preview of userID —
// the identity consumer's entry point for user.settings_changed, so a user
// who turns previews off is honoured on the next push rather than after the
// TTL. (A lookup already in flight may still store the old answer; the TTL
// bounds that window to a minute.)
func (s *Service) InvalidateChatPreview(ctx context.Context, userID uuid.UUID) error {
	c := s.chatCache()
	c.mu.Lock()
	delete(c.preview, userID)
	c.mu.Unlock()
	if s.rdb != nil {
		return s.rdb.Del(ctx, chatPreviewKey(userID)).Err()
	}
	return nil
}

// senderDisplayName is the cached display name (falling back to username) of
// userID; "" when unknown, which keeps the push generic.
func (s *Service) senderDisplayName(ctx context.Context, userID uuid.UUID) string {
	c := s.chatCache()
	c.mu.Lock()
	if e, ok := c.names[userID]; ok && time.Since(e.at) < chatSenderNameTTL {
		c.mu.Unlock()
		return e.v
	}
	c.mu.Unlock()

	reader := s.nameReader
	if reader == nil {
		reader = s.fetchSenderName
	}
	name := reader(ctx, userID)
	if name == "" {
		return ""
	}
	c.mu.Lock()
	if len(c.names) >= chatLookupCacheMax {
		c.names = make(map[uuid.UUID]cachedString)
	}
	c.names[userID] = cachedString{v: name, at: time.Now()}
	c.mu.Unlock()
	return name
}

func (s *Service) fetchSenderName(ctx context.Context, userID uuid.UUID) string {
	if s.profileServiceURL == "" {
		return ""
	}
	profiles, err := s.fetchActorProfiles(ctx, []string{userID.String()})
	if err != nil {
		slog.Warn("chat push: sender name lookup failed", "error", err)
		return ""
	}
	p, ok := profiles[userID]
	if !ok {
		return ""
	}
	if n := strings.TrimSpace(p.DisplayName); n != "" {
		return n
	}
	return strings.TrimSpace(p.Username)
}

// fetchShowMessagePreview reads the recipient's settings from identity
// user-service. The authority always sends chat_availability; a 200 without
// it is not the authority and is treated as unknown (mirrors message-service
// policy.go). A missing show_message_preview is unknown too: fail closed.
func (s *Service) fetchShowMessagePreview(ctx context.Context, userID uuid.UUID) (bool, bool) {
	if s.identityUserURL == "" {
		return false, false
	}
	ctx, cancel := context.WithTimeout(ctx, chatLookupTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		fmt.Sprintf("%s/v1/users/%s/settings", s.identityUserURL, userID), nil)
	if err != nil {
		return false, false
	}
	if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	client := s.profileClient
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		slog.Warn("chat push: preview setting lookup failed", "error", err)
		return false, false
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, false
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if err != nil {
		return false, false
	}
	return parseShowMessagePreview(body)
}

func parseShowMessagePreview(body []byte) (bool, bool) {
	var envelope struct {
		Data struct {
			ChatAvailability   string `json:"chat_availability"`
			ShowMessagePreview *bool  `json:"show_message_preview"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return false, false
	}
	if envelope.Data.ChatAvailability == "" || envelope.Data.ShowMessagePreview == nil {
		return false, false
	}
	return *envelope.Data.ShowMessagePreview, true
}

// CreateChatMessageNotification is the "dm" delivery for one chat message
// with its content. The durable row and realtime are unchanged; only the
// device push copy depends on the recipient's preview setting, and the
// lookups run only when a push will actually be sent.
func (s *Service) CreateChatMessageNotification(ctx context.Context, userID, actorID, conversationID uuid.UUID, deepLink string, createdAt time.Time, msg ChatMessagePush, muted bool) error {
	if s.recipientSuppressed(ctx, userID, "dm") {
		return nil
	}
	decision := s.resolveGeneralDelivery(ctx, userID, "dm")
	if muted {
		decision.SendPush = false
		decision.DeferPush = false
	}
	var render RenderOverride
	if decision.SendPush {
		render = s.chatMessageRenderFor(ctx, userID, actorID, conversationID, msg)
	} else {
		render = DMRender(msg.MessageType)
	}
	return s.deliverWithDecision(ctx, decision, userID, actorID, "dm", "conversation", conversationID, deepLink, createdAt, "", render)
}

// chatMessageRenderFor resolves the recipient's setting and, only when it is
// on, the sender's name.
func (s *Service) chatMessageRenderFor(ctx context.Context, userID, senderID, conversationID uuid.UUID, msg ChatMessagePush) RenderOverride {
	previewOn := s.recipientShowsPreview(ctx, userID)
	name := ""
	if previewOn {
		name = s.senderDisplayName(ctx, senderID)
	}
	return ChatMessageRender(msg, conversationID, previewOn, name)
}
