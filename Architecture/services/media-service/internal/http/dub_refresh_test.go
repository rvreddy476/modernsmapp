package http

import (
	"testing"
	"time"

	"github.com/atpost/media-service/internal/delivery"
)

// A dub's playback_url (/v1/media/<id>/serve/dub_<lang>_<rung>) is the
// refresh route for its progressive MP4, as hls_url is for HLS: every GET is
// authorized again and answered 307 to a freshly signed object URL. That only
// holds while a client may not reuse a cached 307 for longer than the URL it
// points at stays valid; otherwise re-preparing the same playback_url would
// replay a dead signature.
func TestServeRedirectCacheNeverOutlivesItsSignature(t *testing.T) {
	cached := time.Duration(redirectCacheTTL) * time.Second
	if cached <= 0 || cached >= delivery.GuaranteedProtectedTTL {
		t.Fatalf("a cached serve redirect (%s) may outlive the signature it points at (at least %s left)",
			cached, delivery.GuaranteedProtectedTTL)
	}
}
