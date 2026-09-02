package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// DeliveryDecision determines which delivery channels to use for a notification.
type DeliveryDecision struct {
	CreateInbox   bool // write the durable inbox row (gated by the category's inapp_* toggle)
	SendWebSocket bool // send real-time via WS (follows CreateInbox: no invisible realtime events)
	SendPush      bool // send push notification (gated by push_enabled, quiet hours, push_* toggle)
	SendEmail     bool // send email digest/alert
	DeferPush     bool // queue for after quiet hours end
}

// ResolveDelivery implements the preference resolution tree:
//
//  1. Critical events override everything.
//  2. In-app split — the category's inapp_* toggle decides whether the inbox
//     row (and the realtime event announcing it) exists at all.
//  3. Global push toggle — if off, no push for anything.
//  4. Quiet hours — defer push (don't drop) unless critical/system.
//  5. Per-category push toggle — check the specific event type toggle.
//  6. Context mute — if muted, suppress everything unless template overrides.
//
// Mention and announcement overrides are applied after the main chain.
func ResolveDelivery(ctx context.Context, db *pgxpool.Pool, rdb *redis.Client, recipientID, eventType, contextType, contextID string, isMuted bool) DeliveryDecision {
	prefs, err := postgres.QueryNotificationPreferences(ctx, db, recipientID)
	if err != nil || prefs == nil {
		slog.Warn("preference resolution: failed to load prefs, using defaults",
			"user", recipientID, "error", err)
		prefs = postgres.DefaultNotificationPreferences(recipientID)
	}
	return resolveDecision(eventType, prefs, isMuted)
}

// resolveDecision is the pure, unit-testable core of ResolveDelivery.
func resolveDecision(eventType string, prefs *postgres.NotificationPreferences, isMuted bool) DeliveryDecision {
	template, known := Templates[eventType]
	if !known {
		template = GetTemplate(eventType)
		// Legacy short types ("reaction", "dm", "missed_call", QA/dating/
		// wallet/... event names) predate the template registry and were
		// ALWAYS pushed by the old createNotification path. The registry
		// only restricts push eligibility for types it actually knows —
		// an unknown type stays push-eligible so wiring the resolver into
		// createNotification cannot silently drop whole domains.
		template.PushEligible = true
	}

	decision := DeliveryDecision{
		CreateInbox:   true,
		SendWebSocket: true,
		SendPush:      template.PushEligible,
		SendEmail:     false,
	}

	// Critical events override everything — always push, always inbox.
	if template.Priority == "critical" {
		decision.SendPush = true
		return decision
	}

	// In-app split: the category's inapp_* toggle controls the inbox row.
	// Realtime follows it — a WS event for a notification that has no inbox
	// row would be a ghost the client can never fetch again.
	if !inappCategoryAllowed(prefs, eventType) {
		decision.CreateInbox = false
		decision.SendWebSocket = false
	}

	// Global push toggle + quiet hours (shared with the call path).
	if decision.SendPush {
		allowed, deferred := masterPushAllowed(prefs)
		// Security/system alerts ignore quiet hours.
		if deferred && template.EventType == "system.login_alert" {
			allowed, deferred = true, false
		}
		decision.SendPush = allowed
		decision.DeferPush = deferred
	}

	// Per-category push toggle.
	if decision.SendPush {
		decision.SendPush = pushCategoryAllowed(prefs, eventType)
	}

	// Context mute check.
	if isMuted && !template.OverrideMute {
		decision.CreateInbox = false
		decision.SendWebSocket = false
		decision.SendPush = false
		decision.DeferPush = false
		return decision
	}

	// Mention/announcement overrides — re-enable delivery channels.
	if template.OverridePrefs {
		decision.SendPush = template.PushEligible
		decision.SendWebSocket = true
		decision.CreateInbox = true
	}
	if template.OverrideMute {
		decision.CreateInbox = true
		decision.SendWebSocket = true
	}

	// Email for high/critical priority when email is enabled.
	if prefs.EmailEnabled && (template.Priority == "high" || template.Priority == "critical") {
		decision.SendEmail = true
	}

	return decision
}

// masterPushAllowed is the ONE master gate for device push: the global
// push_enabled toggle, then quiet hours (which defer rather than drop).
// Category toggles are deliberately not consulted here — this is exactly the
// check the call path needs (calls are push-eligible regardless of category
// toggles; only the master switch and quiet hours may silence a ring).
func masterPushAllowed(p *postgres.NotificationPreferences) (allowed, deferred bool) {
	if !p.PushEnabled {
		return false, false
	}
	if p.QuietHoursEnabled && isInQuietHours(p) {
		return false, true
	}
	return true, false
}

// prefCategory is the TikTok-style preference bucket an event type belongs to.
type prefCategory int

const (
	catDefault prefCategory = iota // no toggle for this type — always delivered
	catAlwaysOn                    // time-critical: never category-gated (calls)
	catLikes
	catSuperLikes
	catComments
	catReplies
	catMentions
	catFollows
	catFriendRequests
	catGroupPosts
	catGroupMentions
	catChannelUpdates
	catChannelUrgent
	catCommunityPosts
	catCommunityMentions
	catEventReminders
	catSystem
	catReposts
	catLive
	catMessages
)

// categoryForEvent maps every event type this service delivers — both the
// dotted registry names and the legacy short names the Kafka consumers emit —
// onto one preference category. Unknown types map to catDefault (delivered).
func categoryForEvent(eventType string) prefCategory {
	switch eventType {
	// Likes: post reactions and reactions on your comment.
	case "post.liked", "reaction", "comment_reaction":
		return catLikes
	case "post.super_liked":
		return catSuperLikes
	case "post.commented", "comment":
		return catComments
	case "comment.replied":
		return catReplies
	// Mentions and tags.
	case "mention.created", "group.mention", "community.mention", "mention", "comment_mention":
		return catMentions
	// Reposts: "post.shared" moves here from the follows toggle — a share IS
	// a repost, and the Reposts toggle is what TikTok-parity users expect to
	// control it (both default TRUE, so no effective change for defaults).
	case "post_reposted", "post.reposted", "post.shared":
		return catReposts
	case "user.followed", "follow":
		return catFollows
	case "user.friend_request", "user.friend_accepted", "friend_request", "friend_accepted":
		return catFriendRequests
	case "group.post.published", "group.post.submitted", "group.post.approved", "group.post.rejected",
		"group.announcement", "group.member.joined", "group.invite.received",
		"group.join_request", "group.join_approved", "group.poll.created":
		return catGroupPosts
	case "group.event.created", "group.event.reminder":
		return catEventReminders
	case "channel.update.published", "channel.event.created", "channel.event.reminder":
		return catChannelUpdates
	case "channel.urgent.info", "channel.urgent.warning", "channel.urgent.critical":
		return catChannelUrgent
	case "community.post.published", "community.answer_accepted", "community.expert_answer",
		"community.invite", "community.join_approved", "community.announcement":
		return catCommunityPosts
	case "system.login_alert", "system.verification", "system.report_result":
		return catSystem
	// LIVE.
	case "creator_went_live", "live.started":
		return catLive
	// Messages: DMs and message requests share one toggle (TikTok parity).
	case "dm", "message_request":
		return catMessages
	// Calls are time-critical: a missed-call notice the user asked the app
	// not to show would hide that a human tried to reach them. Only the
	// master push toggle and quiet hours apply — never a category toggle.
	case "missed_call":
		return catAlwaysOn
	}
	if len(eventType) > 5 && eventType[:5] == "live." {
		return catLive
	}
	return catDefault
}

// pushCategoryAllowed checks whether the per-category push toggle allows this
// event type.
func pushCategoryAllowed(p *postgres.NotificationPreferences, eventType string) bool {
	switch categoryForEvent(eventType) {
	case catLikes:
		return p.PushLikes
	case catSuperLikes:
		return p.PushSuperLikes
	case catComments:
		return p.PushComments
	case catReplies:
		return p.PushReplies
	case catMentions:
		return p.PushMentions
	case catFollows:
		return p.PushFollows
	case catFriendRequests:
		return p.PushFriendRequests
	case catGroupPosts:
		return p.PushGroupPosts
	case catGroupMentions:
		return p.PushGroupMentions
	case catChannelUpdates:
		return p.PushChannelUpdates
	case catChannelUrgent:
		return p.PushChannelUrgent
	case catCommunityPosts:
		return p.PushCommunityPosts
	case catCommunityMentions:
		return p.PushCommunityMentions
	case catEventReminders:
		return p.PushEventReminders
	case catSystem:
		return p.PushSystem
	case catReposts:
		return p.PushReposts
	case catLive:
		return p.PushLive
	case catMessages:
		return p.PushMessages
	default: // catDefault, catAlwaysOn
		return true
	}
}

// inappCategoryAllowed checks whether the per-category in-app toggle allows
// the inbox row + realtime event for this event type.
func inappCategoryAllowed(p *postgres.NotificationPreferences, eventType string) bool {
	switch categoryForEvent(eventType) {
	case catLikes:
		return p.InappLikes
	case catSuperLikes:
		return p.InappSuperLikes
	case catComments:
		return p.InappComments
	case catReplies:
		return p.InappReplies
	case catMentions:
		return p.InappMentions
	case catFollows:
		return p.InappFollows
	case catFriendRequests:
		return p.InappFriendRequests
	case catGroupPosts:
		return p.InappGroupPosts
	case catGroupMentions:
		return p.InappGroupMentions
	case catChannelUpdates:
		return p.InappChannelUpdates
	case catChannelUrgent:
		return p.InappChannelUrgent
	case catCommunityPosts:
		return p.InappCommunityPosts
	case catCommunityMentions:
		return p.InappCommunityMentions
	case catEventReminders:
		return p.InappEventReminders
	case catSystem:
		return p.InappSystem
	case catReposts:
		return p.InappReposts
	case catLive:
		return p.InappLive
	case catMessages:
		return p.InappMessages
	default: // catDefault, catAlwaysOn
		return true
	}
}

// isInQuietHours checks whether the current moment falls within the user's quiet window.
// Handles cross-midnight ranges (e.g. 22:00–07:00).
func isInQuietHours(p *postgres.NotificationPreferences) bool {
	if p.QuietHoursStart == nil || p.QuietHoursEnd == nil {
		return false
	}

	tz := "UTC"
	if p.QuietHoursTZ != nil {
		tz = *p.QuietHoursTZ
	}
	loc, err := time.LoadLocation(tz)
	if err != nil {
		loc = time.UTC
	}

	now := time.Now().In(loc)
	h, m, _ := now.Clock()
	nowMins := h*60 + m

	startMins := parseTimeMins(*p.QuietHoursStart)
	endMins := parseTimeMins(*p.QuietHoursEnd)

	if startMins < 0 || endMins < 0 {
		return false
	}

	if startMins < endMins {
		// Same-day range (e.g. 08:00–17:00).
		return nowMins >= startMins && nowMins < endMins
	}
	// Cross-midnight range (e.g. 22:00–07:00).
	return nowMins >= startMins || nowMins < endMins
}

// parseTimeMins parses "HH:MM" into minutes since midnight. Returns -1 on failure.
func parseTimeMins(s string) int {
	if len(s) < 5 {
		return -1
	}
	t, err := time.Parse("15:04", s[:5])
	if err != nil {
		return -1
	}
	return t.Hour()*60 + t.Minute()
}
