package push

import (
	"context"
	"log/slog"
)

// Dispatcher routes push notifications to the correct provider based on platform.
type Dispatcher struct {
	fcm  *FCMPusher
	apns *APNSPusher
}

// NewDispatcher creates a dispatcher with FCM and APNs pushers.
// Either pusher may be nil if not configured.
func NewDispatcher(fcm *FCMPusher, apns *APNSPusher) *Dispatcher {
	return &Dispatcher{fcm: fcm, apns: apns}
}

// Send routes the notification to the appropriate pusher.
func (d *Dispatcher) Send(ctx context.Context, token, platform, title, body string, data map[string]string) error {
	switch platform {
	case "ios":
		if d.apns == nil {
			slog.Warn("push: APNs not configured, skipping iOS push")
			return nil
		}
		if err := d.apns.Send(ctx, token, platform, title, body, data); err != nil {
			slog.Error("push: APNs send failed", "error", err)
			return err
		}
	case "android", "web":
		if d.fcm == nil {
			slog.Warn("push: FCM not configured, skipping push")
			return nil
		}
		if err := d.fcm.Send(ctx, token, platform, title, body, data); err != nil {
			slog.Error("push: FCM send failed", "error", err)
			return err
		}
	default:
		slog.Warn("push: unknown platform", "platform", platform)
	}
	return nil
}
