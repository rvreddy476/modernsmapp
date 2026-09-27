package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	MTube owner routes (2026-09-27).

	  PATCH  /v1/posts/:postId              edit after publish (owner only)
	           body: any subset of title, text, tags, hashtags, category,
	                 visibility, cover_media_id, allow_download, no_comments,
	                 made_for_kids, language   -> the post detail
	  GET    /v1/posts/me/summary            {videos, shorts, live, collections, followers}
	  POST   /v1/uploads/bulk                {post_ids, patch:{visibility}} -> {results:[{id, ok, error?}]}
	  POST   /v1/posts/:postId/watch-later   {queued:true}   (idempotent)
	  DELETE /v1/posts/:postId/watch-later   {queued:false}  (idempotent)
	  GET    /v1/playlists/system/:kind      the caller's Queue / Loved, created on first read

	Status codes: 401 no identity, 404 NOT_FOUND (post / playlist missing,
	or a post the caller may not see), 403 FORBIDDEN (not the owner),
	422 for a value the field cannot take (INVALID_CATEGORY,
	INVALID_VISIBILITY, TITLE_TOO_LONG, TITLE_REQUIRED, TEXT_TOO_LONG,
	EMPTY_POST, INVALID_HASHTAG, TOO_MANY_HASHTAGS, INVALID_LANGUAGE,
	INVALID_TAGS, MEDIA_NOT_FOUND, MEDIA_NOT_READY, MEDIA_TYPE_MISMATCH),
	403 MEDIA_NOT_OWNED for a cover the caller did not upload,
	409 SYSTEM_PLAYLIST for a generic write on a server-owned collection.
*/

// updatePostRequest keeps every field raw-presence-aware: absent = leave it.
type updatePostRequest struct {
	Title         *string   `json:"title"`
	Text          *string   `json:"text"`
	Tags          *[]string `json:"tags"`
	Hashtags      *[]string `json:"hashtags"`
	Category      *string   `json:"category"`
	Visibility    *string   `json:"visibility"`
	CoverMediaID  *string   `json:"cover_media_id"`
	AllowDownload *bool     `json:"allow_download"`
	NoComments    *bool     `json:"no_comments"`
	MadeForKids   *bool     `json:"made_for_kids"`
	Language      *string   `json:"language"`
}

// maxUpdatePostBodyBytes mirrors the create route's cap.
const maxUpdatePostBodyBytes = maxCreatePostBodyBytes

// UpdatePost is PATCH /v1/posts/:postId.
func (h *Handler) UpdatePost(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxUpdatePostBodyBytes)
	var req updatePostRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	in := service.PostEditInput{
		Title: req.Title, Text: req.Text, Tags: req.Tags, Hashtags: req.Hashtags,
		Category: req.Category, Visibility: req.Visibility,
		AllowDownload: req.AllowDownload, NoComments: req.NoComments, MadeForKids: req.MadeForKids,
		Language: req.Language,
	}
	if req.CoverMediaID != nil {
		id, err := uuid.Parse(strings.TrimSpace(*req.CoverMediaID))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "MEDIA_NOT_FOUND", "cover_media_id is not a media id", nil)
			return
		}
		in.CoverMediaID = &id
	}
	detail, err := h.svc.UpdatePost(c.Request.Context(), userID, postID, in)
	if err != nil {
		writePostEditError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, detail, nil)
}

// writePostEditError maps every refusal of the owner-edit routes.
func writePostEditError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	write := func(status int, code, msg string) {
		api.ErrorWithContext(ctx, c.Writer, status, code, msg, nil)
	}
	switch {
	case errors.Is(err, service.ErrPostNotFound), errors.Is(err, service.ErrPostNotVisible):
		write(http.StatusNotFound, "NOT_FOUND", "Post not found")
	case errors.Is(err, service.ErrNotPostAuthor), errors.Is(err, service.ErrPostForbidden):
		write(http.StatusForbidden, "FORBIDDEN", "Only the post's owner can do this")
	case errors.Is(err, service.ErrMediaNotOwned):
		write(http.StatusForbidden, "MEDIA_NOT_OWNED", "You cannot use this media")
	case errors.Is(err, service.ErrInvalidCategory):
		write(http.StatusUnprocessableEntity, "INVALID_CATEGORY", err.Error())
	case errors.Is(err, service.ErrInvalidVisibility):
		write(http.StatusUnprocessableEntity, "INVALID_VISIBILITY", err.Error())
	case errors.Is(err, service.ErrTitleTooLong):
		write(http.StatusUnprocessableEntity, "TITLE_TOO_LONG", err.Error())
	case errors.Is(err, service.ErrTitleRequired):
		write(http.StatusUnprocessableEntity, "TITLE_REQUIRED", err.Error())
	case errors.Is(err, service.ErrTextTooLong):
		write(http.StatusUnprocessableEntity, "TEXT_TOO_LONG", err.Error())
	case errors.Is(err, service.ErrEmptyPost):
		write(http.StatusUnprocessableEntity, "EMPTY_POST", err.Error())
	case errors.Is(err, service.ErrInvalidHashtag):
		write(http.StatusUnprocessableEntity, "INVALID_HASHTAG", err.Error())
	case errors.Is(err, service.ErrTooManyHashtags):
		write(http.StatusUnprocessableEntity, "TOO_MANY_HASHTAGS", err.Error())
	case errors.Is(err, service.ErrInvalidLanguage):
		write(http.StatusUnprocessableEntity, "INVALID_LANGUAGE", err.Error())
	case errors.Is(err, service.ErrTooManyTags), errors.Is(err, service.ErrTagTooLong):
		write(http.StatusUnprocessableEntity, "INVALID_TAGS", err.Error())
	case errors.Is(err, service.ErrMediaNotFound):
		write(http.StatusUnprocessableEntity, "MEDIA_NOT_FOUND", err.Error())
	case errors.Is(err, service.ErrMediaNotReady):
		write(http.StatusUnprocessableEntity, "MEDIA_NOT_READY", err.Error())
	case errors.Is(err, service.ErrMediaTypeMismatch):
		write(http.StatusUnprocessableEntity, "MEDIA_TYPE_MISMATCH", "cover_media_id must be an image")
	case errors.Is(err, service.ErrBulkNothing), errors.Is(err, service.ErrBulkTooMany):
		write(http.StatusUnprocessableEntity, "INVALID_REQUEST", err.Error())
	case errors.Is(err, service.ErrAuthoringStoreUnavailable):
		write(http.StatusServiceUnavailable, "STORE_UNAVAILABLE", err.Error())
	default:
		write(http.StatusInternalServerError, "INTERNAL_ERROR", err.Error())
	}
}

// GetCreatorSummary is GET /v1/posts/me/summary.
func (h *Handler) GetCreatorSummary(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	summary, err := h.svc.GetCreatorSummary(c.Request.Context(), userID)
	if err != nil {
		writePostEditError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, summary, nil)
}

// bulkUploadsRequest is POST /v1/uploads/bulk. The patch carries the one
// field the Creator Hub bulk-edits today; unknown patch fields are ignored
// rather than refused so a later field is additive.
type bulkUploadsRequest struct {
	PostIDs []string `json:"post_ids"`
	Patch   struct {
		Visibility string `json:"visibility"`
	} `json:"patch"`
}

// bulkUploadsResponse is the body: one outcome per distinct id, in order.
type bulkUploadsResponse struct {
	Results []postgres.BulkVisibilityOutcome `json:"results"`
}

// BulkUpdateUploads is POST /v1/uploads/bulk.
func (h *Handler) BulkUpdateUploads(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	var req bulkUploadsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if len(req.PostIDs) == 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "post_ids is required", nil)
		return
	}
	if len(req.PostIDs) > service.MaxBulkPostIDs {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", service.ErrBulkTooMany.Error(), nil)
		return
	}
	if strings.TrimSpace(req.Patch.Visibility) == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "patch.visibility is required", nil)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.PostIDs))
	for _, raw := range req.PostIDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post id: "+raw, nil)
			return
		}
		ids = append(ids, id)
	}
	results, err := h.svc.BulkSetVisibility(c.Request.Context(), userID, service.BulkVisibilityInput{PostIDs: ids, Visibility: req.Patch.Visibility})
	if err != nil {
		writePostEditError(c, err)
		return
	}
	if results == nil {
		results = []postgres.BulkVisibilityOutcome{}
	}
	api.JSON(c.Writer, http.StatusOK, bulkUploadsResponse{Results: results}, nil)
}

// AddWatchLater is POST /v1/posts/:postId/watch-later.
func (h *Handler) AddWatchLater(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	res, err := h.svc.AddToWatchLater(c.Request.Context(), userID, postID)
	if err != nil {
		writePostEditError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// RemoveWatchLater is DELETE /v1/posts/:postId/watch-later.
func (h *Handler) RemoveWatchLater(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	res, err := h.svc.RemoveFromWatchLater(c.Request.Context(), userID, postID)
	if err != nil {
		writePostEditError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}

// GetSystemPlaylist is GET /v1/playlists/system/:kind.
func (h *Handler) GetSystemPlaylist(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	kind := strings.ToLower(strings.TrimSpace(c.Param("kind")))
	if !postgres.IsSystemPlaylistKind(kind) {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_KIND", service.ErrInvalidSystemPlaylistKind.Error(), nil)
		return
	}
	p, err := h.svc.GetSystemPlaylist(c.Request.Context(), userID, kind)
	if err != nil {
		writeVideoAuthoringError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, p, nil)
}

// rawJSONNull reports whether a raw JSON field was sent as null.
func rawJSONNull(raw json.RawMessage) bool {
	return bytes.Equal(bytes.TrimSpace(raw), []byte("null"))
}
