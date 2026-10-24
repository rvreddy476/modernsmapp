package http

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	Copyright title review (founder quick wins 2026-10-24; migration 073;
	service/copyright_title_review.go).

	Author (gateway-authenticated, X-User-Id):
	POST /v1/posts/:postId/copyright-review/request   {"note": "I own the rights…"} (1-1000 chars)
	     → 200 {state, matched_title, created_at, decided_at?, review_requested_at, appealed_at?, can_appeal}
	     On a pending review: the note for the reviewer. On a BLOCKED review
	     (the system's owner_registered block or a reviewer's) it is the ONE
	     appeal (migration 076): state goes back to "pending" with
	     appealed_at set, the video stays hidden until a reviewer clears it.
	     404 REVIEW_NOT_FOUND (no review of the caller's on that post, or the
	     post is deleted), 409 REVIEW_ALREADY_DECIDED (cleared),
	     409 APPEAL_ALREADY_USED (a second appeal — waiting or answered)

	The author's own GET /v1/posts/:postId and uploads rows carry
	"copyright_review": {state: pending|cleared|blocked, matched_title,
	created_at, decided_at?, review_requested_at?}; absent otherwise.

	Reviewers (admin-service tokens only; Tube permissions):
	GET   /v1/posts/internal/admin/copyright/titles?active=true|false&q=&cursor=&limit=
	      tube:videos.moderate | tube:videos.remove
	POST  /v1/posts/internal/admin/copyright/titles            tube:videos.remove
	      {"title","aliases":[],"language","owner_channel_ids":[],"active",
	       "auto_block": false, "rights_owner": "Sun Pictures"}
	      auto_block (migration 075): the rights owner registered the title and
	      asked for blocking — a matching upload is BLOCKED at once by the
	      system (reason owner_registered), no reviewer; requires rights_owner
	      (1-200 printable characters), else 400 INVALID_COPYRIGHT_TITLE.
	PATCH /v1/posts/internal/admin/copyright/titles/:titleId   tube:videos.remove
	      any of the same fields ("language": null / "rights_owner": null clear;
	      "active": false disables), "note"
	      Entries answer with auto_block and rights_owner; review rows with
	      rights_owner, reason (owner_registered | mute_requested | null) and
	      title_auto_block. A system block has decided_by null.
	GET   /v1/posts/internal/admin/copyright/reviews?state=pending|cleared|blocked&cursor=&limit=
	      tube:videos.moderate | tube:videos.remove — oldest first
	POST  /v1/posts/internal/admin/copyright/reviews/:reviewId/decision {"decision":"clear"|"block","note"}
	      clear: tube:videos.moderate (or .remove); block: tube:videos.remove
	      An appealed review (appealed_at set) is decided the same way: clear
	      publishes it as normal (the withheld PostCreated once, subscriber
	      fan-out included); block confirms the block.
	DELETE /v1/posts/internal/admin/copyright/tracks/:postId/:titleId  {"note"} (optional body)
	      tube:videos.remove — removes a tracked owner match; the post's
	      eligibility is restated so earnings_excluded clears (unless another
	      owner tracks it) → 200 {post_id, title_id, still_tracked};
	      404 TRACK_NOT_FOUND

	Every write is audited (post_admin_audit) with the token's act claim.
	Errors: 400 INVALID_REQUEST / INVALID_COPYRIGHT_TITLE, 404 TITLE_NOT_FOUND /
	REVIEW_NOT_FOUND / TRACK_NOT_FOUND, 409 REVIEW_ALREADY_DECIDED,
	503 COPYRIGHT_REVIEW_UNAVAILABLE.

	Fingerprint provider (service-only; copyright_owner_match_internal.go):
	POST  /v1/posts/internal/copyright/owner-match
	      {"post_id","title_id","reference","policy":"block"|"track"|"mute"}
	      internal key AND a trust-safety-service token carrying
	      post:copyright-owner-match.write → 200 {outcome, review_id?}
*/

// Error codes these routes answer with.
const (
	CodeCopyrightTitleInvalid      = "INVALID_COPYRIGHT_TITLE"
	CodeCopyrightTitleNotFound     = "TITLE_NOT_FOUND"
	CodeCopyrightReviewNotFound    = "REVIEW_NOT_FOUND"
	CodeCopyrightReviewDecided     = "REVIEW_ALREADY_DECIDED"
	CodeCopyrightReviewUnavailable = "COPYRIGHT_REVIEW_UNAVAILABLE"
	// CodeCopyrightAppealUsed: the author already appealed this block (076).
	CodeCopyrightAppealUsed = "APPEAL_ALREADY_USED"
	// CodeCopyrightTrackNotFound: no tracked owner match for that post and entry.
	CodeCopyrightTrackNotFound = "TRACK_NOT_FOUND"
)

func (h *Handler) registerCopyrightReviewAdminRoutes(g *gin.RouterGroup, gate func(perms ...string) gin.HandlerFunc) {
	g.GET("/copyright/titles", gate(PermVideosModerate, PermVideosRemove), h.AdminListCopyrightTitles)
	g.POST("/copyright/titles", gate(PermVideosRemove), h.AdminCreateCopyrightTitle)
	g.PATCH("/copyright/titles/:titleId", gate(PermVideosRemove), h.AdminUpdateCopyrightTitle)
	g.GET("/copyright/reviews", gate(PermVideosModerate, PermVideosRemove), h.AdminListCopyrightReviews)
	g.POST("/copyright/reviews/:reviewId/decision", gate(PermVideosModerate, PermVideosRemove), h.AdminDecideCopyrightReview)
	// Removing a tracked owner match restores the video's earnings: the
	// catalogue's own permission (tube:videos.remove), like the title writes.
	g.DELETE("/copyright/tracks/:postId/:titleId", gate(PermVideosRemove), h.AdminRemoveCopyrightOwnerTrack)
}

// CopyrightReviewDecisionPermission: a block keeps a video down (a
// takedown, tube:videos.remove); a clear needs tube:videos.moderate.
func CopyrightReviewDecisionPermission(decision string) string {
	if strings.EqualFold(strings.TrimSpace(decision), postgres.TitleReviewDecisionBlock) {
		return PermVideosRemove
	}
	return PermVideosModerate
}

func writeCopyrightReviewError(c *gin.Context, op string, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrInvalidCopyrightTitle):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeCopyrightTitleInvalid, err.Error(), nil)
	case errors.Is(err, postgres.ErrTitleReviewInvalid):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
	case errors.Is(err, postgres.ErrCopyrightTitleNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeCopyrightTitleNotFound, "Title not found", nil)
	case errors.Is(err, postgres.ErrTitleReviewNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeCopyrightReviewNotFound, "Copyright review not found", nil)
	case errors.Is(err, postgres.ErrTitleReviewDecided):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeCopyrightReviewDecided, "This copyright review has already been decided", nil)
	case errors.Is(err, postgres.ErrTitleReviewAppealed):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeCopyrightAppealUsed, "This block has already been appealed", nil)
	case errors.Is(err, postgres.ErrOwnerTrackNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeCopyrightTrackNotFound, "No tracked owner match for that post and title", nil)
	case errors.Is(err, postgres.ErrPostNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "POST_NOT_FOUND", "Post not found", nil)
	case errors.Is(err, service.ErrCopyrightReviewUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeCopyrightReviewUnavailable, "Copyright review is unavailable", nil)
	default:
		adminInternalError(c, op, err)
	}
}

func copyrightPageLimit(c *gin.Context) int {
	limit := 50
	if n, err := strconv.Atoi(c.Query("limit")); err == nil && n > 0 && n <= postgres.MaxTitleReviewPage {
		limit = n
	}
	return limit
}

// copyrightTitleBody is the create / update body. Pointers say "present";
// a JSON null language (rawLanguage) clears it on update.
type copyrightTitleBody struct {
	Title           *string          `json:"title"`
	Aliases         *[]string        `json:"aliases"`
	Language        *json.RawMessage `json:"language"`
	OwnerChannelIDs *[]string        `json:"owner_channel_ids"`
	Active          *bool            `json:"active"`
	// AutoBlock (migration 075): the rights owner registered the title and
	// asked for copies to be blocked; requires rights_owner.
	AutoBlock *bool `json:"auto_block"`
	// RightsOwner is the owner's display name; JSON null clears it on update.
	RightsOwner *json.RawMessage `json:"rights_owner"`
	Note        string           `json:"note"`
}

func (b copyrightTitleBody) input() (service.CopyrightTitleInput, error) {
	in := service.CopyrightTitleInput{Title: b.Title, Aliases: b.Aliases, Active: b.Active, AutoBlock: b.AutoBlock, Note: b.Note}
	if b.RightsOwner != nil {
		if strings.TrimSpace(string(*b.RightsOwner)) == "null" {
			in.ClearRightsOwner = true
		} else {
			var owner string
			if err := json.Unmarshal(*b.RightsOwner, &owner); err != nil {
				return in, errors.New("rights_owner must be a string or null")
			}
			in.RightsOwner = &owner
		}
	}
	if b.Language != nil {
		raw := strings.TrimSpace(string(*b.Language))
		if raw == "null" {
			in.ClearLanguage = true
		} else {
			var lang string
			if err := json.Unmarshal(*b.Language, &lang); err != nil {
				return in, errors.New("language must be a string or null")
			}
			in.Language = &lang
		}
	}
	if b.OwnerChannelIDs != nil {
		ids := make([]uuid.UUID, 0, len(*b.OwnerChannelIDs))
		for _, raw := range *b.OwnerChannelIDs {
			id, err := uuid.Parse(strings.TrimSpace(raw))
			if err != nil || id == uuid.Nil {
				return in, errors.New("owner_channel_ids must be channel UUIDs")
			}
			ids = append(ids, id)
		}
		in.OwnerChannelIDs = &ids
	}
	return in, nil
}

func bindCopyrightTitleBody(c *gin.Context) (service.CopyrightTitleInput, bool) {
	var body copyrightTitleBody
	dec := json.NewDecoder(c.Request.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST",
			"body must be {title, aliases, language, owner_channel_ids, active, auto_block, rights_owner, note}", nil)
		return service.CopyrightTitleInput{}, false
	}
	in, err := body.input()
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return in, false
	}
	return in, true
}

func requireActor(c *gin.Context) (uuid.UUID, bool) {
	actor, ok := tokenActor(c)
	if !ok {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusForbidden, CodeAdminActorRequired, "the token does not name the acting admin", nil)
	}
	return actor, ok
}

// AdminListCopyrightTitles — GET .../copyright/titles.
func (h *Handler) AdminListCopyrightTitles(c *gin.Context) {
	f := postgres.CopyrightTitleFilter{Query: c.Query("q"), Cursor: c.Query("cursor"), Limit: copyrightPageLimit(c)}
	switch c.Query("active") {
	case "":
	case "true":
		v := true
		f.Active = &v
	case "false":
		v := false
		f.Active = &v
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "active must be true or false", nil)
		return
	}
	items, next, err := h.svc.ListCopyrightTitles(c.Request.Context(), f)
	if err != nil {
		writeCopyrightReviewError(c, "list copyright titles", err)
		return
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, items, meta)
}

// AdminCreateCopyrightTitle — POST .../copyright/titles (201).
func (h *Handler) AdminCreateCopyrightTitle(c *gin.Context) {
	actor, ok := requireActor(c)
	if !ok {
		return
	}
	in, ok := bindCopyrightTitleBody(c)
	if !ok {
		return
	}
	t, err := h.svc.CreateCopyrightTitle(c.Request.Context(), in, actor)
	if err != nil {
		writeCopyrightReviewError(c, "create copyright title", err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, t, nil)
}

// AdminUpdateCopyrightTitle — PATCH .../copyright/titles/:titleId.
func (h *Handler) AdminUpdateCopyrightTitle(c *gin.Context) {
	actor, ok := requireActor(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("titleId"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid title ID", nil)
		return
	}
	in, ok := bindCopyrightTitleBody(c)
	if !ok {
		return
	}
	t, err := h.svc.UpdateCopyrightTitle(c.Request.Context(), id, in, actor)
	if err != nil {
		writeCopyrightReviewError(c, "update copyright title", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, t, nil)
}

// AdminListCopyrightReviews — GET .../copyright/reviews (oldest first).
func (h *Handler) AdminListCopyrightReviews(c *gin.Context) {
	state := c.DefaultQuery("state", postgres.TitleReviewPending)
	switch state {
	case postgres.TitleReviewPending, postgres.TitleReviewCleared, postgres.TitleReviewBlocked:
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "state must be pending, cleared or blocked", nil)
		return
	}
	items, next, err := h.svc.ListCopyrightReviews(c.Request.Context(), state, c.Query("cursor"), copyrightPageLimit(c))
	if err != nil {
		writeCopyrightReviewError(c, "list copyright reviews", err)
		return
	}
	var meta *api.Meta
	if next != "" {
		meta = &api.Meta{NextCursor: next}
	}
	api.JSON(c.Writer, http.StatusOK, items, meta)
}

type copyrightDecisionRequest struct {
	Decision string `json:"decision" binding:"required"`
	Note     string `json:"note"`
}

// AdminDecideCopyrightReview — POST .../copyright/reviews/:reviewId/decision.
// The permission is narrowed by the decision: a block needs
// tube:videos.remove whatever the token was admitted with.
func (h *Handler) AdminDecideCopyrightReview(c *gin.Context) {
	ctx := c.Request.Context()
	actor, ok := requireActor(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("reviewId"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid review ID", nil)
		return
	}
	var req copyrightDecisionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "body must be {decision, note}", nil)
		return
	}
	decision := strings.ToLower(strings.TrimSpace(req.Decision))
	if decision != postgres.TitleReviewDecisionClear && decision != postgres.TitleReviewDecisionBlock {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "decision must be clear or block", nil)
		return
	}
	if decision == postgres.TitleReviewDecisionClear {
		if !adminMayAny(c, PermVideosModerate, PermVideosRemove) {
			return
		}
	} else if !adminMayAny(c, CopyrightReviewDecisionPermission(decision)) {
		return
	}
	out, err := h.svc.DecideCopyrightReview(ctx, id, actor, decision, req.Note)
	if err != nil {
		writeCopyrightReviewError(c, "decide copyright review", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

type copyrightTrackRemoveBody struct {
	Note string `json:"note"`
}

// AdminRemoveCopyrightOwnerTrack — DELETE .../copyright/tracks/:postId/:titleId.
// The body ({"note"}) is optional.
func (h *Handler) AdminRemoveCopyrightOwnerTrack(c *gin.Context) {
	ctx := c.Request.Context()
	actor, ok := requireActor(c)
	if !ok {
		return
	}
	postID, perr := uuid.Parse(c.Param("postId"))
	titleID, terr := uuid.Parse(c.Param("titleId"))
	if perr != nil || terr != nil || postID == uuid.Nil || titleID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post or title ID", nil)
		return
	}
	var body copyrightTrackRemoveBody
	if c.Request.Body != nil && c.Request.ContentLength != 0 {
		dec := json.NewDecoder(c.Request.Body)
		dec.DisallowUnknownFields()
		if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "body must be {note}", nil)
			return
		}
	}
	out, err := h.svc.RemoveCopyrightOwnerTrack(ctx, postID, titleID, actor, body.Note)
	if err != nil {
		writeCopyrightReviewError(c, "remove copyright owner track", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

type copyrightReviewRequestBody struct {
	Note string `json:"note"`
}

// RequestCopyrightReview — POST /v1/posts/:postId/copyright-review/request.
func (h *Handler) RequestCopyrightReview(c *gin.Context) {
	ctx := c.Request.Context()
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	authorID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || authorID == uuid.Nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid user", nil)
		return
	}
	var body copyrightReviewRequestBody
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "body must be {note}", nil)
		return
	}
	view, err := h.svc.RequestCopyrightReview(ctx, postID, authorID, body.Note)
	if err != nil {
		switch {
		case errors.Is(err, postgres.ErrTitleReviewInvalid):
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "note must be 1-1000 characters", nil)
		case errors.Is(err, postgres.ErrTitleReviewNotFound):
			api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, CodeCopyrightReviewNotFound, "No copyright review on this post", nil)
		case errors.Is(err, postgres.ErrTitleReviewDecided):
			api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeCopyrightReviewDecided, "This copyright review has already been decided", nil)
		case errors.Is(err, postgres.ErrTitleReviewAppealed):
			api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, CodeCopyrightAppealUsed, "You have already asked for a review of this block", nil)
		case errors.Is(err, service.ErrCopyrightReviewUnavailable):
			api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, CodeCopyrightReviewUnavailable, "Copyright review is unavailable", nil)
		default:
			api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Request failed", nil)
		}
		return
	}
	c.Header("Cache-Control", "private, no-store")
	api.JSON(c.Writer, http.StatusOK, view, nil)
}
