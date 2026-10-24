package http

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/atpost/post-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

/*
	Saved collections (2026-10-24, service/saved_collections.go). Every route
	is the caller's own (X-User-Id): another user's collection id answers
	exactly like one that does not exist.

	GET    /v1/saved/collections
	  200 data: [ {id:null, name:"All Saved", count, is_default:true},
	              {id, name, count, is_default:false, created_at, updated_at}, ... ]
	  (default first, then the caller's collections oldest first)
	POST   /v1/saved/collections                 {name}
	  201 data: collection
	PATCH  /v1/saved/collections/:collectionId   {name}
	  200 data: collection
	DELETE /v1/saved/collections/:collectionId
	  200 data: {deleted:true, collection_id, moved_to_default:n}
	PUT    /v1/saved/:savedId/collection          {collection_id: uuid|"default"|null}
	  200 data: saved item {id, user_id, target_type, target_id, collection_name, collection_id, created_at}
	PUT    /v1/posts/:postId/bookmark/collection  {collection_id: uuid|"default"|null}
	  200 data: saved item

	Errors: 400 INVALID_COLLECTION_NAME (name not 1-40 characters after
	trimming, control characters, or "All Saved" / "default"),
	400 INVALID_COLLECTION_ID, 400 INVALID_REQUEST (no collection_id key),
	404 COLLECTION_NOT_FOUND, 404 NOT_SAVED (the item / post is not in the
	caller's saved items), 409 COLLECTION_NAME_TAKEN (case-insensitive),
	409 COLLECTION_LIMIT_REACHED (50), 401 UNAUTHORIZED.
*/

// errCollectionIDRequired: a move body without a collection_id key.
var errCollectionIDRequired = errors.New("collection_id is required (null or \"default\" for the default collection)")

// isSavedCollectionError reports an error writeSavedCollectionError maps to
// a 4xx / 503 of its own.
func isSavedCollectionError(err error) bool {
	for _, e := range []error{service.ErrInvalidCollectionName, service.ErrInvalidCollectionID, errCollectionIDRequired,
		service.ErrCollectionNameTaken, service.ErrCollectionLimit, service.ErrCollectionNotFound,
		service.ErrSavedItemNotFound, service.ErrAuthoringStoreUnavailable} {
		if errors.Is(err, e) {
			return true
		}
	}
	return false
}

// writeSavedCollectionError maps the collection refusals; anything else is
// a 500 that does not echo the error.
func writeSavedCollectionError(c *gin.Context, err error) {
	ctx := c.Request.Context()
	switch {
	case errors.Is(err, service.ErrInvalidCollectionName):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_COLLECTION_NAME", err.Error(), nil)
	case errors.Is(err, service.ErrInvalidCollectionID):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_COLLECTION_ID", err.Error(), nil)
	case errors.Is(err, errCollectionIDRequired):
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil)
	case errors.Is(err, service.ErrCollectionNameTaken):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "COLLECTION_NAME_TAKEN", "You already have a collection with this name", nil)
	case errors.Is(err, service.ErrCollectionLimit):
		api.ErrorWithContext(ctx, c.Writer, http.StatusConflict, "COLLECTION_LIMIT_REACHED", "You can have at most 50 collections", nil)
	case errors.Is(err, service.ErrCollectionNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "COLLECTION_NOT_FOUND", "Collection not found", nil)
	case errors.Is(err, service.ErrSavedItemNotFound):
		api.ErrorWithContext(ctx, c.Writer, http.StatusNotFound, "NOT_SAVED", "This is not in your saved items", nil)
	case errors.Is(err, service.ErrAuthoringStoreUnavailable):
		api.ErrorWithContext(ctx, c.Writer, http.StatusServiceUnavailable, "STORE_UNAVAILABLE", "Saved items are unavailable right now", nil)
	default:
		api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Internal server error", nil)
	}
}

// parseCollectionRef is a collection_id value: "default" (any case) or ""
// is the default collection (nil), else a UUID.
func parseCollectionRef(raw string) (*uuid.UUID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.EqualFold(raw, "default") {
		return nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, service.ErrInvalidCollectionID
	}
	return &id, nil
}

// decodeCollectionField reads collection_id from a JSON object body:
// present reports whether the key was there; null / "default" is nil.
func decodeCollectionField(body []byte) (id *uuid.UUID, present bool, err error) {
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(body, &obj); err != nil {
		return nil, false, service.ErrInvalidCollectionID
	}
	raw, ok := obj["collection_id"]
	if !ok {
		return nil, false, nil
	}
	if rawJSONNull(raw) {
		return nil, true, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, true, service.ErrInvalidCollectionID
	}
	id, err = parseCollectionRef(s)
	return id, true, err
}

// requiredCollectionBody is a move body: the collection_id key must be
// there.
func requiredCollectionBody(c *gin.Context) (*uuid.UUID, error) {
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4096))
	if err != nil {
		return nil, errCollectionIDRequired
	}
	id, present, err := decodeCollectionField(body)
	if err != nil {
		return nil, err
	}
	if !present {
		return nil, errCollectionIDRequired
	}
	return id, nil
}

// optionalCollectionBody is POST /v1/posts/:postId/bookmark's optional body:
// none, {} or a null / "default" collection_id is the plain save.
func optionalCollectionBody(c *gin.Context) (*uuid.UUID, error) {
	if c.Request.Body == nil {
		return nil, nil
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, 4096))
	if err != nil || len(bytes.TrimSpace(body)) == 0 {
		return nil, nil
	}
	id, _, err := decodeCollectionField(body)
	return id, err
}

// callerID (credits.go) is the X-User-Id caller, else 401 UNAUTHORIZED.

type collectionNameRequest struct {
	Name *string `json:"name"`
}

func bindCollectionName(c *gin.Context) (string, bool) {
	var req collectionNameRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.Name == nil {
		writeSavedCollectionError(c, service.ErrInvalidCollectionName)
		return "", false
	}
	return *req.Name, true
}

// ListSavedCollections is GET /v1/saved/collections.
func (h *Handler) ListSavedCollections(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	out, err := h.svc.ListSavedCollections(c.Request.Context(), userID)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// CreateSavedCollection is POST /v1/saved/collections.
func (h *Handler) CreateSavedCollection(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	name, ok := bindCollectionName(c)
	if !ok {
		return
	}
	out, err := h.svc.CreateSavedCollection(c.Request.Context(), userID, name)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, out, nil)
}

func collectionParam(c *gin.Context) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("collectionId"))
	if err != nil {
		// Not a UUID: the default collection, or a typo. Either way not one
		// the caller may rename or delete.
		writeSavedCollectionError(c, service.ErrCollectionNotFound)
		return uuid.Nil, false
	}
	return id, true
}

// RenameSavedCollection is PATCH /v1/saved/collections/:collectionId.
func (h *Handler) RenameSavedCollection(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	id, ok := collectionParam(c)
	if !ok {
		return
	}
	name, ok := bindCollectionName(c)
	if !ok {
		return
	}
	out, err := h.svc.RenameSavedCollection(c.Request.Context(), userID, id, name)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, out, nil)
}

// DeleteSavedCollection is DELETE /v1/saved/collections/:collectionId.
func (h *Handler) DeleteSavedCollection(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	id, ok := collectionParam(c)
	if !ok {
		return
	}
	moved, err := h.svc.DeleteSavedCollection(c.Request.Context(), userID, id)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{"deleted": true, "collection_id": id, "moved_to_default": moved}, nil)
}

// MoveSavedItem is PUT /v1/saved/:savedId/collection.
func (h *Handler) MoveSavedItem(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	savedID, err := uuid.Parse(c.Param("savedId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid saved item ID", nil)
		return
	}
	collectionID, err := requiredCollectionBody(c)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	item, err := h.svc.MoveSavedItem(c.Request.Context(), userID, savedID, collectionID)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, item, nil)
}

// MoveBookmark is PUT /v1/posts/:postId/bookmark/collection.
func (h *Handler) MoveBookmark(c *gin.Context) {
	userID, ok := callerID(c)
	if !ok {
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	collectionID, err := requiredCollectionBody(c)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	item, err := h.svc.MoveBookmark(c.Request.Context(), userID, postID, collectionID)
	if err != nil {
		writeSavedCollectionError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, item, nil)
}
