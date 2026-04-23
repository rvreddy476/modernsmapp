package service

import "time"

// NotificationTemplate defines the rendering and behavior rules for a notification type.
type NotificationTemplate struct {
	EventType       string
	TitleTemplate   string        // "{actor} liked your post"
	BodyTemplate    string        // "{post_preview}"
	AggregateTitle  string        // "{count} people liked your post" — empty = never aggregate
	Icon            string        // spark, supernova, comment, echo, mention, group, channel, community, system
	Priority        string        // low | medium | high | critical
	PushEligible    bool
	CanAggregate    bool
	AggregateWindow time.Duration
	OverridePrefs   bool // override user notification preferences
	OverrideMute    bool // override context mute
}

// Templates is the canonical registry of all notification templates keyed by event type.
var Templates = map[string]NotificationTemplate{
	// === Post engagement ===
	"post.liked": {
		EventType: "post.liked", TitleTemplate: "{actor} liked your post",
		BodyTemplate: "{post_preview}", AggregateTitle: "{count} people liked your post",
		Icon: "spark", Priority: "low", PushEligible: false,
		CanAggregate: true, AggregateWindow: 10 * time.Minute,
	},
	"post.super_liked": {
		EventType: "post.super_liked", TitleTemplate: "{actor} super liked your post!",
		BodyTemplate: "{post_preview}", AggregateTitle: "",
		Icon: "supernova", Priority: "medium", PushEligible: true,
		CanAggregate: false,
	},
	"post.commented": {
		EventType: "post.commented", TitleTemplate: "{actor} commented on your post",
		BodyTemplate: "{comment_preview}", AggregateTitle: "{actor} and {count} others commented on your post",
		Icon: "comment", Priority: "medium", PushEligible: true,
		CanAggregate: true, AggregateWindow: 5 * time.Minute,
	},
	"comment.replied": {
		EventType: "comment.replied", TitleTemplate: "{actor} replied to your comment",
		BodyTemplate: "{comment_preview}", AggregateTitle: "{actor} and {count} others replied to your comment",
		Icon: "comment", Priority: "medium", PushEligible: true,
		CanAggregate: true, AggregateWindow: 5 * time.Minute,
	},
	"mention.created": {
		EventType: "mention.created", TitleTemplate: "{actor} mentioned you in {context}",
		BodyTemplate: "{body_preview}", Icon: "mention", Priority: "high",
		PushEligible: true, CanAggregate: false, OverridePrefs: true, OverrideMute: true,
	},
	"post.shared": {
		EventType: "post.shared", TitleTemplate: "{actor} shared your post",
		BodyTemplate: "{post_preview}", AggregateTitle: "{count} people shared your post",
		Icon: "echo", Priority: "medium", PushEligible: true,
		CanAggregate: true, AggregateWindow: 15 * time.Minute,
	},
	"user.followed": {
		EventType: "user.followed", TitleTemplate: "{actor} followed you",
		AggregateTitle: "{count} new followers",
		Icon: "follow", Priority: "medium", PushEligible: true,
		CanAggregate: true, AggregateWindow: 30 * time.Minute,
	},
	"user.friend_request": {
		EventType: "user.friend_request", TitleTemplate: "{actor} wants to join your Circle",
		Icon: "friend", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"user.friend_accepted": {
		EventType: "user.friend_accepted", TitleTemplate: "{actor} accepted your Circle invite",
		Icon: "friend", Priority: "medium", PushEligible: true, CanAggregate: false,
	},

	// === Group ===
	"group.post.published": {
		EventType: "group.post.published", TitleTemplate: "New post in {group}: {preview}",
		AggregateTitle: "{count} new posts in {group}",
		Icon: "group", Priority: "medium", PushEligible: true,
		CanAggregate: true, AggregateWindow: 15 * time.Minute,
	},
	"group.post.submitted": {
		EventType: "group.post.submitted", TitleTemplate: "{actor} submitted a post for approval",
		AggregateTitle: "{count} posts pending in {group}",
		Icon: "group", Priority: "medium", PushEligible: true,
		CanAggregate: true, AggregateWindow: 10 * time.Minute,
	},
	"group.post.approved": {
		EventType: "group.post.approved", TitleTemplate: "Your post in {group} was approved",
		Icon: "group", Priority: "medium", PushEligible: true, CanAggregate: false,
	},
	"group.post.rejected": {
		EventType: "group.post.rejected", TitleTemplate: "Your post in {group} was not approved",
		Icon: "group", Priority: "medium", PushEligible: true, CanAggregate: false,
	},
	"group.announcement": {
		EventType: "group.announcement", TitleTemplate: "{group}: {title}",
		Icon: "announcement", Priority: "high", PushEligible: true,
		CanAggregate: false, OverridePrefs: false, OverrideMute: true,
	},
	"group.member.joined": {
		EventType: "group.member.joined", TitleTemplate: "{actor} joined {group}",
		AggregateTitle: "{count} new members in {group}",
		Icon: "group", Priority: "low", PushEligible: false,
		CanAggregate: true, AggregateWindow: 60 * time.Minute,
	},
	"group.invite.received": {
		EventType: "group.invite.received", TitleTemplate: "{actor} invited you to {group}",
		Icon: "group", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"group.join_request": {
		EventType: "group.join_request", TitleTemplate: "{actor} wants to join {group}",
		AggregateTitle: "{count} pending requests in {group}",
		Icon: "group", Priority: "medium", PushEligible: true,
		CanAggregate: true, AggregateWindow: 10 * time.Minute,
	},
	"group.join_approved": {
		EventType: "group.join_approved", TitleTemplate: "You've been accepted to {group}!",
		Icon: "group", Priority: "medium", PushEligible: true, CanAggregate: false,
	},
	"group.event.created": {
		EventType: "group.event.created", TitleTemplate: "New event in {group}: {title}",
		Icon: "event", Priority: "medium", PushEligible: true, CanAggregate: false,
	},
	"group.event.reminder": {
		EventType: "group.event.reminder", TitleTemplate: "{event} starts in {time}",
		Icon: "event", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"group.poll.created": {
		EventType: "group.poll.created", TitleTemplate: "New poll in {group}: {question}",
		Icon: "poll", Priority: "medium", PushEligible: true, CanAggregate: false,
	},

	// === Channel ===
	"channel.update.published": {
		EventType: "channel.update.published", TitleTemplate: "{channel} posted: {title}",
		AggregateTitle: "{count} new updates from {channel}",
		Icon: "channel", Priority: "medium", PushEligible: true,
		CanAggregate: true, AggregateWindow: 30 * time.Minute,
	},
	"channel.urgent.info": {
		EventType: "channel.urgent.info", TitleTemplate: "{channel}: {title}",
		Icon: "urgent", Priority: "medium", PushEligible: true, CanAggregate: false,
	},
	"channel.urgent.warning": {
		EventType: "channel.urgent.warning", TitleTemplate: "{channel}: {title}",
		Icon: "urgent", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"channel.urgent.critical": {
		EventType: "channel.urgent.critical", TitleTemplate: "{channel}: {title}",
		Icon: "urgent", Priority: "critical", PushEligible: true,
		CanAggregate: false, OverridePrefs: true, OverrideMute: true,
	},
	"channel.event.created": {
		EventType: "channel.event.created", TitleTemplate: "Event from {channel}: {title}",
		Icon: "event", Priority: "medium", PushEligible: true, CanAggregate: false,
	},
	"channel.event.reminder": {
		EventType: "channel.event.reminder", TitleTemplate: "{event} starts in {time}",
		Icon: "event", Priority: "high", PushEligible: true, CanAggregate: false,
	},

	// === Community ===
	"community.post.published": {
		EventType: "community.post.published", TitleTemplate: "{actor} posted in {space} · {community}",
		AggregateTitle: "{count} new in {space}",
		Icon: "community", Priority: "medium", PushEligible: true,
		CanAggregate: true, AggregateWindow: 15 * time.Minute,
	},
	"community.announcement": {
		EventType: "community.announcement", TitleTemplate: "{community}: {title}",
		Icon: "announcement", Priority: "high", PushEligible: true,
		CanAggregate: false, OverrideMute: true,
	},
	"community.mention": {
		EventType: "community.mention", TitleTemplate: "{actor} mentioned you in {community}",
		Icon: "mention", Priority: "high", PushEligible: true,
		CanAggregate: false, OverridePrefs: true, OverrideMute: true,
	},
	"community.answer_accepted": {
		EventType: "community.answer_accepted", TitleTemplate: "Your answer was accepted in {community}",
		Icon: "community", Priority: "medium", PushEligible: true, CanAggregate: false,
	},
	"community.expert_answer": {
		EventType: "community.expert_answer", TitleTemplate: "Expert answered your question",
		Icon: "community", Priority: "medium", PushEligible: true, CanAggregate: false,
	},
	"community.invite": {
		EventType: "community.invite", TitleTemplate: "You're invited to {community}",
		Icon: "community", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"community.join_approved": {
		EventType: "community.join_approved", TitleTemplate: "Welcome to {community}!",
		Icon: "community", Priority: "medium", PushEligible: true, CanAggregate: false,
	},

	// === System ===
	"system.login_alert": {
		EventType: "system.login_alert", TitleTemplate: "New login from {device} in {location}",
		Icon: "security", Priority: "critical", PushEligible: true,
		CanAggregate: false, OverridePrefs: true, OverrideMute: true,
	},
	"system.verification": {
		EventType: "system.verification", TitleTemplate: "Your verification is {status}",
		Icon: "system", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"system.report_result": {
		EventType: "system.report_result", TitleTemplate: "Your report has been reviewed",
		Icon: "system", Priority: "medium", PushEligible: true, CanAggregate: false,
	},

	// === Commerce ===
	"commerce.order.created": {
		EventType: "commerce.order.created", TitleTemplate: "Order {order_number} placed",
		BodyTemplate: "We've received your order. Total: ₹{amount}.",
		Icon: "system", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"commerce.order.paid": {
		EventType: "commerce.order.paid", TitleTemplate: "Payment received for order {order_number}",
		BodyTemplate: "Your payment of ₹{amount} was successful.",
		Icon: "system", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"commerce.order.shipped": {
		EventType: "commerce.order.shipped", TitleTemplate: "Order {order_number} is on its way",
		BodyTemplate: "Tracking: {tracking_number} via {courier}.",
		Icon: "system", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"commerce.order.delivered": {
		EventType: "commerce.order.delivered", TitleTemplate: "Order {order_number} delivered",
		BodyTemplate: "Enjoy! Tap to review.",
		Icon: "system", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"commerce.invoice.issued": {
		EventType: "commerce.invoice.issued", TitleTemplate: "Invoice {invoice_number} issued",
		BodyTemplate: "Your invoice for order {order_number} is ready.",
		Icon: "system", Priority: "medium", PushEligible: false, CanAggregate: false,
	},
	"commerce.seller.new_order": {
		EventType: "commerce.seller.new_order", TitleTemplate: "New order {order_number}",
		BodyTemplate: "You have a new order worth ₹{amount}. Start packing!",
		Icon: "system", Priority: "high", PushEligible: true, CanAggregate: false,
	},
	"commerce.return.requested": {
		EventType: "commerce.return.requested", TitleTemplate: "Return requested for order {order_number}",
		BodyTemplate: "Reason: {reason}",
		Icon: "system", Priority: "medium", PushEligible: true, CanAggregate: false,
	},
}

// GetTemplate returns the template for an event type, or a sensible default.
func GetTemplate(eventType string) NotificationTemplate {
	if t, ok := Templates[eventType]; ok {
		return t
	}
	return NotificationTemplate{
		EventType:    eventType,
		TitleTemplate: "New notification",
		Priority:     "low",
		PushEligible: false,
	}
}
