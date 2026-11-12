package http

import (
	"encoding/base64"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/atpost/feed-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Echo note timelines (docs/designs/echo-contract.md):
//
//	GET /v1/feed/notes/following  ?cursor=&limit=  200 {items:[Note], next_cursor}, newest first
//	GET /v1/feed/notes/for-you    ?cursor=&limit=  200 {items:[Note], next_cursor}, ranked
//
// Both are signed in only, and answer 503 ECHO_NOT_ENABLED while the
// ECHO_ENABLED switch is off (the fan-out keeps running). Items are
// hydrated exactly like Home (HydratePosts: post-service's batch plus the
// keyword, privacy, listing and feedback filters); each Note carries the
// contract fields post-service put on the row.

// notesPage is the body of both routes. next_cursor is null at the end.
type notesPage struct {
	Items      []service.HydratedPost `json:"items"`
	NextCursor *string                `json:"next_cursor"`
}

// notesGate answers the switch and the signed-in check; ok is false when it
// has already written the response.
func (h *Handler) notesGate(c *gin.Context) (uuid.UUID, bool) {
	if !h.svc.EchoEnabled() {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			"ECHO_NOT_ENABLED", "Echo is not available yet", nil)
		return uuid.Nil, false
	}
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil)
		return uuid.Nil, false
	}
	return userID, true
}

// GetNotesFollowing serves the Following timeline. The cursor is the same
// opaque keyset token as the reels and Tube pages (rankedPageParams).
func (h *Handler) GetNotesFollowing(c *gin.Context) {
	userID, ok := h.notesGate(c)
	if !ok {
		return
	}
	limit, before, err := rankedPageParams(c)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil)
		return
	}
	items, next, err := h.svc.GetNotesFollowingPage(c.Request.Context(), userID, limit, before)
	if err != nil {
		internalError(c, err)
		return
	}
	var cursor *string
	if meta := rankedPageMeta(next); meta != nil {
		cursor = &meta.NextCursor
	}
	h.writeNotesPage(c, userID, items, cursor)
}

// GetNotesForYou serves the ranked For You list.
func (h *Handler) GetNotesForYou(c *gin.Context) {
	userID, ok := h.notesGate(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	cursor, err := decodeForYouCursor(c.Query("cursor"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CURSOR", "Invalid cursor", nil)
		return
	}
	items, next, err := h.svc.GetNotesForYouPage(c.Request.Context(), userID, limit, cursor)
	if err != nil {
		internalError(c, err)
		return
	}
	var token *string
	if next != nil {
		t := encodeForYouCursor(*next)
		token = &t
	}
	h.writeNotesPage(c, userID, items, token)
}

func (h *Handler) writeNotesPage(c *gin.Context, userID uuid.UUID, items []service.FeedItem, next *string) {
	hydrated, err := h.svc.HydratePosts(c.Request.Context(), items, userID)
	if err != nil {
		// Never the raw candidates: they skipped every hydration-time check.
		log.Printf("notes feed hydration failed: %v", err)
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			"FEED_UNAVAILABLE", "Feed is temporarily unavailable", nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, notesPage{Items: onlyNotes(hydrated), NextCursor: next}, nil)
}

// onlyNotes keeps the hydrated rows post-service says are notes: a row
// whose post turned out to be anything else does not belong on Echo.
func onlyNotes(posts []service.HydratedPost) []service.HydratedPost {
	out := make([]service.HydratedPost, 0, len(posts))
	for _, p := range posts {
		if p.ContentType == service.NoteContentType {
			out = append(out, p)
		}
	}
	return out
}

// For You cursors are base64url("fy1:<snapshot>:<offset>").
func encodeForYouCursor(c service.ForYouCursor) string {
	return base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf("fy1:%s:%d", c.Snapshot, c.Offset)))
}

func decodeForYouCursor(raw string) (service.ForYouCursor, error) {
	if raw == "" {
		return service.ForYouCursor{}, nil
	}
	decoded, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return service.ForYouCursor{}, fmt.Errorf("invalid cursor")
	}
	parts := strings.Split(string(decoded), ":")
	if len(parts) != 3 || parts[0] != "fy1" || parts[1] == "" {
		return service.ForYouCursor{}, fmt.Errorf("invalid cursor")
	}
	for _, r := range parts[1] {
		if !strings.ContainsRune("0123456789abcdefghijklmnopqrstuvwxyz", r) {
			return service.ForYouCursor{}, fmt.Errorf("invalid cursor")
		}
	}
	offset, err := strconv.Atoi(parts[2])
	if err != nil || offset < 0 {
		return service.ForYouCursor{}, fmt.Errorf("invalid cursor")
	}
	return service.ForYouCursor{Snapshot: parts[1], Offset: offset}, nil
}
