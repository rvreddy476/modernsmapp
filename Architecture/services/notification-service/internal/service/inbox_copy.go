package service

import (
	"encoding/json"
	"strings"

	"github.com/atpost/notification-service/internal/store/scylla"
)

// Inbox copy: the user-facing "title" and "icon" every Activity-inbox row
// carries on the wire (GET /v1/notifications, SSE replay and the realtime
// notification frame). Stored rows hold only a type and ids, and clients used
// to build the sentence themselves, which left every type they did not know
// reading "You have a new notification" with a "?" avatar. The copy is
// computed here at read time from the stored type and the hydrated actor
// name, so no stored row needs migrating and every client reads the same
// words.
//
// Rules for the wording: short, sentence case, our own words, no exclamation
// marks, never an id. A type with an actor names them ("{actor}"); "Someone"
// stands in when the actor has no resolvable name. System events have no
// actor at all.

// The closed icon set. Clients map each value onto a glyph; adding one is a
// contract change for every client.
const (
	InboxIconSocial      = "social"
	InboxIconMessage     = "message"
	InboxIconCall        = "call"
	InboxIconLive        = "live"
	InboxIconOrder       = "order"
	InboxIconFood        = "food"
	InboxIconHomeService = "home_service"
	InboxIconRide        = "ride"
	InboxIconDating      = "dating"
	InboxIconCommunity   = "community"
	InboxIconQA          = "qa"
	InboxIconAccount     = "account"
	InboxIconSystem      = "system"
)

// InboxIcons lists every icon value a row may carry.
var InboxIcons = []string{
	InboxIconSocial, InboxIconMessage, InboxIconCall, InboxIconLive,
	InboxIconOrder, InboxIconFood, InboxIconHomeService, InboxIconRide,
	InboxIconDating, InboxIconCommunity, InboxIconQA, InboxIconAccount,
	InboxIconSystem,
}

// inboxGenericTitle is for a type nothing below recognises, not even by
// family. It must never be "You have a new notification".
const inboxGenericTitle = "You have an update"

const inboxActorToken = "{actor}"

// inboxCopyEntry is the copy for one exact stored type. Exactly one of title
// and template is set: template names a Templates entry whose TitleTemplate
// is reused as is (so the push registry and the inbox never word the same
// event twice); title is our own sentence. Either may hold {actor} and no
// other placeholder.
type inboxCopyEntry struct {
	title    string
	template string
	icon     string
	// unnamed, when set, is the whole title used when the actor has no
	// resolvable name, for a sentence "someone" cannot fill ("Your {actor}
	// membership has ended"). It holds no placeholder.
	unnamed string
}

func (e inboxCopyEntry) text() string {
	if e.template != "" {
		return Templates[e.template].TitleTemplate
	}
	return e.title
}

func own(title, icon string) inboxCopyEntry { return inboxCopyEntry{title: title, icon: icon} }
func reuse(template, icon string) inboxCopyEntry {
	return inboxCopyEntry{template: template, icon: icon}
}

// ownNamed is own() with the title a nameless actor gets instead.
func ownNamed(title, unnamed, icon string) inboxCopyEntry {
	return inboxCopyEntry{title: title, icon: icon, unnamed: unnamed}
}

// inboxCopyTable covers every type this service writes to the inbox and every
// legacy type still stored on dev (17 Oct inventory), by exact name.
var inboxCopyTable = map[string]inboxCopyEntry{
	// === Social ===
	"follow":                 reuse("user.followed", InboxIconSocial),
	"user.followed":          reuse("user.followed", InboxIconSocial),
	"friend_request":         reuse("user.friend_request", InboxIconSocial),
	"user.friend_request":    reuse("user.friend_request", InboxIconSocial),
	"friend_accepted":        reuse("user.friend_accepted", InboxIconSocial),
	"user.friend_accepted":   reuse("user.friend_accepted", InboxIconSocial),
	"reaction":               own("{actor} reacted to your post", InboxIconSocial),
	"post.liked":             reuse("post.liked", InboxIconSocial),
	"post.super_liked":       own("{actor} super liked your post", InboxIconSocial),
	"comment":                reuse("post.commented", InboxIconSocial),
	"post.commented":         reuse("post.commented", InboxIconSocial),
	"comment.replied":        reuse("comment.replied", InboxIconSocial),
	"comment_reaction":       own("{actor} reacted to your comment", InboxIconSocial),
	"mention":                own("{actor} mentioned you in a post", InboxIconSocial),
	"mention.created":        own("{actor} mentioned you", InboxIconSocial),
	"comment_mention":        own("{actor} mentioned you in a comment", InboxIconSocial),
	"post_reposted":          reuse("post_reposted", InboxIconSocial),
	"post.reposted":          reuse("post.reposted", InboxIconSocial),
	"post.shared":            reuse("post.shared", InboxIconSocial),
	"endorsement":            own("{actor} endorsed you", InboxIconSocial),
	"business_review":        own("{actor} reviewed your business page", InboxIconSocial),
	"new_subscriber":         own("{actor} subscribed to your channel", InboxIconSocial),
	"post_reclassified":      own("Your video was moved to long videos", InboxIconSocial),
	"creator_uploaded_video": own("{actor} uploaded a new video", InboxIconSocial),
	"creator_uploaded_flick": own("{actor} posted a new short", InboxIconSocial),
	CreditTypeRequest:        own("{actor} credited you in a video", InboxIconSocial),
	CreditTypeAccepted:       own("{actor} accepted your credit", InboxIconSocial),

	// === Live ===
	LiveNotifType:  own("{actor} is live now", InboxIconLive),
	"live.started": own("{actor} is live now", InboxIconLive),

	// === Messages ===
	"dm":              own("{actor} sent you a message", InboxIconMessage),
	"message_request": own("{actor} sent you a message request", InboxIconMessage),

	// === Calls === An incoming-call row is the ring itself, which the user
	// may have answered, so it is not called "missed". A missed-call row is
	// written for the caller with no actor.
	"incoming_call":       own("Call from {actor}", InboxIconCall),
	"incoming_video_call": own("Video call from {actor}", InboxIconCall),
	"missed_call":         own("You missed a call", InboxIconCall),

	// === MStore orders, buyer side ===
	"commerce_order_created":   own("Your order was placed", InboxIconOrder),
	"commerce_order_paid":      own("Your payment was received", InboxIconOrder),
	"commerce_order_shipped":   own("Your order has shipped", InboxIconOrder),
	"commerce_invoice_issued":  own("Your invoice is ready", InboxIconOrder),
	OrderTypeConfirmed:         own("Your order is confirmed", InboxIconOrder),
	OrderTypeShipped:           own("Your order has shipped", InboxIconOrder),
	OrderTypeDelivered:         own("Your order was delivered", InboxIconOrder),
	OrderTypeCancelled:         own("Your order was cancelled", InboxIconOrder),
	OrderTypeRefunded:          own("Your refund was issued", InboxIconOrder),
	OrderTypePaymentFailed:     own("Your payment did not go through", InboxIconOrder),
	"commerce.order.created":   own("Your order was placed", InboxIconOrder),
	"commerce.order.paid":      own("Your payment was received", InboxIconOrder),
	"commerce.order.shipped":   own("Your order has shipped", InboxIconOrder),
	"commerce.order.delivered": own("Your order was delivered", InboxIconOrder),
	"commerce.invoice.issued":  own("Your invoice is ready", InboxIconOrder),
	// === MStore orders, seller side ===
	OrderTypeSellerNewOrder:     own("You have a new order", InboxIconOrder),
	"commerce_seller_new_order": own("You have a new order", InboxIconOrder),
	"commerce.seller.new_order": own("You have a new order", InboxIconOrder),
	"commerce.return.requested": own("A buyer asked to return an order", InboxIconOrder),
	// === Commerce delivery partner ===
	"commerce.delivery.offer":                own("You have a new delivery request", InboxIconOrder),
	"commerce.delivery.assigned":             own("You accepted a delivery", InboxIconOrder),
	"commerce.delivery.picked_up":            own("Your pickup was confirmed", InboxIconOrder),
	"commerce.delivery.delivered":            own("Your delivery is complete", InboxIconOrder),
	"commerce.delivery.failed":               own("A delivery needs your attention", InboxIconOrder),
	"commerce.delivery.cancelled":            own("A delivery was cancelled", InboxIconOrder),
	"commerce.delivery.earnings":             own("Your delivery earnings were updated", InboxIconOrder),
	"commerce.delivery.support":              own("Your support request was updated", InboxIconOrder),
	"commerce.delivery.application.reviewed": own("Your delivery partner application was reviewed", InboxIconOrder),
	"commerce.delivery.document.reviewed":    own("Your document was reviewed", InboxIconOrder),

	// === Feast (food) ===
	FoodTypeOrderStatus:      own("Your food order was updated", InboxIconFood),
	FoodTypeOrderNew:         own("Your restaurant has a new order", InboxIconFood),
	FoodTypeDeliveryOffer:    own("You have a new delivery request", InboxIconFood),
	FoodTypeDeliveryAccount:  own("Your delivery partner account was updated", InboxIconFood),
	FoodTypeDeliveryDocument: own("Your document was reviewed", InboxIconFood),
	FoodTypeKitchenAccount:   own("Your restaurant account was updated", InboxIconFood),
	FoodTypeKitchenStatus:    own("An order at your restaurant was updated", InboxIconFood),
	FoodTypeDeliveryStatus:   own("Your delivery was updated", InboxIconFood),

	// === Doorstep (home services), customer side ===
	DoorstepTypeBookingConfirmed:        own("Your booking is confirmed", InboxIconHomeService),
	DoorstepTypeBookingAssigned:         own("A professional was assigned to your booking", InboxIconHomeService),
	DoorstepTypeBookingReassigned:       own("Your booking has a new professional", InboxIconHomeService),
	DoorstepTypeBookingProChanged:       own("Your professional was changed", InboxIconHomeService),
	DoorstepTypeBookingProEnRoute:       own("Your professional is on the way", InboxIconHomeService),
	DoorstepTypeBookingProArrived:       own("Your professional has arrived", InboxIconHomeService),
	DoorstepTypeBookingStarted:          own("Your service has started", InboxIconHomeService),
	DoorstepTypeBookingExtrasProposed:   own("Extra work was proposed for your booking", InboxIconHomeService),
	DoorstepTypeBookingExtrasPaymentDue: own("A payment is due for your booking", InboxIconHomeService),
	DoorstepTypeBookingCompleted:        own("Your service is complete", InboxIconHomeService),
	DoorstepTypeBookingCancelled:        own("Your booking was cancelled", InboxIconHomeService),
	DoorstepTypeBookingExpired:          own("Your booking request expired", InboxIconHomeService),
	DoorstepTypeBookingRefundIssued:     own("Your refund was issued", InboxIconHomeService),
	DoorstepTypeBookingReminder:         own("Your booking is coming up", InboxIconHomeService),
	DoorstepTypeBookingProNoShow:        own("Your professional did not arrive", InboxIconHomeService),
	DoorstepTypeBookingProUnavailable:   own("Your professional is unavailable", InboxIconHomeService),
	DoorstepTypeOutstandingDue:          own("You have an amount due", InboxIconHomeService),
	DoorstepTypeMessageNew:              own("You have a new message about your booking", InboxIconHomeService),
	DoorstepTypeReworkUpdated:           own("Your rework request was updated", InboxIconHomeService),
	// === Doorstep, professional side ===
	DoorstepTypeProOfferNew:                own("You have a new job offer", InboxIconHomeService),
	DoorstepTypeProOfferExpired:            own("A job offer expired", InboxIconHomeService),
	DoorstepTypeProJobCancelled:            own("A job was cancelled", InboxIconHomeService),
	DoorstepTypeProJobRescheduled:          own("A job was rescheduled", InboxIconHomeService),
	DoorstepTypeProJobReminder:             own("You have a job coming up", InboxIconHomeService),
	DoorstepTypeProExtrasApproved:          own("Your extra work was approved", InboxIconHomeService),
	DoorstepTypeProExtrasDeclined:          own("Your extra work was declined", InboxIconHomeService),
	DoorstepTypeProExtrasPaid:              own("Your extra work was paid", InboxIconHomeService),
	DoorstepTypeProMessageNew:              own("You have a new message from a customer", InboxIconHomeService),
	DoorstepTypeProApplicationApproved:     own("Your application was approved", InboxIconHomeService),
	DoorstepTypeProApplicationRejected:     own("Your application was not approved", InboxIconHomeService),
	DoorstepTypeProAccountSuspended:        own("Your account was suspended", InboxIconHomeService),
	DoorstepTypeProAccountReinstated:       own("Your account was reinstated", InboxIconHomeService),
	DoorstepTypeProDocumentReviewed:        own("Your document was reviewed", InboxIconHomeService),
	DoorstepTypeProChangeReviewed:          own("Your change request was reviewed", InboxIconHomeService),
	DoorstepTypeProBackgroundCheckExpiring: own("Your background check expires soon", InboxIconHomeService),
	DoorstepTypeProRatingReceived:          own("You received a new rating", InboxIconHomeService),
	DoorstepTypeProSettlementComputed:      own("Your payout was calculated", InboxIconHomeService),
	DoorstepTypeProPriceReviewed:           own("Your price was reviewed", InboxIconHomeService),
	"doorstep.safety.incident.responder":   own("A safety incident needs your response", InboxIconHomeService),

	// === Mopedu (rides), customer side ===
	RideTypeAssigned:         own("A captain was assigned to your ride", InboxIconRide),
	RideTypeArriving:         own("Your captain is arriving", InboxIconRide),
	RideTypeArrived:          own("Your captain has arrived", InboxIconRide),
	RideTypeStarted:          own("Your ride has started", InboxIconRide),
	RideTypeCompleted:        own("Your ride is complete", InboxIconRide),
	RideTypeCancelled:        own("Your ride was cancelled", InboxIconRide),
	RideTypePaymentPaid:      own("Your ride payment was received", InboxIconRide),
	"rider.safety.sos":       own("We received your SOS alert", InboxIconRide),
	"rider.complaint.raised": own("We received your complaint", InboxIconRide),
	// === Mopedu, captain side ===
	CaptainTypeOffer:                     own("You have a new ride request", InboxIconRide),
	CaptainTypePaymentReceived:           own("You received a ride payment", InboxIconRide),
	CaptainTypeRideAssigned:              own("You were assigned a ride", InboxIconRide),
	CaptainTypeRideCancelled:             own("A ride was cancelled", InboxIconRide),
	CaptainTypeRideCompleted:             own("You completed a ride", InboxIconRide),
	CaptainTypeApproved:                  own("Your captain account was approved", InboxIconRide),
	CaptainTypeUnderReview:               own("Your captain account is under review", InboxIconRide),
	CaptainTypeReviewUpdated:             own("Your captain review was updated", InboxIconRide),
	CaptainTypeRestricted:                own("Your captain account was restricted", InboxIconRide),
	CaptainTypeDocumentExpiring:          own("Your document expires soon", InboxIconRide),
	CaptainTypePaymentReviewRejected:     own("Your subscription payment was not accepted", InboxIconRide),
	CaptainTypeSubscriptionExpiring:      own("Your subscription expires soon", InboxIconRide),
	CaptainTypeSubscriptionExpired:       own("Your subscription has expired", InboxIconRide),
	CaptainTypeSubscriptionRenewed:       own("Your subscription was renewed", InboxIconRide),
	CaptainTypeSubscriptionPaymentFailed: own("Your subscription payment did not go through", InboxIconRide),
	"captain.support.updated":            own("Your support request was updated", InboxIconRide),
	"rider.partner.fraud_flagged":        own("Your captain account was flagged for review", InboxIconRide),

	// === Pulse (dating) ===
	"dating.spark.created":                own("{actor} sent you a spark", InboxIconDating),
	"dating.match.formed":                 own("You matched with {actor}", InboxIconDating),
	"dating.match.first_message":          own("{actor} sent you their first message", InboxIconDating),
	"dating.match.new_message":            own("{actor} sent you a message", InboxIconDating),
	"dating.match.expired":                own("Your match expired", InboxIconDating),
	"dating.match.quiet":                  own("Your conversation has gone quiet", InboxIconDating),
	"dating.vouch.requested":              own("{actor} asked you to vouch for them", InboxIconDating),
	"dating.vouch.accepted":               own("{actor} vouched for you", InboxIconDating),
	"dating.safe_meet.reminder":           own("Your safe meet starts soon", InboxIconDating),
	"dating.safe_meet.missed_check_in":    own("You missed a safe meet check-in", InboxIconDating),
	"dating.safety.panic.acknowledged":    own("Your safety alert was received", InboxIconDating),
	"dating.safety.panic.responder":       own("A member triggered a safety alert", InboxIconDating),
	"dating.safety.panic.trusted_contact": own("{actor} triggered a safety alert", InboxIconDating),
	"dating.safety.location_shared":       own("{actor} is sharing their live location with you", InboxIconDating),
	"dating.report.status_updated":        own("Your report was updated", InboxIconDating),
	"dating.verification.completed":       own("Your verification is complete", InboxIconDating),
	"dating.verification.rejected":        own("Your verification was not approved", InboxIconDating),
	"dating.photo.rejected":               own("One of your photos was not approved", InboxIconDating),
	"dating.premium.payment_failure":      own("Your payment did not go through", InboxIconDating),
	"dating.data_export.ready":            own("Your data export is ready", InboxIconDating),
	DatingScamAlertType:                   own("One of your matches was removed for scam behaviour", InboxIconDating),
	DatingDateCheckinType:                 own("How did your date go?", InboxIconDating),

	// === Q&A ===
	"qa.answer.created":             own("{actor} answered your question", InboxIconQA),
	"qa.answer.best_selected":       own("{actor} chose your answer as the best", InboxIconQA),
	"qa.answer.comment.created":     own("{actor} commented on your answer", InboxIconQA),
	"qa.answer.requested":           own("{actor} asked you to answer a question", InboxIconQA),
	"qa.question.voted":             own("{actor} upvoted your question", InboxIconQA),
	"qa.answer.voted":               own("{actor} upvoted your answer", InboxIconQA),
	"qa.question.pinned":            own("Your question was pinned", InboxIconQA),
	"qa.question.followed.answered": own("{actor} answered a question you follow", InboxIconQA),

	// === Channels, groups and communities ===
	"channel.update.published":  own("{actor} posted an update in a channel you follow", InboxIconCommunity),
	"channel.urgent.info":       own("A channel you follow posted an important update", InboxIconCommunity),
	"channel.urgent.warning":    own("A channel you follow posted a warning", InboxIconCommunity),
	"channel.urgent.critical":   own("A channel you follow posted an urgent alert", InboxIconCommunity),
	"channel.event.created":     own("A channel you follow created an event", InboxIconCommunity),
	"channel.event.reminder":    own("An event from a channel you follow starts soon", InboxIconCommunity),
	"group.post.published":      own("There is a new post in your group", InboxIconCommunity),
	"group.post.submitted":      reuse("group.post.submitted", InboxIconCommunity),
	"group.post.approved":       own("Your group post was approved", InboxIconCommunity),
	"group.post.rejected":       own("Your group post was not approved", InboxIconCommunity),
	"group.announcement":        own("Your group posted an announcement", InboxIconCommunity),
	"group.member.joined":       own("{actor} joined your group", InboxIconCommunity),
	"group.invite.received":     own("{actor} invited you to a group", InboxIconCommunity),
	"group.join_request":        own("{actor} wants to join your group", InboxIconCommunity),
	"group.join_approved":       own("You were accepted into the group", InboxIconCommunity),
	"group.event.created":       own("Your group created an event", InboxIconCommunity),
	"group.event.reminder":      own("An event in your group starts soon", InboxIconCommunity),
	"group.poll.created":        own("Your group started a poll", InboxIconCommunity),
	"community.post.published":  own("{actor} posted in your community", InboxIconCommunity),
	"community.announcement":    own("Your community posted an announcement", InboxIconCommunity),
	"community.mention":         own("{actor} mentioned you in a community", InboxIconCommunity),
	"community.answer_accepted": own("Your answer was accepted", InboxIconCommunity),
	"community.expert_answer":   own("An expert answered your question", InboxIconCommunity),
	"community.invite":          own("You are invited to join a community", InboxIconCommunity),
	"community.join_approved":   own("You were accepted into the community", InboxIconCommunity),

	// === Paid memberships (membership_notify.go) === The creator's notice
	// names the member; the member's notices name the creator, and a creator
	// with no resolvable name is "a creator".
	MembershipTypeNewMember:     own("{actor} joined your membership", InboxIconAccount),
	MembershipTypeJoined:        ownNamed("You're a member of {actor}", "You're a member of a creator", InboxIconAccount),
	MembershipTypeRenewed:       ownNamed("Your {actor} membership was renewed", "Your membership with a creator was renewed", InboxIconAccount),
	MembershipTypeRenewalDue:    ownNamed("Your {actor} membership ends soon — renew to keep access", "Your membership with a creator ends soon — renew to keep access", InboxIconAccount),
	MembershipTypePaymentFailed: ownNamed("Your {actor} membership payment didn't go through", "Your membership payment to a creator didn't go through", InboxIconAccount),
	MembershipTypeEnded:         ownNamed("Your {actor} membership has ended", "Your membership with a creator has ended", InboxIconAccount),

	// === Account ===
	"system.login_alert":   own("There was a new login to your account", InboxIconAccount),
	"system.verification":  own("Your verification status changed", InboxIconAccount),
	"system.report_result": reuse("system.report_result", InboxIconAccount),
}

// inboxFamily is the fallback for a type the table does not name: the
// product area is still known from its prefix, so the row says which.
type inboxFamily struct {
	match func(string) bool
	title string
	icon  string
}

func hasAnyPrefix(s string, prefixes ...string) bool {
	for _, p := range prefixes {
		if strings.HasPrefix(s, p) {
			return true
		}
	}
	return false
}

// inboxFamilies is checked in order; the first match wins. doorstep. comes
// before the order family so nothing in it is read as a shop order.
var inboxFamilies = []inboxFamily{
	{func(t string) bool { return strings.HasPrefix(t, "incoming_") && strings.HasSuffix(t, "call") },
		"{actor} called you", InboxIconCall},
	{func(t string) bool { return strings.HasPrefix(t, "missed_") && strings.HasSuffix(t, "call") },
		"{actor} tried to call you", InboxIconCall},
	{func(t string) bool { return hasAnyPrefix(t, "doorstep.", "doorstep_") },
		"You have a home service update", InboxIconHomeService},
	{func(t string) bool { return hasAnyPrefix(t, "commerce_", "commerce.", "order_", "seller_") },
		"You have an order update", InboxIconOrder},
	{func(t string) bool { return hasAnyPrefix(t, "food_", "food.") },
		"You have a food order update", InboxIconFood},
	{func(t string) bool { return hasAnyPrefix(t, "rider.", "captain.", "ride.", "mopedu") },
		"You have a ride update", InboxIconRide},
	{func(t string) bool { return hasAnyPrefix(t, "dating.", "dating_") },
		"You have a dating update", InboxIconDating},
	{func(t string) bool { return strings.HasPrefix(t, "qa.") },
		"You have a Q&A update", InboxIconQA},
	{func(t string) bool { return hasAnyPrefix(t, "channel.", "community.", "group.") },
		"You have a community update", InboxIconCommunity},
	{func(t string) bool { return strings.HasPrefix(t, "live.") },
		"You have a live video update", InboxIconLive},
	{func(t string) bool { return hasAnyPrefix(t, "system.", "account.", "security.") },
		"You have an account update", InboxIconAccount},
	{func(t string) bool {
		return hasAnyPrefix(t, "post.", "post_", "comment.", "comment_", "user.", "mention")
	},
		"You have new activity", InboxIconSocial},
}

// InboxCopy is the title and icon for one inbox row: exact type first, then
// the product family, then a generic but still honest sentence. actorName is
// the hydrated display name; empty renders "Someone". Pure: no lookups.
func InboxCopy(rowType, actorName string) (title, icon string) {
	if e, ok := inboxCopyTable[rowType]; ok {
		if e.unnamed != "" && strings.TrimSpace(actorName) == "" {
			return e.unnamed, e.icon
		}
		return renderInboxTitle(e.text(), actorName), e.icon
	}
	for _, f := range inboxFamilies {
		if f.match(rowType) {
			return renderInboxTitle(f.title, actorName), f.icon
		}
	}
	return inboxGenericTitle, InboxIconSystem
}

// renderInboxTitle substitutes the actor. A nameless actor is "Someone" at
// the start of the sentence and "someone" inside it.
func renderInboxTitle(text, actorName string) string {
	idx := strings.Index(text, inboxActorToken)
	if idx < 0 {
		return text
	}
	name := strings.TrimSpace(actorName)
	if name == "" {
		name = "someone"
		if idx == 0 {
			name = "Someone"
		}
	}
	return strings.ReplaceAll(text, inboxActorToken, name)
}

// inboxActorName is the name a row's title uses: the hydrated display name,
// else the username, else nothing (the title then says "Someone").
func inboxActorName(n *scylla.Notification) string {
	if n.Actor == nil {
		return ""
	}
	if name := strings.TrimSpace(n.Actor.DisplayName); name != "" {
		return name
	}
	return strings.TrimSpace(n.Actor.Username)
}

// ApplyInboxCopy fills Title and Icon on one row. Call it after actor
// hydration so the title can name the actor.
func ApplyInboxCopy(n *scylla.Notification) {
	n.Title, n.Icon = InboxCopy(n.Type, inboxActorName(n))
}

// notificationFrame is the realtime "notification" frame for one inbox row:
// the row itself, with the same title and icon the list returns. There is
// no actor lookup on the write path, so an actor type reads "Someone ..."
// here where the list names the actor.
func notificationFrame(n *scylla.Notification) ([]byte, error) {
	ApplyInboxCopy(n)
	return json.Marshal(map[string]any{"type": "notification", "payload": n})
}

// ApplyInboxCopyAll fills Title and Icon on every row of a page.
func ApplyInboxCopyAll(items []scylla.Notification) {
	for i := range items {
		ApplyInboxCopy(&items[i])
	}
}
