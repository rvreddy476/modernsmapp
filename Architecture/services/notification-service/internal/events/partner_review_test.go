package events

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
)

func TestFoodDeliveryReviewsReachIdentityAccount(t *testing.T) {
	for _, status := range []string{"APPROVED", "REJECTED", "SUSPENDED", "PENDING_REVIEW"} {
		t.Run(status, func(t *testing.T) {
			raw, _ := json.Marshal(map[string]string{"partner_id": rxPartner, "partner_user_id": rxPartnerUser, "status": status, "reason": "private identity number"})
			plan := planFoodDeliveryReview("food.delivery_partner.status_changed", raw, "review-1", time.Now())
			if plan.outcome != foodOutcomePlanned || len(plan.pushes) != 1 {
				t.Fatalf("plan=%+v", plan)
			}
			p := plan.pushes[0]
			if p.RecipientID.String() != rxPartnerUser || p.EntityID.String() != rxPartner || p.App != service.AppFeastRider || p.Type != service.FoodTypeDeliveryAccount {
				t.Fatalf("incorrect account routing: %+v", p)
			}
			if strings.Contains(p.Body, "private") || p.DeepLink != "/rider/account" || p.Title == "" {
				t.Fatalf("unsafe or empty copy: %+v", p)
			}
		})
	}
	for _, status := range []string{"APPROVED", "REJECTED"} {
		raw, _ := json.Marshal(map[string]string{"partner_id": rxPartner, "partner_user_id": rxPartnerUser, "document_id": rxOffer, "status": status})
		plan := planFoodDeliveryReview("food.delivery_partner.document_reviewed", raw, "doc-1", time.Now())
		if len(plan.pushes) != 1 || plan.pushes[0].Type != service.FoodTypeDeliveryDocument || plan.pushes[0].EntityID.String() != rxOffer {
			t.Fatalf("document routing: %+v", plan)
		}
	}
}

func TestFoodDeliveryReviewDoesNotGuessRecipientOrNotifyDutyChanges(t *testing.T) {
	raw, _ := json.Marshal(map[string]string{"partner_id": rxPartner, "status": "APPROVED"})
	plan := planFoodDeliveryReview("food.delivery_partner.status_changed", raw, "test", time.Now())
	if plan.outcome != foodOutcomeMissingRecipient || len(plan.pushes) != 0 {
		t.Fatalf("local partner ID used as recipient: %+v", plan)
	}
	raw, _ = json.Marshal(map[string]string{"partner_id": rxPartner, "partner_user_id": rxPartnerUser, "status": "OFFLINE"})
	plan = planFoodDeliveryReview("food.delivery_partner.status_changed", raw, "test", time.Now())
	if plan.outcome != foodOutcomeIgnored {
		t.Fatalf("unnecessary duty alert: %+v", plan)
	}
	plan = planFoodDeliveryReview("food.delivery_partner.document_reviewed", raw, "test", time.Now())
	if plan.outcome != foodOutcomeMalformed {
		t.Fatalf("missing document accepted: %+v", plan)
	}
}

func TestMopedReviewEventsUseCaptainDeliveryNotGenericCustomerNotifications(t *testing.T) {
	cases := map[string]string{
		"rider.partner.document_reviewed":            service.CaptainTypeReviewUpdated,
		events.EventRiderPartnerKYCApproved:          service.CaptainTypeReviewUpdated,
		events.EventRiderPartnerKYCRejected:          service.CaptainTypeReviewUpdated,
		events.EventRiderPartnerVehicleApproved:      service.CaptainTypeReviewUpdated,
		events.EventRiderPartnerVehicleRejected:      service.CaptainTypeReviewUpdated,
		events.EventRiderPartnerSuspended:            service.CaptainTypeRestricted,
		events.EventRiderPartnerBlocked:              service.CaptainTypeRestricted,
		events.EventRiderDocumentExpiring:            service.CaptainTypeDocumentExpiring,
		events.EventRiderSubscriptionPaymentRejected: service.CaptainTypePaymentReviewRejected,
	}
	for eventType, pushType := range cases {
		t.Run(eventType, func(t *testing.T) {
			c, d := rideConsumer()
			payload := map[string]string{"partner_id": rxPartner, "partner_user_id": rxPartnerUser, "reason": "private admin notes"}
			handled, err := c.handleRiderEvent(context.Background(), rideEnvelope(eventType, payload))
			if !handled || err != nil || len(d.pushes) != 1 {
				t.Fatalf("handled=%v err=%v pushes=%d", handled, err, len(d.pushes))
			}
			p := d.pushes[0]
			if p.Type != pushType || p.RecipientID.String() != rxPartnerUser || p.App != service.AppMopeduCaptain || p.AndroidChannel != service.CaptainChannelAccount || p.EntityID.String() != rxPartner {
				t.Fatalf("incorrect routing: %+v", p)
			}
			if strings.Contains(p.Body, "private") || p.Title == "" {
				t.Fatalf("unsafe copy: %+v", p)
			}
			if pushType == service.CaptainTypePaymentReviewRejected && p.DeepLink != "/captain/subscription" {
				t.Fatalf("wrong payment link: %s", p.DeepLink)
			}
			delete(payload, "partner_user_id")
			raw, _ := json.Marshal(payload)
			_, _, missing, err := planCaptainAccountPush(eventType, "missing-user", raw, time.Now())
			if err != nil || missing != "partner_user_id" {
				t.Fatalf("missing identity recipient not rejected: %s %v", missing, err)
			}
		})
	}
}
