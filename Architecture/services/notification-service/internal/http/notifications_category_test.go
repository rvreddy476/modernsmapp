package http

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/atpost/notification-service/internal/service"
	"github.com/atpost/notification-service/internal/store/scylla"
	"github.com/gin-gonic/gin"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

type stubInbox struct {
	rows  []scylla.Notification
	calls int
}

func (s *stubInbox) GetNotificationsWithCursor(_ context.Context, _ uuid.UUID, _ int, _ *gocql.UUID, limit int) ([]scylla.Notification, error) {
	s.calls++
	if limit > len(s.rows) {
		limit = len(s.rows)
	}
	return s.rows[:limit], nil
}

func newCategoryRouter(t *testing.T, types ...string) (*gin.Engine, *stubInbox) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	base := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	inbox := &stubInbox{}
	for i, typ := range types {
		at := base.Add(-time.Duration(i) * time.Minute)
		inbox.rows = append(inbox.rows, scylla.Notification{Bucket: 202610, TS: gocql.UUIDFromTime(at), Type: typ})
	}
	svc := service.New(nil, nil)
	svc.SetInboxPageReader(inbox)
	r := gin.New()
	r.GET("/v1/notifications", New(svc, nil).GetNotifications)
	return r, inbox
}

type listEnvelope struct {
	Data []struct {
		Type string `json:"type"`
	} `json:"data"`
	Error *struct {
		Code    string              `json:"code"`
		Message string              `json:"message"`
		Details map[string][]string `json:"details"`
	} `json:"error"`
	Meta *struct {
		NextCursor string `json:"next_cursor"`
	} `json:"meta"`
}

func getList(t *testing.T, r *gin.Engine, query string) (int, listEnvelope) {
	t.Helper()
	req := httptest.NewRequest("GET", "/v1/notifications"+query, nil)
	req.Header.Set("X-User-Id", uuid.NewString())
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	var env listEnvelope
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("%s: undecodable body %q: %v", query, w.Body.String(), err)
	}
	return w.Code, env
}

// Contract: GET /v1/notifications?category=likes returns only like rows in the
// standard {data, meta} envelope, newest first, with a next_cursor when more
// likes follow.
func TestGetNotifications_CategoryLikesContract(t *testing.T) {
	r, _ := newCategoryRouter(t,
		"reaction", "comment", "follow", "post.liked", "mention", "comment_reaction", "dm", "post.super_liked")

	code, env := getList(t, r, "?category=likes&limit=2")
	if code != 200 || env.Error != nil {
		t.Fatalf("status %d, error %+v", code, env.Error)
	}
	if len(env.Data) != 2 || env.Data[0].Type != "reaction" || env.Data[1].Type != "post.liked" {
		t.Fatalf("data = %+v", env.Data)
	}
	if env.Meta == nil || env.Meta.NextCursor == "" {
		t.Fatal("more likes follow but meta.next_cursor is missing")
	}

	code, env = getList(t, r, "?category=likes&limit=20")
	if code != 200 || len(env.Data) != 4 {
		t.Fatalf("status %d, data %+v", code, env.Data)
	}
	for _, n := range env.Data {
		switch n.Type {
		case "reaction", "post.liked", "comment_reaction", "post.super_liked":
		default:
			t.Errorf("non-like type %q under likes", n.Type)
		}
	}
	if env.Meta != nil && env.Meta.NextCursor != "" {
		t.Errorf("last page carries next_cursor %q", env.Meta.NextCursor)
	}
}

func TestGetNotifications_EachCategory(t *testing.T) {
	r, _ := newCategoryRouter(t,
		"reaction", "comment", "comment.replied", "follow", "friend_request", "mention", "comment_mention",
		"qa.answer.created", "dm", "post_reposted")
	want := map[string][]string{
		"likes":     {"reaction"},
		"comments":  {"comment", "comment.replied"},
		"followers": {"follow", "friend_request"},
		"mentions":  {"mention", "comment_mention"},
		"qa":        {"qa.answer.created"},
		"activity":  {"reaction", "comment", "comment.replied", "follow", "friend_request"},
	}
	for category, types := range want {
		code, env := getList(t, r, "?category="+category)
		if code != 200 || len(env.Data) != len(types) {
			t.Fatalf("%s: status %d, data %+v", category, code, env.Data)
		}
		for i, typ := range types {
			if env.Data[i].Type != typ {
				t.Errorf("%s: row %d = %q, want %q", category, i, env.Data[i].Type, typ)
			}
		}
	}
	for _, all := range []string{"", "?category=all", "?category="} {
		code, env := getList(t, r, all)
		if code != 200 || len(env.Data) != 10 {
			t.Fatalf("%q: status %d, %d rows, want 10", all, code, len(env.Data))
		}
	}
}

func TestGetNotifications_UnknownCategoryIs400(t *testing.T) {
	r, inbox := newCategoryRouter(t, "reaction")
	code, env := getList(t, r, "?category=bogus")
	if code != 400 {
		t.Fatalf("status %d, want 400", code)
	}
	if env.Error == nil || env.Error.Code != "INVALID_CATEGORY" {
		t.Fatalf("error = %+v, want code INVALID_CATEGORY", env.Error)
	}
	if len(env.Error.Details["supported"]) == 0 || env.Error.Details["supported"][0] != "all" {
		t.Errorf("details.supported = %v", env.Error.Details["supported"])
	}
	if inbox.calls != 0 {
		t.Errorf("unknown category read the inbox (%d calls)", inbox.calls)
	}
}
