package matcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"

	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const testGraphKey = "test-internal-key"

// fakeGraph mirrors graph-service's engine for the route the matcher uses:
// RequireInternalKey on every route, then /v1/graph/:userId/following-ids
// registered beside the static routes it shares a prefix with. Any other
// path is gin's 404, which is what the retired /v1/graph/follows/:id got.
func fakeGraph(t *testing.T, following map[uuid.UUID][]string, hits *atomic.Int32, lastLimit *atomic.Value) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sharedmiddleware.RequireInternalKey(testGraphKey))
	v1 := r.Group("/v1/graph")
	v1.GET("/relationship", func(c *gin.Context) { c.Status(http.StatusOK) })
	v1.GET("/following/:userId", func(c *gin.Context) { c.Status(http.StatusOK) })
	v1.GET("/:userId/following-ids", func(c *gin.Context) {
		hits.Add(1)
		lastLimit.Store(c.Query("limit"))
		id, err := uuid.Parse(c.Param("userId"))
		if err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		ids := following[id]
		if ids == nil {
			ids = []string{}
		}
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"items": ids, "count": len(ids)}})
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestFollowsOverlapReadsFollowingIDsWithInternalKey(t *testing.T) {
	viewer, candidate := uuid.New(), uuid.New()
	shared1, shared2, onlyA, onlyB := uuid.NewString(), uuid.NewString(), uuid.NewString(), uuid.NewString()
	var hits atomic.Int32
	var lastLimit atomic.Value
	srv := fakeGraph(t, map[uuid.UUID][]string{
		viewer:    {shared1, shared2, onlyA},
		candidate: {shared1, shared2, onlyB},
	}, &hits, &lastLimit)

	p := NewHTTPGraphProvider(srv.URL+"/", "http://community.invalid", testGraphKey)
	got := p.FollowsOverlap(context.Background(), viewer, candidate)

	// |{s1,s2}| / |{s1,s2,a,b}|
	if got != 0.5 {
		t.Fatalf("FollowsOverlap = %v, want 0.5", got)
	}
	if hits.Load() != 2 {
		t.Fatalf("following-ids hits = %d, want 2", hits.Load())
	}
	if l, _ := lastLimit.Load().(string); l != strconv.Itoa(followingIDsLimit) {
		t.Fatalf("limit = %q, want %d", l, followingIDsLimit)
	}
}

func TestFollowsOverlapWithoutKeyIsRefusedAndCollapsesToZero(t *testing.T) {
	viewer, candidate := uuid.New(), uuid.New()
	same := uuid.NewString()
	var hits atomic.Int32
	var lastLimit atomic.Value
	srv := fakeGraph(t, map[uuid.UUID][]string{viewer: {same}, candidate: {same}}, &hits, &lastLimit)

	p := NewHTTPGraphProvider(srv.URL, "http://community.invalid", "")
	if got := p.FollowsOverlap(context.Background(), viewer, candidate); got != 0 {
		t.Fatalf("FollowsOverlap without key = %v, want 0", got)
	}
	if hits.Load() != 0 {
		t.Fatalf("handler reached %d times without the key; RequireInternalKey should refuse", hits.Load())
	}
}

func TestNewHTTPGraphProviderDefaultsToGraphServicePort(t *testing.T) {
	p := NewHTTPGraphProvider("", "", "")
	if p.graphBase != "http://graph-service:8083" {
		t.Fatalf("default graph base = %q, want http://graph-service:8083", p.graphBase)
	}
}

// fakeCommunity mirrors community-service's /v1/communities routes:
// RequireInternalKey on the engine, /my paged by limit (<=100) and offset for
// the X-User-Id user, and /:communityId beside it (the retired /me path landed
// there and was a 400).
func fakeCommunity(t *testing.T, memberships map[uuid.UUID][]string) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sharedmiddleware.RequireInternalKey(testGraphKey))
	v1 := r.Group("/v1/communities")
	v1.GET("/my", func(c *gin.Context) {
		uid, err := uuid.Parse(c.GetHeader("X-User-Id"))
		if err != nil {
			c.Status(http.StatusUnauthorized)
			return
		}
		limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
		if limit <= 0 || limit > 100 {
			limit = 20
		}
		offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))
		all := memberships[uid]
		page := []gin.H{}
		for i := offset; i < len(all) && i < offset+limit; i++ {
			page = append(page, gin.H{"id": all[i], "name": "c"})
		}
		c.JSON(http.StatusOK, gin.H{"data": page})
	})
	v1.GET("/:communityId", func(c *gin.Context) {
		if _, err := uuid.Parse(c.Param("communityId")); err != nil {
			c.Status(http.StatusBadRequest)
			return
		}
		c.Status(http.StatusOK)
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func manyIDs(n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = uuid.NewString()
	}
	return out
}

func TestCommunitiesOverlapReadsMyAcrossPages(t *testing.T) {
	viewer, candidate := uuid.New(), uuid.New()
	// 150 communities for the viewer, so the shared ones sit on page two.
	viewerAll := manyIDs(150)
	shared := viewerAll[120:]                      // 30 shared
	candidateAll := append(manyIDs(30), shared...) // 30 own + 30 shared
	srv := fakeCommunity(t, map[uuid.UUID][]string{viewer: viewerAll, candidate: candidateAll})

	p := NewHTTPGraphProvider("http://graph.invalid", srv.URL+"/", testGraphKey)
	got := p.CommunitiesOverlap(context.Background(), viewer, candidate)

	// |shared| / |union| = 30 / (150 + 30)
	want := 30.0 / 180.0
	if got < want-1e-9 || got > want+1e-9 {
		t.Fatalf("CommunitiesOverlap = %v, want %v", got, want)
	}
}

func TestCommunitiesOverlapWithoutKeyCollapsesToZero(t *testing.T) {
	viewer, candidate := uuid.New(), uuid.New()
	same := uuid.NewString()
	srv := fakeCommunity(t, map[uuid.UUID][]string{viewer: {same}, candidate: {same}})
	p := NewHTTPGraphProvider("http://graph.invalid", srv.URL, "")
	if got := p.CommunitiesOverlap(context.Background(), viewer, candidate); got != 0 {
		t.Fatalf("CommunitiesOverlap without key = %v, want 0", got)
	}
}

func TestNewHTTPGraphProviderDefaultsToCommunityServicePort(t *testing.T) {
	p := NewHTTPGraphProvider("", "", "")
	if p.communityBase != "http://community-service:8107" {
		t.Fatalf("default community base = %q, want http://community-service:8107", p.communityBase)
	}
}
