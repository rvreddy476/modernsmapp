package service

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// A Service with NO stores: any touch of Scylla/Redis/Postgres would panic,
// so a nil return proves the channel was skipped per the decision.
func bareService() *Service { return &Service{} }

func TestDeliverWithDecision_NothingWhenAllChannelsOff(t *testing.T) {
	err := bareService().deliverWithDecision(context.Background(), DeliveryDecision{},
		uuid.New(), uuid.New(), "comment", "post", uuid.New(), "/p/1", time.Now(), "")
	if err != nil {
		t.Fatalf("expected silent skip, got %v", err)
	}
}

func TestDeliverWithDecision_SkipsInboxAndRealtimeWhenInappOff(t *testing.T) {
	// In-app off, push on: no inbox row, no realtime publish. With no pusher
	// configured the push leg is a no-op, so this must return cleanly.
	d := DeliveryDecision{CreateInbox: false, SendWebSocket: false, SendPush: true}
	err := bareService().deliverWithDecision(context.Background(), d,
		uuid.New(), uuid.New(), "reaction", "post", uuid.New(), "/p/1", time.Now(), "id-1")
	if err != nil {
		t.Fatalf("expected inbox/realtime skipped, got %v", err)
	}
}

func TestResolveGeneralDelivery_NoPGStoreMeansEverythingOn(t *testing.T) {
	d := bareService().resolveGeneralDelivery(context.Background(), uuid.New(), "dm")
	if !d.CreateInbox || !d.SendWebSocket || !d.SendPush {
		t.Fatalf("no pg store should keep every channel on: %+v", d)
	}
}

func TestNotifTitleBody_NewTypes(t *testing.T) {
	for _, ty := range []string{"post_reposted", "creator_went_live", "missed_call"} {
		title, body := notifTitleBody(ty)
		if title == "New Notification" || body == "" {
			t.Fatalf("%s has no dedicated title/body", ty)
		}
	}
}
