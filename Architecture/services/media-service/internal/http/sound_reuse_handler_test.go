package http

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Reels audit 2026-10-30, 13: POST /v1/audio/:id/use on a library sound
// that was withdrawn, switched off or is past its licence answers 410
// SOUND_UNAVAILABLE and counts nothing; GET /v1/audio/:id still answers the
// sound (posts that carry it keep playing it) with
// available_for_new_posts false and the reason.
func TestUseOfAnUnavailableLibrarySoundIsGone(t *testing.T) {
	f := newRecordFixture(t)
	lib := uuid.New()
	id := uuid.New()
	f.store.tracks[id] = &postgres.AudioTrack{ID: id, LibraryID: &lib, AudioKey: "library/s.m4a",
		Status: "ready", ReuseBlock: postgres.SoundReuseLicenseExpired}

	req := httptest.NewRequest(http.MethodPost, "/v1/audio/"+id.String()+"/use", nil)
	req.Header.Set("X-User-Id", f.viewer.String())
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone || errorCode(t, rec) != "SOUND_UNAVAILABLE" {
		t.Fatalf("use: %d %s, want 410 SOUND_UNAVAILABLE", rec.Code, rec.Body.String())
	}

	got := f.get("/v1/audio/"+id.String(), f.viewer)
	if got.Code != http.StatusOK {
		t.Fatalf("the sound no longer answers: %d %s", got.Code, got.Body.String())
	}
	if v := dataField(t, got, "available_for_new_posts"); v != "false" {
		t.Fatalf("available_for_new_posts = %s", v)
	}
	if v := dataField(t, got, "unavailable_reason"); v != "license_expired" {
		t.Fatalf("unavailable_reason = %s", v)
	}
}
