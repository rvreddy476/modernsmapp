package service

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/atpost/shared/mailer"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Service struct {
	scyllaStore *scylla.NotificationStore
	pgStore     *postgres.Store
	rdb         *redis.Client
	pusher      push.Pusher
	mail        mailer.Mailer

	// callPushRequired makes the CALL delivery path fail closed on any
	// missing push transport (CALL-LB-4). Set via SetCallPushRequired.
	callPushRequired bool

	// Actor hydration (identity-profile batch contract). Empty URL
	// disables it; nil client falls back to http.DefaultClient.
	profileServiceURL string
	profileClient     *http.Client
}

func New(scyllaStore *scylla.NotificationStore, rdb *redis.Client) *Service {
	return &Service{
		scyllaStore: scyllaStore,
		rdb:         rdb,
	}
}

func (s *Service) SetPGStore(pg *postgres.Store) {
	s.pgStore = pg
}

// SetPusher sets the push notification dispatcher.
func (s *Service) SetPusher(p push.Pusher) {
	s.pusher = p
}

// SetMailer wires the transactional email transport.
func (s *Service) SetMailer(m mailer.Mailer) {
	s.mail = m
}

// SendEmail renders an HTML template via mailer.Render and dispatches it.
// Safe no-op when mailer is unconfigured or recipient is empty.
func (s *Service) SendEmail(ctx context.Context, to, htmlTemplate string, data any) error {
	if s.mail == nil || to == "" {
		return nil
	}
	subject, body, err := mailer.Render(htmlTemplate, data)
	if err != nil {
		return err
	}
	return s.mail.Send(ctx, mailer.Message{
		To: []string{to}, Subject: subject, HTMLBody: body,
	})
}

// CreateNotification creates a notification with a fresh random identity.
// Use CreateNotificationIdempotent for any at-least-once path (retries,
// resumable batches) where a duplicate row would be user-visible.
func (s *Service) CreateNotification(ctx context.Context, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time) error {
	return s.createNotification(ctx, userID, actorID, notifType, entityType, entityID, deepLink, createdAt, "", false)
}

// CreateNotificationWithoutPush writes the durable inbox row and publishes
// realtime, but sends NO device push.
//
// This is what a per-conversation mute means: the conversation is not hidden
// and the notification is still recorded for when the user looks — the device
// simply stays quiet. The mute itself is chat's state and arrives on the
// event, because this service must not reach into chat's tables to ask.
func (s *Service) CreateNotificationWithoutPush(ctx context.Context, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time) error {
	return s.createNotification(ctx, userID, actorID, notifType, entityType, entityID, deepLink, createdAt, "", true)
}

// CreateNotificationIdempotent writes the inbox row under a deterministic
// identity (Module 1 fixes-v1/v2, Codex P0-1).
//
// GUARANTEE, stated precisely:
//
//   - Inbox row: EXACTLY ONE per identity. The insert is a lightweight
//     transaction (`IF NOT EXISTS`), so a retry neither duplicates the row
//     nor rewrites mutable state — in particular it can no longer reset
//     `is_read` on a notification the user has already read.
//   - Realtime publish and device push: AT MOST ONCE. They run only on the
//     attempt that actually created the row. A crash between the row
//     insert and the transport therefore drops that transport for that
//     recipient; the durable inbox row still exists and appears on the
//     next fetch. This matches how the rest of the service already treats
//     Redis/push (best-effort transports over a durable inbox) and is a
//     deliberate trade: re-firing them would spam a user who has already
//     been notified.
//   - Clients additionally de-duplicate on the deterministic
//     notification_id, and push carries a collapse key.
//
// `identity` must be stable for the logical delivery — the subscriber
// fan-out uses "<post_id>:<user_id>:<type>".
func (s *Service) CreateNotificationIdempotent(ctx context.Context, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time, identity string) error {
	return s.createNotification(ctx, userID, actorID, notifType, entityType, entityID, deepLink, createdAt, identity, false)
}

// resolveGeneralDelivery consults the user's channel preferences (in-app vs
// push, per category, master toggle, quiet hours) for one notification.
// Without a Postgres store there are no preferences to consult, so every
// channel stays on — the pre-existing dev/test posture.
func (s *Service) resolveGeneralDelivery(ctx context.Context, userID uuid.UUID, notifType string) DeliveryDecision {
	if s.pgStore == nil || s.pgStore.Pool() == nil {
		return DeliveryDecision{CreateInbox: true, SendWebSocket: true, SendPush: true}
	}
	return ResolveDelivery(ctx, s.pgStore.Pool(), s.rdb, userID.String(), notifType, "", "", false)
}

func (s *Service) createNotification(ctx context.Context, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time, identity string, suppressPush bool) error {
	// 0. Preferences FIRST. Previously this path stored + published
	// unconditionally and only checked the master push toggle at the very
	// end, so per-category and in-app toggles were dead letters.
	decision := s.resolveGeneralDelivery(ctx, userID, notifType)
	if suppressPush {
		// Per-conversation mute (CreateNotificationWithoutPush): inbox and
		// realtime per the decision, device stays quiet.
		decision.SendPush = false
		decision.DeferPush = false
	}
	return s.deliverWithDecision(ctx, decision, userID, actorID, notifType, entityType, entityID, deepLink, createdAt, identity)
}

// deliverWithDecision applies an already-resolved DeliveryDecision. Split
// from createNotification so the channel gating is unit-testable without
// live stores.
func (s *Service) deliverWithDecision(ctx context.Context, decision DeliveryDecision, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time, identity string) error {
	if !decision.CreateInbox && !decision.SendWebSocket && !decision.SendPush {
		return nil
	}

	id := uuid.New()

	// 1. Save to Scylla (Inbox) — only when the category's in-app toggle
	// allows it. With in-app off but push on, the device is still told (a
	// TikTok-style "push only" category) and nothing lands in the inbox.
	n := &scylla.Notification{
		UserID:         userID,
		NotificationID: id,
		Type:           notifType,
		ActorUserID:    actorID,
		EntityType:     entityType,
		EntityID:       entityID,
		DeepLink:       deepLink,
		IsRead:         false,
		CreatedAt:      createdAt,
	}
	if identity != "" {
		// Deterministic clustering key + id ⇒ the insert is an upsert.
		n.TS = scylla.DeterministicTS(createdAt, identity)
		n.NotificationID = scylla.DeterministicNotificationID(identity)
		id = n.NotificationID
	}

	if decision.CreateInbox {
		if identity != "" {
			// Idempotent path: conditional insert. `applied=false` means this
			// notification already exists, so every downstream side effect
			// below must be skipped — otherwise a retry re-publishes realtime
			// and re-sends push for a notification the user already has.
			applied, err := s.scyllaStore.CreateNotificationIfNotExists(ctx, n)
			if err != nil {
				return fmt.Errorf("failed to create notification in scylla: %w", err)
			}
			if !applied {
				return nil
			}
		} else if err := s.scyllaStore.CreateNotification(ctx, n); err != nil {
			return fmt.Errorf("failed to create notification in scylla: %w", err)
		}
	}
	// NOTE: with the inbox row skipped (in-app off, push on) the idempotent
	// path loses its `applied` dedup gate, so a redelivered event may push
	// again. The collapse key bounds that to a device-side replace.

	// 2. Push to Redis (Realtime) — follows the in-app decision.
	// Channel: notify:{user_id}
	if decision.SendWebSocket {
		channel := fmt.Sprintf("notify:%s", userID.String())
		payload, _ := json.Marshal(map[string]interface{}{
			"type":    "notification",
			"payload": n,
		})
		if err := s.rdb.Publish(ctx, channel, payload).Err(); err != nil {
			// Log error but don't fail the operation
			fmt.Printf("failed to publish to redis: %v\n", err)
		}
	}

	// 3. Device push — the decision already applied the master toggle,
	// quiet hours (DeferPush ⇒ SendPush=false; there is no defer queue yet,
	// the inbox row is what the user finds after quiet hours) and the
	// category's push_* toggle.
	if !decision.SendPush {
		return nil
	}
	if s.pusher != nil && s.pgStore != nil {
		tokens, err := s.pgStore.GetUserDevices(ctx, userID)
		if err == nil && len(tokens) > 0 {
			title, body := notifTitleBody(notifType)
			// entity_id and deep_link ride the data payload so the
			// client can open the exact destination from a tap —
			// including background taps, where FCM hands these keys
			// to the launch intent as extras. No message content is
			// ever included here: chat pushes stay generic by
			// construction, which is what keeps previews and lock
			// screens privacy-safe regardless of client settings.
			pushData := map[string]string{
				"type":      notifType,
				"entity_id": entityID.String(),
				"deep_link": deepLink,
			}
			// Compute collapse key so repeated notifications (e.g. many likes)
			// replace each other on the device instead of flooding.
			if ck := GetCollapseKey(notifType, entityID.String(), userID.String()); ck != "" {
				pushData["collapse_key"] = ck
			}
			for _, t := range tokens {
				if err := s.pusher.Send(ctx, t.PushToken, t.Platform, title, body, pushData); err != nil {
					slog.Warn("push: send failed", "error", err, "platform", t.Platform)
				}
			}
		}
	}

	return nil
}

// notifTitleBody returns a human-readable title and body for a notification type.
func notifTitleBody(notifType string) (string, string) {
	switch notifType {
	case "follow":
		return "New Follower", "Someone followed you"
	case "reaction":
		return "New Reaction", "Someone reacted to your post"
	case "comment":
		return "New Comment", "Someone commented on your post"
	case "comment_reaction":
		return "New Reaction", "Someone reacted to your comment"
	case "friend_request":
		return "Friend Request", "You have a new friend request"
	case "friend_accepted":
		return "Friend Request Accepted", "Your friend request was accepted"
	case "endorsement":
		return "New Endorsement", "Someone endorsed you"
	case "business_review":
		return "New Review", "Your business page has a new review"
	case "new_subscriber":
		return "New Subscriber", "Someone subscribed to your content"
	case "mention":
		return "You were mentioned", "Someone mentioned you in a post"
	case "qa.answer.created":
		return "New Answer", "Someone answered your question"
	case "qa.answer.best_selected":
		return "Answer Accepted", "Your answer was accepted"
	case "qa.answer.comment.created":
		return "New Comment", "Someone commented on your answer"
	case "qa.answer.requested":
		return "Answer Requested", "Someone asked you to answer"
	case "qa.question.voted":
		return "New Upvote", "Your question was upvoted"
	case "qa.answer.voted":
		return "New Upvote", "Your answer was upvoted"
	case "qa.question.pinned":
		return "Question Pinned", "Your question was pinned"
	case "dm":
		return "New Message", "You have a new message"
	case "message_request":
		return "Message Request", "You have a new message request"
	case "post_reposted":
		return "New Repost", "Someone reposted your post"
	case "creator_went_live":
		return "LIVE now", "Someone you follow just went live"
	case "missed_call":
		return "Missed Call", "You missed a call"
	default:
		return "New Notification", "You have a new notification"
	}
}

// isQuietHours returns true if `now` is within the user's quiet hours
// window. Start + end are "HH:MM" 24-hour strings in the user's local
// time. A wrap-around window (e.g. 22:00–07:00) is supported by checking
// `now >= start OR now < end` when start > end; non-wrap windows use
// the obvious inclusive-of-start / exclusive-of-end check.
//
// Quiet hours suppress PUSH only; in-app notifications still write
// to the user's inbox so they see them when they next open the app.
//
// MS2: implementation landed; previously `return false` so quiet
// hours were silently ignored.
func isQuietHours(start, end string) bool {
	return isQuietHoursAt(start, end, time.Now())
}

// isQuietHoursAt is the testable form — pass an explicit time.
func isQuietHoursAt(start, end string, now time.Time) bool {
	if start == "" || end == "" {
		return false
	}
	startMin, ok := parseHHMM(start)
	if !ok {
		return false
	}
	endMin, ok := parseHHMM(end)
	if !ok {
		return false
	}
	if startMin == endMin {
		return false
	}
	nowMin := now.Hour()*60 + now.Minute()
	if startMin < endMin {
		// Non-wrap: e.g. 13:00 → 15:00.
		return nowMin >= startMin && nowMin < endMin
	}
	// Wrap-around: e.g. 22:00 → 07:00 means "any time after 22:00
	// OR before 07:00". This is the more common configuration.
	return nowMin >= startMin || nowMin < endMin
}

func parseHHMM(s string) (int, bool) {
	if len(s) != 5 || s[2] != ':' {
		return 0, false
	}
	h, err1 := strconv.Atoi(s[:2])
	m, err2 := strconv.Atoi(s[3:])
	if err1 != nil || err2 != nil {
		return 0, false
	}
	if h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, false
	}
	return h*60 + m, true
}

// NotificationsPage holds a page of notifications with a cursor for the next page.
type NotificationsPage struct {
	Items      []scylla.Notification `json:"items"`
	NextCursor string                `json:"next_cursor,omitempty"`
}

// GetNotifications returns notifications without cursor (legacy).
func (s *Service) GetNotifications(ctx context.Context, userID uuid.UUID, limit int) ([]scylla.Notification, error) {
	return s.scyllaStore.GetNotifications(ctx, userID, limit)
}

// GetNotificationsAfter is the forward-walking cursor query used by
// the SSE Last-Event-ID replay path. cursorBucket+cursorTS identify
// the last event the client saw; this returns everything newer in
// chronological order, capped at `limit` (default 500 — README §13).
func (s *Service) GetNotificationsAfter(ctx context.Context, userID uuid.UUID, cursorBucket int, cursorTS gocql.UUID, limit int) ([]scylla.Notification, error) {
	return s.scyllaStore.GetNotificationsAfter(ctx, userID, cursorBucket, cursorTS, limit)
}

// GetNotificationsPage returns a cursor-paginated page of notifications.
// Cursor format: "bucket:timeuuid" (e.g. "202603:550e8400-e29b-41d4-a716-446655440000").
// When category is non-empty and recognized (currently only "qa"), notifications
// are filtered server-side by notification-type prefix. The function over-fetches
// from Scylla while filtering so the visible page still respects `limit`.
func (s *Service) GetNotificationsPage(ctx context.Context, userID uuid.UUID, limit int, cursor, category string) (*NotificationsPage, error) {
	var cursorBucket int
	var cursorTS *gocql.UUID

	if cursor != "" {
		parts := splitCursor(cursor)
		if len(parts) == 2 {
			if b, err := strconv.Atoi(parts[0]); err == nil {
				cursorBucket = b
			}
			if ts, err := gocql.ParseUUID(parts[1]); err == nil {
				cursorTS = &ts
			}
		}
	}

	// Determine an optional type-prefix filter from the category.
	prefix := categoryTypePrefix(category)

	// Fetch up to limit+1 normally; when filtering, over-fetch (cap at 200)
	// so the page still has a chance of returning `limit` items.
	fetchLimit := limit + 1
	if prefix != "" {
		fetchLimit = limit * 4
		if fetchLimit > 200 {
			fetchLimit = 200
		}
	}

	notifs, err := s.scyllaStore.GetNotificationsWithCursor(ctx, userID, cursorBucket, cursorTS, fetchLimit)
	if err != nil {
		return nil, err
	}

	if prefix != "" {
		filtered := notifs[:0]
		for _, n := range notifs {
			if strings.HasPrefix(n.Type, prefix) {
				filtered = append(filtered, n)
			}
		}
		notifs = filtered
	}

	page := &NotificationsPage{}
	if len(notifs) > limit {
		page.Items = notifs[:limit]
		last := notifs[limit-1]
		page.NextCursor = fmt.Sprintf("%d:%s", last.Bucket, last.TS.String())
	} else {
		page.Items = notifs
	}

	return page, nil
}

// categoryTypePrefix maps a category query param to a notification-type prefix.
// Returns "" when the category is empty/"all" or unknown — in which case no
// filtering is applied (preserves existing behavior for other tabs).
func categoryTypePrefix(category string) string {
	switch category {
	case "qa":
		return "qa."
	default:
		return ""
	}
}

// splitCursor splits "bucket:timeuuid" on the first colon.
func splitCursor(cursor string) []string {
	idx := -1
	for i, c := range cursor {
		if c == ':' {
			idx = i
			break
		}
	}
	if idx < 0 {
		return nil
	}
	return []string{cursor[:idx], cursor[idx+1:]}
}

// MarkRead marks a single notification as read and decrements the unread counter.
func (s *Service) MarkRead(ctx context.Context, userID uuid.UUID, bucket int, ts string) error {
	tsUUID, err := gocql.ParseUUID(ts)
	if err != nil {
		return err
	}
	if err := s.scyllaStore.MarkRead(ctx, userID, bucket, tsUUID); err != nil {
		return err
	}
	// Decrement unread counter in Redis
	key := fmt.Sprintf("unread:%s", userID.String())
	s.rdb.Decr(ctx, key)
	return nil
}

// GetUnreadCount returns the count of unread notifications from Redis.
func (s *Service) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int64, error) {
	key := fmt.Sprintf("unread:%s", userID.String())
	count, err := s.rdb.Get(ctx, key).Int64()
	if err != nil {
		if err.Error() == "redis: nil" {
			// Recompute from Scylla
			notifs, err := s.scyllaStore.GetNotifications(ctx, userID, 100)
			if err != nil {
				return 0, err
			}
			var unread int64
			for _, n := range notifs {
				if !n.IsRead {
					unread++
				}
			}
			s.rdb.Set(ctx, key, unread, time.Minute)
			return unread, nil
		}
		return 0, err
	}
	return count, nil
}

// MarkAllRead marks all notifications as read for a user.
func (s *Service) MarkAllRead(ctx context.Context, userID uuid.UUID) error {
	notifs, err := s.scyllaStore.GetNotifications(ctx, userID, 200)
	if err != nil {
		return err
	}
	for _, n := range notifs {
		if !n.IsRead {
			if err := s.scyllaStore.MarkRead(ctx, userID, n.Bucket, n.TS); err != nil {
				log.Printf("Warning: failed to mark notification read: %v", err)
			}
		}
	}
	// Reset unread counter
	key := fmt.Sprintf("unread:%s", userID.String())
	s.rdb.Set(ctx, key, 0, time.Minute)
	return nil
}

// DeleteNotification removes a notification.
func (s *Service) DeleteNotification(ctx context.Context, userID uuid.UUID, bucket int, ts string) error {
	tsUUID, err := gocql.ParseUUID(ts)
	if err != nil {
		return err
	}
	return s.scyllaStore.DeleteNotification(ctx, userID, bucket, tsUUID)
}

// GetPreferences returns legacy notification preferences for a user.
func (s *Service) GetPreferences(ctx context.Context, userID uuid.UUID) (*postgres.NotificationPreferencesLegacy, error) {
	if s.pgStore == nil {
		return &postgres.NotificationPreferencesLegacy{UserID: userID, EmailEnabled: true, PushEnabled: true}, nil
	}
	return s.pgStore.GetPreferences(ctx, userID)
}

// UpdatePreferences updates legacy notification preferences for a user.
func (s *Service) UpdatePreferences(ctx context.Context, prefs *postgres.NotificationPreferencesLegacy) error {
	if s.pgStore == nil {
		return fmt.Errorf("PG store not configured")
	}
	return s.pgStore.UpsertPreferences(ctx, prefs)
}

// RegisterDevice registers a push notification device.
func (s *Service) RegisterDevice(ctx context.Context, userID uuid.UUID, platform, pushToken string) (*postgres.UserDevice, error) {
	if s.pgStore == nil {
		return nil, fmt.Errorf("PG store not configured")
	}
	return s.pgStore.RegisterDevice(ctx, userID, platform, pushToken)
}

// UnregisterDevice removes a registered device.
func (s *Service) UnregisterDevice(ctx context.Context, deviceID, userID uuid.UUID) error {
	if s.pgStore == nil {
		return fmt.Errorf("PG store not configured")
	}
	return s.pgStore.UnregisterDevice(ctx, deviceID, userID)
}

// GetNotifPreferences returns the granular v2 notification preferences for a user.
func (s *Service) GetNotifPreferences(ctx context.Context, userID string) (*postgres.NotificationPreferences, error) {
	if s.pgStore == nil {
		return nil, fmt.Errorf("PG store not configured")
	}
	return s.pgStore.GetNotificationPreferences(ctx, userID)
}

// UpdateNotifPreferences updates the granular v2 notification preferences for a user.
func (s *Service) UpdateNotifPreferences(ctx context.Context, prefs *postgres.NotificationPreferences) error {
	if s.pgStore == nil {
		return fmt.Errorf("PG store not configured")
	}
	return s.pgStore.UpdateNotificationPreferences(ctx, prefs)
}

// DeleteNotificationsForUser removes all notifications for the given user (GDPR erasure).
func (s *Service) DeleteNotificationsForUser(ctx context.Context, userID uuid.UUID) error {
	return s.scyllaStore.DeleteNotificationsForUser(ctx, userID)
}

// DeactivateDeviceTokens deactivates all push-notification device tokens for the given user (GDPR erasure).
func (s *Service) DeactivateDeviceTokens(ctx context.Context, userID uuid.UUID) error {
	if s.pgStore == nil {
		return fmt.Errorf("PG store not configured")
	}
	return s.pgStore.DeactivateDeviceTokens(ctx, userID)
}
