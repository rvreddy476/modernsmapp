// HTTP handlers for §P0-7 Phase A account-risk surface.
//
//   - GET /v1/dating/admin/risk?level=&limit=&offset= — admin queue read.
//   - GET /v1/dating/risk/:userId — internal lookup for cross-service gates
//     (api-gateway, commerce-service, message-service). Same
//     internal-service-key gate as the rest of /v1/dating.
package http

import (
	"net/http"

	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
)

// ListAccountRisks — GET /v1/dating/admin/risk?level=&limit=&offset=
//
// `level` is optional; empty returns rows at every level newest-first.
// The handler does not 400 on an unknown level — it just returns the
// empty list since the store's WHERE clause won't match anything.
func (h *Handler) ListAccountRisks(c *gin.Context) {
	level := c.Query("level")
	limit := parseIntQuery(c, "limit", 50, 200)
	offset := parseIntQuery(c, "offset", 0, 100000)
	items, err := h.svc.ListAccountRisksByLevel(c.Request.Context(), level, limit, offset)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"items":  items,
		"level":  level,
		"limit":  limit,
		"offset": offset,
	}, nil)
}

// GetAccountRisk — GET /v1/dating/risk/:userId
//
// Internal cross-service lookup. Returns 200 + {risk_level: "allow",
// risk_score: 0} when the user has no row yet so callers don't have
// to branch on 404 vs missing-row. The full AccountRisk shape is
// included for admin tooling that wants the signals breakdown.
func (h *Handler) GetAccountRisk(c *gin.Context) {
	userID, ok := parseUUID(c, "userId")
	if !ok {
		return
	}
	r, err := h.svc.GetAccountRisk(c.Request.Context(), userID)
	if err != nil {
		respondServiceError(c, err, http.StatusInternalServerError, "QUERY_FAILED")
		return
	}
	if r == nil {
		api.JSON(c.Writer, http.StatusOK, gin.H{
			"user_id":    userID.String(),
			"risk_level": "allow",
			"risk_score": 0,
			"evaluated":  false,
		}, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"user_id":           r.UserID.String(),
		"risk_level":        r.RiskLevel,
		"risk_score":        r.RiskScore,
		"signals":           r.Signals,
		"last_evaluated_at": r.LastEvaluatedAt,
		"updated_at":        r.UpdatedAt,
		"evaluated":         true,
	}, nil)
}
