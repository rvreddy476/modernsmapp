package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Library sounds over httptest (2026-10-15): the REAL handler, the REAL
// service.LibrarySounds and service.RecordReads, a REAL delivery.Gate, on
// the record fixture's fakes. A library row is materialised into the same
// fake audio_tracks map the /v1/audio routes read, so "use it, then play it"
// runs end to end.
//
// The property under test: any signed-in creator can turn an active
// library row into an audio_tracks id; that sound is heard by everyone
// (signed out included) through the same presigned redirect as every other
// sound, and nothing else about the sound gates changes.

// fakeLibraryRows is the library table plus the materialise step, writing
// into the record store's tracks the way the unique index arbitrates.
type fakeLibraryRows struct {
	mu      sync.Mutex
	rows    map[uuid.UUID]*postgres.AudioLibraryTrack
	byLib   map[uuid.UUID]uuid.UUID
	records *fakeRecordStore
	inserts int
	fail    error
}

func (f *fakeLibraryRows) GetAudioLibraryTrack(_ context.Context, id uuid.UUID) (*postgres.AudioLibraryTrack, error) {
	if f.fail != nil {
		return nil, f.fail
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.rows[id]
	if !ok || !r.IsActive {
		return nil, nil
	}
	cp := *r
	return &cp, nil
}

func (f *fakeLibraryRows) EnsureLibrarySound(_ context.Context, libraryID uuid.UUID, audioKey string) (*postgres.AudioTrack, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records.mu.Lock()
	defer f.records.mu.Unlock()
	if id, ok := f.byLib[libraryID]; ok {
		cp := *f.records.tracks[id]
		return &cp, nil
	}
	r, ok := f.rows[libraryID]
	if !ok || !r.IsActive {
		return nil, pgx.ErrNoRows
	}
	lib := libraryID
	t := &postgres.AudioTrack{ID: uuid.New(), LibraryID: &lib, Title: r.Title, Artist: r.Artist,
		AudioKey: audioKey, DurationMs: r.DurationMs, Status: "ready", LicenseType: "licensed"}
	f.records.tracks[t.ID] = t
	f.records.listed = append(f.records.listed, t.ID)
	f.byLib[libraryID] = t.ID
	f.inserts++
	cp := *t
	return &cp, nil
}

type libraryFixture struct {
	*recordFixture
	lib                         *fakeLibraryRows
	keyed, linked, off, unready uuid.UUID
}

func newLibraryFixture(t *testing.T) *libraryFixture {
	t.Helper()
	f := &libraryFixture{recordFixture: newRecordFixture(t),
		keyed: uuid.New(), linked: uuid.New(), off: uuid.New(), unready: uuid.New()}
	f.lib = &fakeLibraryRows{records: f.store, byLib: map[uuid.UUID]uuid.UUID{}, rows: map[uuid.UUID]*postgres.AudioLibraryTrack{
		f.keyed:   {ID: f.keyed, Title: "Monsoon Groove", Artist: "Momentum Library", DurationMs: 30_000, AudioURL: "library/monsoon.m4a", IsActive: true},
		f.linked:  {ID: f.linked, Title: "Linked", AudioURL: "https://cdn.example.com/lib/linked.m4a", IsActive: true},
		f.off:     {ID: f.off, Title: "Retired", AudioURL: "library/retired.m4a", IsActive: false},
		f.unready: {ID: f.unready, Title: "Nowhere", AudioURL: "ftp://files.example/a.m4a", IsActive: true},
	}}
	r := gin.New()
	h := &Handler{
		records: service.NewRecordReads(f.store, f.blobs, delivery.NewGate(nil, f.authz)),
		sounds:  service.NewSounds(f.store, f.blobs, f.extractor),
		library: service.NewLibrarySounds(f.lib, f.blobs),
	}
	h.RegisterRoutes(r, passthrough, passthrough)
	h.RegisterAudioRoutes(r, passthrough)
	f.router = r
	return f
}

func (f *libraryFixture) useLibrary(id, user uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/audio-library/"+id.String()+"/use", nil)
	if user != uuid.Nil {
		req.Header.Set("X-User-Id", user.String())
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

// usedTrack decodes a 200 from /use: audio_track_id and the sound agree.
func usedTrack(t *testing.T, rec *httptest.ResponseRecorder) (uuid.UUID, map[string]json.RawMessage) {
	t.Helper()
	if rec.Code != http.StatusOK {
		t.Fatalf("use: %d %s, want 200", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			AudioTrackID uuid.UUID                  `json:"audio_track_id"`
			Sound        map[string]json.RawMessage `json:"sound"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode use: %v (%s)", err, rec.Body.String())
	}
	var soundID uuid.UUID
	_ = json.Unmarshal(env.Data.Sound["id"], &soundID)
	if env.Data.AudioTrackID == uuid.Nil || soundID != env.Data.AudioTrackID {
		t.Fatalf("audio_track_id %s, sound.id %s: want the same non-nil id", env.Data.AudioTrackID, soundID)
	}
	return env.Data.AudioTrackID, env.Data.Sound
}

func TestLibrarySoundUseThenEveryoneHearsIt(t *testing.T) {
	f := newLibraryFixture(t)

	id, sound := usedTrack(t, f.useLibrary(f.keyed, f.stranger))
	if string(sound["library_id"]) != `"`+f.keyed.String()+`"` {
		t.Fatalf("sound.library_id = %s, want %s", sound["library_id"], f.keyed)
	}
	if strings.Contains(f.useLibrary(f.keyed, f.stranger).Body.String(), "library/monsoon.m4a") {
		t.Fatal("the storage key reached the wire")
	}
	// A second creator gets the same row; nothing new is made.
	again, _ := usedTrack(t, f.useLibrary(f.keyed, f.owner))
	if again != id || f.lib.inserts != 1 {
		t.Fatalf("second use: %s (inserts %d), want %s and one insert", again, f.lib.inserts, id)
	}

	// Heard by everyone: the record, the URL, the redirect and HEAD, signed
	// out included, through the same presigned path as every sound.
	for _, viewer := range []uuid.UUID{uuid.Nil, f.stranger, f.viewer} {
		if rec := f.sound(id, viewer); rec.Code != http.StatusOK {
			t.Fatalf("GET /v1/audio/%s as %s: %d %s", id, viewer, rec.Code, rec.Body.String())
		}
		rec := f.get("/v1/audio/"+id.String()+"/serve", viewer)
		if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "https://signed.example/library/monsoon.m4a?ttl="+delivery.MaxProtectedTTL.String() {
			t.Fatalf("serve as %s: %d Location %q", viewer, rec.Code, rec.Header().Get("Location"))
		}
		req := httptest.NewRequest(http.MethodHead, "/v1/audio/"+id.String()+"/serve", nil)
		head := httptest.NewRecorder()
		f.router.ServeHTTP(head, req)
		if head.Code != http.StatusOK || head.Header().Get("Location") != "" {
			t.Fatalf("HEAD serve: %d Location %q", head.Code, head.Header().Get("Location"))
		}
	}
	// Found by search for a signed-out viewer, and usable by anyone.
	if ids := listedIDs(t, f.get("/v1/audio/search?q=monsoon", uuid.Nil)); !containsID(ids, id) {
		t.Fatalf("search for a signed-out viewer = %v, want it to include %s", ids, id)
	}
	if rec := f.use(id, f.stranger); rec.Code != http.StatusOK {
		t.Fatalf("POST /v1/audio/%s/use: %d %s", id, rec.Code, rec.Body.String())
	}
	if f.authz.calls != 0 {
		t.Fatalf("the content authority was asked %d times about a library sound", f.authz.calls)
	}
}

func TestLibrarySoundConcurrentFirstUsesConverge(t *testing.T) {
	f := newLibraryFixture(t)
	var wg sync.WaitGroup
	ids := make([]uuid.UUID, 8)
	recs := make([]*httptest.ResponseRecorder, 8)
	for i := range ids {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			recs[i] = f.useLibrary(f.keyed, uuid.New())
		}(i)
	}
	wg.Wait()
	for i, rec := range recs {
		ids[i], _ = usedTrack(t, rec)
		if ids[i] != ids[0] {
			t.Fatalf("use %d answered %s, use 0 %s", i, ids[i], ids[0])
		}
	}
	if f.lib.inserts != 1 {
		t.Fatalf("%d rows made, want 1", f.lib.inserts)
	}
}

func TestLibrarySoundUseRefusals(t *testing.T) {
	f := newLibraryFixture(t)
	for name, tc := range map[string]struct {
		path string
		user uuid.UUID
		code int
		want string
	}{
		"no caller":           {"/v1/audio-library/" + f.keyed.String() + "/use", uuid.Nil, http.StatusUnauthorized, "UNAUTHORIZED"},
		"a bad id":            {"/v1/audio-library/not-a-uuid/use", f.stranger, http.StatusBadRequest, "BAD_REQUEST"},
		"no such row":         {"/v1/audio-library/" + uuid.NewString() + "/use", f.stranger, http.StatusNotFound, "NOT_FOUND"},
		"an inactive row":     {"/v1/audio-library/" + f.off.String() + "/use", f.stranger, http.StatusNotFound, "NOT_FOUND"},
		"undeliverable audio": {"/v1/audio-library/" + f.unready.String() + "/use", f.stranger, http.StatusUnprocessableEntity, "NOT_STORED"},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, tc.path, nil)
			if tc.user != uuid.Nil {
				req.Header.Set("X-User-Id", tc.user.String())
			}
			rec := httptest.NewRecorder()
			f.router.ServeHTTP(rec, req)
			if rec.Code != tc.code || !strings.Contains(rec.Body.String(), `"`+tc.want+`"`) {
				t.Fatalf("%d %s, want %d %s", rec.Code, rec.Body.String(), tc.code, tc.want)
			}
		})
	}
	if f.lib.inserts != 0 {
		t.Fatalf("a refused use made %d rows", f.lib.inserts)
	}

	f.lib.fail = errors.New("connection reset by 10.0.0.5:5432")
	rec := f.useLibrary(f.keyed, f.stranger)
	if rec.Code != http.StatusServiceUnavailable || strings.Contains(rec.Body.String(), "10.0.0.5") {
		t.Fatalf("store fault: %d %s, want 503 without the cause", rec.Code, rec.Body.String())
	}
}

// Withdrawing the materialised sound (status no longer 'ready') silences it
// everywhere, and /use stops answering it.
func TestLibrarySoundWithdrawn(t *testing.T) {
	f := newLibraryFixture(t)
	id, _ := usedTrack(t, f.useLibrary(f.keyed, f.stranger))
	f.store.tracks[id].Status = "deleted"
	if rec := f.useLibrary(f.keyed, f.stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("use of a withdrawn sound: %d %s", rec.Code, rec.Body.String())
	}
	wantDeniedLikeMissing(t, "a withdrawn library sound", f.sound(id, uuid.Nil), f.sound(uuid.New(), uuid.Nil))
	if rec := f.get("/v1/audio/"+id.String()+"/serve", f.stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("serve of a withdrawn sound: %d", rec.Code)
	}
	if ids := listedIDs(t, f.get("/v1/audio/search?q=monsoon", f.stranger)); containsID(ids, id) {
		t.Fatalf("a withdrawn library sound is still listed: %v", ids)
	}
}

// A library row may name an https URL; its sound redirects there as it is.
// Only a library sound may: an extracted sound's key is always signed.
func TestLibrarySoundHTTPSAudioAndPreview(t *testing.T) {
	f := newLibraryFixture(t)
	id, _ := usedTrack(t, f.useLibrary(f.linked, f.stranger))
	rec := f.get("/v1/audio/"+id.String()+"/serve", uuid.Nil)
	if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != "https://cdn.example.com/lib/linked.m4a" {
		t.Fatalf("serve: %d Location %q", rec.Code, rec.Header().Get("Location"))
	}

	f.store.tracks[f.trackPublic].AudioKey = "https://evil.example/a.m4a"
	rec = f.get("/v1/audio/"+f.trackPublic.String()+"/serve", uuid.Nil)
	if loc := rec.Header().Get("Location"); rec.Code != http.StatusTemporaryRedirect || !strings.HasPrefix(loc, "https://signed.example/") {
		t.Fatalf("an extracted sound's URL-shaped key was not signed: %d %q", rec.Code, loc)
	}

	// The preview of a row not yet used: the same redirect, public.
	for row, want := range map[uuid.UUID]string{
		f.keyed:  "https://signed.example/library/monsoon.m4a?ttl=" + delivery.MaxProtectedTTL.String(),
		f.linked: "https://cdn.example.com/lib/linked.m4a",
	} {
		rec := f.get("/v1/audio-library/"+row.String()+"/serve", uuid.Nil)
		if rec.Code != http.StatusTemporaryRedirect || rec.Header().Get("Location") != want {
			t.Fatalf("preview of %s: %d Location %q, want %q", row, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	for _, row := range []uuid.UUID{f.off, f.unready, uuid.New()} {
		if rec := f.get("/v1/audio-library/"+row.String()+"/serve", uuid.Nil); rec.Code != http.StatusNotFound {
			t.Fatalf("preview of %s: %d, want 404", row, rec.Code)
		}
	}
	req := httptest.NewRequest(http.MethodHead, "/v1/audio-library/"+f.keyed.String()+"/serve", nil)
	head := httptest.NewRecorder()
	f.router.ServeHTTP(head, req)
	if head.Code != http.StatusOK || head.Header().Get("Content-Type") != "audio/mp4" {
		t.Fatalf("HEAD preview: %d %q", head.Code, head.Header().Get("Content-Type"))
	}
}

func containsID(ids []uuid.UUID, id uuid.UUID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}
