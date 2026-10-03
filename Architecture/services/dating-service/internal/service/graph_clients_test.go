package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

const testGraphInternalKey = "test-graph-internal-key"

// graphRel is graph-service's /v1/graph/relationship body for one ordered
// pair (actor=user_id, target=other_id).
type graphRel struct {
	Follows    bool `json:"follows"`
	FollowedBy bool `json:"followed_by"`
	Blocked    bool `json:"blocked"`
	BlockedBy  bool `json:"blocked_by"`
}

type blockCall struct{ blocker, blocked string }

// fakeGraphService mirrors graph-service's contract for the routes dating
// calls: RequireInternalKey on the engine, the strict SR-3 write-source guard
// on mutations, POST /v1/graph/block taking the blocker from X-User-Id and the
// target from the body, and GET /v1/graph/relationship?user_id=&other_id=.
// The retired paths (/v1/graph/blocks, /v1/graph/follows/mutual) are gin 404s.
type fakeGraphService struct {
	mu     sync.Mutex
	rels   map[[2]uuid.UUID]graphRel
	blocks []blockCall
	reads  int
}

func newFakeGraphService(t *testing.T) (*fakeGraphService, *httptest.Server) {
	t.Helper()
	f := &fakeGraphService{rels: map[[2]uuid.UUID]graphRel{}}
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sharedmiddleware.RequireInternalKey(testGraphInternalKey))
	v1 := r.Group("/v1/graph")
	v1.Use(func(c *gin.Context) {
		if c.Request.Method != http.MethodGet && c.GetHeader("X-Graph-Write-Source") != "dating-service" {
			c.AbortWithStatus(http.StatusForbidden)
			return
		}
		c.Next()
	})
	v1.POST("/block", func(c *gin.Context) {
		blocker, err := uuid.Parse(c.GetHeader("X-User-Id"))
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		var req struct {
			UserID string `json:"user_id" binding:"required"`
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		blocked, err := uuid.Parse(req.UserID)
		if err != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.blocks = append(f.blocks, blockCall{blocker.String(), blocked.String()})
		f.mu.Unlock()
		c.JSON(http.StatusOK, gin.H{"data": gin.H{"status": "blocked"}})
	})
	v1.GET("/relationship", func(c *gin.Context) {
		a, errA := uuid.Parse(c.Query("user_id"))
		b, errB := uuid.Parse(c.Query("other_id"))
		if errA != nil || errB != nil {
			c.AbortWithStatus(http.StatusBadRequest)
			return
		}
		f.mu.Lock()
		f.reads++
		rel := f.rels[[2]uuid.UUID{a, b}]
		f.mu.Unlock()
		c.JSON(http.StatusOK, gin.H{"data": rel})
	})
	v1.GET("/mutuals", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"data": []string{}}) })
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return f, srv
}

func TestIsMutualFollowReadsRelationship(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	cases := []struct {
		name string
		rel  graphRel
		want bool
	}{
		{"both follow", graphRel{Follows: true, FollowedBy: true}, true},
		{"only a follows b", graphRel{Follows: true}, false},
		{"only b follows a", graphRel{FollowedBy: true}, false},
		{"neither", graphRel{}, false},
		{"mutual but a is blocked", graphRel{Follows: true, FollowedBy: true, Blocked: true}, false},
		{"mutual but a blocked b", graphRel{Follows: true, FollowedBy: true, BlockedBy: true}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f, srv := newFakeGraphService(t)
			f.rels[[2]uuid.UUID{a, b}] = tc.rel
			c := NewHTTPGraphServiceClient(srv.URL+"/", testGraphInternalKey, nil)
			got, err := c.IsMutualFollow(context.Background(), a, b)
			if err != nil {
				t.Fatalf("IsMutualFollow: %v", err)
			}
			if got != tc.want {
				t.Fatalf("IsMutualFollow = %v, want %v", got, tc.want)
			}
			if f.reads != 1 {
				t.Fatalf("relationship reads = %d, want 1", f.reads)
			}
		})
	}
}

// A refused or missing route must surface as an error (RequestVouch then
// fails the request), never as a quiet "not mutual".
func TestIsMutualFollowWithoutKeyIsAnError(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	f, srv := newFakeGraphService(t)
	f.rels[[2]uuid.UUID{a, b}] = graphRel{Follows: true, FollowedBy: true}
	c := NewHTTPGraphServiceClient(srv.URL, "", nil)
	got, err := c.IsMutualFollow(context.Background(), a, b)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("IsMutualFollow without key: got (%v, %v), want a 401 error", got, err)
	}
	if got {
		t.Fatal("IsMutualFollow without key reported mutual")
	}
}

func TestNewHTTPGraphServiceClientDefaultsToGraphServicePort(t *testing.T) {
	c := NewHTTPGraphServiceClient("", "", nil).(*httpGraphServiceClient)
	if c.baseURL != "http://graph-service:8083" {
		t.Fatalf("default base = %q, want http://graph-service:8083", c.baseURL)
	}
}

func TestPostGraphBlockMatchesGraphContract(t *testing.T) {
	blocker, blocked := uuid.New(), uuid.New()
	f, srv := newFakeGraphService(t)
	if err := postGraphBlock(context.Background(), srv.Client(), srv.URL+"/", testGraphInternalKey, blocker, blocked); err != nil {
		t.Fatalf("postGraphBlock: %v", err)
	}
	if len(f.blocks) != 1 {
		t.Fatalf("graph blocks recorded = %d, want 1", len(f.blocks))
	}
	got := f.blocks[0]
	if got.blocker != blocker.String() || got.blocked != blocked.String() {
		t.Fatalf("graph recorded %s blocking %s, want %s blocking %s", got.blocker, got.blocked, blocker, blocked)
	}
}

func TestPostGraphBlockWithoutKeyIsAnError(t *testing.T) {
	f, srv := newFakeGraphService(t)
	err := postGraphBlock(context.Background(), srv.Client(), srv.URL, "", uuid.New(), uuid.New())
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("postGraphBlock without key: err = %v, want a 401 error", err)
	}
	if len(f.blocks) != 0 {
		t.Fatalf("graph recorded %d blocks without the key", len(f.blocks))
	}
}

// fakeCommunityService mirrors community-service for the vouch check:
// RequireInternalKey, GET /v1/communities/my paged (limit <= 100, offset) for
// the X-User-Id user, and GET /:communityId/members, which ignores ?ids= and
// answers a bare array — the shape the old client misread.
func newFakeCommunityService(t *testing.T, memberships map[uuid.UUID][]string) *httptest.Server {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Use(sharedmiddleware.RequireInternalKey(testGraphInternalKey))
	v1 := r.Group("/v1/communities")
	v1.GET("/my", func(c *gin.Context) {
		uid, err := uuid.Parse(c.GetHeader("X-User-Id"))
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
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
			page = append(page, gin.H{"id": all[i]})
		}
		c.JSON(http.StatusOK, gin.H{"data": page})
	})
	v1.GET("/:communityId/members", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"data": []gin.H{}})
	})
	srv := httptest.NewServer(r)
	t.Cleanup(srv.Close)
	return srv
}

func TestUsersShareCommunityReadsEachUsersMemberships(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	target := uuid.New()
	filler := func(n int) []string {
		out := make([]string, n)
		for i := range out {
			out[i] = uuid.NewString()
		}
		return out
	}
	cases := []struct {
		name string
		a, b []string
		want bool
	}{
		{"both members", []string{target.String()}, []string{target.String()}, true},
		// 130 others first, so the target is on the second page.
		{"both members, second page", append(filler(130), target.String()), append(filler(100), target.String()), true},
		{"only a", []string{target.String()}, filler(3), false},
		{"only b", filler(3), []string{target.String()}, false},
		{"neither", nil, nil, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := newFakeCommunityService(t, map[uuid.UUID][]string{a: tc.a, b: tc.b})
			c := NewHTTPCommunityServiceClient(srv.URL+"/", testGraphInternalKey, nil)
			got, err := c.UsersShareCommunity(context.Background(), a, b, target)
			if err != nil {
				t.Fatalf("UsersShareCommunity: %v", err)
			}
			if got != tc.want {
				t.Fatalf("UsersShareCommunity = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestUsersShareCommunityWithoutKeyIsAnError(t *testing.T) {
	a, b, target := uuid.New(), uuid.New(), uuid.New()
	srv := newFakeCommunityService(t, map[uuid.UUID][]string{a: {target.String()}, b: {target.String()}})
	c := NewHTTPCommunityServiceClient(srv.URL, "", nil)
	got, err := c.UsersShareCommunity(context.Background(), a, b, target)
	if err == nil || !strings.Contains(err.Error(), "401") {
		t.Fatalf("UsersShareCommunity without key: got (%v, %v), want a 401 error", got, err)
	}
}

func TestNewHTTPCommunityServiceClientDefaultsToCommunityServicePort(t *testing.T) {
	c := NewHTTPCommunityServiceClient("", "", nil).(*httpCommunityServiceClient)
	if c.baseURL != "http://community-service:8107" {
		t.Fatalf("default base = %q, want http://community-service:8107", c.baseURL)
	}
}
