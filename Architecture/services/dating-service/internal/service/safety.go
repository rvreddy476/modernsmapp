// Safety service — spec §15. Wraps the safety store with explicit error
// paths, Kafka emission, and the "persist before respond" contract for
// panic + report (rule #6: no silent failures on safety-adjacent code).
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
	"time"

	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// PanicRequest is the input shape for RecordPanic.
type PanicRequest struct {
	Latitude  *float64       `json:"latitude,omitempty"`
	Longitude *float64       `json:"longitude,omitempty"`
	Context   map[string]any `json:"context,omitempty"`
}

// LocationShareRequest is the input shape for ShareLocation.
type LocationShareRequest struct {
	ContactID       uuid.UUID `json:"contact_id"`
	DurationMinutes int       `json:"duration_minutes"`
	Latitude        *float64  `json:"latitude,omitempty"`
	Longitude       *float64  `json:"longitude,omitempty"`
}

// LocationShareResult is what the handler echoes back to the user.
type LocationShareResult struct {
	ShareID   uuid.UUID `json:"share_id"`
	ExpiresAt time.Time `json:"expires_at"`
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

// ReportRequest is the input shape for Report.
type ReportRequest struct {
	TargetID uuid.UUID `json:"target_id"`
	Category string    `json:"category"`
	Details  string    `json:"details"`
}

// LocationShareKey returns the Redis key holding a live share's static
// location. The S5 WebSocket worker writes moving updates to this key.
func LocationShareKey(shareID uuid.UUID) string {
	return "dating:location_share:" + shareID.String()
}

// RecordPanic persists, then publishes. If the persist step fails we
// return the error to the handler (which surfaces 500) so the user-side
// notification can retry. We do NOT swallow the error.
//
// CRITICAL RULES #6: no silent failures on safety-adjacent code.
func (s *Service) RecordPanic(ctx context.Context, userID uuid.UUID, req PanicRequest) error {
	if userID == uuid.Nil {
		return fmt.Errorf("invalid: userID required")
	}
	details := map[string]any{}
	if req.Latitude != nil {
		details["latitude"] = *req.Latitude
	}
	if req.Longitude != nil {
		details["longitude"] = *req.Longitude
	}
	for k, v := range req.Context {
		details[k] = v
	}
	if err := s.store.RecordSafetyEvent(ctx, userID, "panic", details); err != nil {
		return fmt.Errorf("persist panic: %w", err)
	}
	if s.producer != nil {
		if perr := s.producer.PublishSafetyPanic(ctx, userID, req.Latitude, req.Longitude, req.Context); perr != nil {
			// We log but do NOT return — the row is persisted, so trust-
			// safety-service can pick it up via a backfill job. We log at
			// ERROR level (not Warn) because this is the safety path.
			slog.Error("publish safety.panic failed; row persisted, see audit", "user_id", userID, "error", perr)
		}
	}
	return nil
}

// ShareLocation creates the share row + Redis key + emits the event.
func (s *Service) ShareLocation(ctx context.Context, userID uuid.UUID, req LocationShareRequest) (*LocationShareResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
	}
	if req.ContactID == uuid.Nil {
		return nil, fmt.Errorf("invalid: contact_id required")
	}
	share, err := s.store.CreateLiveLocationShare(ctx, userID, req.ContactID, req.DurationMinutes)
	if err != nil {
		return nil, err
	}
	// Static at-creation snapshot in Redis (S5 will push moving updates).
	if s.rdb != nil && (req.Latitude != nil || req.Longitude != nil) {
		payload, _ := json.Marshal(map[string]any{
			"user_id":    userID.String(),
			"contact_id": req.ContactID.String(),
			"latitude":   req.Latitude,
			"longitude":  req.Longitude,
			"updated_at": time.Now().UTC(),
		})
		ttl := time.Until(share.ExpiresAt)
		if ttl <= 0 {
			ttl = 1 * time.Hour
		}
		if err := s.rdb.Set(ctx, LocationShareKey(share.ShareID), payload, ttl).Err(); err != nil {
			slog.Warn("location share redis write failed", "share_id", share.ShareID, "error", err)
		}
	}
	if s.producer != nil {
		if perr := s.producer.PublishSafetyLocationShared(ctx, userID, req.ContactID, share.ShareID, share.ExpiresAt); perr != nil {
			slog.Error("publish safety.location_shared failed; row persisted", "share_id", share.ShareID, "error", perr)
		}
	}
	return &LocationShareResult{ShareID: share.ShareID, ExpiresAt: share.ExpiresAt}, nil
}

// ScheduleMeet creates the dating_meets row, persists a safety event, and
// emits the kafka event. The 2.5h no-show worker is scheduled by the S5
// background runner — flagged here for follow-up.
func (s *Service) ScheduleMeet(ctx context.Context, userID uuid.UUID, req MeetRequest) (*MeetResult, error) {
	if userID == uuid.Nil {
		return nil, fmt.Errorf("invalid: userID required")
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

// MeetCheckIn records the user's safe/help confirmation, emits the event,
// and (if status='help') also fires a panic for the trust-safety pipeline.
func (s *Service) MeetCheckIn(ctx context.Context, meetID, userID uuid.UUID, status string) error {
	if err := s.store.MeetCheckIn(ctx, meetID, userID, status); err != nil {
		return err
	}
	if s.producer != nil {
		if perr := s.producer.PublishSafetyMeetCheckin(ctx, meetID, userID, status); perr != nil {
			slog.Error("publish safety.meet_checkin failed; checkin persisted", "meet_id", meetID, "error", perr)
		}
		if status == "help" {
			// Mirror as a panic so the same trust-safety pipeline takes
			// over. The persist already succeeded above.
			_ = s.producer.PublishSafetyPanic(ctx, userID, nil, nil, map[string]any{
				"source":  "meet_checkin",
				"meet_id": meetID.String(),
			})
		}
	}
	return nil
}

// Block records the dating_blocks row, propagates to graph-service (best
// effort), and emits the block event.
func (s *Service) Block(ctx context.Context, userID, targetID uuid.UUID) error {
	if err := s.store.BlockUser(ctx, userID, targetID); err != nil {
		return err
	}
	// Phase 1 §3: viewer's own deck may already include the just-
	// blocked candidate — drop it so the next pulse refresh re-runs
	// the candidate query (which already filters mutual blocks).
	s.InvalidatePulseCache(ctx, userID)
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
	}
	return nil
}

// AcknowledgePanic stamps acknowledged_at + acknowledged_by on a
// safety_events row of kind='panic' and emits
// dating.safety.panic.acknowledged so the affected user's notification
// arm fires "support has reviewed your alert". Idempotent at the
// store layer — a second ack returns the row with acked=false and the
// service does not re-emit.
//
// adminID is the gateway-injected actor (may be uuid.Nil). The audit
// row in dating_admin_audit is left to the caller / handler.
func (s *Service) AcknowledgePanic(ctx context.Context, panicID, adminID uuid.UUID) error {
	if panicID == uuid.Nil {
		return fmt.Errorf("invalid: panic_id required")
	}
	event, acked, err := s.store.AcknowledgePanic(ctx, panicID, adminID)
	if err != nil {
		// Already-acked is a soft success: the row exists, the
		// event has already been emitted (or will be replayed by
		// the original caller's path), and we don't bounce the
		// admin click. Re-surface other errors.
		if errors.Is(err, store.ErrPanicAlreadyAcked) {
			return nil
		}
		return err
	}
	if !acked {
		// Defensive: store contract returns (row, false, ErrPanicAlreadyAcked)
		// on replay. If we ever see (row, false, nil) treat the same.
		return nil
	}
	if s.producer != nil {
		ackBy := ""
		if adminID != uuid.Nil {
			ackBy = adminID.String()
		}
		if perr := s.producer.PublishSafetyPanicAcknowledged(ctx, event.UserID, ackBy); perr != nil {
			// Persist already succeeded; surface to slog but don't
			// fail the admin click. Convention follows the rest of the
			// safety code path.
			slog.Error("publish safety.panic.acknowledged failed; row persisted", "panic_id", panicID, "user_id", event.UserID, "error", perr)
		}
	}
	return nil
}

// Report persists, then emits. Same persist-first contract as panic.
func (s *Service) Report(ctx context.Context, reporterID uuid.UUID, req ReportRequest) (*store.Report, error) {
	if reporterID == uuid.Nil {
		return nil, fmt.Errorf("invalid: reporterID required")
	}
	r, err := s.store.CreateReport(ctx, reporterID, req.TargetID, req.Category, req.Details)
	if err != nil {
		return nil, err
	}
	if s.producer != nil {
		if perr := s.producer.PublishReportCreated(ctx, r.ID, reporterID, req.TargetID, req.Category, req.Details); perr != nil {
			slog.Error("publish report.created failed; row persisted", "report_id", r.ID, "error", perr)
		}
	}
	return r, nil
}

