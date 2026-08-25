package http

import (
	"errors"
	"net/http"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Module 4 M4-P0-1 / M4-P0-2 / M4-P0-4 — the authorized story surfaces.
//
// Three rules hold across every handler in this file.
//
//  1. THE VIEWER COMES FROM THE VERIFIED IDENTITY HEADER, NEVER THE BODY OR
//     QUERY. The removed `followed_ids` parameter let a caller name the authors
//     whose stories it wanted. Worse, that branch never read X-User-Id at all,
//     so it worked with no identity — the gateway lets a tokenless request
//     through, and the handler asked for nothing more.
//
//  2. A RESOLVED DENIAL IS ONE 404. Missing, deleted, expired, unapproved,
//     blocked in either direction, and out-of-audience produce a byte-identical
//     response. Anything else is an oracle for probing which stories exist and
//     who has blocked whom.
//
//  3. AN UNRESOLVED DEPENDENCY IS 503, NOT 404. If graph-service times out we
//     do not know whether the viewer is blocked. Answering 404 would hide an
//     outage behind a response meaning "this does not exist" — both a lie and
//     unretryable.

// storyNotFound is the single non-enumerating denial.
func storyNotFound(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusNotFound,
		"NOT_FOUND", "Story not found", nil)
}

// storyUnavailable is the retryable answer for unresolved dependency state.
func storyUnavailable(c *gin.Context) {
	api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
		"DEPENDENCY_UNAVAILABLE", "Story visibility could not be determined; retry", nil)
}

// writeStoryError maps a service error onto the two shapes above.
func writeStoryError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, service.ErrStoryNotVisible):
		storyNotFound(c)
	case errors.Is(err, service.ErrStoryPolicyUnresolved):
		storyUnavailable(c)
	default:
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError,
			"INTERNAL_ERROR", "Internal server error", nil)
	}
}

// requireViewer resolves the verified viewer, or writes 401 and returns false.
//
// Anonymous story access returns 401 rather than an empty 200. An empty 200
// tells an unauthenticated caller "you are allowed here, there is just nothing
// to see" — and it is the shape that let the old feed answer with no identity.
func requireViewer(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil || id == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized,
			"UNAUTHORIZED", "Sign-in required", nil)
		return uuid.Nil, false
	}
	return id, true
}

// CreateStoryRequest takes a canonical media_id.
//
// There is deliberately no media_url field. Accepting a URL meant the story
// pointed at whatever string the client sent, with no owned relationship to any
// asset this platform processed, scanned, or is allowed to serve.
type CreateStoryRequest struct {
	MediaID        string  `json:"media_id" binding:"required,uuid"`
	MediaType      string  `json:"media_type" binding:"required,oneof=image video"`
	Caption        string  `json:"caption"`
	Visibility     string  `json:"visibility" binding:"required,oneof=public followers close_friends"`
	IsHighlight    bool    `json:"is_highlight"`
	HighlightGroup *string `json:"highlight_group"`
	IdempotencyKey string  `json:"idempotency_key"`
}

func (h *Handler) CreateStory(c *gin.Context) {
	authorID, ok := requireViewer(c)
	if !ok {
		return
	}
	var req CreateStoryRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_REQUEST", err.Error(), nil)
		return
	}
	mediaID, err := uuid.Parse(req.MediaID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
			"INVALID_REQUEST", "media_id must be a UUID", nil)
		return
	}

	story, err := h.svc.CreateStoryPending(c.Request.Context(), &service.CreateStoryInput{
		AuthorID:       authorID,
		MediaID:        mediaID,
		MediaType:      req.MediaType,
		Caption:        req.Caption,
		Visibility:     req.Visibility,
		IsHighlight:    req.IsHighlight,
		HighlightGroup: req.HighlightGroup,
		IdempotencyKey: req.IdempotencyKey,
	})
	if err != nil {
		// Media that is missing, owned by another user, deleted, or the wrong
		// type all return ONE response. Distinguishing them would let a caller
		// probe which media ids exist and who owns them.
		if errors.Is(err, postgres.ErrStoryMediaInvalid) {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"INVALID_MEDIA", "media_id is not usable for a story", nil)
			return
		}
		writeStoryError(c, err)
		return
	}
	// 202, not 201: the story exists but is NOT published. Returning 201 with a
	// story body implied it was live, which is the false-success state the
	// client then had no way to correct.
	api.JSON(c.Writer, http.StatusAccepted, story, nil)
}

func (h *Handler) GetStoriesFeed(c *gin.Context) {
	viewerID, ok := requireViewer(c)
	if !ok {
		return
	}
	stories, err := h.svc.GetStoriesFeedForViewer(c.Request.Context(), viewerID)
	if err != nil {
		writeStoryError(c, err)
		return
	}
	if stories == nil {
		stories = []postgres.Story{}
	}
	api.JSON(c.Writer, http.StatusOK, stories, nil)
}

func (h *Handler) GetStoriesByAuthor(c *gin.Context) {
	viewerID, ok := requireViewer(c)
	if !ok {
		return
	}
	authorID, err := uuid.Parse(c.Param("authorId"))
	if err != nil {
		// An unparseable author is answered like an author with nothing
		// visible, so the route cannot distinguish a malformed id from a real
		// one that denies.
		api.JSON(c.Writer, http.StatusOK, []postgres.Story{}, nil)
		return
	}
	stories, err := h.svc.GetStoriesByAuthorForViewer(c.Request.Context(), viewerID, authorID)
	if err != nil {
		writeStoryError(c, err)
		return
	}
	if stories == nil {
		stories = []postgres.Story{}
	}
	api.JSON(c.Writer, http.StatusOK, stories, nil)
}

func (h *Handler) GetStory(c *gin.Context) {
	viewerID, ok := requireViewer(c)
	if !ok {
		return
	}
	storyID, err := uuid.Parse(c.Param("storyId"))
	if err != nil {
		// Same body as a denied story: a malformed id must not be
		// distinguishable from one that exists and is not for this viewer.
		storyNotFound(c)
		return
	}
	story, err := h.svc.GetStoryForViewer(c.Request.Context(), viewerID, storyID)
	if err != nil {
		writeStoryError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, story, nil)
}

func (h *Handler) ViewStory(c *gin.Context) {
	viewerID, ok := requireViewer(c)
	if !ok {
		return
	}
	storyID, err := uuid.Parse(c.Param("storyId"))
	if err != nil {
		storyNotFound(c)
		return
	}
	// The counter moves only for a viewer who may actually see the story. The
	// previous handler took no viewer and incremented for any UUID — including
	// ids that did not exist — so the count was writable by anyone.
	if err := h.svc.ViewStoryForViewer(c.Request.Context(), viewerID, storyID); err != nil {
		writeStoryError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "viewed"}, nil)
}

// GetMyStoryStatus is the owner-only truthful view.
//
// It is the ONLY surface that returns a non-approved story, and it is scoped to
// the authenticated author. Without it, a creator whose story is pending or
// rejected just sees it not appear — indistinguishable from the upload having
// been lost.
func (h *Handler) GetMyStoryStatus(c *gin.Context) {
	ownerID, ok := requireViewer(c)
	if !ok {
		return
	}
	stories, err := h.svc.OwnerStoryStatus(c.Request.Context(), ownerID)
	if err != nil {
		writeStoryError(c, err)
		return
	}
	if stories == nil {
		stories = []postgres.Story{}
	}
	api.JSON(c.Writer, http.StatusOK, stories, nil)
}
