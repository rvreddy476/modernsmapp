//go:build livefcm

package push

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Explicit opt-in physical-device diagnostic. Sends exactly one labelled,
// expiring notification through the production adapter; never creates an offer,
// booking, assignment or payment. A tap opens Home, not a fictional job.
func TestLiveDoorstepNotificationProbe(t *testing.T) {
	if os.Getenv("FCM_LIVE_DOORSTEP_CONFIRM") != "send-one-test-notification" {
		t.Skip("requires explicit consent to send one Doorstep test notification")
	}
	project, token := os.Getenv("FCM_LIVE_PROJECT"), os.Getenv("FCM_LIVE_TOKEN")
	sa := []byte(os.Getenv("FCM_LIVE_SA_JSON"))
	if len(sa) == 0 {
		var err error
		sa, err = os.ReadFile(os.Getenv("FCM_LIVE_SA_PATH"))
		if err != nil {
			t.Fatal("cannot read configured service-account file")
		}
	}
	var identity struct {
		ProjectID string `json:"project_id"`
	}
	if json.Unmarshal(sa, &identity) != nil || project == "" || identity.ProjectID != project || token == "" {
		t.Fatal("missing or mismatched Firebase project, credential or device configuration")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// A unique notification subject prevents replacement of any real job alert.
	now := time.Now().UTC()
	data := map[string]string{
		"type":                "doorstep.pro.offer.new",
		"entity_id":           "notification-test-" + now.Format("20060102T150405.000000000"),
		"deep_link":           "doorstep-pro://home",
		"expires_at":          now.Add(30 * time.Second).Format(time.RFC3339Nano),
		AndroidTTLDataKey:     "30s",
		AndroidChannelDataKey: "doorstep_pro_offers",
	}
	// Opt-in interactive practice uses the APK's isolated debug-only responder.
	// It never inserts a real job and cannot invoke the live offer APIs.
	title := "TEST ONLY - Doorstep alert (no booking)"
	if os.Getenv("FCM_LIVE_INTERACTIVE_TEST") == "true" {
		category := os.Getenv("FCM_LIVE_TEST_CATEGORY")
		types := map[string]string{"home": "doorstep.pro.offer.new", "moped": "captain.offer", "commerce": "commerce.delivery.offer", "food": "food_delivery_offer"}
		channels := map[string]string{"home": "doorstep_pro_offers", "moped": "captain_offer", "commerce": "commerce_delivery_offers", "food": "rider_job_offer"}
		typ, ok := types[category]
		if !ok {
			t.Fatal("choose home, moped, commerce or food for the practice request")
		}
		id := uuid.NewString()
		data["type"], data["offer_id"], data["entity_id"] = typ, id, id
		data["test_only"] = "true"
		data["test_service"] = os.Getenv("FCM_LIVE_TEST_SERVICE")
		data["test_location"] = os.Getenv("FCM_LIVE_TEST_LOCATION")
		data["test_amount_paise"] = os.Getenv("FCM_LIVE_TEST_AMOUNT_PAISE")
		data["expires_at"] = now.Add(20 * time.Second).Format(time.RFC3339Nano)
		data[AndroidTTLDataKey], data[AndroidChannelDataKey] = "20s", channels[category]
		title = "TEST ONLY - Partner practice request"
	}
	err := NewFCMPusher(project, string(sa)).Send(ctx, token, "android",
		title,
		"Notification test only. No work has been assigned.", data)
	if err != nil {
		// Device credentials must not appear even if a provider echoes a token.
		t.Fatalf("test notification failed: %s", strings.ReplaceAll(err.Error(), token, "[redacted-device-token]"))
	}
	t.Logf("FCM accepted ONE test notification at %s; phone delivery/sound require user confirmation", time.Now().UTC().Format(time.RFC3339))
}

// A separately authorized batch, for comparing three cards on an updated debug APK.
// These values are labelled practice data only, never seeded prices or live payouts.
func TestLivePartnerComparisonProbe(t *testing.T) {
	if os.Getenv("FCM_LIVE_COMPARISON_CONFIRM") != "send-three-test-requests" {
		t.Skip("requires explicit consent for three practice requests")
	}
	t.Setenv("FCM_LIVE_DOORSTEP_CONFIRM", "send-one-test-notification")
	t.Setenv("FCM_LIVE_INTERACTIVE_TEST", "true")
	for _, sample := range []struct{ category, service, location, amount string }{
		{"home", "AC service (practice)", "Uppal, Hyderabad", "65000"},
		{"food", "Food pickup (practice)", "Uppal to Peerzadiguda", "9000"},
		{"commerce", "Shopping delivery (practice)", "Boduppal to Uppal", "12000"},
	} {
		t.Run(sample.category, func(t *testing.T) {
			t.Setenv("FCM_LIVE_TEST_CATEGORY", sample.category)
			t.Setenv("FCM_LIVE_TEST_SERVICE", sample.service)
			t.Setenv("FCM_LIVE_TEST_LOCATION", sample.location)
			t.Setenv("FCM_LIVE_TEST_AMOUNT_PAISE", sample.amount)
			TestLiveDoorstepNotificationProbe(t)
		})
	}
}

// A five-item burst exercises duplicate-category cards and decline-to-next UI.
// Delivery is time-staggered, not conditional on a decision: practice decisions
// stay on the device and are never reported as real assignments to the backend.
func TestLivePartnerDeclineQueueProbe(t *testing.T) {
	if os.Getenv("FCM_LIVE_QUEUE_CONFIRM") != "send-five-test-requests" {
		t.Skip("requires explicit consent for five practice requests")
	}
	t.Setenv("FCM_LIVE_DOORSTEP_CONFIRM", "send-one-test-notification")
	t.Setenv("FCM_LIVE_INTERACTIVE_TEST", "true")
	for i, sample := range []struct{ category, service, location, amount string }{
		{"home", "1/5 AC service (practice)", "Uppal, Hyderabad", "65000"},
		{"home", "2/5 AC installation (practice)", "Peerzadiguda, Hyderabad", "90000"},
		{"food", "3/5 Food delivery (practice)", "Uppal to Boduppal", "9500"},
		{"home", "4/5 AC repair (practice)", "Boduppal, Hyderabad", "55000"},
		{"commerce", "5/5 Shopping delivery (practice)", "Peerzadiguda to Uppal", "12500"},
	} {
		if i > 0 {
			time.Sleep(2 * time.Second)
		}
		ok := t.Run(sample.service, func(t *testing.T) {
			t.Setenv("FCM_LIVE_TEST_CATEGORY", sample.category)
			t.Setenv("FCM_LIVE_TEST_SERVICE", sample.service)
			t.Setenv("FCM_LIVE_TEST_LOCATION", sample.location)
			t.Setenv("FCM_LIVE_TEST_AMOUNT_PAISE", sample.amount)
			TestLiveDoorstepNotificationProbe(t)
		})
		if !ok {
			t.Fatal("stopped practice queue after provider failure; do not blindly resend successful items")
		}
	}
}
