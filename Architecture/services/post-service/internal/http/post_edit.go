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
	MTube owner routes (2026-09-27; Creator Hub widening 2026-09-28).

	  PATCH  /v1/posts/:postId              edit after publish (owner only)
	           body: any subset of title, text, tags, hashtags, category,
	                 visibility, cover_media_id, allow_download, no_comments,
	                 made_for_kids, language, paid_promotion, altered_content,
	                 license, allow_embedding, recording_date,
	                 recording_location, remix_setting, comment_moderation,
	                 comment_access, notify_subscribers, age_restricted,
	                 hide_like_count, default_comment_sort, related_post_id
	                 -> the post detail
	  GET    /v1/posts/me/summary            {videos, shorts, live, collections, followers}
	  POST   /v1/uploads/bulk                {post_ids, patch:{...}} -> {results:[{id, ok, error?}]}
	  POST   /v1/uploads/bulk-delete         {post_ids} -> {results:[{id, ok, error?}]}
	  GET    /v1/posts/:postId/private-shares  owner -> {users:[...]}
	  PUT    /v1/posts/:postId/private-shares  owner, {user_ids} -> {users:[...]}
	  POST   /v1/posts/:postId/watch-later   {queued:true}   (idempotent)
	  DELETE /v1/posts/:postId/watch-later   {queued:false}  (idempotent)
	  GET    /v1/playlists/system/:kind      the caller's Queue / Loved, created on first read

	Status codes: 401 no identity, 404 NOT_FOUND (post / playlist missing,
	or a post the caller may not see), 403 FORBIDDEN (not the owner),
	422 for a value the field cannot take (INVALID_CATEGORY,
	INVALID_VISIBILITY, TITLE_TOO_LONG, TITLE_REQUIRED, TEXT_TOO_LONG,
	EMPTY_POST, INVALID_HASHTAG, TOO_MANY_HASHTAGS, INVALID_LANGUAGE,
	INVALID_TAGS, MEDIA_NOT_FOUND, MEDIA_NOT_READY, MEDIA_TYPE_MISMATCH,
	INVALID_LICENSE, INVALID_RECORDING_DATE, INVALID_RECORDING_LOCATION,
	INVALID_REMIX_SETTING, INVALID_COMMENT_MODERATION,
	INVALID_COMMENT_ACCESS, INVALID_COMMENT_SORT, RELATED_NOT_FOUND,
	RELATED_SELF, TOO_MANY_SHARES, INVALID_USER), 403 MEDIA_NOT_OWNED for a
	cover the caller did not upload, 409 SYSTEM_PLAYLIST for a generic write
	on a server-owned collection. The table is service.PostEditErrorStatus.
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
	// Creator Hub (2026-09-28). recording_date and related_post_id take ""
	// to clear.
	PaidPromotion      *bool   `json:"paid_promotion"`
	AlteredContent     *bool   `json:"altered_content"`
	License            *string `json:"license"`
	AllowEmbedding     *bool   `json:"allow_embedding"`
	RecordingDate      *string `json:"recording_date"`
	RecordingLocation  *string `json:"recording_location"`
	RemixSetting       *string `json:"remix_setting"`
	CommentModeration  *string `json:"comment_moderation"`
	CommentAccess      *string `json:"comment_access"`
	NotifySubscribers  *bool   `json:"notify_subscribers"`
	AgeRestricted      *bool   `json:"age_restricted"`
	HideLikeCount      *bool   `json:"hide_like_count"`
	DefaultCommentSort *string `json:"default_comment_sort"`
	RelatedPostID      *string `json:"related_post_id"`
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
		Language:      req.Language,
		PaidPromotion: req.PaidPromotion, AlteredContent: req.AlteredContent, License: req.License,
		AllowEmbedding: req.AllowEmbedding, RecordingDate: req.RecordingDate, RecordingLocation: req.RecordingLocation,
		RemixSetting: req.RemixSetting, CommentModeration: req.CommentModeration, CommentAccess: req.CommentAccess,
		NotifySubscribers: req.NotifySubscribers, AgeRestricted: req.AgeRestricted, HideLikeCount: req.HideLikeCount,
		DefaultCommentSort: req.DefaultCommentSort, RelatedPostID: req.RelatedPostID,
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

// writePostEditError maps every refusal of the owner-edit routes
// (service.PostEditErrorStatus). A 500 does not echo the error.
func writePostEditError(c *gin.Context, err error) {
	status, code := service.PostEditErrorStatus(err)
	msg := err.Error()
	switch status {
	case http.StatusNotFound:
		msg = "Post not found"
	case http.StatusForbidden:
		if code == "FORBIDDEN" {
			msg = "Only the post's owner can do this"
		} else if code == "MEDIA_NOT_OWNED" {
			msg = "You cannot use this media"
		}
	case http.StatusInternalServerError:
		msg = "Internal server error"
	}
	if code == "MEDIA_TYPE_MISMATCH" {
		msg = "cover_media_id must be an image"
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, msg, nil)
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

// bulkPatchRequest is the bulk-editable subset (Creator Hub, 2026-09-28).
// title and text are named only so a request carrying them is refused
// rather than half-applied; any other unknown field is ignored so a later
// field stays additive.
type bulkPatchRequest struct {
	Visibility         *string         `json:"visibility"`
	Category           *string         `json:"category"`
	Language           *string         `json:"language"`
	MadeForKids        *bool           `json:"made_for_kids"`
	AgeRestricted      *bool           `json:"age_restricted"`
	NoComments         *bool           `json:"no_comments"`
	CommentModeration  *string         `json:"comment_moderation"`
	CommentAccess      *string         `json:"comment_access"`
	DefaultCommentSort *string         `json:"default_comment_sort"`
	AllowEmbedding     *bool           `json:"allow_embedding"`
	License            *string         `json:"license"`
	RemixSetting       *string         `json:"remix_setting"`
	RecordingDate      *string         `json:"recording_date"`
	HideLikeCount      *bool           `json:"hide_like_count"`
	AlteredContent     *bool           `json:"altered_content"`
	PaidPromotion      *bool           `json:"paid_promotion"`
	Tags               *[]string       `json:"tags"`
	TagsMode           string          `json:"tags_mode,omitempty"`
	Title              json.RawMessage `json:"title,omitempty"`
	Text               json.RawMessage `json:"text,omitempty"`
}

func (p bulkPatchRequest) toService() service.BulkPatch {
	return service.BulkPatch{
		Visibility: p.Visibility, Category: p.Category, Language: p.Language, MadeForKids: p.MadeForKids,
		AgeRestricted: p.AgeRestricted, NoComments: p.NoComments, CommentModeration: p.CommentModeration,
		CommentAccess: p.CommentAccess, DefaultCommentSort: p.DefaultCommentSort, AllowEmbedding: p.AllowEmbedding,
		License: p.License, RemixSetting: p.RemixSetting, RecordingDate: p.RecordingDate,
		HideLikeCount: p.HideLikeCount, AlteredContent: p.AlteredContent, PaidPromotion: p.PaidPromotion,
		Tags: p.Tags, TagsMode: p.TagsMode,
	}
}

// bulkUploadsRequest is POST /v1/uploads/bulk.
type bulkUploadsRequest struct {
	PostIDs []string         `json:"post_ids"`
	Patch   bulkPatchRequest `json:"patch"`
}

// bulkDeleteRequest is POST /v1/uploads/bulk-delete.
type bulkDeleteRequest struct {
	PostIDs []string `json:"post_ids"`
}

// bulkUploadsResponse is the body of both bulk routes: one outcome per
// distinct id, in order.
type bulkUploadsResponse struct {
	Results []service.BulkOutcome `json:"results"`
}

// parseBulkIDs is the shared front of the bulk routes: 422 for an empty or
// over-cap list, 400 INVALID_ID for a malformed id. false = answered.
func parseBulkIDs(c *gin.Context, raw []string) ([]uuid.UUID, bool) {
	ctx := c.Request.Context()
	if len(raw) == 0 {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "post_ids is required", nil)
		return nil, false
	}
	if len(raw) > service.MaxBulkPostIDs {
		api.ErrorWithContext(ctx, c.Writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", service.ErrBulkTooMany.Error(), nil)
		return nil, false
	}
	ids := make([]uuid.UUID, 0, len(raw))
	for _, s := range raw {
		id, err := uuid.Parse(strings.TrimSpace(s))
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post id: "+s, nil)
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
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
	ids, ok := parseBulkIDs(c, req.PostIDs)
	if !ok {
		return
	}
	if len(req.Patch.Title) > 0 || len(req.Patch.Text) > 0 {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "INVALID_REQUEST", "title and text are not bulk-editable", nil)
		return
	}
	// An empty patch, a bad tags_mode and every value no post may take are
	// the service's 422s (validateBulkPatch), before any store call.
	results, err := h.svc.BulkEditUploads(c.Request.Context(), userID, service.BulkEditInput{PostIDs: ids, Patch: req.Patch.toService()})
	if err != nil {
		writePostEditError(c, err)
		return
	}
	if results == nil {
		results = []service.BulkOutcome{}
	}
	api.JSON(c.Writer, http.StatusOK, bulkUploadsResponse{Results: results}, nil)
}

// BulkDeleteUploads is POST /v1/uploads/bulk-delete.
func (h *Handler) BulkDeleteUploads(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing or invalid X-User-Id header", nil)
		return
	}
	var req bulkDeleteRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	ids, ok := parseBulkIDs(c, req.PostIDs)
	if !ok {
		return
	}
	results, err := h.svc.BulkDeleteUploads(c.Request.Context(), userID, ids)
	if err != nil {
		writePostEditError(c, err)
		return
	}
	if results == nil {
		results = []service.BulkOutcome{}
	}
	api.JSON(c.Writer, http.StatusOK, bulkUploadsResponse{Results: results}, nil)
}

// privateSharesRequest is PUT /v1/posts/:postId/private-shares.
type privateSharesRequest struct {
	UserIDs []string `json:"user_ids"`
}

// maxPrivateSharesBodyBytes bounds the PUT body well above 50 ids.
const maxPrivateSharesBodyBytes = 16 * 1024

// GetPrivateShares is GET /v1/posts/:postId/private-shares.
func (h *Handler) GetPrivateShares(c *gin.Context) {
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
	view, err := h.svc.ListPrivateShares(c.Request.Context(), userID, postID)
	if err != nil {
		writePostEditError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
}

// PutPrivateShares is PUT /v1/posts/:postId/private-shares. The id list is
// parsed here (a malformed id is 422 INVALID_USER, the same code as an
// unknown one); dedupe, the cap, the owner's own id and existence are the
// service's.
func (h *Handler) PutPrivateShares(c *gin.Context) {
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
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPrivateSharesBodyBytes)
	var req privateSharesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
		return
	}
	if req.UserIDs == nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "user_ids is required (an empty list clears the shares)", nil)
		return
	}
	ids := make([]uuid.UUID, 0, len(req.UserIDs))
	for _, raw := range req.UserIDs {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil || id == uuid.Nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnprocessableEntity, "INVALID_USER", service.ErrInvalidShareUser.Error(), nil)
			return
		}
		ids = append(ids, id)
	}
	view, err := h.svc.SetPrivateShares(c.Request.Context(), userID, postID, ids)
	if err != nil {
		writePostEditError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, view, nil)
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

// writeReadGateError answers the single-post read refusals shared by
// GET /v1/posts/:postId, GET /v1/videos/:videoId and the comments reads:
// 404 NOT_FOUND for a post the viewer may not see, and the age gate's
// 401 AGE_RESTRICTED_SIGN_IN / 403 AGE_RESTRICTED / 403 AGE_UNVERIFIED
// (service/age_gate.go). false when err is none of these.
func writeReadGateError(c *gin.Context, err error) bool {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrPostNotFound), errors.Is(err, service.ErrPostNotVisible):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_FOUND", "Post not found", nil)
	case errors.Is(err, service.ErrAgeSignIn), errors.Is(err, service.ErrAgeRestricted), errors.Is(err, service.ErrAgeUnverified):
		status, code := service.PostEditErrorStatus(err)
		api.ErrorWithContext(ctx, c.Writer, status, code, err.Error(), nil)
	default:
		return false
	}
	return true
}
