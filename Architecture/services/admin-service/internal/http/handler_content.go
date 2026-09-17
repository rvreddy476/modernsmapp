package http

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"sync"

	"github.com/atpost/admin-service/internal/adminauth"
	"github.com/atpost/admin-service/internal/service"
	"github.com/atpost/admin-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Content on post-service (Social and Tube) and the merged stats views the
// content dashboards share.
//
// post-service narrows every post route by the post's kind (post, reel or
// video, from its stored content_type) and by the action: a takedown needs
// the kind's .remove permission, anything else its .moderate. The console
// states the kind in the URL — /v1/admin/social/posts/…, /…/social/reels/…,
// /v1/admin/tube/videos/… — and admin-service scopes the token to that kind's
// permission for the requested action. The kind is a claim, not a lookup:
// post-service loads the stored kind and refuses a token scoped to another
// kind (a reel takedown sent as /posts/ carries social:posts.remove, which a
// reel does not accept), so a mislabelled URL can never widen what the admin
// may do, and admin-service never needs a preceding read.

// contentKind is one kind of post-service content with the console segment
// it is reached under and the two permissions it narrows to.
type contentKind struct {
	kind     string // post-service's content kind: post | reel | video
	segment  string // console URL segment: posts | reels | videos
	app      string // social | tube
	moderate string
	remove   string
	target   string // audit target type
}

var (
	kindPost  = contentKind{kind: "post", segment: "posts", app: "social", moderate: permSocialPostsModerate, remove: permSocialPostsRemove, target: "post"}
	kindReel  = contentKind{kind: "reel", segment: "reels", app: "social", moderate: permSocialReelsModerate, remove: permSocialReelsRemove, target: "reel"}
	kindVideo = contentKind{kind: "video", segment: "videos", app: "tube", moderate: permTubeVideosModerate, remove: permTubeVideosRemove, target: "video"}
)

// Decision actions and review statuses post-service accepts; the takedowns
// among them need the kind's .remove permission and a fresh step-up.
var (
	contentDecisionActions  = map[string]bool{"approve": true, "reject": true, "needs_changes": true}
	contentDecisionTakedown = map[string]bool{"reject": true}
	contentReviewStatuses   = map[string]bool{"approved": true, "rejected": true}
	contentReviewTakedown   = map[string]bool{"rejected": true}
)

// Operation suffixes of the per-kind routes (operation = <app>.<segment>.<suffix>).
const (
	opContentReviewQueue  = "review_queue"
	opContentRead         = "read"
	opContentHistory      = "history"
	opContentDecide       = "decide"
	opContentReviewStatus = "review_status"
	opContentVisibility   = "visibility"
)

func (k contentKind) op(suffix string) string { return k.app + "." + k.segment + "." + suffix }

// contentKindRoutes declares the routes every kind has. Reads admit the
// kind's moderate or remove; writes start from moderate and are narrowed to
// remove (with step-up) by the action in the body.
func contentKindRoutes(k contentKind) []productRoute {
	alts := []string{k.moderate, k.remove}
	seg := "/" + k.segment
	return []productRoute{
		{method: http.MethodGet, path: seg + "/review-queue", operation: k.op(opContentReviewQueue), permission: k.moderate, alternatives: alts, upstream: "/posts/review-queue"},
		{method: http.MethodGet, path: seg + "/:postId", operation: k.op(opContentRead), permission: k.moderate, alternatives: alts, upstream: "/posts/:postId", targetType: k.target},
		{method: http.MethodGet, path: seg + "/:postId/moderation-history", operation: k.op(opContentHistory), permission: k.moderate, alternatives: alts, upstream: "/posts/:postId/moderation-history", targetType: k.target},
		// Narrowed by the action: reject → <kind>.remove + step-up.
		{method: http.MethodPost, path: seg + "/:postId/moderation", operation: k.op(opContentDecide), permission: k.moderate, upstream: "/posts/:postId/moderation", targetType: k.target},
		// Narrowed by the status: rejected → <kind>.remove + step-up.
		{method: http.MethodPost, path: seg + "/review-status", operation: k.op(opContentReviewStatus), permission: k.moderate, upstream: "/posts/review-status", targetType: k.target},
		{method: http.MethodPost, path: seg + "/visibility", operation: k.op(opContentVisibility), permission: k.moderate, upstream: "/posts/visibility", targetType: k.target},
	}
}

// attachContentKind adds the kind's decisions and special handlers to a
// per-registration copy of the routes.
func (h *Handler) attachContentKind(p product, k contentKind, routes []productRoute, special map[string]gin.HandlerFunc) {
	for i := range routes {
		switch routes[i].operation {
		case k.op(opContentDecide):
			routes[i].decide = contentDecision(k, "action", contentDecisionActions, contentDecisionTakedown)
		case k.op(opContentReviewStatus):
			routes[i].decide = contentDecision(k, "status", contentReviewStatuses, contentReviewTakedown)
			special[routes[i].operation] = h.contentBodyTarget(p, routes[i], k)
		case k.op(opContentVisibility):
			special[routes[i].operation] = h.contentBodyTarget(p, routes[i], k)
		case k.op(opContentReviewQueue):
			special[routes[i].operation] = h.contentReviewQueue(p, k)
		}
	}
}

// contentDecision reads the action (or status) from the body BEFORE the gate
// judges the request: a takedown swaps the declared moderate permission for
// the kind's remove permission and requires a step-up. Unknown values are
// refused here and never reach post-service. The body forwarded is the
// console's own; post-service applies the same rule to the stored kind.
func contentDecision(k contentKind, field string, allowed, takedown map[string]bool) func(*gin.Context, adminauth.Permissions) (Decision, error) {
	return func(c *gin.Context, _ adminauth.Permissions) (Decision, error) {
		raw, fields, err := jsonBody(c)
		if err != nil || len(raw) == 0 {
			return Decision{}, badRequest(CodeInvalidBody, "The request body must be JSON with "+field+" and reason")
		}
		v := normaliseOutcome(stringField(fields, field))
		if !allowed[v] {
			return Decision{}, badRequest(CodeInvalidAction, field+" is not one post-service accepts")
		}
		d := Decision{Audit: map[string]any{field: v, "kind": k.kind}}
		if takedown[v] {
			d.Permission = k.remove
			d.StepUp = true
		}
		return d, nil
	}
}

// contentReviewQueue forwards the review queue with the route's kind fixed:
// a kind in the query that disagrees with the URL is refused.
func (h *Handler) contentReviewQueue(p product, k contentKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		q := c.Request.URL.Query()
		if got := q.Get("kind"); got != "" && got != k.kind {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, CodeInvalidAction,
				"kind is fixed by the route: "+k.kind, nil)
			return
		}
		q.Set("kind", k.kind)
		h.productCall(c, p, service.ProductRequest{Method: http.MethodGet, Path: "/posts/review-queue", RawQuery: q.Encode()}, false)
	}
}

// contentBodyTarget forwards a write whose target post is named in the body
// (review-status, visibility), pointing the audit row at that post.
func (h *Handler) contentBodyTarget(p product, rt productRoute, k contentKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		raw, fields, err := jsonBody(c)
		if err != nil || len(raw) == 0 {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidBody, "The request body must be JSON with post_id", nil)
			return
		}
		id, perr := uuid.Parse(stringField(fields, "post_id"))
		if perr != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeInvalidID, "post_id must be a post id", nil)
			return
		}
		info := auditFrom(c)
		info.targetType, info.targetID, info.reason = k.target, id.String(), stringField(fields, "reason")
		h.productCall(c, p, service.ProductRequest{Method: rt.method, Path: rt.upstream, RawBody: raw}, false)
	}
}

// --- merged stats ---

// A content dashboard's stats come from more than one service (Social: post-
// service and user-service's pages; Chat: channel, group and community). Each
// source is one signed call; the console receives one object with a part per
// source. A source that did not answer is shown as unavailable with the
// reason — never as zeros, which would read as "nothing pending".

// Stats part states and the error code when no source answered.
const (
	StatsOK              = "ok"
	StatsUnavailable     = "unavailable"
	CodeStatsUnavailable = "STATS_UNAVAILABLE"
)

// statsSource is one product stats route feeding a merged view.
type statsSource struct {
	name   string // key in the merged response
	label  string // the service, for the console
	client *service.ProductClient
	path   string // relative to the client's admin prefix
}

// StatsPart is one source's slice of a merged stats response.
type StatsPart struct {
	Status string `json:"status"` // ok | unavailable
	Source string `json:"source"`
	// Stats is the product's own stats object when Status is ok.
	Stats json.RawMessage `json:"stats,omitempty"`
	// Error says why the part is unavailable; UpstreamStatus is the product's
	// HTTP status when it answered at all.
	Error          string `json:"error,omitempty"`
	UpstreamStatus int    `json:"upstream_status,omitempty"`
}

// MergedStats is the console's view: one part per source plus whether every
// source answered.
type MergedStats struct {
	Parts    map[string]StatsPart `json:"parts"`
	Complete bool                 `json:"complete"`
}

// mergedStats answers with every source's stats. The parts are fetched
// together, each with a token scoped to the route's permission (the same
// permission string on every source). Some sources unavailable: 200 with
// complete=false and the audit row naming them. None answered: 503
// STATS_UNAVAILABLE carrying the parts, audited as a failure.
func (h *Handler) mergedStats(sources []statsSource) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		req, ok := effectiveRequirement(c)
		if !ok || req.Permission == "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Route is not declared", nil)
			return
		}
		actor := actorFrom(c)
		callCtx := productContext(c)
		parts := make([]StatsPart, len(sources))
		var wg sync.WaitGroup
		for i, s := range sources {
			wg.Add(1)
			go func(i int, s statsSource) {
				defer wg.Done()
				parts[i] = fetchStats(callCtx, s, req.Permission, actor)
			}(i, s)
		}
		wg.Wait()

		out := MergedStats{Parts: map[string]StatsPart{}, Complete: true}
		var down []string
		for i, s := range sources {
			out.Parts[s.name] = parts[i]
			if parts[i].Status != StatsOK {
				down = append(down, s.name)
			}
		}
		info := auditFrom(c)
		if len(down) > 0 {
			out.Complete = false
			info.set("unavailable", down)
		}
		if len(down) == len(sources) {
			info.outcome = postgres.AuditOutcomeFailure
			api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeStatsUnavailable,
				"No stats source answered", out)
			return
		}
		api.JSON(c.Writer, http.StatusOK, out, nil)
	}
}

// fetchStats calls one source and classifies the answer. Only a 200 whose
// body carries a data object counts as ok.
func fetchStats(ctx context.Context, s statsSource, permission, actor string) StatsPart {
	part := StatsPart{Status: StatsUnavailable, Source: s.label}
	resp, err := s.client.Do(ctx, service.ProductRequest{Method: http.MethodGet, Path: s.path, Permission: permission, Actor: actor})
	switch {
	case errors.Is(err, service.ErrProductUnavailable):
		part.Error = "service token key not configured"
		return part
	case err != nil:
		slog.WarnContext(ctx, "admin stats source unreachable", "source", s.label, "error", err)
		part.Error = "unreachable"
		return part
	}
	part.UpstreamStatus = resp.Status
	if resp.Status != http.StatusOK {
		part.Error = "answered " + strconv.Itoa(resp.Status)
		return part
	}
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if json.Unmarshal(resp.Body, &env) != nil || len(env.Data) == 0 || string(env.Data) == "null" {
		part.Error = "malformed stats"
		return part
	}
	part.Status, part.Stats = StatsOK, env.Data
	return part
}
