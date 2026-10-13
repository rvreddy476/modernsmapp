package events

import (
	"encoding/json"
	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
	"strings"
	"testing"
	"time"
)

func TestCaptainCancellationUsesAuthenticatedAccountRecipient(t *testing.T) {
	raw := json.RawMessage(`{"ride_id":"` + rxRide + `","partner_id":"` + rxPartner + `","partner_user_id":"` + rxPartnerUser + `","cancelled_by_kind":"customer","otp":"1234","pickup_address":"PRIVATE"}`)
	p, missing, ok, err := planCaptainLifecyclePush(events.EventRiderRideCancelled, "event-1", raw, time.Now())
	if err != nil || !ok || missing != "" {
		t.Fatalf("plan: %v %v %s", err, ok, missing)
	}
	if p.RecipientID != uuid.MustParse(rxPartnerUser) || p.App != service.AppMopeduCaptain {
		t.Fatal("wrong recipient or app")
	}
	data, _ := json.Marshal(service.RidePushData(p))
	if strings.Contains(string(data), "PRIVATE") || strings.Contains(string(data), "1234") {
		t.Fatal("private data in push")
	}
}

func TestCaptainLifecycleNeverUsesPartnerRowIDAsAccount(t *testing.T) {
	raw := json.RawMessage(`{"ride_id":"` + rxRide + `","partner_id":"` + rxPartner + `"}`)
	for _, typ := range []string{events.EventRiderRideAssigned, events.EventRiderRideCompleted, events.EventRiderRideCancelled} {
		_, missing, ok, err := planCaptainLifecyclePush(typ, "", raw, time.Now())
		if err != nil || ok || missing != "partner_user_id" {
			t.Fatalf("%s: %v %v %s", typ, err, ok, missing)
		}
	}
}
