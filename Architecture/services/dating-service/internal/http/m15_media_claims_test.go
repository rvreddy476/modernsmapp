package http

import (
	"net/http"
	"testing"

	"github.com/atpost/dating-service/internal/mediaclient"
	"github.com/atpost/dating-service/internal/store"
	"github.com/google/uuid"
)

// A prompt's clip slot holds a media-service claim on its clip from before
// the row names it until it stops naming it (service/media_claims.go); a
// replaced or removed clip is released before media-service is asked to
// delete it, because that purge refuses a claimed asset. DB-backed (the M15
// deck rig); CI runs it.
func TestM15PromptClipClaims(t *testing.T) {
	d, m := newM15Deck(t, true)
	claims := newStubClaims()
	d.env.svc.SetMediaClaims(claims)
	slot := mediaclient.KindPromptClip + "|" + store.PromptClipRef(d.viewer, 1)
	held := func(id uuid.UUID) bool {
		claims.mu.Lock()
		defer claims.mu.Unlock()
		return claims.held[slot][id]
	}

	first := m.add(d.viewer, "audio", "passed", 5000)
	if rec := d.putClip(d.viewer, 1, first); rec.code != http.StatusOK {
		t.Fatalf("put: %d %s", rec.code, rec.body)
	}
	if !held(first) {
		t.Fatal("an attached clip holds no claim")
	}

	second := m.add(d.viewer, "video", "passed", 6000)
	if rec := d.putClip(d.viewer, 1, second); rec.code != http.StatusOK {
		t.Fatalf("replace: %d %s", rec.code, rec.body)
	}
	if !held(second) || held(first) {
		t.Fatalf("after replacing: second held=%v, first held=%v", held(second), held(first))
	}

	rec := contractDo(d.env.r, http.MethodDelete, "/v1/dating/prompts/1/clip", ``, d.viewer)
	if rec.Code != http.StatusOK && rec.Code != http.StatusNoContent {
		t.Fatalf("delete: %d %s", rec.Code, rec.Body.String())
	}
	if held(second) {
		t.Fatal("a removed clip is still claimed")
	}

	// No claim client: attaching is refused, never stored unclaimed.
	d.env.svc.SetMediaClaims(nil)
	third := m.add(d.viewer, "audio", "passed", 5000)
	if rec := d.putClip(d.viewer, 2, third); rec.code != http.StatusServiceUnavailable {
		t.Fatalf("no claim client: %d %s, want 503 CLIP_MEDIA_UNAVAILABLE", rec.code, rec.body)
	}
}
