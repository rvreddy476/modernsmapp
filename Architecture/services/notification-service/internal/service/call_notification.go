package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/google/uuid"
)

// CALL-LB-4: a RING is only as durable as its DELIVERY. The general
// createNotification path deliberately treats realtime and push as
// best-effort transports over a durable inbox row — acceptable for a like or
// a comment, fatal for an incoming call, where the push IS the product: a
// backgrounded or killed recipient who misses it misses the call forever.
//
// The call path therefore runs AT-LEAST-ONCE transports: every failure —
// inbox row, realtime publish, preference/device lookup, push send — is
// RETURNED to the caller, so the durable Kafka consumer keeps the event
// uncommitted and retries. Duplicate delivery on retry is bounded by
// construction: the inbox row is IF NOT EXISTS under a deterministic
// identity, realtime consumers de-duplicate on the deterministic
// notification id, and pushes carry the per-call collapse key.

// callPushTarget is one deliverable device.
type callPushTarget struct {
	Token    string
	Platform string
}

// callTransports is the exact production dependency set one call
// notification rides on. *Service implements it over Scylla, Redis,
// PostgreSQL and FCM/APNs; runCallDelivery owns the orchestration so its
// failure contract is unit-testable without live stores.
type callTransports interface {
	ensureCallRow(ctx context.Context, n *scylla.Notification) error
	publishCallRealtime(ctx context.Context, userID uuid.UUID, n *scylla.Notification) error
	// callPushTargets returns the deliverable devices and whether push is
	// allowed right now (configured, enabled, outside quiet hours). A
	// lookup FAILURE is an error — "no devices" because the database was
	// down must never be mistaken for "nothing to deliver".
	callPushTargets(ctx context.Context, userID uuid.UUID) ([]callPushTarget, bool, error)
	sendCallPush(ctx context.Context, target callPushTarget, notifType string, n *scylla.Notification) error
	// retireCallDevice durably deactivates a PERMANENTLY rejected device so
	// a dead token cannot stall retries forever. Its failure is an error —
	// a retirement that did not persist would resurrect the stall.
	retireCallDevice(ctx context.Context, userID uuid.UUID, target callPushTarget) error
}

// CreateCallNotification delivers one call notification (ring or missed
// call) with the at-least-once contract above. `identity` must be stable for
// the logical delivery — the call consumer uses "call:<event_id>:<recipient>".
func (s *Service) CreateCallNotification(ctx context.Context, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time, identity string) error {
	n := &scylla.Notification{
		UserID:         userID,
		NotificationID: scylla.DeterministicNotificationID(identity),
		TS:             scylla.DeterministicTS(createdAt, identity),
		Type:           notifType,
		ActorUserID:    actorID,
		EntityType:     entityType,
		EntityID:       entityID,
		DeepLink:       deepLink,
		IsRead:         false,
		CreatedAt:      createdAt,
	}
	return runCallDelivery(ctx, s, userID, notifType, n)
}

// runCallDelivery: row, then realtime, then push — and EVERY failure
// propagates (the pre-fix path logged transport errors and returned nil, so
// the durable consumer committed a lost ring).
func runCallDelivery(ctx context.Context, t callTransports, userID uuid.UUID, notifType string, n *scylla.Notification) error {
	if err := t.ensureCallRow(ctx, n); err != nil {
		return fmt.Errorf("call notification row: %w", err)
	}
	if err := t.publishCallRealtime(ctx, userID, n); err != nil {
		return fmt.Errorf("call realtime publish: %w", err)
	}
	targets, pushAllowed, err := t.callPushTargets(ctx, userID)
	if err != nil {
		return fmt.Errorf("call push targets: %w", err)
	}
	if !pushAllowed {
		return nil
	}
	for _, target := range targets {
		err := t.sendCallPush(ctx, target, notifType, n)
		switch {
		case err == nil:
		case errors.Is(err, push.ErrDeviceRejected):
			// Permanent for THIS device only (CALL-LB-4): retire it
			// durably and continue with the remaining devices — one stale
			// token must not block other devices or the Kafka partition.
			if retireErr := t.retireCallDevice(ctx, userID, target); retireErr != nil {
				return fmt.Errorf("retire rejected device (%s): %w", target.Platform, retireErr)
			}
		default:
			// Transient (outage/throttle/credentials): the event must stay
			// uncommitted and retry.
			return fmt.Errorf("call push (%s): %w", target.Platform, err)
		}
	}
	return nil
}

// ensureCallRow keeps the inbox row single across retries. The `applied`
// result is deliberately IGNORED: on a redelivery the row already exists and
// the transports must still run — that is the whole difference from the
// at-most-once general path.
func (s *Service) ensureCallRow(ctx context.Context, n *scylla.Notification) error {
	_, err := s.scyllaStore.CreateNotificationIfNotExists(ctx, n)
	return err
}

func (s *Service) publishCallRealtime(ctx context.Context, userID uuid.UUID, n *scylla.Notification) error {
	payload, err := json.Marshal(map[string]interface{}{
		"type":    "notification",
		"payload": n,
	})
	if err != nil {
		return err
	}
	channel := fmt.Sprintf("notify:%s", userID.String())
	return s.rdb.Publish(ctx, channel, payload).Err()
}

func (s *Service) callPushTargets(ctx context.Context, userID uuid.UUID) ([]callPushTarget, bool, error) {
	if s.pusher == nil || s.pgStore == nil {
		if s.callPushRequired {
			// Fail CLOSED (CALL-LB-4): a deployment that requires call
			// pushes but has no transport must not commit rings as
			// delivered. The startup guard should have refused this
			// configuration; this is defense in depth.
			return nil, false, fmt.Errorf("call push required but push transport is not configured: %w",
				push.ErrProviderNotConfigured)
		}
		// Push transport not configured (dev posture): nothing to deliver,
		// and nothing to fail on.
		return nil, false, nil
	}
	prefs, err := s.pgStore.GetPreferences(ctx, userID)
	if err != nil {
		return nil, false, fmt.Errorf("preferences: %w", err)
	}
	quietStart, quietEnd := "", ""
	if prefs.QuietHoursStart != nil {
		quietStart = *prefs.QuietHoursStart
	}
	if prefs.QuietHoursEnd != nil {
		quietEnd = *prefs.QuietHoursEnd
	}
	if !prefs.PushEnabled || isQuietHours(quietStart, quietEnd) {
		return nil, false, nil
	}
	devices, err := s.pgStore.GetUserDevices(ctx, userID)
	if err != nil {
		return nil, false, fmt.Errorf("devices: %w", err)
	}
	targets := make([]callPushTarget, 0, len(devices))
	for _, d := range devices {
		targets = append(targets, callPushTarget{Token: d.PushToken, Platform: d.Platform})
	}
	return targets, true, nil
}

func (s *Service) sendCallPush(ctx context.Context, target callPushTarget, notifType string, n *scylla.Notification) error {
	title, body := notifTitleBody(notifType)
	data := map[string]string{
		"type":      notifType,
		"entity_id": n.EntityID.String(),
		"deep_link": n.DeepLink,
	}
	if ck := GetCollapseKey(notifType, n.EntityID.String(), n.UserID.String()); ck != "" {
		data["collapse_key"] = ck
	}
	err := s.pusher.Send(ctx, target.Token, target.Platform, title, body, data)
	if err != nil && errors.Is(err, push.ErrProviderNotConfigured) && !s.callPushRequired {
		// Dev posture: a platform without its provider is a loud skip, not
		// a failure. In REQUIRED mode this propagates and keeps the event
		// uncommitted — the fail-closed contract (CALL-LB-4).
		slog.Warn("call push skipped: provider not configured",
			"platform", target.Platform, "err", err)
		return nil
	}
	return err
}

func (s *Service) retireCallDevice(ctx context.Context, userID uuid.UUID, target callPushTarget) error {
	slog.Warn("call push: retiring permanently rejected device",
		"user_id", userID, "platform", target.Platform)
	return s.pgStore.RetireDeviceToken(ctx, userID, target.Token)
}

// SetCallPushRequired makes call-push delivery FAIL CLOSED on any missing
// transport (CALL-LB-4). Wired from CALL_PUSH_REQUIRED at startup; required
// whenever CALLS_ENABLED goes true on call-service.
func (s *Service) SetCallPushRequired(required bool) {
	s.callPushRequired = required
}
