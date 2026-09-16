// Safety service — spec §15 and Dating plan lane D8. Wraps the safety store
// with explicit error paths, Kafka emission, and the "persist before
// respond" contract for panic + report (rule #6: no silent failures on
// safety-adjacent code).
package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// Lane D8 refusals. The HTTP layer maps each to a stable code.
var (
	// ErrTrustedContactNotEligible: the contact is neither an accepted
	// connection nor a current match.
	ErrTrustedContactNotEligible = errors.New("a trusted contact must be an accepted connection or a current match")
	// ErrConnectionCheckUnavailable: graph-service could not confirm a
	// connection, so the add is refused rather than trusted.
	ErrConnectionCheckUnavailable = errors.New("connection check unavailable")
	// ErrShareRecipientNotAllowed: live location goes only to a trusted
	// contact or a current match.
	ErrShareRecipientNotAllowed = errors.New("location can only be shared with a trusted contact or a current match")
	// ErrMeetRequiresMatch: a meet needs a current match.
	ErrMeetRequiresMatch = errors.New("a meet can only be scheduled with a current match")
	// ErrReportTargetMismatch: an admin action named a user other than the
	// report's target.
	ErrReportTargetMismatch = errors.New("the action's target is not the report's target")
	// ErrInvalidReportReason: the reason is not one of store.ReportReasons.
	ErrInvalidReportReason = errors.New("invalid: report reason must be one of " + strings.Join(store.ReportReasons, ", "))
)

// PanicRequest is the input shape for RecordPanic.
type PanicRequest struct {
	Latitude  *float64       `json:"latitude,omitempty"`
	Longitude *float64       `json:"longitude,omitempty"`
	Context   map[string]any `json:"context,omitempty"`
}

// PanicResult is what the user sees: the incident, never whether it paged.
type PanicResult struct {
	IncidentID   uuid.UUID `json:"incident_id"`
	Status       string    `json:"status"`
	Deduplicated bool      `json:"deduplicated"`
}

// LocationShareRequest is the input shape for ShareLocation.
type LocationShareRequest struct {
	RecipientID     uuid.UUID `json:"recipient_id"`
	DurationMinutes int       `json:"duration_minutes"`
	Latitude        *float64  `json:"latitude,omitempty"`
	Longitude       *float64  `json:"longitude,omitempty"`
}

// LocationShareResult is what the handler echoes back to the sharer.
type LocationShareResult struct {
	ShareID       uuid.UUID `json:"share_id"`
	RecipientID   uuid.UUID `json:"recipient_id"`
	RecipientKind string    `json:"recipient_kind"`
	ExpiresAt     time.Time `json:"expires_at"`
}

// MeetRequest is the input shape for ScheduleMeet.
type MeetRequest struct {
	WithUserID uuid.UUID `json:"with_user_id"`
	When       time.Time `json:"when"`
	Latitude   float64   `json:"latitude"`
	Longitude  float64   `json:"longitude"`
	Venue      string    `json:"venue"`
}

// MeetResult is what the handler echoes back.
type MeetResult struct {
	MeetID uuid.UUID `json:"meet_id"`
}

// ReportRequest is the input shape for Report. Category is the legacy name
// of Reason, accepted when Reason is empty.
type ReportRequest struct {
	TargetID uuid.UUID            `json:"target_id"`
	Reason   string               `json:"reason"`
	Category string               `json:"category,omitempty"`
	Details  string               `json:"details"`
	Evidence store.ReportEvidence `json:"evidence"`
}

// ReportResult is the saved report plus whether the reporter is now
// blocking the target.
type ReportResult struct {
	*store.Report
	Blocked bool `json:"blocked"`
}

// panicContextMaxKeys / panicContextMaxString bound the client context kept
// on an incident.
const (
	panicContextMaxKeys   = 16
	panicContextMaxString = 200
)

// panicLocationKeys never survive in the context: the point lives only in
// the incident's latitude/longitude columns.
var panicLocationKeys = map[string]bool{
	"latitude": true, "longitude": true, "lat": true, "lng": true, "lon": true,
	"location": true, "coords": true, "coordinates": true, "geo": true,
}

// sanitizePanicContext keeps a bounded set of scalar client hints (source
// screen, app version) and drops anything location-shaped.
func sanitizePanicContext(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		if len(out) >= panicContextMaxKeys {
			break
		}
		key := strings.TrimSpace(k)
		if key == "" || len(key) > 64 || panicLocationKeys[strings.ToLower(key)] {
			continue
		}
		switch val := v.(type) {
		case string:
			if utf8.RuneCountInString(val) > panicContextMaxString {
				val = string([]rune(val)[:panicContextMaxString])
			}
			out[key] = val
		case bool, float64, int, int64:
			out[key] = val
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// RecordPanic writes the incident before anything else and pages only a
// new incident within the daily limit. A panic is never refused over its
// location: a missing, half or out-of-range point is dropped and the
// incident still records.
func (s *Service) RecordPanic(ctx context.Context, userID uuid.UUID, req PanicRequest) (*PanicResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	return s.recordPanicIncident(ctx, userID, store.PanicSourcePanic, nil, req.Latitude, req.Longitude, sanitizePanicContext(req.Context))
}

func (s *Service) recordPanicIncident(ctx context.Context, userID uuid.UUID, source string, meetID *uuid.UUID, lat, lng *float64, meta map[string]any) (*PanicResult, error) {
	if !store.ValidExactLocation(lat, lng) {
		if lat != nil || lng != nil {
			slog.Warn("panic: unusable location dropped; the incident still records", "user_id", userID, "source", source)
		}
		lat, lng = nil, nil
	}
	cfg := s.safetyConfig()
	out, err := s.store.RecordPanicIncident(ctx, store.RecordPanicParams{
		UserID:       userID,
		Source:       source,
		MeetID:       meetID,
		Latitude:     lat,
		Longitude:    lng,
		Context:      meta,
		DedupeWindow: cfg.PanicDedupeWindow,
		DailyLimit:   cfg.PanicDailyLimit,
	})
	if err != nil {
		return nil, fmt.Errorf("persist panic: %w", err)
	}
	inc := out.Incident
	switch {
	case out.Page:
		// The row is committed; a failed publish is retried by the sweeper
		// (RepublishUnpagedPanics), so the request still succeeds.
		s.pagePanic(context.WithoutCancel(ctx), inc)
	case out.Created && inc.SuspectedAbuse:
		slog.Warn("panic incident over the daily limit: recorded as suspected abuse and not paged",
			"incident_id", inc.ID, "user_id", userID, "daily_limit", cfg.PanicDailyLimit)
	}
	return &PanicResult{IncidentID: inc.ID, Status: inc.Status, Deduplicated: !out.Created}, nil
}

// pagePanic publishes dating.safety.panic for a new incident and marks it
// paged. The event carries no coordinates.
func (s *Service) pagePanic(ctx context.Context, inc *store.PanicIncident) {
	if s.producer == nil {
		slog.Error("panic incident recorded but no event producer is configured: responders are NOT paged",
			"incident_id", inc.ID, "user_id", inc.UserID)
		return
	}
	if err := s.producer.PublishSafetyPanic(ctx, inc.ID, inc.UserID, inc.CreatedAt, inc.HasLocation()); err != nil {
		slog.Error("publish dating.safety.panic failed; incident persisted, sweeper will retry",
			"incident_id", inc.ID, "user_id", inc.UserID, "error", err)
		return
	}
	if err := s.store.MarkPanicPaged(ctx, inc.ID); err != nil {
		// The page went out; a retry would only re-publish an event
		// notification-service dedupes by incident id.
		slog.Warn("panic paged but not marked; the sweeper may re-publish it", "incident_id", inc.ID, "error", err)
	}
}

// RepublishUnpagedPanics re-publishes pages that never reached Kafka for
// incidents younger than the configured age. Sweeper step.
func (s *Service) RepublishUnpagedPanics(ctx context.Context, limit int) (int, error) {
	if s.producer == nil {
		return 0, nil
	}
	incidents, err := s.store.ListPanicIncidentsAwaitingPage(ctx, s.safetyConfig().PanicRepageMaxAge, limit)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, inc := range incidents {
		if err := s.producer.PublishSafetyPanic(ctx, inc.ID, inc.UserID, inc.CreatedAt, inc.HasLocation()); err != nil {
			slog.Error("re-publish dating.safety.panic failed", "incident_id", inc.ID, "error", err)
			continue
		}
		if err := s.store.MarkPanicPaged(ctx, inc.ID); err != nil {
			slog.Warn("panic re-paged but not marked", "incident_id", inc.ID, "error", err)
		}
		n++
	}
	return n, nil
}

// ── Trusted contacts ────────────────────────────────────────────────────────

// TrustedContactWithPerson is one trusted contact plus the compact person
// card (lane D10), so the app can name the contact instead of falling back
// to "Your match" for anyone who is not a current match.
//
// Person is deliberately NOT omitempty: a contact whose profile is gone,
// suspended or purged still lists, and the app switches on an explicit null
// rather than on a missing member.
type TrustedContactWithPerson struct {
	*store.TrustedContact
	Person *PersonCard `json:"person"`
}

// ListTrustedContacts returns the user's trusted contacts, each with the
// compact person card. The card is null for a contact the caller may no
// longer see (deleted, suspended or purged); the contact itself still lists
// so the user can see — and remove — what they set.
func (s *Service) ListTrustedContacts(ctx context.Context, userID uuid.UUID) ([]*TrustedContactWithPerson, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	contacts, err := s.store.ListTrustedContacts(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(contacts))
	for _, tc := range contacts {
		ids = append(ids, tc.ContactID)
	}
	cards := s.personCards(ctx, userID, ids)
	out := make([]*TrustedContactWithPerson, 0, len(contacts))
	for _, tc := range contacts {
		out = append(out, &TrustedContactWithPerson{TrustedContact: tc, Person: cards[tc.ContactID]})
	}
	return out, nil
}

// SetTrustedContact adds contactID as a trusted contact (or updates the
// location opt-in). The contact must not be blocked either way and must be
// a current match or an accepted connection; the cap is store.MaxTrustedContacts.
func (s *Service) SetTrustedContact(ctx context.Context, userID, contactID uuid.UUID, shareLocationOnPanic bool) (*store.TrustedContact, error) {
	if userID == uuid.Nil || contactID == uuid.Nil {
		return nil, fmt.Errorf("invalid: user and contact ids required")
	}
	if userID == contactID {
		return nil, fmt.Errorf("invalid: you cannot be your own trusted contact")
	}
	if err := s.requireNotBlocked(ctx, userID, contactID); err != nil {
		return nil, err
	}
	ok, err := s.isEligibleTrustedContact(ctx, userID, contactID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrTrustedContactNotEligible
	}
	tc, _, err := s.store.UpsertTrustedContact(ctx, userID, contactID, shareLocationOnPanic)
	return tc, err
}

// isEligibleTrustedContact: a current match, else an accepted connection
// confirmed by graph-service. A graph failure refuses (fail closed).
func (s *Service) isEligibleTrustedContact(ctx context.Context, userID, contactID uuid.UUID) (bool, error) {
	matched, err := s.store.HasOpenMatch(ctx, userID, contactID)
	if err != nil {
		return false, err
	}
	if matched {
		return true, nil
	}
	if s.connections == nil {
		return false, nil
	}
	connected, err := s.connections.IsAcceptedConnection(ctx, userID, contactID)
	if err != nil {
		slog.Warn("trusted contact: connection check failed; refusing", "user_id", userID, "contact_id", contactID, "error", err)
		return false, ErrConnectionCheckUnavailable
	}
	return connected, nil
}

// RemoveTrustedContact removes one trusted contact.
func (s *Service) RemoveTrustedContact(ctx context.Context, userID, contactID uuid.UUID) error {
	if userID == uuid.Nil || contactID == uuid.Nil {
		return fmt.Errorf("invalid: user and contact ids required")
	}
	return s.store.RemoveTrustedContact(ctx, userID, contactID)
}

// ── Live location ───────────────────────────────────────────────────────────

// ShareLocation shares the sharer's exact point with one recipient — a
// trusted contact or a current match, not blocked either way — until the
// share expires (default 60 minutes, capped by the configured maximum).
// The recipient is notified through dating.safety.location_shared, which
// carries no coordinates.
func (s *Service) ShareLocation(ctx context.Context, userID uuid.UUID, req LocationShareRequest) (*LocationShareResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	if req.RecipientID == uuid.Nil {
		return nil, fmt.Errorf("invalid: recipient_id required")
	}
	if req.RecipientID == userID {
		return nil, fmt.Errorf("invalid: cannot share your location with yourself")
	}
	if !store.ValidExactLocation(req.Latitude, req.Longitude) {
		return nil, store.ErrInvalidLocation
	}
	if err := s.requireNotBlocked(ctx, userID, req.RecipientID); err != nil {
		return nil, err
	}
	kind := ""
	trusted, err := s.store.IsTrustedContact(ctx, userID, req.RecipientID)
	if err != nil {
		return nil, err
	}
	if trusted {
		kind = store.ShareRecipientTrustedContact
	} else {
		matched, err := s.store.HasOpenMatch(ctx, userID, req.RecipientID)
		if err != nil {
			return nil, err
		}
		if matched {
			kind = store.ShareRecipientMatch
		}
	}
	if kind == "" {
		return nil, ErrShareRecipientNotAllowed
	}
	cfg := s.safetyConfig()
	ttl := time.Duration(req.DurationMinutes) * time.Minute
	if ttl <= 0 {
		ttl = cfg.LocationShareDefault
	}
	if ttl > cfg.LocationShareMax {
		ttl = cfg.LocationShareMax
	}
	share, err := s.store.CreateLocationShare(ctx, userID, req.RecipientID, kind, *req.Latitude, *req.Longitude, ttl)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		if perr := s.producer.PublishSafetyLocationShared(ctx, userID, req.RecipientID, share.ShareID, share.ExpiresAt); perr != nil {
			slog.Error("publish safety.location_shared failed; share persisted", "share_id", share.ShareID, "error", perr)
		}
	}
	return &LocationShareResult{ShareID: share.ShareID, RecipientID: share.RecipientID, RecipientKind: kind, ExpiresAt: share.ExpiresAt}, nil
}

// StopLocationShare ends the sharer's own share and clears its point.
func (s *Service) StopLocationShare(ctx context.Context, userID, shareID uuid.UUID) (*store.LocationShare, error) {
	return s.store.StopLocationShare(ctx, shareID, userID)
}

// GetSharedLocation returns a share's point to its recipient while it is
// live. Anyone else, or after expiry or stop, gets not found.
func (s *Service) GetSharedLocation(ctx context.Context, recipientID, shareID uuid.UUID) (*store.LocationShare, error) {
	return s.store.GetLocationShareForRecipient(ctx, shareID, recipientID)
}

// ── Meets ───────────────────────────────────────────────────────────────────

// ScheduleMeet creates the dating_meets row with a current match (not
// blocked either way), persists a safety event, and emits the kafka event.
func (s *Service) ScheduleMeet(ctx context.Context, userID uuid.UUID, req MeetRequest) (*MeetResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	if req.WithUserID == uuid.Nil {
		return nil, fmt.Errorf("invalid: with_user_id required")
	}
	// Lane D3: no meet with a user blocked either way.
	if err := s.requireNotBlocked(ctx, userID, req.WithUserID); err != nil {
		return nil, err
	}
	// Lane D8: only with a current match, so reminders and missed check-in
	// alerts can only ever reach someone the user matched with.
	matched, err := s.store.HasOpenMatch(ctx, userID, req.WithUserID)
	if err != nil {
		return nil, err
	}
	if !matched {
		return nil, ErrMeetRequiresMatch
	}
	id, err := s.store.ScheduleMeet(ctx, userID, req.WithUserID, req.When, req.Latitude, req.Longitude, req.Venue)
	if err != nil {
		return nil, err
	}
	if err := s.store.RecordSafetyEvent(ctx, userID, "meet_scheduled", map[string]any{
		"meet_id":      id.String(),
		"with_user_id": req.WithUserID.String(),
		"scheduled_at": req.When.UTC(),
	}); err != nil {
		// Non-fatal — the dating_meets row is the canonical record.
		slog.Warn("safety event write failed for meet_scheduled", "meet_id", id, "error", err)
	}
	if s.producer != nil {
		if perr := s.producer.PublishSafetyMeetScheduled(ctx, id, userID, req.WithUserID, req.When, req.Venue); perr != nil {
			slog.Error("publish safety.meet_scheduled failed; meet persisted", "meet_id", id, "error", perr)
		}
	}
	return &MeetResult{MeetID: id}, nil
}

// MeetCheckIn records the user's safe/help confirmation without a location.
func (s *Service) MeetCheckIn(ctx context.Context, meetID, userID uuid.UUID, status string) error {
	return s.MeetCheckInAt(ctx, meetID, userID, status, nil, nil)
}

// MeetCheckInAt records the user's safe/help confirmation and emits the
// event. "help" also writes a panic incident (deduplicated like a panic)
// so it reaches the admin queue and pages responders. If that incident
// cannot be written the call fails, so the client retries: the check-in
// update is idempotent and the retry lands in the same incident.
func (s *Service) MeetCheckInAt(ctx context.Context, meetID, userID uuid.UUID, status string, lat, lng *float64) error {
	if err := s.store.MeetCheckIn(ctx, meetID, userID, status); err != nil {
		return err
	}
	if s.producer != nil {
		if perr := s.producer.PublishSafetyMeetCheckin(ctx, meetID, userID, status); perr != nil {
			slog.Error("publish safety.meet_checkin failed; checkin persisted", "meet_id", meetID, "error", perr)
		}
	}
	if status == "help" {
		mid := meetID
		if _, err := s.recordPanicIncident(ctx, userID, store.PanicSourceMeetCheckIn, &mid, lat, lng,
			map[string]any{"meet_id": meetID.String()}); err != nil {
			return fmt.Errorf("check-in saved but the help alert was not recorded: %w", err)
		}
	}
	return nil
}

// ── Block ───────────────────────────────────────────────────────────────────

// Block records the dating_blocks row and severs the pair in the same
// transaction (open match closed, sparks and stashes deleted both ways),
// clears both users' deck caches, propagates to graph-service (best
// effort), and emits the block events plus dating.match.closed for each
// match it closed. Unblocking restores nothing.
func (s *Service) Block(ctx context.Context, userID, targetID uuid.UUID) error {
	outcome, err := s.store.BlockUserAndSever(ctx, userID, targetID)
	if err != nil {
		return err
	}
	// Lane D3: either deck may hold the other user for up to 24h. Drop
	// both so the next pulse re-runs the candidate query, which filters
	// blocks in both directions.
	s.InvalidatePulseCache(ctx, userID)
	s.InvalidatePulseCache(ctx, targetID)
	// Propagate to graph-service so the graph layer also stops surfacing
	// the user. Best-effort: log on failure, do not fail the user request.
	if base := os.Getenv("GRAPH_SERVICE_URL"); base != "" {
		go func() {
			ctx2, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			body, _ := json.Marshal(map[string]string{
				"user_id":    userID.String(),
				"blocked_id": targetID.String(),
				"source":     "dating",
			})
			req, err := http.NewRequestWithContext(ctx2, http.MethodPost, base+"/v1/graph/blocks", io.NopCloser(bytes.NewReader(body)))
			if err != nil {
				slog.Warn("graph block propagation: build req", "error", err)
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.ContentLength = int64(len(body))
			// Module 3 LB-3: attribute this graph mutation. Every approved
			// caller stamps its OWN reviewed source; the gateway overwrites
			// any label arriving from a client, so this cannot be forged.
			req.Header.Set("X-Graph-Write-Source", "dating-service")
			if key := os.Getenv("INTERNAL_SERVICE_KEY"); key != "" {
				req.Header.Set("X-Internal-Key", key)
			}
			resp, err := s.graphHTTPClient.Do(req)
			if err != nil {
				slog.Warn("graph block propagation failed", "error", err)
				return
			}
			defer resp.Body.Close()
			if resp.StatusCode >= 400 {
				slog.Warn("graph block propagation status", "status", resp.StatusCode)
			}
		}()
	}
	if s.producer != nil {
		if perr := s.producer.PublishBlockCreated(ctx, userID, targetID); perr != nil {
			slog.Error("publish block.created failed; row persisted", "user_id", userID, "error", perr)
		}
		// Phase 1 — chat-service listens for dating.user.blocked to
		// sever any existing dating_match conversation between the
		// pair. Persist already succeeded; failure here is logged.
		if perr := s.producer.PublishUserBlocked(ctx, userID, targetID); perr != nil {
			slog.Error("publish user.blocked failed; row persisted", "user_id", userID, "error", perr)
		}
		// Lane D3: chat-service closes the dating conversation on
		// dating.match.closed by match_id (dating_consumer.go).
		for _, m := range outcome.ClosedMatches {
			if perr := s.producer.PublishMatchClosed(ctx, m.ID, userID, m.UserA, m.UserB); perr != nil {
				slog.Error("publish match.closed after block failed; match closed", "match_id", m.ID, "error", perr)
			}
		}
	}
	return nil
}

// ── Admin: panic incidents ──────────────────────────────────────────────────

// AcknowledgePanic moves an open incident to acknowledged and emits
// dating.safety.panic.acknowledged so the user's notification arm fires
// "support has reviewed your alert". Idempotent: a second ack writes no
// audit row and emits nothing.
//
// adminID is the admin's gateway-derived user id; uuid.Nil is refused. The
// acknowledgement that lands writes one dating_admin_audit row (action
// "panic_acknowledged"). As with the other admin actions, an audit insert
// failure is logged but does not roll back the acknowledgement.
func (s *Service) AcknowledgePanic(ctx context.Context, panicID, adminID uuid.UUID) error {
	if adminID == uuid.Nil {
		return errAdminActorRequired
	}
	if panicID == uuid.Nil {
		return fmt.Errorf("invalid: panic_id required")
	}
	inc, acked, err := s.store.AcknowledgePanicIncident(ctx, panicID, adminID)
	if err != nil {
		if errors.Is(err, store.ErrPanicAlreadyAcked) {
			return nil
		}
		return err
	}
	if !acked {
		return nil
	}
	entry := &store.AdminAuditEntry{
		ActorAdminID:   adminID,
		Action:         "panic_acknowledged",
		TargetUserID:   inc.UserID,
		TargetResource: "panic_incident:" + panicID.String(),
	}
	if aerr := s.store.InsertAdminAudit(ctx, entry); aerr != nil {
		slog.Error("admin audit: insert failed for AcknowledgePanic",
			"panic_id", panicID, "user_id", inc.UserID, "actor_admin_id", adminID, "error", aerr)
	}
	if s.producer != nil && !inc.Anonymised {
		if perr := s.producer.PublishSafetyPanicAcknowledged(ctx, inc.UserID, adminID.String()); perr != nil {
			slog.Error("publish safety.panic.acknowledged failed; row persisted", "panic_id", panicID, "user_id", inc.UserID, "error", perr)
		}
	}
	return nil
}

// ── Reports ─────────────────────────────────────────────────────────────────

// validateReportRequest normalises and bounds a report.
func validateReportRequest(reporterID uuid.UUID, req ReportRequest) (string, string, store.ReportEvidence, error) {
	var ev store.ReportEvidence
	if reporterID == uuid.Nil {
		return "", "", ev, fmt.Errorf("invalid: reporterID required")
	}
	if req.TargetID == uuid.Nil {
		return "", "", ev, fmt.Errorf("invalid: target_id required")
	}
	if req.TargetID == reporterID {
		return "", "", ev, fmt.Errorf("invalid: cannot report yourself")
	}
	reason := strings.TrimSpace(req.Reason)
	if reason == "" {
		reason = strings.TrimSpace(req.Category)
	}
	if !store.IsReportReason(reason) {
		return "", "", ev, ErrInvalidReportReason
	}
	details := strings.TrimSpace(req.Details)
	if utf8.RuneCountInString(details) > store.MaxReportDetailsChars {
		return "", "", ev, fmt.Errorf("invalid: details must be at most %d characters", store.MaxReportDetailsChars)
	}
	if reason == store.ReportReasonOther && details == "" {
		return "", "", ev, fmt.Errorf("invalid: details are required when the reason is other")
	}
	if len(req.Evidence.PhotoIDs) > store.MaxReportEvidencePhotos {
		return "", "", ev, fmt.Errorf("invalid: at most %d photo ids", store.MaxReportEvidencePhotos)
	}
	if len(req.Evidence.SparkIDs) > store.MaxReportEvidenceSparks {
		return "", "", ev, fmt.Errorf("invalid: at most %d spark ids", store.MaxReportEvidenceSparks)
	}
	if len(req.Evidence.MessageIDs) > store.MaxReportEvidenceMessages {
		return "", "", ev, fmt.Errorf("invalid: at most %d message ids", store.MaxReportEvidenceMessages)
	}
	ev.PhotoIDs = req.Evidence.PhotoIDs
	ev.SparkIDs = req.Evidence.SparkIDs
	for _, m := range req.Evidence.MessageIDs {
		m = strings.TrimSpace(m)
		if m == "" {
			continue
		}
		if len(m) > store.MaxReportMessageRefLen {
			return "", "", ev, fmt.Errorf("invalid: message ids must be at most %d characters", store.MaxReportMessageRefLen)
		}
		ev.MessageIDs = append(ev.MessageIDs, m)
	}
	return reason, details, ev, nil
}

// Report persists the report (reason code, bounded details, evidence
// references, per-reporter daily limit), then:
//   - blocks the target for the reporter through the D3 block path (match
//     closed, dating.match.closed emitted) and reports blocked=true;
//   - for an underage report, holds the target in pending_review through
//     the status writer until a moderator acts;
//   - emits dating.report.created;
//   - links a trust-safety grievance (15-day timer). If trust-safety is
//     unavailable the report is still saved and the sweeper retries.
//
// Everything after the insert is best effort and logged: the report is the
// record and is never lost because a side effect failed.
func (s *Service) Report(ctx context.Context, reporterID uuid.UUID, req ReportRequest) (*ReportResult, error) {
	reason, details, evidence, err := validateReportRequest(reporterID, req)
	if err != nil {
		return nil, err
	}
	r, err := s.store.CreateReportWithParams(ctx, store.CreateReportParams{
		ReporterID: reporterID,
		TargetID:   req.TargetID,
		Reason:     reason,
		Details:    details,
		Evidence:   evidence,
		DailyLimit: s.safetyConfig().ReportDailyLimit,
	})
	if err != nil {
		return nil, err
	}
	ctx = context.WithoutCancel(ctx)
	result := &ReportResult{Report: r}

	if berr := s.Block(ctx, reporterID, req.TargetID); berr != nil {
		slog.Error("report saved but the auto-block failed", "report_id", r.ID, "reporter_id", reporterID, "error", berr)
	} else {
		result.Blocked = true
		if merr := s.store.MarkReportAutoBlocked(ctx, r.ID); merr != nil {
			slog.Warn("report auto-block not recorded on the report", "report_id", r.ID, "error", merr)
		} else {
			r.AutoBlocked = true
		}
	}

	if reason == store.ReportReasonUnderage {
		s.holdForUnderageReport(ctx, r)
	}

	if s.producer != nil {
		if perr := s.producer.PublishReportCreated(ctx, r.ID, reporterID, req.TargetID, reason, details); perr != nil {
			slog.Error("publish report.created failed; row persisted", "report_id", r.ID, "error", perr)
		}
	}
	s.linkReportGrievance(ctx, r)
	return result, nil
}

// holdForUnderageReport moves the target to pending_review as the system
// actor. The writer never softens a suspension or restriction. A target
// with no dating profile has nothing to hold.
func (s *Service) holdForUnderageReport(ctx context.Context, r *store.Report) {
	if _, err := s.store.TransitionProfileStatus(ctx, r.TargetID, store.ProfileEventReview, store.ProfileActorSystem); err != nil {
		if errors.Is(err, store.ErrProfileNotFound) {
			slog.Info("underage report: target has no dating profile to hold", "report_id", r.ID)
			return
		}
		slog.Error("underage report: moving the target to pending_review failed", "report_id", r.ID, "target_id", r.TargetID, "error", err)
		return
	}
	s.InvalidatePulseCache(ctx, r.TargetID)
	s.InvalidateDecksForCandidate(ctx, r.TargetID)
	slog.Warn("underage report: target held in pending_review for moderator action", "report_id", r.ID, "target_id", r.TargetID)
}

// linkReportGrievance makes one attempt to link the report to a
// trust-safety grievance and records the outcome for the retry worker.
func (s *Service) linkReportGrievance(ctx context.Context, r *store.Report) {
	if r.GrievanceID != nil {
		return
	}
	if s.trustSafety == nil {
		slog.Warn("trust-safety client not configured; report left pending for the grievance retry worker", "report_id", r.ID)
		return
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	gid, err := s.trustSafety.LinkDatingReportGrievance(cctx, GrievanceLinkRequest{
		ReportID:   r.ID,
		ReporterID: r.ReporterID,
		TargetID:   r.TargetID,
		Reason:     r.Category,
		Details:    r.Details,
		ReportedAt: r.CreatedAt,
	})
	if err != nil {
		slog.Warn("report grievance link failed; will retry", "report_id", r.ID, "error", err)
		if ferr := s.store.RecordGrievanceLinkFailure(ctx, r.ID, err.Error()); ferr != nil {
			slog.Error("report grievance link failure not recorded", "report_id", r.ID, "error", ferr)
		}
		return
	}
	if serr := s.store.SetReportGrievance(ctx, r.ID, gid); serr != nil {
		slog.Error("report grievance created but not stored on the report", "report_id", r.ID, "grievance_id", gid, "error", serr)
		return
	}
	r.GrievanceID = &gid
}

// LinkPendingReportGrievances retries due grievance links. Sweeper step.
func (s *Service) LinkPendingReportGrievances(ctx context.Context, limit int) (int, error) {
	if s.trustSafety == nil {
		return 0, nil
	}
	reports, err := s.store.ClaimReportsForGrievanceLink(ctx, limit)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range reports {
		s.linkReportGrievance(ctx, r)
		if r.GrievanceID != nil {
			n++
		}
	}
	return n, nil
}

// ── Internal: panic notification context ────────────────────────────────────

// PanicNotifyContact is one trusted contact to notify. The point is present
// only when the user opted in to share it with that contact and the
// incident has one.
type PanicNotifyContact struct {
	UserID    uuid.UUID `json:"user_id"`
	Latitude  *float64  `json:"latitude,omitempty"`
	Longitude *float64  `json:"longitude,omitempty"`
}

// PanicNotifyContext is what notification-service needs to page for one
// incident. Served only on the service-only internal route.
type PanicNotifyContext struct {
	IncidentID      uuid.UUID            `json:"incident_id"`
	UserID          uuid.UUID            `json:"user_id"`
	FirstName       string               `json:"first_name"`
	Status          string               `json:"status"`
	SuspectedAbuse  bool                 `json:"suspected_abuse"`
	HasLocation     bool                 `json:"has_location"`
	CreatedAt       time.Time            `json:"created_at"`
	TrustedContacts []PanicNotifyContact `json:"trusted_contacts"`
}

// PanicNotifyContext returns the incident's paging context. A suspected
// abuse, resolved or anonymised incident lists no contacts.
func (s *Service) PanicNotifyContext(ctx context.Context, incidentID uuid.UUID) (*PanicNotifyContext, error) {
	inc, err := s.store.GetPanicIncident(ctx, incidentID)
	if err != nil {
		return nil, err
	}
	out := &PanicNotifyContext{
		IncidentID:      inc.ID,
		UserID:          inc.UserID,
		Status:          inc.Status,
		SuspectedAbuse:  inc.SuspectedAbuse,
		HasLocation:     inc.HasLocation(),
		CreatedAt:       inc.CreatedAt,
		TrustedContacts: []PanicNotifyContact{},
	}
	if inc.Anonymised || inc.SuspectedAbuse || inc.Status == store.PanicStatusResolved {
		return out, nil
	}
	if name, nerr := s.store.LookupFirstName(ctx, inc.UserID); nerr == nil {
		out.FirstName = name
	}
	contacts, err := s.store.ListTrustedContacts(ctx, inc.UserID)
	if err != nil {
		return nil, err
	}
	for _, tc := range contacts {
		c := PanicNotifyContact{UserID: tc.ContactID}
		if tc.ShareLocationOnPanic && inc.HasLocation() {
			c.Latitude, c.Longitude = inc.Latitude, inc.Longitude
		}
		out.TrustedContacts = append(out.TrustedContacts, c)
	}
	return out, nil
}

// ── Live location: the two list views (lane D10) ────────────────────────────

// OutgoingLocationShare is one of the caller's live shares. No coordinates:
// the sharer already knows where they are, and the recipient reads the point
// one share at a time.
type OutgoingLocationShare struct {
	*store.LocationShareSummary
	// Recipient is the compact person the share goes to, when the caller
	// may still see them.
	Recipient *PersonCard `json:"recipient,omitempty"`
}

// IncomingLocationShare is one live share aimed at the caller: the share id
// to read the point with, who is sharing, and when it ends.
type IncomingLocationShare struct {
	*store.LocationShareSummary
	// Person is the sharer's compact card.
	Person *PersonCard `json:"person,omitempty"`
}

// ListMyLocationShares returns the caller's live outgoing shares.
func (s *Service) ListMyLocationShares(ctx context.Context, userID uuid.UUID) ([]*OutgoingLocationShare, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	shares, err := s.store.ListActiveSharesByUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(shares))
	for _, sh := range shares {
		ids = append(ids, sh.RecipientID)
	}
	cards := s.personCards(ctx, userID, ids)
	out := make([]*OutgoingLocationShare, 0, len(shares))
	for _, sh := range shares {
		out = append(out, &OutgoingLocationShare{LocationShareSummary: sh, Recipient: cards[sh.RecipientID]})
	}
	return out, nil
}

// ListSharedLocationsForMe returns the live shares aimed at the caller, each
// with the share id the point is read with.
func (s *Service) ListSharedLocationsForMe(ctx context.Context, recipientID uuid.UUID) ([]*IncomingLocationShare, error) {
	if recipientID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	shares, err := s.store.ListActiveSharesForRecipient(ctx, recipientID)
	if err != nil {
		return nil, err
	}
	ids := make([]uuid.UUID, 0, len(shares))
	for _, sh := range shares {
		ids = append(ids, sh.UserID)
	}
	cards := s.personCards(ctx, recipientID, ids)
	out := make([]*IncomingLocationShare, 0, len(shares))
	for _, sh := range shares {
		out = append(out, &IncomingLocationShare{LocationShareSummary: sh, Person: cards[sh.UserID]})
	}
	return out, nil
}
