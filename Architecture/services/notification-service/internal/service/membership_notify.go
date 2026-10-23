package service

// Paid memberships (phase 1 payments contract, pinned 2026-10-23, "Events").
//
// monetization-service publishes the membership life cycle on
// monetization.events (shared JSON envelope, keyed by event id). The consumer
// (events/membership_consumer.go) turns each event into MembershipNotification
// values; this file renders and delivers them through the ordinary resolution
// path (suppression, the `memberships` preference category, master toggle,
// quiet hours), under an identity of (event id, recipient, type) so a
// redelivered event cannot notify twice:
//
//	membership.started        -> membership_new_member     to the CREATOR (actor: member), push
//	                          -> membership_joined         to the MEMBER  (actor: creator), push
//	membership.renewed        -> membership_renewed        to the MEMBER, in-app only (quiet)
//	membership.renewal_due    -> membership_renewal_due    to the MEMBER, push ("in 3 days" / "today")
//	membership.payment_failed -> membership_payment_failed to the MEMBER, push
//	membership.ended          -> membership_ended          to the MEMBER, push
//
// The push names people by their profile display name, looked up at delivery
// (the same identity-profile batch lookup chat pushes use); the inbox row
// names them by the actor id, hydrated at read time like every other row.

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/notification-service/internal/push"
)

// Membership notification types. Every one is registered in Templates and maps
// to the `memberships` preference category (push_memberships /
// inapp_memberships, migration 018) and the system inbox tab.
const (
	MembershipTypeNewMember     = "membership_new_member"
	MembershipTypeJoined        = "membership_joined"
	MembershipTypeRenewed       = "membership_renewed"
	MembershipTypeRenewalDue    = "membership_renewal_due"
	MembershipTypePaymentFailed = "membership_payment_failed"
	MembershipTypeEnded         = "membership_ended"
)

// MembershipNotificationTypes lists every membership type, for the registry tests.
var MembershipNotificationTypes = []string{
	MembershipTypeNewMember, MembershipTypeJoined, MembershipTypeRenewed,
	MembershipTypeRenewalDue, MembershipTypePaymentFailed, MembershipTypeEnded,
}

// MembershipEntityType is the entity a membership notice points at: the
// subscription (entity_id = subscription_id).
const MembershipEntityType = "membership"

// MembershipCreatorDeepLink is where the creator's new-member notice lands:
// their memberships (tiers and members) page.
const MembershipCreatorDeepLink = "/monetization/tiers"

// MembershipDeepLink is where every notice to the member lands: that one
// membership, which carries the Renew / Pay again action.
func MembershipDeepLink(subscriptionID uuid.UUID) string {
	return "/memberships/" + subscriptionID.String()
}

// MembershipAndroidChannel is the Android notification channel membership
// pushes post to: the app's existing "account" channel (payments, renewals
// and access are account notices).
const MembershipAndroidChannel = "account"

// Names a notice uses when the profile lookup has none.
const (
	membershipFallbackMember  = "Someone"
	membershipFallbackCreator = "a creator"
)

// MembershipDaysLeftUnknown marks a renewal reminder whose event carried no
// days_left.
const MembershipDaysLeftUnknown = -1

// membershipIST is the zone dates are written in (the product is India-only;
// a fixed offset needs no tzdata on the host).
var membershipIST = time.FixedZone("IST", 5*3600+1800)

// MembershipNotification is one recipient's notice about one membership event.
type MembershipNotification struct {
	// EventID is the envelope's event id: with RecipientID and Type it is the
	// idempotency key.
	EventID        string
	RecipientID    uuid.UUID
	ActorID        uuid.UUID // the other party: the member on a creator notice, the creator on a member notice
	Type           string
	SubscriptionID uuid.UUID
	TierName       string
	PeriodEnd      time.Time
	DaysLeft       int // membership_renewal_due only; MembershipDaysLeftUnknown when absent
	DeepLink       string
	CreatedAt      time.Time
	// ActorName is the actor's display name, filled at delivery from the
	// profile lookup; empty renders "Someone" / "a creator".
	ActorName string
}

// Identity keys the idempotent inbox write: one notice per event, recipient
// and type, however often Kafka redelivers the event.
func (n MembershipNotification) Identity() string {
	return n.EventID + ":" + n.RecipientID.String() + ":" + n.Type
}

// membershipUnnamedTitles reword a member notice whose creator has no name:
// "Your a creator membership" is not a sentence.
var membershipUnnamedTitles = map[string]string{
	MembershipTypeRenewed:       "Your membership with a creator was renewed",
	MembershipTypeRenewalDue:    "Your membership with a creator ends {when} — renew to keep access",
	MembershipTypePaymentFailed: "Your membership payment to a creator didn't go through",
	MembershipTypeEnded:         "Your membership with a creator has ended",
}

// membershipWhen words a renewal reminder's days_left.
func membershipWhen(daysLeft int) string {
	switch {
	case daysLeft == 0:
		return "today"
	case daysLeft == 1:
		return "tomorrow"
	case daysLeft > 1:
		return "in " + strconv.Itoa(daysLeft) + " days"
	default:
		return "soon"
	}
}

// RenderMembershipNotification renders the push copy from the registered
// template, with the per-membership collapse key and the account channel.
func RenderMembershipNotification(n MembershipNotification) RenderOverride {
	t := GetTemplate(n.Type)
	name := strings.TrimSpace(n.ActorName)
	title, body := t.TitleTemplate, t.BodyTemplate
	if name == "" {
		if alt, ok := membershipUnnamedTitles[n.Type]; ok {
			title = alt
		}
	}
	tier := strings.TrimSpace(n.TierName)
	if tier == "" {
		title = strings.ReplaceAll(title, " {tier}", "")
		body = strings.ReplaceAll(body, " {tier}", "")
	}
	member, creator := name, name
	if member == "" {
		member = membershipFallbackMember
	}
	if creator == "" {
		creator = membershipFallbackCreator
	}
	date := "your next renewal"
	if !n.PeriodEnd.IsZero() {
		date = n.PeriodEnd.In(membershipIST).Format("2 Jan 2006")
	}
	vars := map[string]string{
		"member": member, "creator": creator, "tier": tier,
		"when": membershipWhen(n.DaysLeft), "date": date,
	}
	return RenderOverride{
		Title: RenderTitle(title, vars),
		Body:  RenderTitle(body, vars),
		// Per membership and recipient: "ends today" replaces "ends in 3
		// days" on the device, and "has ended" replaces both.
		CollapseKey: "membership:" + n.SubscriptionID.String() + ":" + n.RecipientID.String(),
		Data: map[string]string{
			push.AndroidChannelDataKey: MembershipAndroidChannel,
			"subscription_id":          n.SubscriptionID.String(),
		},
	}
}

// membershipTransports is the dependency set one membership notice rides on;
// *Service implements it and runMembershipNotice owns the rules, so they are
// testable without live stores.
type membershipTransports interface {
	recipientSuppressed(ctx context.Context, userID uuid.UUID, notifType string) bool
	resolveGeneralDelivery(ctx context.Context, userID uuid.UUID, notifType string) DeliveryDecision
	membershipActorName(ctx context.Context, userID uuid.UUID) string
	deliverWithDecision(ctx context.Context, decision DeliveryDecision, userID, actorID uuid.UUID, notifType, entityType string, entityID uuid.UUID, deepLink string, createdAt time.Time, identity string, render RenderOverride) error
}

// membershipActorName is the display name (username as a second choice) the
// push uses; "" when unknown.
func (s *Service) membershipActorName(ctx context.Context, userID uuid.UUID) string {
	if userID == uuid.Nil {
		return ""
	}
	return s.senderDisplayName(ctx, userID)
}

// CreateMembershipNotification delivers one membership notice.
func (s *Service) CreateMembershipNotification(ctx context.Context, n MembershipNotification) error {
	return runMembershipNotice(ctx, s, n)
}

func runMembershipNotice(ctx context.Context, t membershipTransports, n MembershipNotification) error {
	// A notice to nobody, to oneself, or without an event id (no identity:
	// a redelivery could notify twice) is dropped.
	if n.RecipientID == uuid.Nil || n.RecipientID == n.ActorID || n.EventID == "" || n.SubscriptionID == uuid.Nil {
		return nil
	}
	if t.recipientSuppressed(ctx, n.RecipientID, n.Type) {
		return nil
	}
	decision := t.resolveGeneralDelivery(ctx, n.RecipientID, n.Type)
	if !GetTemplate(n.Type).PushEligible {
		// Quiet notices (a renewal) never push, even where no preference
		// store answers (resolveGeneralDelivery then turns every channel on).
		decision.SendPush = false
		decision.DeferPush = false
	}
	if !decision.CreateInbox && !decision.SendWebSocket && !decision.SendPush {
		return nil
	}
	if decision.SendPush && strings.TrimSpace(n.ActorName) == "" {
		// Only the push needs the name; the inbox row hydrates at read time.
		n.ActorName = t.membershipActorName(ctx, n.ActorID)
	}
	return t.deliverWithDecision(ctx, decision, n.RecipientID, n.ActorID, n.Type,
		MembershipEntityType, n.SubscriptionID, n.DeepLink, n.CreatedAt, n.Identity(), RenderMembershipNotification(n))
}
