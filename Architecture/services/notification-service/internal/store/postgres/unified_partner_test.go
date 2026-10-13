package postgres

import (
	"context"
	"github.com/google/uuid"
	"testing"
)

func TestUnifiedPartnerAppAccepted(t *testing.T) {
	if !ValidDeviceApp(AppPartner) {
		t.Fatal("unified partner install must be accepted")
	}
	if ValidDeviceApp("partner-admin") {
		t.Fatal("unrecognised app accepted")
	}
}

func TestUnifiedPartnerPushIsolationAndUpgrade(t *testing.T) {
	pool := notificationTestPool(t)
	store := New(pool)
	ctx := context.Background()
	user, other := uuid.New(), uuid.New()
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), "DELETE FROM user_devices WHERE user_id IN ($1,$2)", user, other)
	})
	token := "partner-test-" + user.String()
	old, err := store.RegisterDevice(ctx, user, "android", token, AppDoorstepPro)
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := store.RegisterDevice(ctx, user, "android", token, AppPartner)
	if err != nil {
		t.Fatal(err)
	}
	if upgraded.ID != old.ID {
		t.Fatal("re-register must return actual ID so sign-out can remove it")
	}
	if _, err = store.RegisterDevice(ctx, other, "android", "other-"+token, AppPartner); err != nil {
		t.Fatal(err)
	}
	for _, app := range []string{AppDoorstepPro, AppFeastRider, AppMopeduCaptain, AppPartner} {
		devices, err := store.GetUserDevicesForApp(ctx, user, app)
		if err != nil {
			t.Fatal(err)
		}
		if len(devices) != 1 || devices[0].UserID != user || devices[0].PushToken != token {
			t.Fatalf("wrong recipients for %s", app)
		}
	}
	kitchen, err := store.GetUserDevicesForApp(ctx, user, AppFeastKitchen)
	if err != nil || len(kitchen) != 0 {
		t.Fatal("restaurant pushes must exclude unified partner")
	}
	devices, err := store.GetUserDevices(ctx, user)
	if err != nil || len(devices) != 0 {
		t.Fatal("customer/social pushes must exclude unified partner")
	}
	if err := store.UnregisterDevice(ctx, upgraded.ID, user); err != nil {
		t.Fatal(err)
	}
	devices, err = store.GetUserDevicesForApp(ctx, user, AppDoorstepPro)
	if err != nil || len(devices) != 0 {
		t.Fatal("signed-out device still receives partner pushes")
	}
}

func TestUnifiedPartnerExcludesMerchantAndCustomerTargets(t *testing.T) {
	for _, app := range []string{AppFeastKitchen, AppMomentum, "seller", "unknown"} {
		if includesUnifiedPartner(app) {
			t.Fatalf("unexpected delivery/service target %s", app)
		}
	}
	for _, app := range []string{AppDoorstepPro, AppFeastRider, AppMopeduCaptain} {
		if !includesUnifiedPartner(app) {
			t.Fatalf("missing delivery/service target %s", app)
		}
	}
}
