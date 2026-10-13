package service

import (
	"context"
	"errors"
	"time"

	"github.com/atpost/notification-service/internal/push"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Commerce delivery notices contain routing identifiers, never verification
// codes, customer contact details or pickup/drop addresses.
func commerceWorkNotice(typ, key string, user, job, offer uuid.UUID, at, expires time.Time) (PartnerNotice, error) {
	titles := map[string]string{
		"commerce.delivery.offer":     "New delivery request",
		"commerce.delivery.assigned":  "Delivery accepted",
		"commerce.delivery.picked_up": "Pickup confirmed",
		"commerce.delivery.delivered": "Delivery completed",
		"commerce.delivery.failed":    "Delivery needs attention",
		"commerce.delivery.cancelled": "Delivery cancelled",
		"commerce.delivery.earnings":  "Delivery earnings updated",
		"commerce.delivery.support":   "Delivery support updated",
	}
	title, ok := titles[typ]
	if !ok || user == uuid.Nil || job == uuid.Nil || key == "" {
		return PartnerNotice{}, errors.New("invalid commerce delivery notice")
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	entity := job
	body := "Open Commerce Delivery to see the latest job details."
	var expiry *time.Time
	if typ == "commerce.delivery.offer" {
		if offer == uuid.Nil || expires.IsZero() {
			return PartnerNotice{}, errors.New("delivery offer requires identity and absolute expiry")
		}
		entity = offer
		expiry = &expires
		body = "A delivery is available. Open the app to review the payout and respond before the offer expires."
	}
	link := "/partner/commerce-delivery"
	data := map[string]string{"type": typ, "entity_id": entity.String(), "job_id": job.String(), "deep_link": link, "title": title, "body": body, "collapse_key": "commerce-delivery:" + entity.String()}
	data[push.AndroidChannelDataKey] = "commerce_delivery_updates"
	if expiry != nil {
		data[push.AndroidChannelDataKey] = "commerce_delivery_offers"
		data["offer_id"] = offer.String()
		data["expires_at"] = expires.UTC().Format(time.RFC3339Nano)
	}
	return PartnerNotice{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("commerce-delivery:"+key+":"+user.String())), UserID: user, App: postgres.AppPartner, Type: typ, EntityID: entity, EntityType: "commerce_delivery_job", Title: title, Body: body, DeepLink: link, Data: data, CreatedAt: at, ExpiresAt: expiry, Inbox: expiry == nil}, nil
}

func (s *Service) QueueCommerceDeliveryWork(ctx context.Context, typ, key string, user, job, offer uuid.UUID, at, expires time.Time) error {
	n, err := commerceWorkNotice(typ, key, user, job, offer, at, expires)
	if err != nil {
		return err
	}
	return s.queuePartnerNotice(ctx, n)
}
