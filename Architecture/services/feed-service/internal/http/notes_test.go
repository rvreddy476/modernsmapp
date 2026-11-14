package http

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/atpost/feed-service/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ECHO_ENABLED (echo-contract.md "Switches"): off means both note routes
// answer 503 ECHO_NOT_ENABLED; on, they are signed-in routes.

func notesRouter(t *testing.T, enabled string) *gin.Engine {
	t.Helper()
	t.Setenv("ECHO_ENABLED", enabled)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(nil, nil, nil)).RegisterRoutes(r)
	return r
}

func notesGet(r *gin.Engine, path, user string) (int, string) {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if user != "" {
		req.Header.Set("X-User-Id", user)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var body struct {
		Error *struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	code := ""
	if body.Error != nil {
		code = body.Error.Code
	}
	return w.Code, code
}

func TestNotesRoutes_SwitchedOffAnswer503(t *testing.T) {
	for _, enabled := range []string{"", "false", "no"} {
		r := notesRouter(t, enabled)
		for _, path := range []string{"/v1/feed/notes/following", "/v1/feed/notes/for-you"} {
			status, code := notesGet(r, path, uuid.NewString())
			if status != http.StatusServiceUnavailable || code != "ECHO_NOT_ENABLED" {
				t.Errorf("ECHO_ENABLED=%q %s: got %d %q, want 503 ECHO_NOT_ENABLED", enabled, path, status, code)
			}
		}
	}
}

func TestNotesRoutes_SwitchedOnRequireSignIn(t *testing.T) {
	r := notesRouter(t, "true")
	for _, path := range []string{"/v1/feed/notes/following", "/v1/feed/notes/for-you"} {
		status, code := notesGet(r, path, "")
		if status != http.StatusUnauthorized || code != "UNAUTHORIZED" {
			t.Errorf("%s signed out: got %d %q, want 401 UNAUTHORIZED", path, status, code)
		}
	}
}

func TestNotesRoutes_BadCursorIs400(t *testing.T) {
	r := notesRouter(t, "true")
	for _, path := range []string{"/v1/feed/notes/following?cursor=bogus!", "/v1/feed/notes/for-you?cursor=bogus!"} {
		if status, code := notesGet(r, path, uuid.NewString()); status != http.StatusBadRequest || code != "INVALID_CURSOR" {
			t.Errorf("%s: got %d %q, want 400 INVALID_CURSOR", path, status, code)
		}
	}
}

func TestForYouCursorRoundTripAndRejects(t *testing.T) {
	in := service.ForYouCursor{Snapshot: "0a1b2c3d4e5f6a7b", Offset: 40}
	got, err := decodeForYouCursor(encodeForYouCursor(in))
	if err != nil || got != in {
		t.Fatalf("round trip: got %+v, %v", got, err)
	}
	for _, raw := range []string{"%%%", "Zm9v", encodeForYouCursor(service.ForYouCursor{Snapshot: "X!", Offset: 1})} {
		if _, err := decodeForYouCursor(raw); err == nil {
			t.Errorf("cursor %q accepted", raw)
		}
	}
}

// A notes feed page built from post-service's golden Note (copied byte for
// byte into internal/service/testdata/contracts/notes) decodes to that Note
// unchanged, v2 fields included.
func TestNotesPage_AnswersPostServiceNoteJSON(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "service", "testdata", "contracts", "notes", "note_v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	var ids struct {
		ID       uuid.UUID `json:"id"`
		AuthorID uuid.UUID `json:"author_id"`
	}
	if err := json.Unmarshal(fixture.Data, &ids); err != nil {
		t.Fatal(err)
	}
	viewer := uuid.New()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/internal/notes/batch":
			if r.Header.Get("X-User-Id") != viewer.String() {
				t.Errorf("notes batch X-User-Id = %q, want the viewer", r.Header.Get("X-User-Id"))
			}
			_, _ = w.Write([]byte(`{"data":{"items":[` + string(fixture.Data) + `]}}`))
		case "/v1/internal/graph/can":
			_, _ = w.Write([]byte(`{"data":{"` + ids.AuthorID.String() + `":true}}`))
		case "/v1/internal/keyword-filters":
			_, _ = w.Write([]byte(`{"data":{"keywords":[]}}`))
		default:
			t.Errorf("unexpected upstream %s", r.URL.Path)
		}
	}))
	defer upstream.Close()
	t.Setenv("POST_SERVICE_URL", upstream.URL)
	t.Setenv("GRAPH_SERVICE_URL", upstream.URL)
	t.Setenv("TRUST_SAFETY_SERVICE_URL", upstream.URL)
	gin.SetMode(gin.TestMode)
	h := New(service.New(nil, nil, nil))

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/v1/feed/notes/following", nil)
	h.writeNotesPage(c, viewer, []service.FeedItem{{PostID: ids.ID, AuthorID: ids.AuthorID, ContentType: service.NoteContentType}}, nil)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body.String())
	}

	var page struct {
		Data struct {
			Items      []json.RawMessage `json:"items"`
			NextCursor *string           `json:"next_cursor"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &page); err != nil {
		t.Fatal(err)
	}
	if len(page.Data.Items) != 1 || page.Data.NextCursor != nil {
		t.Fatalf("page = %s", w.Body.String())
	}
	var want, got any
	_ = json.Unmarshal(fixture.Data, &want)
	if err := json.Unmarshal(page.Data.Items[0], &got); err != nil {
		t.Fatal(err)
	}
	wb, _ := json.Marshal(want)
	gb, _ := json.Marshal(got)
	if string(wb) != string(gb) {
		t.Fatalf("feed Note differs from post-service's.\n--- want\n%s\n--- got\n%s", wb, gb)
	}
	var note struct {
		ViewCount           int             `json:"view_count"`
		BookmarkCount       int             `json:"bookmark_count"`
		ViewerHasBookmarked bool            `json:"viewer_has_bookmarked"`
		QuoteCount          int             `json:"quote_count"`
		Status              string          `json:"status"`
		ScheduledAt         *string         `json:"scheduled_at"`
		Poll                json.RawMessage `json:"poll"`
	}
	if err := json.Unmarshal(page.Data.Items[0], &note); err != nil {
		t.Fatal(err)
	}
	if note.ViewCount != 7 || note.BookmarkCount != 1 || !note.ViewerHasBookmarked || note.QuoteCount != 1 ||
		note.Status != "published" || note.ScheduledAt != nil || len(note.Poll) == 0 {
		t.Fatalf("v2 fields = %+v", note)
	}
}
