package service

import "fmt"

// GetCollapseKey returns the FCM collapse_key / APNs thread-id for a notification.
// Notifications with the same collapse key replace each other on the device,
// preventing notification flood when many similar events fire in quick succession.
//
// An empty return means no collapsing — the notification is always shown individually.
func GetCollapseKey(eventType, targetID, recipientID string) string {
	switch eventType {
	// Sparks (likes) on same post collapse into one notification.
	case "post.liked", "post.super_liked":
		return fmt.Sprintf("spark:%s", targetID)

	// Comments on same post collapse.
	case "post.commented", "comment.replied":
		return fmt.Sprintf("comment:%s", targetID)

	// Group activity collapses per group.
	case "group.post.published", "group.member.joined", "group.poll.created",
		"group.post.submitted", "group.post.approved", "group.post.rejected":
		return fmt.Sprintf("group:%s", targetID)

	// Group announcements and invites collapse per group.
	case "group.announcement", "group.invite.received",
		"group.join_request", "group.join_approved":
		return fmt.Sprintf("group:%s:admin", targetID)

	// Channel updates collapse per channel.
	case "channel.update.published":
		return fmt.Sprintf("channel:%s:update", targetID)

	// Urgent channel alerts NEVER collapse (always show individually).
	case "channel.urgent.info", "channel.urgent.warning", "channel.urgent.critical":
		return "" // no collapse

	// Community activity collapses per community.
	case "community.post.published", "community.answer_accepted",
		"community.expert_answer", "community.announcement":
		return fmt.Sprintf("community:%s", targetID)

	// Community admin activity collapses per community.
	case "community.invite", "community.join_approved":
		return fmt.Sprintf("community:%s:admin", targetID)

	// Direct messages collapse per conversation — multiple messages in
	// one DM thread replace each other into a single device notification.
	// targetID is the conversation ID.
	case "dm":
		return fmt.Sprintf("dm:%s", targetID)

	// Message requests collapse per recipient — every pending request
	// (regardless of which conversation it belongs to) folds into one
	// quiet "you have message requests" notification.
	case "message_request":
		return fmt.Sprintf("message_request_batch:%s", recipientID)

	// Followers collapse per recipient — "5 people followed you".
	case "user.followed":
		return fmt.Sprintf("follow:%s", recipientID)

	// Friend requests collapse per recipient.
	case "user.friend_request":
		return fmt.Sprintf("friend_req:%s", recipientID)

	// Friend accepted does not collapse (individual event is important).
	case "user.friend_accepted":
		return ""

	// Mentions NEVER collapse (always show individually).
	case "mention.created", "group.mention", "community.mention":
		return "" // no collapse

	// Event reminders NEVER collapse (each event is distinct).
	case "group.event.reminder", "channel.event.reminder",
		"group.event.created", "channel.event.created":
		return "" // no collapse

	// System/security alerts NEVER collapse.
	case "system.login_alert", "system.verification", "system.report_result":
		return "" // no collapse

	// Endorsements and reviews collapse per entity.
	case "endorsement":
		return fmt.Sprintf("endorse:%s", targetID)
	case "business_review":
		return fmt.Sprintf("review:%s", targetID)

	// New subscribers collapse per channel/creator.
	case "new_subscriber":
		return fmt.Sprintf("subscriber:%s", targetID)

	// Post shares collapse per post.
	case "post.shared":
		return fmt.Sprintf("share:%s", targetID)

	// Default: collapse by event type + target to be safe.
	default:
		return fmt.Sprintf("notif:%s:%s", eventType, targetID)
	}
}
