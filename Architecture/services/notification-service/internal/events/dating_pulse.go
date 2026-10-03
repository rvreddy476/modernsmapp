// Pulse mechanics notices (2026-10-02): scam alerts (M17) and date
// check-ins (M14). The copy, the safety class and the delivery rules live in
// service/dating_pulse.go; this file decodes and validates the payloads.
//
// A payload without a valid recipient_id or match_id is a poison message:
// the handler returns an error, which the worker logs and skips, the same
// as every other malformed dating payload. First names are never logged.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/google/uuid"
)

// datingPulseNotifier is the service seam; *service.Service satisfies it.
type datingPulseNotifier interface {
	CreateSafetyAlertNotification(ctx context.Context, a service.SafetyAlert) error
	CreateDatingCheckinNotification(ctx context.Context, n service.DatingCheckinNotification) error
}

// dating.safety.scam_alert, one per recipient.
type datingScamAlertPayload struct {
	RecipientID      string    `json:"recipient_id"`
	MatchID          string    `json:"match_id"`
	RemovedFirstName string    `json:"removed_first_name"`
	IssuedAt         time.Time `json:"issued_at"`
}

// dating.date_checkin.due, one per person.
type datingDateCheckinPayload struct {
	RecipientID   string    `json:"recipient_id"`
	MatchID       string    `json:"match_id"`
	MeetID        string    `json:"meet_id"`
	WithFirstName string    `json:"with_first_name"`
	DueAt         time.Time `json:"due_at"`
}

// parseRecipientAndMatch validates the two ids every Pulse notice needs. A
// nil UUID is as unusable as an unparseable one.
func parseRecipientAndMatch(event, recipientID, matchID string) (uuid.UUID, uuid.UUID, error) {
	recipient, err := uuid.Parse(recipientID)
	if err != nil || recipient == uuid.Nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%s: invalid recipient_id %q", event, recipientID)
	}
	match, err := uuid.Parse(matchID)
	if err != nil || match == uuid.Nil {
		return uuid.Nil, uuid.Nil, fmt.Errorf("%s: invalid match_id %q", event, matchID)
	}
	return recipient, match, nil
}

// datingScamAlert maps a scam-alert payload to its safety alert. The removed
// person is never the actor: the alert names them in the copy only.
func datingScamAlert(raw json.RawMessage) (service.SafetyAlert, error) {
	var e datingScamAlertPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return service.SafetyAlert{}, fmt.Errorf("dating.safety.scam_alert: undecodable payload: %w", err)
	}
	recipient, match, err := parseRecipientAndMatch("dating.safety.scam_alert", e.RecipientID, e.MatchID)
	if err != nil {
		return service.SafetyAlert{}, err
	}
	title, body := service.DatingScamAlertCopy(service.CleanDatingFirstName(e.RemovedFirstName))
	issuedAt := e.IssuedAt
	if issuedAt.IsZero() {
		issuedAt = time.Now().UTC()
	}
	return service.SafetyAlert{
		Recipient:  recipient,
		Actor:      uuid.Nil,
		NotifType:  service.DatingScamAlertType,
		EntityType: service.DatingMatchEntityType,
		EntityID:   match,
		DeepLink:   service.DatingScamAlertDeepLink,
		Title:      title,
		Body:       body,
		CreatedAt:  issuedAt,
		// One alert per match and recipient however often Kafka redelivers.
		Identity: service.DatingScamAlertType + ":" + match.String() + ":" + recipient.String(),
	}, nil
}

// datingDateCheckin maps a check-in payload to its notice.
func datingDateCheckin(raw json.RawMessage) (service.DatingCheckinNotification, error) {
	var e datingDateCheckinPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return service.DatingCheckinNotification{}, fmt.Errorf("dating.date_checkin.due: undecodable payload: %w", err)
	}
	recipient, match, err := parseRecipientAndMatch("dating.date_checkin.due", e.RecipientID, e.MatchID)
	if err != nil {
		return service.DatingCheckinNotification{}, err
	}
	// Identity per date: the meet when there is one, else the due time, so a
	// second date with the same match is asked about too.
	occasion := e.DueAt.UTC().Format(time.RFC3339)
	if meet, err := uuid.Parse(e.MeetID); err == nil && meet != uuid.Nil {
		occasion = meet.String()
	}
	createdAt := e.DueAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return service.DatingCheckinNotification{
		RecipientID: recipient,
		MatchID:     match,
		FirstName:   service.CleanDatingFirstName(e.WithFirstName),
		DeepLink:    "/dating/matches/" + match.String() + "?checkin=1",
		CreatedAt:   createdAt,
		Identity:    service.DatingDateCheckinType + ":" + match.String() + ":" + occasion + ":" + recipient.String(),
	}, nil
}

func (c *Consumer) handleDatingScamAlert(ctx context.Context, raw json.RawMessage) error {
	alert, err := datingScamAlert(raw)
	if err != nil {
		return err
	}
	if c.datingNotify == nil {
		slog.Error("dating.safety.scam_alert received but delivery is not wired: recipient NOT warned",
			"recipient_id", alert.Recipient, "match_id", alert.EntityID)
		return nil
	}
	return c.datingNotify.CreateSafetyAlertNotification(ctx, alert)
}

func (c *Consumer) handleDatingDateCheckinDue(ctx context.Context, raw json.RawMessage) error {
	n, err := datingDateCheckin(raw)
	if err != nil {
		return err
	}
	if c.datingNotify == nil {
		slog.Error("dating.date_checkin.due received but delivery is not wired",
			"recipient_id", n.RecipientID, "match_id", n.MatchID)
		return nil
	}
	return c.datingNotify.CreateDatingCheckinNotification(ctx, n)
}
