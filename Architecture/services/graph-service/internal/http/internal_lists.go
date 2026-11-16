package http

import (
	"context"
	"net/http"
	"strconv"

	"github.com/atpost/graph-service/internal/store"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// Internal keyset-paged id lists (16 Nov 2026).
//
//	GET /v1/internal/graph/followers/:userId?limit=&cursor=
//	GET /v1/internal/graph/following/:userId?limit=&cursor=
//	GET /v1/internal/graph/connections/:userId?limit=&cursor=
//
// For in-cluster callers that walk a WHOLE list (feed-service's fan-out and
// its following-list reads). The gateway does not proxy /v1/internal, and
// the internal key gates the group. The public offset lists under /v1/graph
// are unchanged, including their 10,000 offset cap.
//
// Contract (testdata/contracts/internal_lists/, copied byte for byte into
// feed-service):
//
//	200 {"data": {"ids": ["<uuid>", ...], "next_cursor": "<opaque>"}}
//	    newest first; next_cursor is "" on the last page, otherwise it is
//	    passed back verbatim as ?cursor= for the page after it. `ids` is
//	    never null.
//	400 INVALID_ID      the path id is not a UUID
//	400 INVALID_CURSOR  ?cursor= is present and is not one this route issued
//	403 INTERNAL_ONLY   the request carries the gateway's stamp
//
// limit: default 100, at most 500; anything else is clamped, not refused.
// There is no audience rule here (the caller is a service acting for the
// system, not a viewer); hidden accounts are still left out.

// idPageReader is the slice of the service these routes need.
type idPageReader interface {
	ListIDPage(ctx context.Context, kind string, userID uuid.UUID, limit int, after *store.ListCursor) ([]uuid.UUID, string, error)
}

// idPageResponse is the `data` member of the answer.
type idPageResponse struct {
	IDs        []uuid.UUID `json:"ids"`
	NextCursor string      `json:"next_cursor"`
}

func (h *Handler) listIDsInternal(kind string) gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		// These lists skip the audience rule, so they must never answer edge
		// traffic. The gateway stamps this header on what it forwards (after
		// stripping any client value) and does not route /v1/internal at
		// all; a request that carries it got here by a misrouting.
		if c.GetHeader(GraphWriteSourceHeader) != "" {
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, "INTERNAL_ONLY", "this route is not served to edge traffic", nil)
			return
		}
		userID, err := uuid.Parse(c.Param("userId"))
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil)
			return
		}
		limit, _ := strconv.Atoi(c.Query("limit")) // absent or malformed: the default
		limit = store.ClampIDPageLimit(limit)

		var after *store.ListCursor
		if raw := c.Query("cursor"); raw != "" {
			cur, err := store.ParseListCursor(raw)
			if err != nil {
				api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, "INVALID_CURSOR",
					"cursor is not one this route issued; restart the list without it", nil)
				return
			}
			after = &cur
		}

		ids, next, err := h.idPages.ListIDPage(ctx, kind, userID, limit, after)
		if err != nil {
			api.ErrorWithContext(ctx, c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
			return
		}
		if ids == nil {
			ids = []uuid.UUID{}
		}
		api.JSON(c.Writer, http.StatusOK, idPageResponse{IDs: ids, NextCursor: next}, nil)
	}
}
