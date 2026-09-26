package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RevealPostAuthor — GET /v1/groups/:groupId/posts/v2/:postId/author
//
// Who wrote the post. For a non-anonymous post this is the author_id the
// post already shows. For an anonymous post it is answered only to the
// group's owner, admins and moderators (403 otherwise) and every answer is
// written to group_admin_audit. The post's own JSON never carries it.
func (h *Handler) RevealPostAuthor(c *gin.Context) {
	actorID, ok := getUserID(c)
	if !ok {
		return
	}
	groupID, err := uuid.Parse(c.Param("groupId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid group ID", nil)
		return
	}
	postID, err := uuid.Parse(c.Param("postId"))
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid post ID", nil)
		return
	}
	out, err := h.svc.RevealPostAuthor(c.Request.Context(), actorID, groupID, postID)
	if err != nil {
		handleServiceError(c, err)
		return
	}
	c.Header("Cache-Control", "private, no-store")
	api.JSON(c.Writer, http.StatusOK, out, nil)
}
