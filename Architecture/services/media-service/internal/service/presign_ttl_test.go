package service

import (
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
)

// P-9 (2026-09-29): every presigned GET this service hands to a caller
// outside the delivery gate lives no longer than the gate's own cap, because
// a presigned URL cannot be revoked before it expires. The one longer window
// is internal-only (the captions backend's source fetch) and is pinned here
// so a change to it is a deliberate one.
func TestPresignLifetimesOutsideTheGateAreCapped(t *testing.T) {
	if defaultURLExpiry > delivery.MaxProtectedTTL {
		t.Fatalf("defaultURLExpiry %s exceeds the delivery gate's cap %s", defaultURLExpiry, delivery.MaxProtectedTTL)
	}
	if captionsSourceURLExpiry != 30*time.Minute {
		t.Fatalf("captionsSourceURLExpiry changed to %s: it is internal-only and justified at 30m; update the justification with it", captionsSourceURLExpiry)
	}
}
