package http

import (
	"bytes"
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

// H1: slot writes are tied to the owner entity. The refusal happens before
// any store access, so a zero Service is enough to prove it.

func slotRouter() *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{svc: &service.Service{}}).RegisterSlotRoutes(r, func(c *gin.Context) { c.Next() })
	return r
}

func slotCall(r *gin.Engine, method, path, userID, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", userID)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestSlotWritesRefuseAnOwnerTheCallerDoesNotOwn(t *testing.T) {
	caller, other := uuid.New(), uuid.New()
	body := `{"media_asset_id":"` + uuid.New().String() + `"}`
	cases := map[string]string{
		"someone else's profile": "/v1/media/slots/profile/" + other.String() + "/avatar",
		"a channel":              "/v1/media/slots/channel/" + other.String() + "/banner",
		"a group":                "/v1/media/slots/group/" + other.String() + "/cover",
		"a module profile":       "/v1/media/slots/module_profile/" + other.String() + "/avatar",
		// Not verifiable from media-service: fail closed even if the id
		// happens to equal the caller's.
		"a channel named by the caller's own id": "/v1/media/slots/channel/" + caller.String() + "/banner",
	}
	for name, path := range cases {
		for _, method := range []string{http.MethodPut, http.MethodDelete} {
			t.Run(method+" "+name, func(t *testing.T) {
				rec := slotCall(slotRouter(), method, path, caller.String(), body)
				if rec.Code != http.StatusForbidden {
					t.Fatalf("want 403, got %d: %s", rec.Code, rec.Body.String())
				}
			})
		}
	}
}

// M6: renditions go through the delivery gate; object keys and errors are
// the uploader's alone.

func renditionsFixture() *service.RenditionStatusResponse {
	key, msg := "uploads/"+uuid.New().String()+"/raw.mp4", "ffmpeg: boom"
	return &service.RenditionStatusResponse{
		MediaID: uuid.New(),
		Renditions: []postgres.MediaRendition{
			{Quality: "720p", Status: "failed", ObjectKey: &key, ErrorMessage: &msg},
		},
	}
}

func renditionsRouter(f *fakeClipsService) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	(&Handler{subtitles: f}).RegisterRenditionRoutes(r, func(c *gin.Context) { c.Next() })
	return r
}

func TestRenditionsAreGatedAndRedacted(t *testing.T) {
	owner, stranger := uuid.New(), uuid.New()
	path := "/v1/media/" + uuid.New().String() + "/renditions"

	t.Run("signed-out or refused viewer gets not-found", func(t *testing.T) {
		f := &fakeClipsService{authorizeErr: delivery.ErrDeliveryDenied, renditions: renditionsFixture()}
		for _, viewer := range []string{"", stranger.String()} {
			rec := captionGet(renditionsRouter(f), path, viewer)
			if rec.Code != http.StatusNotFound || strings.Contains(rec.Body.String(), "uploads/") {
				t.Fatalf("viewer %q: want clean 404, got %d %s", viewer, rec.Code, rec.Body.String())
			}
		}
	})
	t.Run("unresolved authority is retryable", func(t *testing.T) {
		f := &fakeClipsService{authorizeErr: delivery.ErrDeliveryUnresolved, renditions: renditionsFixture()}
		if rec := captionGet(renditionsRouter(f), path, stranger.String()); rec.Code != http.StatusServiceUnavailable {
			t.Fatalf("want 503, got %d", rec.Code)
		}
	})
	t.Run("a permitted non-owner gets no object key or error", func(t *testing.T) {
		f := &fakeClipsService{owner: owner, renditions: renditionsFixture()}
		rec := captionGet(renditionsRouter(f), path, stranger.String())
		if rec.Code != http.StatusOK {
			t.Fatalf("want 200, got %d", rec.Code)
		}
		if strings.Contains(rec.Body.String(), "object_key") || strings.Contains(rec.Body.String(), "error_message") {
			t.Fatalf("non-owner saw private fields: %s", rec.Body.String())
		}
	})
	t.Run("the uploader keeps them", func(t *testing.T) {
		f := &fakeClipsService{owner: owner, renditions: renditionsFixture()}
		rec := captionGet(renditionsRouter(f), path, owner.String())
		if !strings.Contains(rec.Body.String(), "object_key") || !strings.Contains(rec.Body.String(), "error_message") {
			t.Fatalf("uploader lost fields: %s", rec.Body.String())
		}
	})
}

// M7: clip reads are gated; clip writes name a post that must be the actor's.

func TestClipReadsAreGated(t *testing.T) {
	postID := uuid.New()
	path := "/v1/clips/" + postID.String()
	clips := []postgres.MediaClip{{PostID: postID, MediaAssetID: uuid.New(), DurationMs: 1000}}

	rec := captionGet(captionRouter(&fakeClipsService{authorizeErr: delivery.ErrDeliveryDenied, clips: clips}), path, "")
	if rec.Code != http.StatusOK || strings.Contains(rec.Body.String(), "media_asset_id") {
		t.Fatalf("signed-out read of a private post leaked clips: %d %s", rec.Code, rec.Body.String())
	}
	rec = captionGet(captionRouter(&fakeClipsService{authorizeErr: delivery.ErrDeliveryUnresolved, clips: clips}), path, "")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("want 503 when the authority is down, got %d", rec.Code)
	}
	rec = captionGet(captionRouter(&fakeClipsService{clips: clips}), path, uuid.New().String())
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "media_asset_id") {
		t.Fatalf("a permitted viewer must see the clips: %d %s", rec.Code, rec.Body.String())
	}
}

func TestSaveClipsForeignPostIs403AndUnresolvedIs503(t *testing.T) {
	post := uuid.New()
	body := `{"clips":[{"media_asset_id":"` + uuid.New().String() + `","duration_ms":1000}]}`
	for name, tc := range map[string]struct {
		err  error
		want int
	}{
		"post not the actor's": {service.ErrPostNotAuthored, http.StatusForbidden},
		"lookup unresolved":    {delivery.ErrDeliveryUnresolved, http.StatusServiceUnavailable},
	} {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodPost, "/v1/clips/"+post.String(), strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-User-Id", uuid.New().String())
			rec := httptest.NewRecorder()
			captionRouter(&fakeClipsService{saveErr: tc.err}).ServeHTTP(rec, req)
			if rec.Code != tc.want {
				t.Fatalf("want %d, got %d: %s", tc.want, rec.Code, rec.Body.String())
			}
		})
	}
}
