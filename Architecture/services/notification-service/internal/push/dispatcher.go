package push

import (
	"context"
	"fmt"
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
//
// CALL-LB-4: a skipped delivery is no longer silent success. A missing
// provider returns ErrProviderNotConfigured (the caller decides whether that
// is fatal — required call pushes fail closed, best-effort paths log) and an
// unknown platform is a permanent per-device rejection: that stored row can
// never be delivered and should be retired, not retried.
func (d *Dispatcher) Send(ctx context.Context, token, platform, title, body string, data map[string]string) error {
	switch platform {
	case "ios":
		if d.apns == nil {
			return fmt.Errorf("apns for %q: %w", platform, ErrProviderNotConfigured)
		}
		if err := d.apns.Send(ctx, token, platform, title, body, data); err != nil {
			slog.Error("push: APNs send failed", "error", err)
			return err
		}
	case "android", "web":
		if d.fcm == nil {
			return fmt.Errorf("fcm for %q: %w", platform, ErrProviderNotConfigured)
		}
		if err := d.fcm.Send(ctx, token, platform, title, body, data); err != nil {
			slog.Error("push: FCM send failed", "error", err)
			return err
		}
	default:
		return fmt.Errorf("unknown platform %q: %w", platform, ErrDeviceRejected)
	}
	return nil
}
