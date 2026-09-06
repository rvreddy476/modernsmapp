package http

// The submit gate's surfaces: what a seller still needs, what a reviewer sees
// on a re-submission, and the compliance gaps a tightened rule left behind.
//
// ─── WHAT IS DELIBERATELY NOT HERE ──────────────────────────────────────
//
// No existing response body changes shape in this file. The gap signal is a
// NEW seller route rather than a field bolted onto GET /products/:id, and the
// reviewer diff is a NEW internal route rather than an addition to the admin
// queue's payload. Both would have been fewer lines as extra keys on existing
// responses, and both would have changed a contract three shipped clients
// already parse — for a feature none of them ask for yet.
//
// The one refusal body that DOES change is the submit route's, which is the
// entire point: it used to be `400 SUBMIT_FAILED` carrying a raw error
// string.

import (
	"net/http"
	"strconv"

	"github.com/atpost/shared/api"
	sharedmiddleware "github.com/atpost/shared/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// RegisterSubmissionRoutes adds the submit gate's reads.
//
// The internal group is re-declared with the same middleware
// RegisterOnboardingRoutes uses rather than sharing a variable, because gin's
// groups are values and the alternative is an ordering dependency between two
// registration functions that a reader has to hold in their head. The
// middleware is the same object either way.
func (h *Handler) RegisterSubmissionRoutes(r *gin.Engine) {
	// ── Seller-facing ───────────────────────────────────────
	//
	// The checklist BEFORE pressing Submit. A gate whose answer is only
	// reachable by failing teaches sellers to press the button and see what
	// happens, which is the behaviour that fills a review queue with
	// half-finished listings.
	r.GET("/v1/commerce/products/:productId/readiness", h.ProductReadiness)

	// The "action needed" signal. A seller whose live listing a tightened
	// rule has flagged sees it here — with, first and loudest, the fact that
	// the listing is still selling.
	r.GET("/v1/commerce/seller/action-needed", h.SellerActionNeeded)

	// ── Internal (admin-service proxies these) ──────────────
	adm := r.Group("/v1/commerce/internal")
	if h.internalKey != "" {
		adm.Use(sharedmiddleware.RequireInternalKey(h.internalKey))
	}
	adm.GET("/products/:productId/submissions", h.AdminProductSubmissions)
	adm.GET("/compliance-gaps", h.AdminComplianceGaps)
	adm.POST("/compliance-gaps/sweep", h.AdminSweepComplianceGaps)
}

// ─── Seller ─────────────────────────────────────────────────────────────

// ProductReadiness GET /v1/commerce/products/:productId/readiness
//
// 200 with an empty `missing` is "ready"; 200 with a populated one is the
// checklist. NOT a 4xx either way — nothing is being refused, the seller is
// asking a question.
func (h *Handler) ProductReadiness(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	missing, err := h.svc.ProductReadiness(c.Request.Context(), productID, userID)
	if err != nil {
		writeProductWriteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"ready":   len(missing) == 0,
		"missing": missing,
	}, nil)
}

// SellerActionNeeded GET /v1/commerce/seller/action-needed
func (h *Handler) SellerActionNeeded(c *gin.Context) {
	userID, ok := getUserID(c)
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "200"))
	items, err := h.svc.SellerActionNeeded(c.Request.Context(), userID, limit)
	if err != nil {
		writeProductWriteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"products": items,
		// Stated once, at the top, in the response that carries the bad
		// news. See ProductActionNeeded.StillSelling.
		"notice": "these listings are still on sale; the fields below became required after " +
			"they were approved and can be filled in on your next edit",
	}, nil)
}

// ─── Internal ───────────────────────────────────────────────────────────

// AdminProductSubmissions GET /v1/commerce/internal/products/:productId/submissions
//
// The reviewer's read: the recent attempts, and the diff between the last
// two. Both in one response because the queue draws both on one screen and a
// second round trip per listing is a second round trip per listing.
func (h *Handler) AdminProductSubmissions(c *gin.Context) {
	productID, ok := parseUUID(c, "productId")
	if !ok {
		return
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "20"))
	subs, diff, err := h.svc.ProductSubmissionHistory(c.Request.Context(), productID, limit)
	if err != nil {
		writeProductWriteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"submissions": subs,
		"diff":        diff,
	}, nil)
}

// AdminComplianceGaps GET /v1/commerce/internal/compliance-gaps
//
// The founder's queue. `definition_id` narrows it to one field, which is the
// read after tightening a rule: "show me what I just broke".
func (h *Handler) AdminComplianceGaps(c *gin.Context) {
	var defID *uuid.UUID
	if raw := c.Query("definition_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest,
				"INVALID_DEFINITION_ID", "definition_id must be a uuid", nil)
			return
		}
		defID = &id
	}
	limit, _ := strconv.Atoi(c.DefaultQuery("limit", "50"))
	offset, _ := strconv.Atoi(c.DefaultQuery("offset", "0"))

	gaps, total, err := h.svc.AdminComplianceGaps(c.Request.Context(), defID, limit, offset)
	if err != nil {
		writeProductWriteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gin.H{
		"gaps":  gaps,
		"total": total,
		// Restated on the founder's surface too, because this is the screen
		// on which somebody is most likely to reach for a bulk delist.
		"notice": "every listing here is still on sale. Flagging is advisory: a rule tightened " +
			"today must not take down a listing that was compliant when it was approved",
	}, nil)
}

// AdminSweepComplianceGaps POST /v1/commerce/internal/compliance-gaps/sweep
//
// POST, not GET, because it WRITES: it opens gap rows and closes the ones
// that no longer apply. It is idempotent — running it twice reaches the same
// state — but idempotent is not the same as safe to prefetch, and a GET that
// a proxy may retry is not where a catalogue-wide write belongs.
func (h *Handler) AdminSweepComplianceGaps(c *gin.Context) {
	res, err := h.svc.SweepComplianceGaps(c.Request.Context())
	if err != nil {
		writeProductWriteError(c, err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, res, nil)
}
