package http

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Members-only from birth on the real routes (2026-10-23): POST /v1/posts,
// both draft kinds and PATCH /v1/posts/:id take tier_required_id. A tier
// that is not one of the author's active tiers is 400 INVALID_TIER; a tier
// list monetization-service cannot answer is 503 and no post. The service
// has no database here, so a request that got past the gate would fail
// loudly instead of creating anything.

type gateRouteRig struct {
	router      *gin.Engine
	author      uuid.UUID
	tier        uuid.UUID
	tiersDown   bool
	tierQueries int
}

func newGateRouteRig(t *testing.T) *gateRouteRig {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := &gateRouteRig{author: uuid.New(), tier: uuid.New()}
	deps := service.HandlerTestDeps{
		CreatorTiers: func(_ context.Context, creator uuid.UUID) ([]service.CreatorTierRef, error) {
			r.tierQueries++
			if r.tiersDown {
				return nil, errors.New("dial tcp 10.0.0.7:8090: connection refused")
			}
			if creator != r.author {
				return []service.CreatorTierRef{}, nil
			}
			return []service.CreatorTierRef{{ID: r.tier, CreatorID: r.author, PricePaise: 4900, IsActive: true}}, nil
		},
		CreateIdempotencyLookup: func(context.Context, uuid.UUID, string) (uuid.UUID, string, bool, error) {
			return uuid.Nil, "", false, nil
		},
	}
	// A long video is listed under a channel; the author has one.
	deps.Channels = &esChannels{byUser: map[uuid.UUID]*postgres.Channel{r.author: {ID: uuid.New(), UserID: r.author, Name: "Gate", Handle: "gate"}}}
	r.router = gin.New()
	r.router.Use(gin.CustomRecovery(func(c *gin.Context, _ any) { c.AbortWithStatus(http.StatusInternalServerError) }))
	h := New(service.NewForHandlerTests(deps), nil)
	h.RegisterRoutes(r.router)
	h.RegisterDraftRoutes(r.router)
	h.RegisterPostDraftRoutes(r.router)
	return r
}

func (r *gateRouteRig) do(method, path, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-User-Id", r.author.String())
	req.Header.Set("Idempotency-Key", uuid.NewString())
	w := httptest.NewRecorder()
	r.router.ServeHTTP(w, req)
	return w
}

// createBody is a create request of the given kind gated on tier (raw JSON).
func createBody(contentType, tier string) string {
	poll := ""
	if contentType == "poll" {
		poll = `"poll":{"question":"Next?","options":["a","b"]},`
	}
	return `{"text":"for members","visibility":"public","content_type":"` + contentType + `","title":"Episode 1",` +
		poll + `"tier_required_id":` + tier + `}`
}

// POST /v1/posts forwards tier_required_id to the gate for every content
// type: another creator's (or an unknown) tier is 400 INVALID_TIER, a tier
// list that cannot be read is 503 and nothing else runs, a value that is not
// a UUID is 400 INVALID_REQUEST — all before any write.
func TestCreatePostRouteChecksTheTier(t *testing.T) {
	for _, kind := range []string{"post", "poll", "flick", "long_video"} {
		t.Run(kind, func(t *testing.T) {
			r := newGateRouteRig(t)
			w := r.do(http.MethodPost, "/v1/posts", createBody(kind, `"`+uuid.NewString()+`"`))
			if w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_TIER" {
				t.Fatalf("foreign tier: %d %s; want 400 INVALID_TIER", w.Code, w.Body.String())
			}
			r.tiersDown = true
			w = r.do(http.MethodPost, "/v1/posts", createBody(kind, `"`+r.tier.String()+`"`))
			if w.Code != http.StatusServiceUnavailable || codeOf(w) != "MEMBERSHIP_TIERS_UNAVAILABLE" {
				t.Fatalf("tiers down: %d %s; want 503 MEMBERSHIP_TIERS_UNAVAILABLE", w.Code, w.Body.String())
			}
			if strings.Contains(w.Body.String(), "10.0.0.7") {
				t.Fatalf("the lookup error leaked: %s", w.Body.String())
			}
		})
	}
	r := newGateRouteRig(t)
	if w := r.do(http.MethodPost, "/v1/posts", createBody("post", `"gold"`)); w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_REQUEST" {
		t.Fatalf("not a uuid: %d %s", w.Code, w.Body.String())
	}
	if r.tierQueries != 0 {
		t.Fatalf("a malformed tier was looked up")
	}
}

// PATCH /v1/posts/:id: tier_required_id that is neither a UUID string nor
// null / "" is 400 INVALID_TIER before the edit runs.
func TestEditRouteRefusesAMalformedTier(t *testing.T) {
	r := newGateRouteRig(t)
	for _, raw := range []string{`"gold"`, `42`, `{"id":"x"}`, `"00000000-0000-0000-0000-000000000000"`} {
		w := r.do(http.MethodPatch, "/v1/posts/"+uuid.NewString(), `{"tier_required_id":`+raw+`}`)
		if w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_TIER" {
			t.Fatalf("%s: %d %s; want 400 INVALID_TIER", raw, w.Code, w.Body.String())
		}
	}
}

// Drafts: a reel draft patch (and a publish that carries the tier) refuses a
// malformed tier with 400 INVALID_TIER before anything is written; a
// composer draft refuses the nil id on save.
func TestDraftRoutesRefuseAMalformedTier(t *testing.T) {
	r := newGateRouteRig(t)
	draft := uuid.NewString()
	for _, path := range []string{"/v1/reels/drafts/" + draft} {
		w := r.do(http.MethodPatch, path, `{"tier_required_id":"gold"}`)
		if w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_TIER" {
			t.Fatalf("PATCH %s: %d %s; want 400 INVALID_TIER", path, w.Code, w.Body.String())
		}
	}
	w := r.do(http.MethodPost, "/v1/reels/drafts/"+draft+"/publish", `{"tier_required_id":"gold"}`)
	if w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_TIER" {
		t.Fatalf("publish with a bad tier: %d %s; want 400 INVALID_TIER", w.Code, w.Body.String())
	}
	w = r.do(http.MethodPost, "/v1/posts/drafts", `{"post_type":"post","payload":{"text":"hi","tier_required_id":"00000000-0000-0000-0000-000000000000"}}`)
	if w.Code != http.StatusBadRequest || codeOf(w) != "INVALID_TIER" {
		t.Fatalf("composer draft nil tier: %d %s; want 400 INVALID_TIER", w.Code, w.Body.String())
	}
}
