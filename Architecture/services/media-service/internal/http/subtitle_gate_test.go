package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// The subtitle endpoints over httptest, with a fake service. No database.
//
// What is pinned here is the wiring that was missing: every subtitle READ
// asks the gate first, with the request's viewer, and a denial is answered
// the way every other media read answers one — 404, indistinguishable from
// media that does not exist. Before this, GetSubtitles and
// GetCaptionStatus (which returns the transcript in `text`) went straight
// to the store for anyone holding the media UUID.

type fakeClipsService struct {
	authorizeErr error
	authorizedAs []uuid.UUID
	subtitles    []postgres.MediaSubtitle
	status       *service.CaptionStatus
	vtt          string
	vttErr       error
	savedActor   uuid.UUID
	savedPost    uuid.UUID
	saveErr      error
}

func (f *fakeClipsService) AuthorizeMediaRead(_ context.Context, viewerID, _ uuid.UUID) error {
	f.authorizedAs = append(f.authorizedAs, viewerID)
	return f.authorizeErr
}

func (f *fakeClipsService) GetSubtitles(context.Context, uuid.UUID) ([]postgres.MediaSubtitle, error) {
	return f.subtitles, nil
}

func (f *fakeClipsService) GetCaptionStatus(context.Context, uuid.UUID) (*service.CaptionStatus, error) {
	return f.status, nil
}

func (f *fakeClipsService) CaptionTrackVTT(_ context.Context, _ uuid.UUID, _ string) (string, error) {
	return f.vtt, f.vttErr
}

func (f *fakeClipsService) SaveMediaClips(_ context.Context, actorID, postID uuid.UUID, _ []postgres.MediaClip) error {
	f.savedActor, f.savedPost = actorID, postID
	return f.saveErr
}

// captionRouter registers the real routes against the fake service.
func captionRouter(fake *fakeClipsService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{subtitles: fake}).RegisterClipsRoutes(r, func(c *gin.Context) { c.Next() })
	return r
}

func captionGet(r *gin.Engine, path, viewerID string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if viewerID != "" {
		req.Header.Set("X-User-Id", viewerID)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSubtitleReadsGoThroughTheDeliveryGate(t *testing.T) {
	mediaID := uuid.New()
	viewer := uuid.New()

	readPaths := map[string]string{
		"tracks list": "/v1/subtitles/" + mediaID.String(),
		"status":      "/v1/subtitles/" + mediaID.String() + "/status",
		"vtt track":   "/v1/subtitles/" + mediaID.String() + "/track/en.vtt",
	}

	for name, path := range readPaths {
		t.Run(name+": a denial is answered as not-found", func(t *testing.T) {
			fake := &fakeClipsService{
				authorizeErr: delivery.ErrDeliveryDenied,
				// Content the handler must never reach for.
				subtitles: []postgres.MediaSubtitle{{Language: "en", Content: "secret transcript"}},
				status:    &service.CaptionStatus{Status: "completed", Text: "secret transcript"},
				vtt:       "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nsecret transcript\n",
			}
			rec := captionGet(captionRouter(fake), path, viewer.String())
			if rec.Code != http.StatusNotFound {
				t.Fatalf("want 404 for a refused viewer, got %d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "secret transcript") {
				t.Fatalf("a refused read disclosed the transcript: %s", rec.Body.String())
			}
		})

		t.Run(name+": an unresolved authority is retryable, not a served transcript", func(t *testing.T) {
			fake := &fakeClipsService{
				authorizeErr: delivery.ErrDeliveryUnresolved,
				subtitles:    []postgres.MediaSubtitle{{Language: "en", Content: "secret transcript"}},
				status:       &service.CaptionStatus{Status: "completed", Text: "secret transcript"},
				vtt:          "WEBVTT\n",
			}
			rec := captionGet(captionRouter(fake), path, viewer.String())
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("want 503 while the content authority is unreachable, got %d", rec.Code)
			}
			if strings.Contains(rec.Body.String(), "secret transcript") {
				t.Fatalf("an unresolved read disclosed the transcript: %s", rec.Body.String())
			}
		})

		t.Run(name+": the request's viewer is the one authorized", func(t *testing.T) {
			fake := &fakeClipsService{vtt: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nhi\n"}
			captionGet(captionRouter(fake), path, viewer.String())
			if len(fake.authorizedAs) != 1 {
				t.Fatalf("the gate must be consulted exactly once, got %d", len(fake.authorizedAs))
			}
			if fake.authorizedAs[0] != viewer {
				t.Fatalf("gate asked about %s, want the request's viewer %s", fake.authorizedAs[0], viewer)
			}

			anon := &fakeClipsService{vtt: "WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nhi\n"}
			captionGet(captionRouter(anon), path, "")
			if len(anon.authorizedAs) != 1 || anon.authorizedAs[0] != uuid.Nil {
				t.Fatalf("an anonymous request must still reach the gate, as uuid.Nil: %v", anon.authorizedAs)
			}
		})
	}

	t.Run("an authorized viewer gets the tracks", func(t *testing.T) {
		fake := &fakeClipsService{subtitles: []postgres.MediaSubtitle{{Language: "en", Content: "hello"}}}
		rec := captionGet(captionRouter(fake), readPaths["tracks list"], viewer.String())
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "hello") {
			t.Fatalf("authorized read lost the caption: %s", rec.Body.String())
		}
	})

	t.Run("an authorized viewer gets the status", func(t *testing.T) {
		fake := &fakeClipsService{status: &service.CaptionStatus{Status: "completed", Text: "hello"}}
		rec := captionGet(captionRouter(fake), readPaths["status"], viewer.String())
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
		}
	})
}

func TestSubtitleTrackServesWebVTT(t *testing.T) {
	mediaID := uuid.New()
	body := "WEBVTT\n\n00:00:00.000 --> 00:00:01.500\nhello there\n"

	t.Run("content type and body", func(t *testing.T) {
		fake := &fakeClipsService{vtt: body}
		rec := captionGet(captionRouter(fake), "/v1/subtitles/"+mediaID.String()+"/track/en.vtt", uuid.New().String())
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d: %s", rec.Code, rec.Body.String())
		}
		if got := rec.Header().Get("Content-Type"); got != "text/vtt; charset=utf-8" {
			t.Fatalf("a <track src> needs text/vtt, got %q", got)
		}
		if rec.Body.String() != body {
			t.Fatalf("body = %q, want %q", rec.Body.String(), body)
		}
		if cache := rec.Header().Get("Cache-Control"); !strings.Contains(cache, "private") {
			t.Fatalf("an authorized transcript must not be cached publicly, got %q", cache)
		}
	})

	t.Run("the .vtt extension is optional", func(t *testing.T) {
		fake := &fakeClipsService{vtt: body}
		rec := captionGet(captionRouter(fake), "/v1/subtitles/"+mediaID.String()+"/track/en", uuid.New().String())
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", rec.Code)
		}
	})

	t.Run("a language with no stored track is 404", func(t *testing.T) {
		fake := &fakeClipsService{vttErr: service.ErrCaptionTrackNotFound}
		rec := captionGet(captionRouter(fake), "/v1/subtitles/"+mediaID.String()+"/track/fr.vtt", uuid.New().String())
		if rec.Code != http.StatusNotFound {
			t.Fatalf("want 404 for a language that does not exist, got %d", rec.Code)
		}
	})

	t.Run("a malformed media id never reaches the store", func(t *testing.T) {
		fake := &fakeClipsService{vtt: body}
		rec := captionGet(captionRouter(fake), "/v1/subtitles/not-a-uuid/track/en.vtt", uuid.New().String())
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("want 400, got %d", rec.Code)
		}
		if len(fake.authorizedAs) != 0 {
			t.Fatalf("a malformed id must be rejected before the gate")
		}
	})
}

func TestCreateSubtitleRefusesCallerSuppliedContentURL(t *testing.T) {
	mediaID := uuid.New()
	// The stored value becomes a <track src>. These are the two shapes that
	// make the column a sink: one executes in the viewer's page, the other
	// points at a host this service does not serve.
	for name, contentURL := range map[string]string{
		"javascript scheme": "javascript:alert(document.cookie)",
		"off-host https":    "https://evil.example/x.vtt",
		"protocol relative": "//evil.example/x.vtt",
		"data url":          "data:text/vtt;base64,V0VCVlRU",
	} {
		t.Run(name, func(t *testing.T) {
			// The handler must refuse BEFORE touching the service; the
			// nil service here would panic if it did not.
			r := captionRouter(&fakeClipsService{})
			payload, _ := json.Marshal(map[string]any{
				"language":    "en",
				"source":      "manual",
				"content":     "hello",
				"content_url": contentURL,
			})
			req := httptest.NewRequest(http.MethodPost, "/v1/subtitles/"+mediaID.String(), strings.NewReader(string(payload)))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", uuid.New().String())
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("want 400 for a caller-supplied content_url, got %d: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), "CONTENT_URL_NOT_ACCEPTED") {
				t.Fatalf("want a stable refusal code, got %s", rec.Body.String())
			}
		})
	}
}

func TestSaveClipsRefusesAStranger(t *testing.T) {
	postID := uuid.New()
	actor := uuid.New()
	payload, _ := json.Marshal(map[string]any{
		"clips": []map[string]any{{"media_asset_id": uuid.New().String(), "duration_ms": 1000}},
	})

	post := func(fake *fakeClipsService, userID string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/v1/clips/"+postID.String(), strings.NewReader(string(payload)))
		req.Header.Set("Content-Type", "application/json")
		if userID != "" {
			req.Header.Set("X-User-Id", userID)
		}
		rec := httptest.NewRecorder()
		captionRouter(fake).ServeHTTP(rec, req)
		return rec
	}

	t.Run("a stranger is refused", func(t *testing.T) {
		fake := &fakeClipsService{saveErr: service.ErrNotMediaOwner}
		if rec := post(fake, actor.String()); rec.Code != http.StatusForbidden {
			t.Fatalf("want 403 when the actor does not own the clip media, got %d: %s", rec.Code, rec.Body.String())
		}
	})

	t.Run("the actor reaches the service instead of being discarded", func(t *testing.T) {
		fake := &fakeClipsService{}
		if rec := post(fake, actor.String()); rec.Code != http.StatusOK {
			t.Fatalf("want 200 for the owner, got %d: %s", rec.Code, rec.Body.String())
		}
		if fake.savedActor != actor {
			t.Fatalf("the authenticated actor must be authorized against, got %s want %s", fake.savedActor, actor)
		}
		if fake.savedPost != postID {
			t.Fatalf("post id = %s, want %s", fake.savedPost, postID)
		}
	})

	t.Run("an unauthenticated caller is refused", func(t *testing.T) {
		fake := &fakeClipsService{}
		if rec := post(fake, ""); rec.Code != http.StatusUnauthorized {
			t.Fatalf("want 401, got %d", rec.Code)
		}
	})
}
