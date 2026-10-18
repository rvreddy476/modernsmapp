package service

import (
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// Chat conversation reports (chat-parity fix pass 2): a member reports a
// whole conversation from its chat settings. message-service has already
// checked membership and calls the internal route
// POST /v1/internal/reports/conversations; the report goes through
// FileReportWith like every other report, so it lands in the same
// trust.reports queue under the same allowlists, the same one-active-report
// index and the same idempotency key space as message reports.
//
//   - a DM reports the other person: entity_type "user", entity_id = them;
//   - a group reports the group: entity_type "conversation",
//     entity_id = the conversation id.
//
// Either way the evidence names the conversation, its type and up to
// MaxEvidenceMessageIDs of the newest messages the reporter could still see.

// ConversationReport is the internal route's request, already decoded.
type ConversationReport struct {
	ReporterID       string
	EntityType       string
	EntityID         string
	ConversationID   string
	ConversationType string
	Reason           string
	Note             string
	MessageIDs       []string
	// SourceEventID is the namespaced idempotency key ("" for none).
	SourceEventID string
}

// conversationReportEntityTypes are the entity types a conversation report
// may file.
var conversationReportEntityTypes = map[string]bool{"user": true, ReportEntityConversation: true}

// ConversationReportInput validates a conversation report and maps it to a
// filing. Errors wrap ErrInvalidReport. The reason is left for
// FileReportWith to normalise (spam|harassment|hate|sexual|violence|scam|
// other map onto the spec §10.5 categories there).
func ConversationReportInput(r ConversationReport) (FileReportInput, error) {
	reporter, err := uuid.Parse(strings.TrimSpace(r.ReporterID))
	if err != nil || reporter == uuid.Nil {
		return FileReportInput{}, fmt.Errorf("%w: reporter_id must be a UUID", ErrInvalidReport)
	}
	entityType := strings.ToLower(strings.TrimSpace(r.EntityType))
	if !conversationReportEntityTypes[entityType] {
		return FileReportInput{}, fmt.Errorf("%w: entity_type must be user or conversation", ErrInvalidReport)
	}
	conv, err := uuid.Parse(strings.TrimSpace(r.ConversationID))
	if err != nil || conv == uuid.Nil {
		return FileReportInput{}, fmt.Errorf("%w: conversation_id must be a UUID", ErrInvalidReport)
	}
	entity, err := uuid.Parse(strings.TrimSpace(r.EntityID))
	if err != nil || entity == uuid.Nil {
		return FileReportInput{}, fmt.Errorf("%w: entity_id must be a UUID", ErrInvalidReport)
	}
	switch entityType {
	case ReportEntityConversation:
		if entity != conv {
			return FileReportInput{}, fmt.Errorf("%w: a conversation report's entity_id is its conversation_id", ErrInvalidReport)
		}
	case "user":
		if entity == reporter {
			return FileReportInput{}, fmt.Errorf("%w: a reporter cannot report themselves", ErrInvalidReport)
		}
	}
	if strings.TrimSpace(r.ConversationType) == "" {
		return FileReportInput{}, fmt.Errorf("%w: conversation_type is required", ErrInvalidReport)
	}
	if len(r.SourceEventID) > MaxSourceEventIDLength {
		return FileReportInput{}, fmt.Errorf("%w: idempotency key longer than %d characters", ErrInvalidReport, MaxSourceEventIDLength)
	}
	// encodeEvidence (in FileReportWith) bounds and canonicalises the type
	// and the message ids; checked here too so a bad body never reaches the
	// store.
	if _, err := canonicalMessageIDs(r.MessageIDs); err != nil {
		return FileReportInput{}, err
	}
	return FileReportInput{
		ReporterID: reporter,
		EntityType: entityType,
		EntityRef:  entity.String(),
		Reason:     r.Reason,
		Details:    r.Note,
		Evidence: &ReportEvidence{
			ConversationID:   conv.String(),
			ConversationType: r.ConversationType,
			MessageIDs:       r.MessageIDs,
			Source:           EvidenceSourceMessageService,
		},
		SourceEventID: r.SourceEventID,
	}, nil
}
