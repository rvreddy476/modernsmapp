// Dating safety paging (Dating plan lane D8).
//
// dating.safety.panic carries {incident_id, user_id, created_at,
// has_location} and never coordinates. For each incident this consumer:
//
//  1. claims the incident once (a redelivered event pages nobody again);
//  2. reads dating-service's service-only notify context (first name,
//     trusted contacts, and a point ONLY for contacts the user opted in);
//  3. pages the configured, staff-verified responders (no coordinates);
//  4. notifies each trusted contact "<first name> triggered a safety alert",
//     with the point in the deep link only when opted in;
//  5. writes an ops alert row and logs at ERROR, critically when no
//     responder could be paged.
//
// dating.safety.location_shared notifies the recipient of a live share.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Notification types for dating safety.
const (
	NotifDatingPanicResponder      = "dating.safety.panic.responder"
	NotifDatingPanicTrustedContact = "dating.safety.panic.trusted_contact"
	NotifDatingLocationShared      = "dating.safety.location_shared"
)

// Ops alert kinds.
const (
	OpsAlertDatingPanicPaged          = "dating_panic_paged"
	OpsAlertDatingPanicNoResponder    = "dating_panic_no_responder"
	OpsAlertDatingPanicSuspectedAbuse = "dating_panic_suspected_abuse"
)

// opsEmailTemplate is the plain operator email for a dating panic. Subject
// and Body carry the incident id and counts only.
const opsEmailTemplate = `<html><head><title>{{.Subject}}</title></head><body><p>{{.Body}}</p></body></html>`

// datingSafetyDedupNamespace scopes the event-dedup ids of dating safety
// events (UUIDv5 of the event type and entity id).
var datingSafetyDedupNamespace = uuid.MustParse("6f1c2d8e-4b7a-4d3e-9c10-2a8b7e6d5f40")

type datingPanicPayload struct {
	IncidentID  string    `json:"incident_id"`
	UserID      string    `json:"user_id"`
	CreatedAt   time.Time `json:"created_at"`
	HasLocation bool      `json:"has_location"`
}

type datingPanicContact struct {
	UserID    string   `json:"user_id"`
	Latitude  *float64 `json:"latitude,omitempty"`
	Longitude *float64 `json:"longitude,omitempty"`
}

// datingPanicContext mirrors dating-service's service.PanicNotifyContext.
type datingPanicContext struct {
	IncidentID      string               `json:"incident_id"`
	UserID          string               `json:"user_id"`
	FirstName       string               `json:"first_name"`
	Status          string               `json:"status"`
	SuspectedAbuse  bool                 `json:"suspected_abuse"`
	HasLocation     bool                 `json:"has_location"`
	TrustedContacts []datingPanicContact `json:"trusted_contacts"`
}

type datingLocationSharedPayload struct {
	UserID    string    `json:"user_id"`
	ContactID string    `json:"contact_id"`
	ShareID   string    `json:"share_id"`
	ExpiresAt time.Time `json:"expires_at"`
	StartedAt time.Time `json:"started_at"`
}

// datingSafetyDeps is everything paging needs; DatingSafetyAdapter is the
// production implementation and tests pass a fake.
type datingSafetyDeps interface {
	ClaimEventDedup(ctx context.Context, id uuid.UUID) (bool, error)
	PanicContext(ctx context.Context, incidentID uuid.UUID) (*datingPanicContext, error)
	FirstName(ctx context.Context, userID uuid.UUID) string
	Responders(ctx context.Context) []uuid.UUID
	SendSafetyAlert(ctx context.Context, a service.SafetyAlert) error
	RecordOpsAlert(ctx context.Context, a postgres.OpsAlert) error
	EmailOps(ctx context.Context, subject, body string) error
}

// WithDatingSafety wires dating panic paging. Without it a panic is logged
// at ERROR and nobody is paged.
func (c *Consumer) WithDatingSafety(d datingSafetyDeps) *Consumer {
	c.datingSafety = d
	return c
}

func (c *Consumer) handleDatingSafetyPanic(ctx context.Context, raw json.RawMessage) error {
	if c.datingSafety == nil {
		slog.Error("dating.safety.panic received but dating safety paging is not wired: NOBODY IS PAGED")
		return nil
	}
	return processDatingPanic(ctx, c.datingSafety, raw)
}

func (c *Consumer) handleDatingSafetyLocationShared(ctx context.Context, raw json.RawMessage) error {
	if c.datingSafety == nil {
		slog.Error("dating.safety.location_shared received but dating safety delivery is not wired")
		return nil
	}
	return processDatingLocationShared(ctx, c.datingSafety, raw)
}

func datingSafetyDedupID(kind string, id uuid.UUID) uuid.UUID {
	return uuid.NewSHA1(datingSafetyDedupNamespace, []byte(kind+":"+id.String()))
}

// processDatingPanic pages for one incident. It returns an error only for an
// undecodable event; delivery failures are logged and recorded as ops alerts
// so a partial failure never blocks the partition.
func processDatingPanic(ctx context.Context, deps datingSafetyDeps, raw json.RawMessage) error {
	var e datingPanicPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	incident, err := uuid.Parse(e.IncidentID)
	if err != nil {
		return fmt.Errorf("dating.safety.panic: invalid incident_id %q", e.IncidentID)
	}
	user, err := uuid.Parse(e.UserID)
	if err != nil {
		return fmt.Errorf("dating.safety.panic: invalid user_id %q", e.UserID)
	}
	createdAt := e.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}

	first, derr := deps.ClaimEventDedup(ctx, datingSafetyDedupID("dating.safety.panic", incident))
	switch {
	case derr != nil:
		// A duplicate page is better than a missed one; the per-recipient
		// identities still stop double delivery.
		slog.Error("dating panic dedup claim failed; paging anyway", "incident_id", incident, "error", derr)
	case !first:
		slog.Info("dating panic already paged; duplicate event ignored", "incident_id", incident)
		return nil
	}

	pctx, cerr := deps.PanicContext(ctx, incident)
	if cerr != nil {
		slog.Error("dating panic context unavailable; paging responders without trusted contacts",
			"incident_id", incident, "error", cerr)
		pctx = nil
	}
	if pctx != nil && pctx.SuspectedAbuse {
		recordOps(ctx, deps, postgres.OpsAlert{
			Source: "dating-service", Kind: OpsAlertDatingPanicSuspectedAbuse, Severity: "warning",
			SubjectID: incident, DedupeKey: "dating_panic:" + incident.String() + ":" + OpsAlertDatingPanicSuspectedAbuse,
			Detail: map[string]any{"responders_paged": 0, "trusted_contacts_notified": 0},
		})
		slog.Warn("dating panic flagged as suspected abuse: not paged", "incident_id", incident)
		return nil
	}
	if pctx != nil && pctx.Status == "resolved" {
		slog.Info("dating panic already resolved before paging; not paged", "incident_id", incident)
		return nil
	}

	paged := 0
	for _, r := range deps.Responders(ctx) {
		if r == user {
			continue
		}
		err := deps.SendSafetyAlert(ctx, service.SafetyAlert{
			Recipient:  r,
			Actor:      user,
			NotifType:  NotifDatingPanicResponder,
			EntityType: "dating_panic",
			EntityID:   incident,
			DeepLink:   "/admin/dating/safety/panic/" + incident.String(),
			Title:      "Dating safety alert",
			Body:       "A member triggered a panic alert. Open the incident now.",
			CreatedAt:  createdAt,
			Identity:   "dating_panic:" + incident.String() + ":responder:" + r.String(),
		})
		if err != nil {
			slog.Error("dating panic responder page failed", "incident_id", incident, "responder_id", r, "error", err)
			continue
		}
		paged++
	}

	notified := 0
	if pctx != nil {
		name := strings.TrimSpace(pctx.FirstName)
		title := name + " triggered a safety alert"
		if name == "" {
			title = "Someone who trusts you triggered a safety alert"
		}
		for _, tc := range pctx.TrustedContacts {
			contact, err := uuid.Parse(tc.UserID)
			if err != nil || contact == user {
				continue
			}
			deepLink := "/dating/safety/alerts/" + incident.String()
			body := "Check on them now."
			if tc.Latitude != nil && tc.Longitude != nil {
				// Only a contact the user opted in to share the point with.
				deepLink += fmt.Sprintf("?lat=%.6f&lng=%.6f", *tc.Latitude, *tc.Longitude)
				body = "Check on them now. Tap to see where they were."
			}
			if err := deps.SendSafetyAlert(ctx, service.SafetyAlert{
				Recipient:  contact,
				Actor:      user,
				NotifType:  NotifDatingPanicTrustedContact,
				EntityType: "dating_panic",
				EntityID:   incident,
				DeepLink:   deepLink,
				Title:      title,
				Body:       body,
				CreatedAt:  createdAt,
				Identity:   "dating_panic:" + incident.String() + ":contact:" + contact.String(),
			}); err != nil {
				slog.Error("dating panic trusted contact notify failed", "incident_id", incident, "error", err)
				continue
			}
			notified++
		}
	}

	kind, severity := OpsAlertDatingPanicPaged, "critical"
	if paged == 0 {
		kind = OpsAlertDatingPanicNoResponder
		slog.Error("DATING PANIC REACHED NO RESPONDER: set DATING_SAFETY_RESPONDER_USER_IDS to staff user ids",
			"incident_id", incident, "trusted_contacts_notified", notified)
	} else {
		slog.Error("dating panic paged", "incident_id", incident,
			"responders_paged", paged, "trusted_contacts_notified", notified)
	}
	recordOps(ctx, deps, postgres.OpsAlert{
		Source: "dating-service", Kind: kind, Severity: severity,
		SubjectID: incident, DedupeKey: "dating_panic:" + incident.String() + ":" + kind,
		Detail: map[string]any{
			"responders_paged":          paged,
			"trusted_contacts_notified": notified,
			"has_location":              e.HasLocation,
			"context_available":         pctx != nil,
		},
	})
	subject := "Dating safety alert: panic incident " + incident.String()
	if paged == 0 {
		subject = "UNPAGED dating safety alert: panic incident " + incident.String()
	}
	if err := deps.EmailOps(ctx, subject, fmt.Sprintf(
		"Panic incident %s. Responders paged: %d. Trusted contacts notified: %d. Open /admin/dating/safety/panic/%s.",
		incident, paged, notified, incident)); err != nil {
		slog.Error("dating panic ops email failed", "incident_id", incident, "error", err)
	}
	return nil
}

func recordOps(ctx context.Context, deps datingSafetyDeps, a postgres.OpsAlert) {
	if err := deps.RecordOpsAlert(ctx, a); err != nil {
		slog.Error("ops alert not recorded", "kind", a.Kind, "subject_id", a.SubjectID, "error", err)
	}
}

// processDatingLocationShared tells the recipient a live share started. The
// event and the notification carry no coordinates; the recipient reads the
// point from dating-service while the share is live.
func processDatingLocationShared(ctx context.Context, deps datingSafetyDeps, raw json.RawMessage) error {
	var e datingLocationSharedPayload
	if err := unmarshalPayload(raw, &e); err != nil {
		return err
	}
	share, err := uuid.Parse(e.ShareID)
	if err != nil {
		return fmt.Errorf("dating.safety.location_shared: invalid share_id %q", e.ShareID)
	}
	recipient, err := uuid.Parse(e.ContactID)
	if err != nil {
		return fmt.Errorf("dating.safety.location_shared: invalid contact_id %q", e.ContactID)
	}
	sharer, _ := uuid.Parse(e.UserID)
	if first, derr := deps.ClaimEventDedup(ctx, datingSafetyDedupID("dating.safety.location_shared", share)); derr == nil && !first {
		return nil
	}
	name := strings.TrimSpace(deps.FirstName(ctx, sharer))
	title := name + " is sharing their live location with you"
	if name == "" {
		title = "Someone is sharing their live location with you"
	}
	startedAt := e.StartedAt
	if startedAt.IsZero() {
		startedAt = time.Now().UTC()
	}
	return deps.SendSafetyAlert(ctx, service.SafetyAlert{
		Recipient:  recipient,
		Actor:      sharer,
		NotifType:  NotifDatingLocationShared,
		EntityType: "dating_location_share",
		EntityID:   share,
		DeepLink:   "/dating/safety/shared-locations/" + share.String(),
		Title:      title,
		Body:       "Tap to see where they are until the share ends.",
		CreatedAt:  startedAt,
		Identity:   "dating_location_share:" + share.String() + ":" + recipient.String(),
	})
}

// datingPanicContextPathFmt is dating-service's service-only notify context.
const datingPanicContextPathFmt = "/v1/dating/internal/safety/panic/%s/notify-context"

// getPanicContext reads the notify context with the service credential only.
func (c *datingClient) getPanicContext(ctx context.Context, incidentID uuid.UUID) (*datingPanicContext, error) {
	if c == nil {
		return nil, fmt.Errorf("dating client not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+fmt.Sprintf(datingPanicContextPathFmt, incidentID), nil)
	if err != nil {
		return nil, err
	}
	// Service credential only — never an end-user identity header.
	if c.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", c.internalKey)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("dating-service unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("dating-service notify context status %d", resp.StatusCode)
	}
	var env struct {
		Data *datingPanicContext `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, fmt.Errorf("decode notify context: %w", err)
	}
	if env.Data == nil {
		return nil, fmt.Errorf("dating-service notify context: empty response")
	}
	return env.Data, nil
}

// DatingSafetyAdapter is the production datingSafetyDeps.
type DatingSafetyAdapter struct {
	svc        *service.Service
	dating     *datingClient
	responders *ResponderDirectory
	opsEmail   string
}

// NewDatingSafetyAdapter builds the adapter. responders may be nil (nobody
// is paged; every panic raises a critical ops alert). opsEmail may be empty.
func NewDatingSafetyAdapter(svc *service.Service, responders *ResponderDirectory, opsEmail string) *DatingSafetyAdapter {
	return &DatingSafetyAdapter{svc: svc, dating: newDatingClient(), responders: responders, opsEmail: strings.TrimSpace(opsEmail)}
}

func (a *DatingSafetyAdapter) ClaimEventDedup(ctx context.Context, id uuid.UUID) (bool, error) {
	return a.svc.ClaimEventDedup(ctx, id)
}

func (a *DatingSafetyAdapter) PanicContext(ctx context.Context, incidentID uuid.UUID) (*datingPanicContext, error) {
	return a.dating.getPanicContext(ctx, incidentID)
}

func (a *DatingSafetyAdapter) FirstName(ctx context.Context, userID uuid.UUID) string {
	if userID == uuid.Nil {
		return ""
	}
	return a.dating.getFirstName(ctx, userID.String())
}

func (a *DatingSafetyAdapter) Responders(ctx context.Context) []uuid.UUID {
	if a.responders == nil {
		return nil
	}
	return a.responders.Responders(ctx)
}

func (a *DatingSafetyAdapter) SendSafetyAlert(ctx context.Context, alert service.SafetyAlert) error {
	return a.svc.CreateSafetyAlertNotification(ctx, alert)
}

func (a *DatingSafetyAdapter) RecordOpsAlert(ctx context.Context, alert postgres.OpsAlert) error {
	return a.svc.RecordOpsAlert(ctx, alert)
}

func (a *DatingSafetyAdapter) EmailOps(ctx context.Context, subject, body string) error {
	if a.opsEmail == "" {
		return nil
	}
	return a.svc.SendEmail(ctx, a.opsEmail, opsEmailTemplate, map[string]string{"Subject": subject, "Body": body})
}
