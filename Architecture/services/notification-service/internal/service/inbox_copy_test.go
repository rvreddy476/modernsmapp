package service

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"

	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/google/uuid"
)

// devStoredTypes is the 17 Oct inventory of distinct types stored on dev.
var devStoredTypes = []string{
	"commerce_order_created", "dm", "commerce_order_shipped", "order_delivered",
	"commerce_invoice_issued", "food_order_status", "commerce_order_paid",
	"friend_request", "dating.verification.completed", "follow", "reaction",
	"comment", "doorstep.pro.price.reviewed", "creator_went_live",
	"dating.spark.created", "dating.match.expired", "commerce_seller_new_order",
	"seller_new_order", "order_shipped", "order_confirmed", "message_request",
	"qa.answer.created", "incoming_video_call", "friend_accepted",
	"channel.update.published", "incoming_call", "doorstep.pro.document.reviewed",
	"qa.question.followed.answered", "comment_reaction", "qa.answer.best_selected",
	"order_payment_failed", "order_cancelled", "doorstep.pro.application.approved",
	"doorstep.booking.pro_unavailable", "doorstep.booking.confirmed",
	"doorstep.booking.cancelled",
}

// writtenTypes is every type this service writes to the inbox, from the
// CreateNotification / deliverWithDecision / call / safety-alert / partner
// queue call sites and the consumers that feed them.
var writtenTypes = []string{
	// events/consumer.go (social)
	"follow", "reaction", "comment", "comment_reaction", "friend_request",
	"friend_accepted", "endorsement", "business_review", "new_subscriber",
	"mention", "post_reposted", "post_reclassified",
	// chat
	"dm", "message_request", "dating.match.new_message",
	// calls
	"incoming_call", "incoming_video_call", "missed_call",
	// live + uploads (fan-out)
	LiveNotifType, "creator_uploaded_video", "creator_uploaded_flick",
	// channel template pipeline
	"channel.update.published",
	// commerce
	"commerce_invoice_issued", OrderTypeSellerNewOrder, OrderTypeConfirmed,
	OrderTypeShipped, OrderTypeDelivered, OrderTypeCancelled, OrderTypeRefunded,
	OrderTypePaymentFailed,
	"commerce.delivery.offer", "commerce.delivery.assigned", "commerce.delivery.picked_up",
	"commerce.delivery.delivered", "commerce.delivery.failed", "commerce.delivery.cancelled",
	"commerce.delivery.earnings", "commerce.delivery.support",
	"commerce.delivery.application.reviewed", "commerce.delivery.document.reviewed",
	// food
	FoodTypeOrderNew, FoodTypeDeliveryOffer, FoodTypeOrderStatus, FoodTypeDeliveryAccount,
	FoodTypeDeliveryDocument, FoodTypeKitchenAccount, FoodTypeKitchenStatus, FoodTypeDeliveryStatus,
	// rides
	RideTypeAssigned, RideTypeArriving, RideTypeArrived, RideTypeStarted, RideTypeCompleted,
	RideTypeCancelled, RideTypePaymentPaid, CaptainTypeOffer, CaptainTypePaymentReceived,
	CaptainTypeRideAssigned, CaptainTypeRideCancelled, CaptainTypeRideCompleted,
	CaptainTypeApproved, CaptainTypeUnderReview, CaptainTypeReviewUpdated, CaptainTypeRestricted,
	CaptainTypeDocumentExpiring, CaptainTypePaymentReviewRejected, CaptainTypeSubscriptionExpiring,
	CaptainTypeSubscriptionExpired, CaptainTypeSubscriptionRenewed, CaptainTypeSubscriptionPaymentFailed,
	"captain.support.updated", "rider.safety.sos", "rider.complaint.raised", "rider.partner.fraud_flagged",
	// dating
	"dating.spark.created", "dating.match.formed", "dating.match.first_message",
	"dating.match.expired", "dating.vouch.requested", "dating.vouch.accepted",
	"dating.match.quiet", "dating.safe_meet.reminder", "dating.safe_meet.missed_check_in",
	"dating.safety.panic.acknowledged", "dating.report.status_updated",
	"dating.verification.completed", "dating.verification.rejected", "dating.photo.rejected",
	"dating.premium.payment_failure", "dating.data_export.ready",
	"dating.safety.panic.responder", "dating.safety.panic.trusted_contact",
	"dating.safety.location_shared", DatingScamAlertType, DatingDateCheckinType,
	// doorstep safety
	"doorstep.safety.incident.responder",
	// Q&A
	"qa.answer.created", "qa.answer.best_selected", "qa.answer.comment.created",
	"qa.answer.requested", "qa.question.voted", "qa.answer.voted", "qa.question.pinned",
}

// allInboxTypes is both lists plus every Doorstep push type and every
// template-registry name (rows written through the template pipeline).
func allInboxTypes() []string {
	out := append([]string{}, devStoredTypes...)
	out = append(out, writtenTypes...)
	out = append(out, DoorstepPushTypes()...)
	for t := range Templates {
		out = append(out, t)
	}
	return out
}

var idLike = regexp.MustCompile(`[0-9a-f]{8}-[0-9a-f]{4}|#\d|\d{4,}`)

func genericTitles() map[string]bool {
	g := map[string]bool{inboxGenericTitle: true}
	for _, f := range inboxFamilies {
		g[renderInboxTitle(f.title, "")] = true
		g[renderInboxTitle(f.title, "Call UserB")] = true
	}
	return g
}

func isInboxIcon(icon string) bool {
	for _, i := range InboxIcons {
		if i == icon {
			return true
		}
	}
	return false
}

func checkWording(t *testing.T, typ, title string) {
	t.Helper()
	if title == "" {
		t.Errorf("%s: empty title", typ)
		return
	}
	if strings.ContainsAny(title, "!{}") {
		t.Errorf("%s: title %q has an exclamation mark or a placeholder", typ, title)
	}
	if strings.Contains(strings.ToLower(title), "new notification") {
		t.Errorf("%s: title %q is the old generic fallback", typ, title)
	}
	if idLike.MatchString(title) {
		t.Errorf("%s: title %q looks like it exposes an id", typ, title)
	}
	if first := title[:1]; first != strings.ToUpper(first) {
		t.Errorf("%s: title %q is not sentence case", typ, title)
	}
}

func TestInboxCopyCoversEveryKnownType(t *testing.T) {
	generic := genericTitles()
	for _, typ := range allInboxTypes() {
		if _, ok := inboxCopyTable[typ]; !ok {
			t.Errorf("%s: no exact inbox copy entry", typ)
			continue
		}
		for _, actor := range []string{"", "Call UserB"} {
			title, icon := InboxCopy(typ, actor)
			checkWording(t, typ, title)
			if generic[title] {
				t.Errorf("%s: title %q is a generic fallback", typ, title)
			}
			if !isInboxIcon(icon) {
				t.Errorf("%s: icon %q is not in the enum", typ, icon)
			}
		}
	}
}

func TestInboxCopyTableIsWellFormed(t *testing.T) {
	for typ, e := range inboxCopyTable {
		if (e.title == "") == (e.template == "") {
			t.Errorf("%s: exactly one of title and template must be set", typ)
		}
		if e.template != "" {
			if _, ok := Templates[e.template]; !ok {
				t.Errorf("%s: reuses missing template %q", typ, e.template)
			}
		}
		text := e.text()
		if rest := strings.ReplaceAll(text, inboxActorToken, ""); strings.ContainsAny(rest, "{}") {
			t.Errorf("%s: %q holds a placeholder other than {actor}", typ, text)
		}
		if !isInboxIcon(e.icon) {
			t.Errorf("%s: icon %q is not in the enum", typ, e.icon)
		}
	}
}

func TestInboxCopyNamesTheActor(t *testing.T) {
	cases := []struct{ typ, actor, want string }{
		{"comment_reaction", "Call UserB", "Call UserB reacted to your comment"},
		{"follow", "Call UserB", "Call UserB followed you"},
		{"friend_request", "Call UserB", "Call UserB wants to join your Circle"},
		{"incoming_video_call", "Call UserB", "Video call from Call UserB"},
		{"dating.match.formed", "Asha", "You matched with Asha"},
		// No resolvable name: "Someone" opens a sentence, "someone" inside one.
		{"comment_reaction", "", "Someone reacted to your comment"},
		{"follow", "   ", "Someone followed you"},
		{"incoming_call", "", "Call from someone"},
		{"dating.match.formed", "", "You matched with someone"},
	}
	for _, c := range cases {
		if got, _ := InboxCopy(c.typ, c.actor); got != c.want {
			t.Errorf("InboxCopy(%q, %q) = %q, want %q", c.typ, c.actor, got, c.want)
		}
	}
}

func TestInboxCopySystemTypesHaveNoActor(t *testing.T) {
	for typ, e := range inboxCopyTable {
		if strings.Contains(e.text(), inboxActorToken) {
			continue
		}
		title, _ := InboxCopy(typ, "")
		if strings.HasPrefix(title, "Someone") {
			t.Errorf("%s: system title %q starts with Someone", typ, title)
		}
		if named, _ := InboxCopy(typ, "Call UserB"); strings.Contains(named, "Call UserB") {
			t.Errorf("%s: system title %q names an actor", typ, named)
		}
	}
	cases := map[string]string{
		"order_delivered":                   "Your order was delivered",
		"dating.match.expired":              "Your match expired",
		"seller_new_order":                  "You have a new order",
		"commerce_seller_new_order":         "You have a new order",
		"commerce_order_created":            "Your order was placed",
		"doorstep.pro.price.reviewed":       "Your price was reviewed",
		"doorstep.pro.document.reviewed":    "Your document was reviewed",
		"doorstep.pro.application.approved": "Your application was approved",
		"missed_call":                       "You missed a call",
	}
	for typ, want := range cases {
		if got, _ := InboxCopy(typ, "Call UserB"); got != want {
			t.Errorf("InboxCopy(%q) = %q, want %q", typ, got, want)
		}
	}
}

func TestInboxCopyFallbacks(t *testing.T) {
	cases := []struct{ typ, title, icon string }{
		{"doorstep.booking.something_new", "You have a home service update", InboxIconHomeService},
		{"commerce_order_returned", "You have an order update", InboxIconOrder},
		{"order_on_hold", "You have an order update", InboxIconOrder},
		{"seller_payout_sent", "You have an order update", InboxIconOrder},
		{"food_order_rated", "You have a food order update", InboxIconFood},
		{"rider.something", "You have a ride update", InboxIconRide},
		{"captain.something", "You have a ride update", InboxIconRide},
		{"mopedu_promo", "You have a ride update", InboxIconRide},
		{"dating.boost.ended", "You have a dating update", InboxIconDating},
		{"qa.answer.edited", "You have a Q&A update", InboxIconQA},
		{"channel.poll.closed", "You have a community update", InboxIconCommunity},
		{"community.thing", "You have a community update", InboxIconCommunity},
		{"incoming_group_video_call", "Someone called you", InboxIconCall},
		{"missed_video_call", "Someone tried to call you", InboxIconCall},
		{"totally_unknown", inboxGenericTitle, InboxIconSystem},
		{"", inboxGenericTitle, InboxIconSystem},
	}
	for _, c := range cases {
		title, icon := InboxCopy(c.typ, "")
		if title != c.title || icon != c.icon {
			t.Errorf("InboxCopy(%q) = %q/%q, want %q/%q", c.typ, title, icon, c.title, c.icon)
		}
	}
	for _, f := range inboxFamilies {
		checkWording(t, "family", renderInboxTitle(f.title, ""))
		if !isInboxIcon(f.icon) {
			t.Errorf("family icon %q is not in the enum", f.icon)
		}
	}
	checkWording(t, "generic", inboxGenericTitle)
}

func TestApplyInboxCopyUsesHydratedName(t *testing.T) {
	actor := uuid.MustParse("00000000-0000-4000-8000-0000000000b2")
	rows := []scylla.Notification{
		{Type: "comment_reaction", ActorUserID: actor, Actor: &scylla.NotificationActor{ID: actor, Username: "call_b", DisplayName: "Call UserB"}},
		{Type: "comment_reaction", ActorUserID: actor, Actor: &scylla.NotificationActor{ID: actor, Username: "call_b"}},
		{Type: "comment_reaction", ActorUserID: actor},
		{Type: "order_delivered"},
	}
	ApplyInboxCopyAll(rows)
	want := []string{
		"Call UserB reacted to your comment",
		"call_b reacted to your comment",
		"Someone reacted to your comment",
		"Your order was delivered",
	}
	for i, w := range want {
		if rows[i].Title != w {
			t.Errorf("row %d title = %q, want %q", i, rows[i].Title, w)
		}
		if !isInboxIcon(rows[i].Icon) {
			t.Errorf("row %d icon = %q", i, rows[i].Icon)
		}
	}
}

// The realtime frame every publish site sends (general, call, safety alert)
// carries the same title and icon as the list row.
func TestNotificationFrameCarriesTitleAndIcon(t *testing.T) {
	cases := []struct{ typ, title, icon string }{
		{"incoming_video_call", "Video call from someone", InboxIconCall},
		{"order_delivered", "Your order was delivered", InboxIconOrder},
		{"dating.safety.location_shared", "Someone is sharing their live location with you", InboxIconDating},
	}
	for _, c := range cases {
		raw, err := notificationFrame(&scylla.Notification{Type: c.typ, ActorUserID: uuid.New()})
		if err != nil {
			t.Fatal(err)
		}
		var env struct {
			Type    string `json:"type"`
			Payload struct {
				Type  string `json:"type"`
				Title string `json:"title"`
				Icon  string `json:"icon"`
			} `json:"payload"`
		}
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatal(err)
		}
		if env.Type != "notification" || env.Payload.Type != c.typ ||
			env.Payload.Title != c.title || env.Payload.Icon != c.icon {
			t.Errorf("%s frame = %+v, want title %q icon %q", c.typ, env, c.title, c.icon)
		}
	}
}
