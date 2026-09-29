package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// The sound routes over httptest, on the record fixture: the REAL handler,
// the REAL service.RecordReads and a REAL delivery.Gate (no PostgreSQL).
//
// The property under test: GET /v1/audio/:audioId, /trending, /search and
// POST /v1/audio/:audioId/use answer only for sounds whose source video the
// viewer may watch, and a sound they may not hear is, everywhere, the same
// as one that does not exist (service/audio_reads.go).

// newSoundFixture is the record fixture with named, listed sounds. A second
// sound of the private video shows that one source is asked about once.
func newSoundFixture(t *testing.T) (*recordFixture, uuid.UUID) {
	t.Helper()
	f := newRecordFixture(t)
	second := uuid.New()
	f.store.tracks[f.trackPrivate].Title, f.store.tracks[f.trackPrivate].Artist = "Kitchen take one", "Asha"
	f.store.tracks[f.trackPublic].Title, f.store.tracks[f.trackPublic].Artist = "Street take", "Asha"
	f.store.tracks[f.trackOrphan].Title, f.store.tracks[f.trackOrphan].Artist = "Lost take", "Nobody"
	f.store.tracks[second] = &postgres.AudioTrack{
		ID: second, SourceMediaID: &f.privateID, Title: "Kitchen take two", Artist: "Asha",
		AudioKey: fmt.Sprintf("audio/%s/%s/audio-2.m4a", f.owner, f.privateID), Status: "ready",
	}
	f.store.listed = []uuid.UUID{f.trackPrivate, f.trackPublic, f.trackOrphan, second}
	return f, second
}

func (f *recordFixture) sound(id, viewer uuid.UUID) *httptest.ResponseRecorder {
	return f.get("/v1/audio/"+id.String(), viewer)
}

func (f *recordFixture) use(id, viewer uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/audio/"+id.String()+"/use", nil)
	if viewer != uuid.Nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// listedIDs reads data.tracks[].id, and insists the list is an array.
func listedIDs(t *testing.T, rec *httptest.ResponseRecorder) []uuid.UUID {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("list: got %d %s want 200", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Tracks json.RawMessage `json:"tracks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode list: %v (%s)", err, rec.Body.String())
	}
	if !strings.HasPrefix(strings.TrimSpace(string(env.Data.Tracks)), "[") {
		t.Fatalf("tracks is %s, want an array", env.Data.Tracks)
	}
	var rows []struct {
		ID uuid.UUID `json:"id"`
	}
	if err := json.Unmarshal(env.Data.Tracks, &rows); err != nil {
		t.Fatalf("decode tracks: %v", err)
	}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, r := range rows {
		ids = append(ids, r.ID)
	}
	return ids
}

func wantIDs(t *testing.T, what string, got []uuid.UUID, want ...uuid.UUID) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: listed %v want %v", what, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s: listed %v want %v", what, got, want)
		}
	}
}

// wantSoundDenied is wantDeniedLikeMissing plus the fields a sound carries.
func wantSoundDenied(t *testing.T, what string, got, missing *httptest.ResponseRecorder) {
	t.Helper()
	wantDeniedLikeMissing(t, what, got, missing)
	for _, leak := range []string{"audio_key", "source_media_id", "Kitchen", "Asha", "usage_count"} {
		if strings.Contains(got.Body.String(), leak) {
			t.Errorf("%s: denial body leaks %q: %s", what, leak, got.Body.String())
		}
	}
}

// ── GET /v1/audio/:audioId ──────────────────────────────────────────────

// The authority is unresolved, so the only way to a 200 is the uploader
// short-circuit.
func TestSoundOwnerSeesOwnWithoutTheAuthority(t *testing.T) {
	f, _ := newSoundFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	rec := f.sound(f.trackPrivate, f.owner)
	if rec.Code != http.StatusOK || dataField(t, rec, "title") != "Kitchen take one" {
		t.Fatalf("owner: got %d %s", rec.Code, rec.Body.String())
	}
	if f.authz.calls != 0 {
		t.Errorf("authority asked %d times for the uploader's own sound", f.authz.calls)
	}
}

func TestSoundAudience(t *testing.T) {
	f, _ := newSoundFixture(t)
	if rec := f.sound(f.trackPrivate, f.viewer); rec.Code != http.StatusOK || dataField(t, rec, "id") != f.trackPrivate.String() {
		t.Errorf("permitted viewer: got %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.sound(f.trackPublic, uuid.Nil); rec.Code != http.StatusOK {
		t.Errorf("signed out on a public video's sound: got %d %s", rec.Code, rec.Body.String())
	}
	wantSoundDenied(t, "signed out on private", f.sound(f.trackPrivate, uuid.Nil), f.sound(uuid.New(), uuid.Nil))
	wantSoundDenied(t, "stranger on private", f.sound(f.trackPrivate, f.stranger), f.sound(uuid.New(), f.stranger))
}

func TestSoundWithoutSourceIsRefused(t *testing.T) {
	f, _ := newSoundFixture(t)
	f.authz.allow = func(string, string) bool { return true }
	wantSoundDenied(t, "orphan, owner", f.sound(f.trackOrphan, f.owner), f.sound(uuid.New(), f.owner))
	gone := uuid.New()
	f.store.tracks[f.trackOrphan].SourceMediaID = &gone
	wantSoundDenied(t, "source gone", f.sound(f.trackOrphan, f.viewer), f.sound(uuid.New(), f.viewer))
}

func TestSoundUnresolvedIs503(t *testing.T) {
	f, _ := newSoundFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	if rec := f.sound(f.trackPublic, f.viewer); rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "DEPENDENCY_UNAVAILABLE" {
		t.Errorf("authority down: got %d %s want 503", rec.Code, rec.Body.String())
	}
	f.authz.err = nil
	f.store.fail = errors.New("connection reset")
	if rec := f.sound(f.trackPublic, f.viewer); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("store down: got %d %s want 503", rec.Code, rec.Body.String())
	}
}

// ── GET /v1/audio/trending, /v1/audio/search ────────────────────────────

func TestSoundListsShowOnlyWhatTheViewerMayHear(t *testing.T) {
	for _, list := range []struct{ name, path string }{
		{"trending", "/v1/audio/trending"},
		{"search", "/v1/audio/search?q=take"},
	} {
		t.Run(list.name, func(t *testing.T) {
			f, second := newSoundFixture(t)
			wantIDs(t, "signed out", listedIDs(t, f.get(list.path, uuid.Nil)), f.trackPublic)
			wantIDs(t, "stranger", listedIDs(t, f.get(list.path, f.stranger)), f.trackPublic)
			wantIDs(t, "permitted viewer", listedIDs(t, f.get(list.path, f.viewer)), f.trackPrivate, f.trackPublic, second)
			wantIDs(t, "owner", listedIDs(t, f.get(list.path, f.owner)), f.trackPrivate, f.trackPublic, second)

			body := f.get(list.path, f.stranger).Body.String()
			for _, leak := range []string{"Kitchen", f.privateID.String(), f.trackPrivate.String(), second.String(), "Lost take"} {
				if strings.Contains(body, leak) {
					t.Errorf("a stranger's list carries %q: %s", leak, body)
				}
			}
		})
	}
}

func TestSoundSearchMatchesBeforeItFilters(t *testing.T) {
	f, _ := newSoundFixture(t)
	wantIDs(t, "viewer, street", listedIDs(t, f.get("/v1/audio/search?q=street", f.viewer)), f.trackPublic)
	wantIDs(t, "stranger, kitchen", listedIDs(t, f.get("/v1/audio/search?q=kitchen", f.stranger)))
	if rec := f.get("/v1/audio/search", f.viewer); rec.Code != http.StatusBadRequest {
		t.Errorf("search without q: got %d want 400", rec.Code)
	}
}

// Two sounds of one video are one question to the authority.
func TestSoundListAsksOncePerSource(t *testing.T) {
	f, _ := newSoundFixture(t)
	listedIDs(t, f.get("/v1/audio/trending", f.viewer))
	if f.authz.calls != 2 {
		t.Errorf("authority asked %d times for two sources, want 2", f.authz.calls)
	}
}

// A list that cannot be checked is a 503, never a shorter list that looks
// complete.
func TestSoundListUnresolvedIs503(t *testing.T) {
	for _, path := range []string{"/v1/audio/trending", "/v1/audio/search?q=take"} {
		f, _ := newSoundFixture(t)
		f.authz.err = delivery.ErrDeliveryUnresolved
		rec := f.get(path, f.viewer)
		if rec.Code != http.StatusServiceUnavailable || errorCode(t, rec) != "DEPENDENCY_UNAVAILABLE" {
			t.Errorf("%s, authority down: got %d %s want 503", path, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), "tracks") {
			t.Errorf("%s: a list was served while the decision was unresolved: %s", path, rec.Body.String())
		}
	}
}

func TestSoundListFaultKeepsItsTextOnTheServer(t *testing.T) {
	f, _ := newSoundFixture(t)
	f.store.listFail = errors.New(`pq: relation "audio_tracks" does not exist`)
	rec := f.get("/v1/audio/trending", f.viewer)
	if rec.Code != http.StatusInternalServerError || strings.Contains(rec.Body.String(), "audio_tracks") {
		t.Errorf("store fault: got %d %s want 500 without the fault's text", rec.Code, rec.Body.String())
	}
}

func TestSoundListPagesAreBounded(t *testing.T) {
	f, second := newSoundFixture(t)
	wantIDs(t, "limit 1", listedIDs(t, f.get("/v1/audio/trending?limit=1", f.viewer)), f.trackPrivate)
	wantIDs(t, "offset 1", listedIDs(t, f.get("/v1/audio/trending?offset=1", f.viewer)), f.trackPublic, second)
	wantIDs(t, "past the end", listedIDs(t, f.get("/v1/audio/trending?offset=40", f.viewer)))
	wantIDs(t, "negative offset", listedIDs(t, f.get("/v1/audio/trending?offset=-5&limit=1", f.viewer)), f.trackPrivate)
}

// ── POST /v1/audio/:audioId/use ─────────────────────────────────────────

func TestSoundUseCountsOnlyWhatTheCallerMayHear(t *testing.T) {
	f, _ := newSoundFixture(t)
	if rec := f.use(f.trackPrivate, f.viewer); rec.Code != http.StatusOK {
		t.Fatalf("permitted viewer: got %d %s", rec.Code, rec.Body.String())
	}
	if rec := f.use(f.trackPublic, f.stranger); rec.Code != http.StatusOK {
		t.Fatalf("anyone on a public video's sound: got %d %s", rec.Code, rec.Body.String())
	}
	if f.store.used[f.trackPrivate] != 1 || f.store.used[f.trackPublic] != 1 {
		t.Fatalf("uses counted: %v", f.store.used)
	}

	missing := f.use(uuid.New(), f.stranger)
	wantSoundDenied(t, "stranger on private", f.use(f.trackPrivate, f.stranger), missing)
	wantSoundDenied(t, "orphan", f.use(f.trackOrphan, f.viewer), f.use(uuid.New(), f.viewer))
	wantSoundDenied(t, "signed out", f.use(f.trackPublic, uuid.Nil), f.use(uuid.New(), uuid.Nil))
	if f.store.used[f.trackPrivate] != 1 || f.store.used[f.trackPublic] != 1 || f.store.used[f.trackOrphan] != 0 {
		t.Errorf("a refused use was counted: %v", f.store.used)
	}
}

func TestSoundUseUnresolvedIs503AndCountsNothing(t *testing.T) {
	f, _ := newSoundFixture(t)
	f.authz.err = delivery.ErrDeliveryUnresolved
	if rec := f.use(f.trackPublic, f.viewer); rec.Code != http.StatusServiceUnavailable {
		t.Errorf("authority down: got %d %s want 503", rec.Code, rec.Body.String())
	}
	if len(f.store.used) != 0 {
		t.Errorf("a use was counted while the decision was unresolved: %v", f.store.used)
	}
}
