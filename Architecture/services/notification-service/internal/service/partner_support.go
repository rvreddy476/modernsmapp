package service

import (
	"context"
	"github.com/atpost/notification-service/internal/store/postgres"
	"github.com/google/uuid"
	"time"
)

func (s *Service) QueuePartnerSupport(ctx context.Context, typ, key string, user, ride, complaint uuid.UUID, at time.Time) error {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	title := "Ride support updated"
	if typ == "rider.partner.support.raised" {
		title = "Ride support request received"
	}
	body := "Open the app to check your support request."
	link := "/captain/rides/" + ride.String()
	data := map[string]string{"type": "captain.support.updated", "entity_id": ride.String(), "ride_id": ride.String(), "complaint_id": complaint.String(), "deep_link": link, "title": title, "body": body, "collapse_key": "ride-support:" + complaint.String()}
	return s.queuePartnerNotice(ctx, PartnerNotice{ID: uuid.NewSHA1(uuid.NameSpaceURL, []byte("partner-support:"+key+":"+user.String())), UserID: user, App: postgres.AppMopeduCaptain, Type: "captain.support.updated", EntityID: ride, EntityType: "rider_ride", Title: title, Body: body, DeepLink: link, Data: data, CreatedAt: at, Inbox: true})
}
