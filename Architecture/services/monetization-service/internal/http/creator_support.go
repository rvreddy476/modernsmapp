package http

import (
	"context"
	"net/http"

	"github.com/atpost/monetization-service/internal/service"
	"github.com/atpost/monetization-service/internal/store/postgres"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// CreatorSupport is GET /v1/monetization/creators/:creatorId/support: what
// the MTube watch page needs to decide whether to show a Thanks button.
type CreatorSupport struct {
	// TipsEnabled: POST /v1/monetization/tips would be accepted for this
	// creator from this viewer right now (see tipsEnabledFor).
	TipsEnabled bool `json:"tips_enabled"`
	// MinTipPaise / Currency: what SendTip enforces (service.MinTipPaise).
	MinTipPaise int64  `json:"min_tip_paise"`
	Currency    string `json:"currency"`
	// MembershipTiers: the creator's ACTIVE tiers, counted the way
	// GET /creators/:creatorId/tiers filters them.
	MembershipTiers int `json:"membership_tiers"`
}

// creatorTierReader is the one read the support endpoint needs.
type creatorTierReader interface {
	GetCreatorTiers(ctx context.Context, creatorID uuid.UUID) ([]postgres.CreatorTier, error)
}

func (h *Handler) tierReader() creatorTierReader {
	if h.tiers != nil {
		return h.tiers
	}
	return h.svc
}

// sendTipPattern is the registered route of SendTip.
const sendTipPattern = "/v1/monetization/tips"

// tipsEnabledFor is the SendTip rule, restated only as far as it can be
// known before a tip is attempted — nothing here is new:
//
//   - the launch boundary must admit POST /v1/monetization/tips. It is the
//     only recipient-independent gate: with MONETIZATION_WRITES_ENABLED off
//     (the beta default, and every staging/prod values file) or in
//     maintenance mode the route answers 503 before SendTip runs;
//   - validateTipInput refuses a nil recipient and a self-tip
//     (CANNOT_TIP_SELF), so the creator never sees a Thanks button on
//     their own video.
//
// SendTip has no per-creator eligibility beyond that: the recipient's
// ledger row is created on demand (EnsureWallet) and credited without an
// is_frozen check, so any creator can receive a tip once the boundary is
// open. The sender-side checks (balance, frozen sender, amount, daily cap)
// depend on the tip, not the creator, and cannot be answered here.
func (h *Handler) tipsEnabledFor(creatorID, viewerID uuid.UUID) bool {
	if creatorID == uuid.Nil || viewerID == creatorID {
		return false
	}
	return h.boundaryAdmitsNonAdmin(http.MethodPost, sendTipPattern)
}

// boundaryAdmitsNonAdmin is launchBoundary's decision for a non-admin
// route, as a predicate. TestTipsEnabledMatchesTheLaunchBoundary serves
// POST /tips through the real middleware under every flag combination and
// holds this to the same answer, so the two cannot drift.
func (h *Handler) boundaryAdmitsNonAdmin(method, pattern string) bool {
	if h.maintenance {
		return betaRuleAllows(method, pattern)
	}
	return h.writesEnabled || betaRuleAllows(method, pattern)
}

// GetCreatorSupport — GET /v1/monetization/creators/:creatorId/support
//
// Public (X-User-Id optional: it only turns off the button on the
// creator's own page). Open in beta, where it answers tips_enabled=false.
func (h *Handler) GetCreatorSupport(c *gin.Context) {
	creatorID, err := uuid.Parse(c.Param("creatorId"))
	if err != nil || creatorID == uuid.Nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid creator ID", nil)
		return
	}
	viewerID, _ := uuid.Parse(c.GetHeader("X-User-Id")) // absent/invalid = anonymous (uuid.Nil)

	tiers, err := h.tierReader().GetCreatorTiers(c.Request.Context(), creatorID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil)
		return
	}
	active := 0
	for _, t := range tiers {
		if t.IsActive {
			active++
		}
	}
	api.JSON(c.Writer, http.StatusOK, CreatorSupport{
		TipsEnabled:     h.tipsEnabledFor(creatorID, viewerID),
		MinTipPaise:     service.MinTipPaise,
		Currency:        service.TipCurrency,
		MembershipTiers: active,
	}, nil)
}
