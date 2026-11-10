package service

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/atpost/trust-safety-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Reportable entities (TikTok parity T-13 / T-03).
//
// user, post, comment and sound are addressed by a UUID (a sound is a
// media-service audio id: GET /v1/audio/:audioId parses a UUID). A hashtag
// is addressed by its text, so it is normalised exactly the way
// post-service indexes tags (service.NormalizeExplicitHashtags: drop one
// leading '#', letters / combining marks / digits / underscore in any
// script, 1..MaxHashtagLength characters, lower case) and stored as
// entity_key, with entity_id a UUIDv5 of that key (migration 014). The
// same tag always maps to the same entity_id, so the one-active-report
// index and every entity_id read treat tags like any other entity.

// ErrInvalidReport marks a report the reporter can fix: an unknown entity
// type, a reason outside the allowlist, or an id that is not valid for its
// type. The handler answers it with 400, never 500.
var ErrInvalidReport = errors.New("invalid report")

// MaxReportHashtagLength mirrors post-service MaxHashtagLength (the
// per-tag ceiling after the leading '#' is dropped).
const MaxReportHashtagLength = 50

// reportHashtagPattern is post-service's explicitHashtagPattern.
var reportHashtagPattern = regexp.MustCompile(`^[\p{L}\p{M}\p{N}_]+$`)

// reportEntityKeyNamespace seeds the UUIDv5 of a key-addressed entity.
// NEVER change it: every stored hashtag report's entity_id derives from it,
// and a new value would split one tag's reports across two ids (and let a
// reporter file a second active report on the same tag).
var reportEntityKeyNamespace = uuid.MustParse("6f1d3c2e-8a4b-5c7d-9e0f-a1b2c3d4e5f6")

// keyAddressedEntityTypes are the types whose id is text, not a UUID.
var keyAddressedEntityTypes = map[string]bool{
	"hashtag": true,
}

// ReportEntityMessage is a chat message (chat-parity CONTRACT §2.6). Its wire
// entity_id is "<conversation_id>:<message_id>"; the message id is stored as
// entity_id (so the one-active-report index applies per message) and the
// canonical pair as entity_key (migration 015).
const ReportEntityMessage = "message"

// ReportEntityConversation is a whole chat conversation (a group), addressed
// by the conversation UUID like any UUID entity, so entity_key stays NULL
// (migration 016). A DM is reported as the other person ("user") instead.
const ReportEntityConversation = "conversation"

// ParseMessageEntityRef splits "<conversation_id>:<message_id>". Both parts
// must be non-nil UUIDs.
func ParseMessageEntityRef(ref string) (conversationID, messageID uuid.UUID, ok bool) {
	parts := strings.Split(strings.TrimSpace(ref), ":")
	if len(parts) != 2 {
		return uuid.Nil, uuid.Nil, false
	}
	conv, ok1 := ParseReportUUID(parts[0])
	msg, ok2 := ParseReportUUID(parts[1])
	if !ok1 || !ok2 {
		return uuid.Nil, uuid.Nil, false
	}
	return conv, msg, true
}

// ParseReportUUID accepts a report target id in the one canonical form,
// 8-4-4-4-12 hex with hyphens (either case, surrounding space ignored), and
// refuses the nil UUID.
//
// uuid.Parse alone also takes "urn:uuid:<id>", "{<id>}" and 32 bare hex
// digits. Those are not ids any service hands out, so a report naming one
// is junk (security walk, 10 Nov 2026) and is refused with 400 like any
// other malformed id. All four forms are exact lengths, so checking for the
// 36-character form is enough.
func ParseReportUUID(raw string) (uuid.UUID, bool) {
	s := strings.TrimSpace(raw)
	if len(s) != 36 {
		return uuid.Nil, false
	}
	id, err := uuid.Parse(s)
	if err != nil || id == uuid.Nil {
		return uuid.Nil, false
	}
	return id, true
}

// MessageEntityKey is the canonical stored key of a message report.
func MessageEntityKey(conversationID, messageID uuid.UUID) string {
	return conversationID.String() + ":" + messageID.String()
}

// NormalizeReportHashtag canonicalises a reported tag. ok is false for
// anything post-service would never have indexed as a tag.
func NormalizeReportHashtag(raw string) (string, bool) {
	tag := strings.TrimPrefix(strings.TrimSpace(raw), "#")
	if tag == "" || len([]rune(tag)) > MaxReportHashtagLength || !reportHashtagPattern.MatchString(tag) {
		return "", false
	}
	return strings.ToLower(tag), true
}

// ReportEntityID is the entity_id stored for a key-addressed entity.
func ReportEntityID(entityType, key string) uuid.UUID {
	return uuid.NewSHA1(reportEntityKeyNamespace, []byte(entityType+"\x00"+key))
}

// ResolveReportEntity turns a (canonical) entity type and the wire
// entity_id into what is stored: the UUID entity_id and, for key-addressed
// types, the normalised key. Errors wrap ErrInvalidReport.
func ResolveReportEntity(entityType, ref string) (uuid.UUID, string, error) {
	if !validEntityTypes[entityType] {
		return uuid.Nil, "", fmt.Errorf("%w: invalid entity_type: %s (must be user, post, comment, hashtag, sound, message, or conversation)", ErrInvalidReport, entityType)
	}
	if entityType == ReportEntityMessage {
		conv, msg, ok := ParseMessageEntityRef(ref)
		if !ok {
			return uuid.Nil, "", fmt.Errorf("%w: invalid entity ID: a message is \"<conversation_id>:<message_id>\"", ErrInvalidReport)
		}
		return msg, MessageEntityKey(conv, msg), nil
	}
	if keyAddressedEntityTypes[entityType] {
		key, ok := NormalizeReportHashtag(ref)
		if !ok {
			return uuid.Nil, "", fmt.Errorf("%w: invalid hashtag: must be 1-%d letters, digits or underscores", ErrInvalidReport, MaxReportHashtagLength)
		}
		return ReportEntityID(entityType, key), key, nil
	}
	id, ok := ParseReportUUID(ref)
	if !ok {
		return uuid.Nil, "", fmt.Errorf("%w: invalid entity ID", ErrInvalidReport)
	}
	return id, "", nil
}

// Reports never consult the target (security walk, 10 Nov 2026).
//
// Filing a report does not look up whether the reported post, user,
// comment, sound or message exists, or whether the reporter may see it.
// Every well-formed report is recorded and answered the same way: 200, the
// reporter's own copy of the report, status "open". That is deliberate. Any
// filing-time existence check leaks through at least one channel: the
// status (200 vs 404), the body, the latency of the lookup, the duplicate
// rule (a dropped report never triggers 409 ACTIVE_REPORT_EXISTS on a
// repeat) or the reporter's own list (GET /v1/reports?mine=true). A
// private post must answer exactly like a missing one and like a public
// one, and only "never look" achieves that for all five.
//
// A report against a target that does not exist is therefore recorded like
// any other and resolved at triage, where the moderator opening it finds
// nothing and dismisses it; the reporter sees the same "dismissed" outcome
// a report on harmless content gets. Malformed ids never get that far: they
// are 400 before anything is read or written (ParseReportUUID).

// reportWriter stores a filed report (*postgres.ReportStore in production;
// a fake in tests).
type reportWriter interface {
	CreateReport(ctx context.Context, report *postgres.Report) error
}

// WithReportWriter overrides where filed reports are written (tests).
func (s *Service) WithReportWriter(w reportWriter) *Service {
	s.reportWriter = w
	return s
}

func (s *Service) writeReport(ctx context.Context, report *postgres.Report) error {
	if s.reportWriter != nil {
		return s.reportWriter.CreateReport(ctx, report)
	}
	return s.store.CreateReport(ctx, report)
}
