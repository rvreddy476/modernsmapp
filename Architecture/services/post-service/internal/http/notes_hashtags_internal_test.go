package http

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/post-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// GET /internal/users/:userId/followed-hashtag-notes (feed-service's Echo
// For You source): internal key only, and the answer shape feed-service
// decodes.

type routeHashtagNotes struct{ ids []uuid.UUID }

func (r routeHashtagNotes) FollowedHashtagTags(context.Context, uuid.UUID) ([]string, error) {
	return []string{"cricket"}, nil
}

func (r routeHashtagNotes) FollowedHashtagNoteIDs(context.Context, []string, uuid.UUID, time.Time, int) ([]uuid.UUID, error) {
	return r.ids, nil
}

const hashtagNotesTestKey = "followed-hashtag-notes-test-key"

func newHashtagNotesRouter(ids []uuid.UUID) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	svc := service.NewForHandlerTests(service.HandlerTestDeps{}).WithFollowedHashtagNoteStore(routeHashtagNotes{ids: ids})
	New(svc, nil).WithInternalKey(hashtagNotesTestKey).RegisterRoutes(r)
	return r
}

func TestFollowedHashtagNotesInternal_RequiresTheInternalKey(t *testing.T) {
	r := newHashtagNotesRouter(nil)
	path := "/internal/users/" + uuid.NewString() + "/followed-hashtag-notes"
	for _, key := range []string{"", "wrong"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if key != "" {
			req.Header.Set("X-Internal-Service-Key", key)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("key %q: %d, want 401", key, w.Code)
		}
	}
}

func TestFollowedHashtagNotesInternal_Answers(t *testing.T) {
	note := uuid.New()
	r := newHashtagNotesRouter([]uuid.UUID{note})
	call := func(path string) (int, []byte) {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Header.Set("X-Internal-Service-Key", hashtagNotesTestKey)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code, w.Body.Bytes()
	}
	code, body := call("/internal/users/" + uuid.NewString() + "/followed-hashtag-notes?since=2026-11-10T00:00:00Z&limit=20")
	if code != http.StatusOK {
		t.Fatalf("%d %s", code, body)
	}
	var env struct {
		Data struct {
			NoteIDs []string `json:"note_ids"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil || len(env.Data.NoteIDs) != 1 || env.Data.NoteIDs[0] != note.String() {
		t.Fatalf("body %s err %v", body, err)
	}
	if code, _ := call("/internal/users/not-a-uuid/followed-hashtag-notes"); code != http.StatusBadRequest {
		t.Fatalf("bad user id: %d", code)
	}
	if code, _ := call("/internal/users/" + uuid.NewString() + "/followed-hashtag-notes?since=yesterday"); code != http.StatusBadRequest {
		t.Fatalf("bad since: %d", code)
	}
}
