package http

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/atpost/search-service/internal/reindex"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// ReindexUsers handles POST /v1/search/internal/reindex/users.
//
// It rebuilds the users_v1 OpenSearch index from profile-service, the
// source of truth. The work runs in a detached background goroutine —
// a full reindex can take a while at scale — so the endpoint returns
// 202 Accepted immediately. Progress and the final fetched/indexed
// counts are logged. Internal-key gated by the engine middleware.
func (h *Handler) ReindexUsers(c *gin.Context) {
	if h.profileServiceURL == "" {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusServiceUnavailable,
			"REINDEX_UNCONFIGURED", "PROFILE_SERVICE_URL is not configured on this deployment", nil)
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
		defer cancel()
		res, err := reindex.ReindexUsers(
			ctx, h.httpClient, h.profileServiceURL, h.internalKey, h.store, slog.Default(),
		)
		if err != nil {
			slog.Error("admin reindex/users failed", "err", err,
				"fetched", res.Fetched, "indexed", res.Indexed)
			return
		}
		slog.Info("admin reindex/users finished", "fetched", res.Fetched, "indexed", res.Indexed)
	}()

	api.JSON(c.Writer, http.StatusAccepted, map[string]string{
		"status": "reindex started",
		"detail": "rebuilding users_v1 from profile-service in the background; check service logs for completion",
	}, nil)
}
