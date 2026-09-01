//go:build livefcm

package push

import (
	"context"
	"os"
	"testing"
	"time"
)

// Live FCM probe (physical-device hold point evidence): sends ONE data-only
// high-priority ringing push through the PRODUCTION FCMPusher to a real
// device token, using the real service account. Never runs in normal gates
// (build tag); requires:
//
//	FCM_LIVE_SA_PATH   — service-account JSON path
//	FCM_LIVE_PROJECT   — Firebase project id
//	FCM_LIVE_TOKEN     — the target device's registration token
func TestLiveFCMRingProbe(t *testing.T) {
	saPath, project, token := os.Getenv("FCM_LIVE_SA_PATH"), os.Getenv("FCM_LIVE_PROJECT"), os.Getenv("FCM_LIVE_TOKEN")
	if saPath == "" || project == "" || token == "" {
		t.Skip("FCM_LIVE_SA_PATH / FCM_LIVE_PROJECT / FCM_LIVE_TOKEN not set")
	}
	sa, err := os.ReadFile(saPath)
	if err != nil {
		t.Fatal(err)
	}
	pusher := NewFCMPusher(project, string(sa))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	data := map[string]string{
		"type":      "incoming_call",
		"entity_id": "live-probe",
		"deep_link": "/call/live-probe",
	}
	if ck := os.Getenv("FCM_LIVE_COLLAPSE"); ck != "" {
		data["collapse_key"] = ck
	}
	err = pusher.Send(ctx, token, "android", "Incoming call", "Live probe", data)
	if err != nil {
		t.Fatalf("live FCM send failed: %v", err)
	}
	t.Log("FCM accepted the ringing push for delivery")
}
