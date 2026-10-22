package service

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
)

// Chat message reports (chat-parity CONTRACT §2.6 / §8).
//
// A report on a chat message reaches the existing pipeline three ways, all
// through FileReportWith, so every one lands in the same trust.reports queue
// under the same allowlists and the same one-active-report index:
//
//   - the public POST /v1/reports with entity_type "message" (evidence, if
//     any, is the reporter's own claim: source "reporter");
//   - the internal POST /v1/internal/reports/messages message-service calls
//     on a user's behalf after it has checked membership (source
//     "message-service");
//   - the chat MessageRequestReported event (source
//     "chat.MessageRequestReported").

// Evidence sources, set by the server, never by the caller.
const (
	EvidenceSourceReporter       = "reporter"
	EvidenceSourceMessageService = "message-service"
	EvidenceSourceRequestEvent   = "chat.MessageRequestReported"
)

// MaxEvidenceTextRunes matches chat's message text ceiling.
const MaxEvidenceTextRunes = 2000

// MaxSourceEventIDLength bounds an idempotency key (migration 015 CHECK).
const MaxSourceEventIDLength = 200

// maxEvidenceMessageTypeLength bounds the free-text message type.
const maxEvidenceMessageTypeLength = 32

// FileReportInput is one report to file.
type FileReportInput struct {
	ReporterID uuid.UUID
	EntityType string
	EntityRef  string
	Reason     string
	Details    string
	// Evidence is optional; allowed for message and user reports only.
	Evidence *ReportEvidence
	// SourceEventID, when set, makes the filing idempotent: a second filing
	// with the same key returns postgres.ErrDuplicateSourceEvent.
	SourceEventID string
	// EnforceReporterQuota applies the per-reporter volume cap
	// (reporter_quota.go). The user-facing POST /v1/reports sets it; the
	// service-side filers (chat consumer, internal message and conversation
	// routes) do not, so a replayed event is never dropped by the cap.
	EnforceReporterQuota bool
}

// ReportEvidence is the snapshot stored with a report.
type ReportEvidence struct {
	Text           string `json:"text,omitempty"`
	MediaID        string `json:"media_id,omitempty"`
	SenderID       string `json:"sender_id,omitempty"`
	ConversationID string `json:"conversation_id,omitempty"`
	MessageType    string `json:"message_type,omitempty"`
	// ConversationType and MessageIDs describe a conversation report
	// (conversation_reports.go): the chat's type ("direct", "group") and up
	// to MaxEvidenceMessageIDs message ids, newest first.
	ConversationType string    `json:"conversation_type,omitempty"`
	MessageIDs       []string  `json:"message_ids,omitempty"`
	Source           string    `json:"source"`
	CapturedAt       time.Time `json:"captured_at"`
}

// evidenceEntityTypes may carry a snapshot: a message, a user reported from
// a message request (the request event names no message id) or from a DM's
// chat settings, and a group conversation.
var evidenceEntityTypes = map[string]bool{ReportEntityMessage: true, "user": true, ReportEntityConversation: true}

// MaxEvidenceMessageIDs bounds the message ids a conversation report
// carries (message-service sends the newest ≤20 the reporter can see).
const MaxEvidenceMessageIDs = 20

// maxEvidenceConversationTypeLength bounds the conversation type token.
const maxEvidenceConversationTypeLength = 32

// conversationTypePattern is a lower-case type token ("direct", "group").
var conversationTypePattern = regexp.MustCompile(`^[a-z_]+$`)

// encodeEvidence validates a snapshot and returns its stored JSON (nil when
// there is none). Errors wrap ErrInvalidReport.
func encodeEvidence(entityType string, ev *ReportEvidence) (json.RawMessage, error) {
	if ev == nil {
		return nil, nil
	}
	if !evidenceEntityTypes[entityType] {
		return nil, fmt.Errorf("%w: evidence is accepted for message reports only", ErrInvalidReport)
	}
	out := *ev
	if utf8.RuneCountInString(out.Text) > MaxEvidenceTextRunes {
		return nil, fmt.Errorf("%w: evidence text longer than %d characters", ErrInvalidReport, MaxEvidenceTextRunes)
	}
	if !utf8.ValidString(out.Text) {
		return nil, fmt.Errorf("%w: evidence text is not valid UTF-8", ErrInvalidReport)
	}
	for name, p := range map[string]*string{"media_id": &out.MediaID, "sender_id": &out.SenderID, "conversation_id": &out.ConversationID} {
		raw := strings.TrimSpace(*p)
		if raw == "" {
			*p = ""
			continue
		}
		id, err := uuid.Parse(raw)
		if err != nil || id == uuid.Nil {
			return nil, fmt.Errorf("%w: evidence %s must be a UUID", ErrInvalidReport, name)
		}
		*p = id.String()
	}
	out.MessageType = strings.ToLower(strings.TrimSpace(out.MessageType))
	if len(out.MessageType) > maxEvidenceMessageTypeLength {
		return nil, fmt.Errorf("%w: evidence message_type too long", ErrInvalidReport)
	}
	out.ConversationType = strings.ToLower(strings.TrimSpace(out.ConversationType))
	if out.ConversationType != "" && (len(out.ConversationType) > maxEvidenceConversationTypeLength ||
		!conversationTypePattern.MatchString(out.ConversationType)) {
		return nil, fmt.Errorf("%w: evidence conversation_type must be a short lower-case word", ErrInvalidReport)
	}
	ids, err := canonicalMessageIDs(ev.MessageIDs)
	if err != nil {
		return nil, err
	}
	out.MessageIDs = ids
	switch out.Source {
	case EvidenceSourceReporter, EvidenceSourceMessageService, EvidenceSourceRequestEvent:
	default:
		return nil, fmt.Errorf("%w: evidence source %q is not a server source", ErrInvalidReport, out.Source)
	}
	if out.CapturedAt.IsZero() {
		out.CapturedAt = time.Now().UTC()
	}
	if out.Text == "" && out.MediaID == "" && out.SenderID == "" && out.ConversationID == "" && out.MessageType == "" &&
		out.ConversationType == "" && len(out.MessageIDs) == 0 {
		return nil, nil // nothing captured
	}
	b, err := json.Marshal(out)
	if err != nil {
		return nil, err
	}
	return b, nil
}

// canonicalMessageIDs validates evidence message ids: at most
// MaxEvidenceMessageIDs, each a non-nil UUID, returned canonical with
// repeats dropped and the caller's order kept. Errors wrap ErrInvalidReport.
func canonicalMessageIDs(raw []string) ([]string, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	if len(raw) > MaxEvidenceMessageIDs {
		return nil, fmt.Errorf("%w: evidence carries at most %d message_ids", ErrInvalidReport, MaxEvidenceMessageIDs)
	}
	out := make([]string, 0, len(raw))
	seen := make(map[uuid.UUID]bool, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil || id == uuid.Nil {
			return nil, fmt.Errorf("%w: evidence message_ids must be UUIDs", ErrInvalidReport)
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id.String())
	}
	return out, nil
}

// MessageRequestReported mirrors chat-service's MessageRequestPayload as
// published for a reported request (shared/events/events.go), plus the
// optional message_id and reason a newer publisher may add.
type MessageRequestReported struct {
	ConversationID string    `json:"conversation_id"`
	SenderID       string    `json:"sender_id"`
	ReceiverID     string    `json:"receiver_id"`
	Preview        string    `json:"preview,omitempty"`
	OccurredAt     time.Time `json:"occurred_at"`
	MessageID      string    `json:"message_id,omitempty"`
	Reason         string    `json:"reason,omitempty"`
}

// MessageRequestReportSourceKey is the idempotency key of a reported
// request. It is the CONVERSATION, not the event id: chat's outbox relay
// mints a fresh event_id on every publish attempt, so a retried publish
// would slip past an event-id key, and a request is reported at most once
// (chat flips it to "reported" idempotently).
func MessageRequestReportSourceKey(conversationID uuid.UUID) string {
	return "chat:MessageRequestReported:" + conversationID.String()
}

// MessageRequestReportInput maps a MessageRequestReported event to a report:
// the receiver reports the request's first message (entity "message") when
// the event names it, else the sender (entity "user"), with the request
// preview as evidence either way. ok=false: the event is malformed and is
// skipped.
func MessageRequestReportInput(e MessageRequestReported) (FileReportInput, bool) {
	conv, err1 := uuid.Parse(strings.TrimSpace(e.ConversationID))
	sender, err2 := uuid.Parse(strings.TrimSpace(e.SenderID))
	receiver, err3 := uuid.Parse(strings.TrimSpace(e.ReceiverID))
	if err1 != nil || err2 != nil || err3 != nil || conv == uuid.Nil || sender == uuid.Nil || receiver == uuid.Nil || sender == receiver {
		return FileReportInput{}, false
	}
	text := e.Preview
	if utf8.RuneCountInString(text) > MaxEvidenceTextRunes {
		text = string([]rune(text)[:MaxEvidenceTextRunes])
	}
	captured := e.OccurredAt
	if captured.IsZero() {
		captured = time.Now().UTC()
	}
	in := FileReportInput{
		ReporterID: receiver,
		EntityType: "user",
		EntityRef:  sender.String(),
		Reason:     "other",
		Details:    "Reported from message requests",
		Evidence: &ReportEvidence{
			Text: text, SenderID: sender.String(), ConversationID: conv.String(),
			Source: EvidenceSourceRequestEvent, CapturedAt: captured.UTC(),
		},
		SourceEventID: MessageRequestReportSourceKey(conv),
	}
	if msg, err := uuid.Parse(strings.TrimSpace(e.MessageID)); err == nil && msg != uuid.Nil {
		in.EntityType = ReportEntityMessage
		in.EntityRef = MessageEntityKey(conv, msg)
	}
	if _, reason := NormalizeReportInput("", e.Reason); validReportCategories[reason] {
		in.Reason = reason
	}
	return in, true
}
