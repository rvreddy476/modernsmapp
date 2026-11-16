package http

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Retired sounds (2026-11-16, migration 032): the author deleted the source
// video, so the sound has no source. It is heard through a reel that
// already plays it (?post_id=), by that reel's audience, and by nobody
// else: never without the reel, never for a new use.

type retiredFixture struct {
	*recordFixture
	retired           uuid.UUID
	key               string
	privateReel       uuid.UUID // a post on privateID's video (admits viewer only)
	publicReel        uuid.UUID // a post on publicID's video (admits everyone)
	unrelatedLivePost uuid.UUID
}

func newRetiredFixture(t *testing.T) *retiredFixture {
	f := &retiredFixture{recordFixture: newRecordFixture(t),
		retired: uuid.New(), privateReel: uuid.New(), publicReel: uuid.New(), unrelatedLivePost: uuid.New()}
	gone := uuid.New()
	f.key = fmt.Sprintf("audio/%s/%s/audio.m4a", f.owner, gone)
	f.store.tracks[f.retired] = &postgres.AudioTrack{ID: f.retired, AudioKey: f.key, Status: postgres.SoundStatusRetired,
		Title: "Kitchen", Artist: "Asha"}
	f.store.carriers = map[[2]uuid.UUID]uuid.UUID{
		{f.retired, f.privateReel}: f.privateID,
		{f.retired, f.publicReel}:  f.publicID,
	}
	return f
}

func (f *retiredFixture) do(method, path string, viewer uuid.UUID) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if viewer != uuid.Nil {
		req.Header.Set("X-User-Id", viewer.String())
	}
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	return rec
}

func (f *retiredFixture) serveVia(method string, post, viewer uuid.UUID) *httptest.ResponseRecorder {
	path := "/v1/audio/" + f.retired.String() + "/serve"
	if post != uuid.Nil {
		path += "?post_id=" + post.String()
	}
	return f.do(method, path, viewer)
}

// A viewer of a reel that plays the retired sound hears it, through that
// reel's audience: the private reel's admitted viewer, anyone on a public
// reel (signed out included).
func TestRetiredSoundPlaysOnTheReelsThatCarryIt(t *testing.T) {
	f := newRetiredFixture(t)
	wantSoundRedirect(t, "admitted viewer of the private reel", f.serveVia(http.MethodGet, f.privateReel, f.viewer), f.key)
	wantSoundRedirect(t, "signed out on the public reel", f.serveVia(http.MethodGet, f.publicReel, uuid.Nil), f.key)
	if rec := f.serveVia(http.MethodHead, f.publicReel, f.stranger); rec.Code != http.StatusOK {
		t.Fatalf("HEAD through the public reel: %d %s", rec.Code, rec.Body.String())
	}
	rec := f.do(http.MethodGet, "/v1/audio/"+f.retired.String()+"/url?post_id="+f.publicReel.String(), f.stranger)
	if rec.Code != http.StatusOK {
		t.Fatalf("/url through the public reel: %d %s", rec.Code, rec.Body.String())
	}
	// The record, through the reel: retired and not for new posts.
	rec = f.do(http.MethodGet, "/v1/audio/"+f.retired.String()+"?post_id="+f.publicReel.String(), f.stranger)
	if rec.Code != http.StatusOK {
		t.Fatalf("record through the reel: %d %s", rec.Code, rec.Body.String())
	}
	var env struct {
		Data struct {
			Status               string `json:"status"`
			AvailableForNewPosts bool   `json:"available_for_new_posts"`
			UnavailableReason    string `json:"unavailable_reason"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Status != "retired" || env.Data.AvailableForNewPosts || env.Data.UnavailableReason != "retired" {
		t.Fatalf("retired record = %+v", env.Data)
	}
}

// Without a carrying reel a retired sound is the missing answer, to
// everyone, its creator included: no sound page, no URL, no use.
func TestRetiredSoundIsMissingWithoutACarryingReel(t *testing.T) {
	f := newRetiredFixture(t)
	missingServe := func(viewer uuid.UUID) *httptest.ResponseRecorder { return f.serve(http.MethodGet, uuid.New(), viewer) }
	for name, tc := range map[string]struct {
		post, viewer uuid.UUID
	}{
		"no reel named, the creator":     {uuid.Nil, f.owner},
		"no reel named, signed out":      {uuid.Nil, uuid.Nil},
		"a post that does not carry it":  {f.unrelatedLivePost, f.viewer},
		"stranger on the private reel":   {f.privateReel, f.stranger},
		"signed out on the private reel": {f.privateReel, uuid.Nil},
	} {
		wantSoundDenied(t, name, f.serveVia(http.MethodGet, tc.post, tc.viewer), missingServe(tc.viewer))
	}
	if rec := f.do(http.MethodGet, "/v1/audio/"+f.retired.String(), f.owner); rec.Code != http.StatusNotFound {
		t.Fatalf("sound page of a retired sound: %d, want 404", rec.Code)
	}
	if rec := f.do(http.MethodGet, "/v1/audio/"+f.retired.String()+"/url", f.viewer); rec.Code != http.StatusNotFound {
		t.Fatalf("/url of a retired sound without a reel: %d, want 404", rec.Code)
	}
	// Never for a new attachment, and the use is not counted.
	if rec := f.use(f.retired, f.viewer); rec.Code != http.StatusNotFound {
		t.Fatalf("use of a retired sound: %d %s, want 404", rec.Code, rec.Body.String())
	}
	if f.store.used[f.retired] != 0 {
		t.Fatal("a use of a retired sound was counted")
	}
	if len(f.blobs.signed) != 0 {
		t.Fatalf("a URL was signed for a denied request: %v", f.blobs.signed)
	}
}

// An unanswerable carrier lookup is a retryable 503, never a served sound or
// a cacheable 404; a post_id that is not a uuid is 400.
func TestRetiredSoundCarrierFaultsAndBadIDs(t *testing.T) {
	f := newRetiredFixture(t)
	f.store.carrierFail = errors.New("db down")
	if rec := f.serveVia(http.MethodGet, f.publicReel, f.viewer); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("carrier fault: %d %s, want 503", rec.Code, rec.Body.String())
	}
	f.store.carrierFail = nil
	for _, path := range []string{
		"/v1/audio/" + f.retired.String() + "/serve?post_id=nope",
		"/v1/audio/" + f.retired.String() + "/url?post_id=nope",
		"/v1/audio/" + f.retired.String() + "?post_id=" + uuid.Nil.String(),
	} {
		if rec := f.do(http.MethodGet, path, f.viewer); rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: %d, want 400", path, rec.Code)
		}
	}
	// A ready sound ignores the reel: its source video still decides.
	if rec := f.do(http.MethodGet, "/v1/audio/"+f.trackPrivate.String()+"/serve?post_id="+f.publicReel.String(), f.stranger); rec.Code != http.StatusNotFound {
		t.Fatalf("a ready sound was admitted through an unrelated reel: %d", rec.Code)
	}
}

// ── POST /v1/media/internal/sounds/:audioId/takedown ─────────────────────

type fakeTakedowns struct {
	objects int
	err     error
	asked   []uuid.UUID
}

func (f *fakeTakedowns) TakeDownSound(_ context.Context, id uuid.UUID) (int, error) {
	f.asked = append(f.asked, id)
	return f.objects, f.err
}

func takedownRouter(fake *fakeTakedowns) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := &Handler{takedowns: fake}
	h.WithInternalKey(recordInternalKey)
	h.RegisterRoutes(r, passthrough, passthrough)
	return r
}

func postTakedown(r *gin.Engine, id string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/media/internal/sounds/"+id+"/takedown", nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSoundTakedownRoute(t *testing.T) {
	id := uuid.New()
	key := []string{internalKeyHeader, recordInternalKey}
	for name, tc := range map[string]struct {
		err      error
		id       string
		headers  []string
		wantCode int
		wantErr  string
	}{
		"taken down":      {nil, id.String(), key, http.StatusOK, ""},
		"no such sound":   {service.ErrSoundNotFound, id.String(), key, http.StatusNotFound, "NOT_FOUND"},
		"a library sound": {service.ErrLibrarySoundTakedown, id.String(), key, http.StatusConflict, "LIBRARY_SOUND"},
		"store fault":     {errors.New("db down"), id.String(), key, http.StatusServiceUnavailable, "SOUND_TAKEDOWN_UNAVAILABLE"},
		"bad id":          {nil, "nope", key, http.StatusBadRequest, "BAD_REQUEST"},
		"no internal key": {nil, id.String(), nil, http.StatusUnauthorized, ""},
		"a gateway user":  {nil, id.String(), append(append([]string{}, key...), "X-User-Id", uuid.NewString()), http.StatusForbidden, ""},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &fakeTakedowns{objects: 2, err: tc.err}
			rec := postTakedown(takedownRouter(fake), tc.id, tc.headers...)
			if rec.Code != tc.wantCode && !(tc.wantCode == http.StatusUnauthorized && rec.Code == http.StatusForbidden) {
				t.Fatalf("got %d %s, want %d", rec.Code, rec.Body.String(), tc.wantCode)
			}
			if tc.wantErr != "" {
				var env struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &env)
				if env.Error.Code != tc.wantErr {
					t.Fatalf("code %q, want %q", env.Error.Code, tc.wantErr)
				}
			}
			reached := len(fake.asked) == 1 && fake.asked[0] == id
			if wantReach := tc.id == id.String() && tc.wantCode != http.StatusUnauthorized && tc.wantCode != http.StatusForbidden; reached != wantReach {
				t.Fatalf("service reached = %v, want %v", reached, wantReach)
			}
			if tc.wantCode == http.StatusOK {
				var env struct {
					Data struct {
						Status  string `json:"status"`
						Objects int    `json:"objects"`
					} `json:"data"`
				}
				_ = json.Unmarshal(rec.Body.Bytes(), &env)
				if env.Data.Status != "taken_down" || env.Data.Objects != 2 {
					t.Fatalf("body %s", rec.Body.String())
				}
			}
		})
	}
}
