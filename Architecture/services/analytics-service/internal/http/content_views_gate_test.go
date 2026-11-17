package http

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"

	pgstore "github.com/atpost/analytics-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// reels-7: GET /v1/analytics/content/:contentId/views is owner-or-admin,
// through the same gate as GetContentInsights. The engine-wide internal
// key is stamped by the gateway on anonymous traffic too, so it proves
// nothing about the caller.

type fakeViewBucketsStore struct {
	fakeInsightsStore
	bucketReads int
}

func (f *fakeViewBucketsStore) GetContentViewBuckets(_ context.Context, contentID uuid.UUID) (*pgstore.ContentViewBuckets, error) {
	f.bucketReads++
	if contentID != mtubeContent {
		return &pgstore.ContentViewBuckets{}, nil
	}
	return &pgstore.ContentViewBuckets{Display: 12, Views1s: 11, Views3s: 9, Views10s: 6, Views30s: 3, Views60s: 1}, nil
}

func viewsRouter(store *fakeViewBucketsStore, internalKey string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(nil, nil).WithInternalKey(internalKey)
	h.viewBuckets = store
	h.RegisterRoutes(r)
	return r
}

const viewsTestKey = "unit-test-internal-key"

func viewsHeaders(user string, extra map[string]string) map[string]string {
	h := map[string]string{"X-Internal-Service-Key": viewsTestKey}
	if user != "" {
		h["X-User-Id"] = user
	}
	for k, v := range extra {
		h[k] = v
	}
	return h
}

func TestContentViewsRefusesAnonymousEvenWithTheInternalKey(t *testing.T) {
	store := &fakeViewBucketsStore{}
	r := viewsRouter(store, viewsTestKey)
	rec := mtubeGet(r, "/v1/analytics/content/"+mtubeContent.String()+"/views", viewsHeaders("", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous with the gateway's key: status=%d body=%s", rec.Code, rec.Body.String())
	}
	if store.bucketReads != 0 {
		t.Fatalf("buckets read for an anonymous caller")
	}
}

func TestContentViewsStrangerCannotTellContentApart(t *testing.T) {
	store := &fakeViewBucketsStore{}
	r := viewsRouter(store, viewsTestKey)
	stranger := uuid.New().String()
	others := mtubeGet(r, "/v1/analytics/content/"+mtubeContent.String()+"/views", viewsHeaders(stranger, nil))
	unknown := mtubeGet(r, "/v1/analytics/content/"+uuid.New().String()+"/views", viewsHeaders(stranger, nil))
	malformed := mtubeGet(r, "/v1/analytics/content/not-a-uuid/views", viewsHeaders(stranger, nil))
	moderator := mtubeGet(r, "/v1/analytics/content/"+mtubeContent.String()+"/views", viewsHeaders(stranger, map[string]string{adminRoleHeader: "moderator"}))
	for name, rec := range map[string]int{"others": others.Code, "unknown": unknown.Code, "malformed": malformed.Code, "moderator": moderator.Code} {
		if rec != http.StatusNotFound {
			t.Fatalf("%s: status=%d, want 404", name, rec)
		}
	}
	if others.Body.String() != unknown.Body.String() || others.Body.String() != malformed.Body.String() || others.Body.String() != moderator.Body.String() {
		t.Fatalf("bodies differ: others=%q unknown=%q malformed=%q moderator=%q", others.Body.String(), unknown.Body.String(), malformed.Body.String(), moderator.Body.String())
	}
	if store.bucketReads != 0 {
		t.Fatalf("buckets read for a stranger: %d", store.bucketReads)
	}
}

func decodeViews(t *testing.T, body []byte) map[string]int64 {
	t.Helper()
	var env struct {
		Data map[string]int64 `json:"data"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatalf("not JSON: %v %s", err, body)
	}
	return env.Data
}

func TestContentViewsOwnerAndAdminRead(t *testing.T) {
	store := &fakeViewBucketsStore{}
	r := viewsRouter(store, viewsTestKey)
	path := "/v1/analytics/content/" + mtubeContent.String() + "/views"
	want := map[string]int64{"display": 12, "views_1s": 11, "views_3s": 9, "views_10s": 6, "views_30s": 3, "views_60s": 1}

	for name, headers := range map[string]map[string]string{
		"owner":      viewsHeaders(mtubeOwner.String(), nil),
		"admin":      viewsHeaders(uuid.New().String(), map[string]string{adminRoleHeader: "admin"}),
		"superadmin": viewsHeaders(uuid.New().String(), map[string]string{adminRoleHeader: "superadmin"}),
	} {
		rec := mtubeGet(r, path, headers)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status=%d body=%s", name, rec.Code, rec.Body.String())
		}
		got := decodeViews(t, rec.Body.Bytes())
		for k, v := range want {
			if got[k] != v {
				t.Fatalf("%s: %s=%d want %d (%v)", name, k, got[k], v, got)
			}
		}
	}
	// An admin still needs a verified user.
	if rec := mtubeGet(r, path, viewsHeaders("", map[string]string{adminRoleHeader: "admin"})); rec.Code != http.StatusUnauthorized {
		t.Fatalf("admin without user: status=%d", rec.Code)
	}
}

func TestContentViewsFailsClosed(t *testing.T) {
	store := &fakeViewBucketsStore{fakeInsightsStore: fakeInsightsStore{ownsErr: errors.New("db down")}}
	r := viewsRouter(store, viewsTestKey)
	rec := mtubeGet(r, "/v1/analytics/content/"+mtubeContent.String()+"/views", viewsHeaders(mtubeOwner.String(), nil))
	if rec.Code != http.StatusServiceUnavailable || store.bucketReads != 0 {
		t.Fatalf("ownership outage: status=%d reads=%d", rec.Code, store.bucketReads)
	}
	bare := gin.New()
	New(nil, nil).RegisterRoutes(bare)
	if rec := mtubeGet(bare, "/v1/analytics/content/"+mtubeContent.String()+"/views", map[string]string{"X-User-Id": mtubeOwner.String()}); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("no store: status=%d", rec.Code)
	}
}

// The handler must keep going through the shared gate; a rewrite that
// reads the buckets first would leak them.
func TestContentViewsHandlerUsesTheOwnerGate(t *testing.T) {
	src, err := os.ReadFile("handler.go")
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	start := strings.Index(text, "func (h *Handler) GetContentViews")
	if start < 0 {
		t.Fatal("GetContentViews missing")
	}
	body := text[start:]
	if next := strings.Index(body[1:], "\nfunc "); next >= 0 {
		body = body[:next+1]
	}
	gate := strings.Index(body, "requireOwnedOrAdminContentID(c, store)")
	read := strings.Index(body, "GetContentViewBuckets(")
	if gate < 0 || read < 0 || gate > read {
		t.Fatal("GetContentViews must pass requireOwnedOrAdminContentID before reading buckets")
	}
}
