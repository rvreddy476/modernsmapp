package http

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/atpost/shared/api"
	"github.com/atpost/shop-service/internal/store/postgres"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ─── helpers ─────────────────────────────────────────────────────────────────

func requireUser(c *gin.Context) (uuid.UUID, bool) {
	raw := c.GetHeader("X-User-Id")
	if raw == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "missing user id", nil, nil)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_USER_ID", "invalid user id", nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

func parseUUIDParam(c *gin.Context, param, errCode string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, errCode, "invalid "+param, nil, nil)
		return uuid.Nil, false
	}
	return id, true
}

// ─── Storefronts ─────────────────────────────────────────────────────────────

func (h *Handler) CreateStorefront(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	var body struct {
		Handle      string `json:"handle"`
		DisplayName string `json:"display_name"`
		Tagline     string `json:"tagline"`
		About       string `json:"about"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	sf, err := h.svc.CreateStorefront(c.Request.Context(), userID, body.Handle, body.DisplayName, body.Tagline, body.About)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, sf, nil)
}

func (h *Handler) GetStorefrontByHandle(c *gin.Context) {
	handle := c.Param("handle")
	sf, err := h.svc.GetStorefrontByHandle(c.Request.Context(), handle)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "storefront not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, sf, nil)
}

func (h *Handler) GetStorefrontBySeller(c *gin.Context) {
	sellerID, ok := parseUUIDParam(c, "sellerId", "INVALID_SELLER_ID")
	if !ok {
		return
	}
	sf, err := h.svc.GetStorefrontBySeller(c.Request.Context(), sellerID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "storefront not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, sf, nil)
}

func (h *Handler) UpdateStorefront(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	storefrontID, ok := parseUUIDParam(c, "storefrontId", "INVALID_STOREFRONT_ID")
	if !ok {
		return
	}
	var body struct {
		DisplayName string `json:"display_name"`
		Tagline     string `json:"tagline"`
		About       string `json:"about"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	if err := h.svc.UpdateStorefront(c.Request.Context(), storefrontID, userID, body.DisplayName, body.Tagline, body.About); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) SetFeaturedListings(c *gin.Context) {
	_, ok := requireUser(c)
	if !ok {
		return
	}
	storefrontID, ok := parseUUIDParam(c, "storefrontId", "INVALID_STOREFRONT_ID")
	if !ok {
		return
	}
	var body struct {
		ListingIDs []string `json:"listing_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	ids := make([]uuid.UUID, 0, len(body.ListingIDs))
	for _, raw := range body.ListingIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_LISTING_ID", "invalid listing id: "+raw, nil, nil)
			return
		}
		ids = append(ids, id)
	}
	if err := h.svc.SetFeaturedListings(c.Request.Context(), storefrontID, ids); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "SET_FEATURED_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) GetFeaturedListings(c *gin.Context) {
	storefrontID, ok := parseUUIDParam(c, "storefrontId", "INVALID_STOREFRONT_ID")
	if !ok {
		return
	}
	ids, err := h.svc.GetFeaturedListings(c.Request.Context(), storefrontID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"listing_ids": ids}, nil)
}

func (h *Handler) CreateCollection(c *gin.Context) {
	_, ok := requireUser(c)
	if !ok {
		return
	}
	storefrontID, ok := parseUUIDParam(c, "storefrontId", "INVALID_STOREFRONT_ID")
	if !ok {
		return
	}
	var body struct {
		Name      string `json:"name"`
		SortOrder int    `json:"sort_order"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	col, err := h.svc.CreateCollection(c.Request.Context(), storefrontID, body.Name, body.SortOrder)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, col, nil)
}

func (h *Handler) GetCollections(c *gin.Context) {
	storefrontID, ok := parseUUIDParam(c, "storefrontId", "INVALID_STOREFRONT_ID")
	if !ok {
		return
	}
	cols, err := h.svc.GetCollections(c.Request.Context(), storefrontID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": cols}, nil)
}

func (h *Handler) AddListingToCollection(c *gin.Context) {
	_, ok := requireUser(c)
	if !ok {
		return
	}
	collectionID, ok := parseUUIDParam(c, "collectionId", "INVALID_COLLECTION_ID")
	if !ok {
		return
	}
	var body struct {
		ListingID string `json:"listing_id"`
		Position  int    `json:"position"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	listingID, err := uuid.Parse(body.ListingID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_LISTING_ID", "invalid listing id", nil, nil)
		return
	}
	if err := h.svc.AddListingToCollection(c.Request.Context(), collectionID, listingID, body.Position); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "ADD_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "added"}, nil)
}

func (h *Handler) GetCollectionListings(c *gin.Context) {
	collectionID, ok := parseUUIDParam(c, "collectionId", "INVALID_COLLECTION_ID")
	if !ok {
		return
	}
	ids, err := h.svc.GetCollectionListings(c.Request.Context(), collectionID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"listing_ids": ids}, nil)
}

// ─── Product Tags ─────────────────────────────────────────────────────────────

func (h *Handler) UpsertPostProductTags(c *gin.Context) {
	_, ok := requireUser(c)
	if !ok {
		return
	}
	postID, ok := parseUUIDParam(c, "postId", "INVALID_POST_ID")
	if !ok {
		return
	}
	var body struct {
		Tags []struct {
			ListingID  string          `json:"listing_id"`
			Position   json.RawMessage `json:"position"`
			AppearAtMs *int            `json:"appear_at_ms"`
			HideAtMs   *int            `json:"hide_at_ms"`
		} `json:"tags"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	tags := make([]postgres.PostProductTag, 0, len(body.Tags))
	for _, bt := range body.Tags {
		lid, err := uuid.Parse(bt.ListingID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_LISTING_ID", "invalid listing id: "+bt.ListingID, nil, nil)
			return
		}
		tags = append(tags, postgres.PostProductTag{
			PostID:     postID,
			ListingID:  lid,
			Position:   bt.Position,
			AppearAtMs: bt.AppearAtMs,
			HideAtMs:   bt.HideAtMs,
		})
	}
	if err := h.svc.UpsertPostProductTags(c.Request.Context(), postID, tags); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "UPSERT_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) GetPostProductTags(c *gin.Context) {
	postID, ok := parseUUIDParam(c, "postId", "INVALID_POST_ID")
	if !ok {
		return
	}
	tags, err := h.svc.GetPostProductTags(c.Request.Context(), postID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": tags}, nil)
}

// ─── Wishlist ─────────────────────────────────────────────────────────────────

func (h *Handler) GetWishlist(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	wishlist, items, err := h.svc.GetWishlist(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{
		"wishlist": wishlist,
		"items":    items,
	}, nil)
}

func (h *Handler) AddToWishlist(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	var body struct {
		ListingID string `json:"listing_id"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	listingID, err := uuid.Parse(body.ListingID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_LISTING_ID", "invalid listing id", nil, nil)
		return
	}
	if err := h.svc.AddToWishlist(c.Request.Context(), userID, listingID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "ADD_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "added"}, nil)
}

func (h *Handler) RemoveFromWishlist(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	listingID, ok := parseUUIDParam(c, "listingId", "INVALID_LISTING_ID")
	if !ok {
		return
	}
	if err := h.svc.RemoveFromWishlist(c.Request.Context(), userID, listingID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "REMOVE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

func (h *Handler) CreateStockAlert(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	listingID, ok := parseUUIDParam(c, "listingId", "INVALID_LISTING_ID")
	if !ok {
		return
	}
	if err := h.svc.CreateStockAlert(c.Request.Context(), userID, listingID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, map[string]string{"status": "created"}, nil)
}

func (h *Handler) RemoveStockAlert(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	listingID, ok := parseUUIDParam(c, "listingId", "INVALID_LISTING_ID")
	if !ok {
		return
	}
	if err := h.svc.RemoveStockAlert(c.Request.Context(), userID, listingID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "REMOVE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "removed"}, nil)
}

// ─── Group Buy ────────────────────────────────────────────────────────────────

func (h *Handler) CreateGroupBuy(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	var body struct {
		ListingID       string    `json:"listing_id"`
		TargetQty       int       `json:"target_qty"`
		DiscountedPrice float64   `json:"discounted_price"`
		OriginalPrice   float64   `json:"original_price"`
		ExpiresAt       time.Time `json:"expires_at"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	listingID, err := uuid.Parse(body.ListingID)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_LISTING_ID", "invalid listing id", nil, nil)
		return
	}
	gb, err := h.svc.CreateGroupBuy(c.Request.Context(), listingID, userID, body.TargetQty, body.DiscountedPrice, body.OriginalPrice, body.ExpiresAt)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, gb, nil)
}

func (h *Handler) GetGroupBuy(c *gin.Context) {
	groupBuyID, ok := parseUUIDParam(c, "groupBuyId", "INVALID_GROUP_BUY_ID")
	if !ok {
		return
	}
	gb, err := h.svc.GetGroupBuy(c.Request.Context(), groupBuyID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "group buy not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, gb, nil)
}

func (h *Handler) JoinGroupBuy(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	groupBuyID, ok := parseUUIDParam(c, "groupBuyId", "INVALID_GROUP_BUY_ID")
	if !ok {
		return
	}
	var body struct {
		PaymentIntentID string `json:"payment_intent_id"`
	}
	// body is optional — ignore bind error
	_ = c.ShouldBindJSON(&body)

	var paymentIntentID *uuid.UUID
	if body.PaymentIntentID != "" {
		pid, err := uuid.Parse(body.PaymentIntentID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_PAYMENT_INTENT_ID", "invalid payment_intent_id", nil, nil)
			return
		}
		paymentIntentID = &pid
	}

	if err := h.svc.JoinGroupBuy(c.Request.Context(), groupBuyID, userID, paymentIntentID); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "JOIN_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "joined"}, nil)
}

func (h *Handler) ListActiveGroupBuys(c *gin.Context) {
	listingID, ok := parseUUIDParam(c, "listingId", "INVALID_LISTING_ID")
	if !ok {
		return
	}
	buys, err := h.svc.ListActiveGroupBuys(c.Request.Context(), listingID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": buys}, nil)
}

// ─── Ads ──────────────────────────────────────────────────────────────────────

func (h *Handler) CreateAdCampaign(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	var body struct {
		Name         string    `json:"name"`
		Objective    string    `json:"objective"`
		BudgetType   string    `json:"budget_type"`
		BudgetAmount float64   `json:"budget_amount"`
		StartsAt     time.Time `json:"starts_at"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	camp, err := h.svc.CreateAdCampaign(c.Request.Context(), userID, body.Name, body.Objective, body.BudgetType, body.BudgetAmount, body.StartsAt)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, camp, nil)
}

func (h *Handler) ListAdCampaigns(c *gin.Context) {
	userID, ok := requireUser(c)
	if !ok {
		return
	}
	status := c.Query("status")
	limit, offset := parsePagination(c)
	camps, err := h.svc.ListAdCampaigns(c.Request.Context(), userID, status, limit, offset)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": camps}, nil)
}

func (h *Handler) GetAdCampaign(c *gin.Context) {
	campaignID, ok := parseUUIDParam(c, "campaignId", "INVALID_CAMPAIGN_ID")
	if !ok {
		return
	}
	camp, err := h.svc.GetAdCampaign(c.Request.Context(), campaignID)
	if err != nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "campaign not found", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, camp, nil)
}

func (h *Handler) UpdateAdCampaignStatus(c *gin.Context) {
	_, ok := requireUser(c)
	if !ok {
		return
	}
	campaignID, ok := parseUUIDParam(c, "campaignId", "INVALID_CAMPAIGN_ID")
	if !ok {
		return
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	if err := h.svc.UpdateAdCampaignStatus(c.Request.Context(), campaignID, body.Status); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "UPDATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": body.Status}, nil)
}

func (h *Handler) CreateAdSet(c *gin.Context) {
	_, ok := requireUser(c)
	if !ok {
		return
	}
	campaignID, ok := parseUUIDParam(c, "campaignId", "INVALID_CAMPAIGN_ID")
	if !ok {
		return
	}
	var body struct {
		Name        string          `json:"name"`
		Targeting   json.RawMessage `json:"targeting"`
		Placement   []string        `json:"placement"`
		BidType     string          `json:"bid_type"`
		BidAmount   *float64        `json:"bid_amount"`
		DailyBudget *float64        `json:"daily_budget"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	if body.BidType == "" {
		body.BidType = "auto"
	}
	targeting := body.Targeting
	if len(targeting) == 0 {
		targeting = json.RawMessage("{}")
	}
	as, err := h.svc.CreateAdSet(c.Request.Context(), &postgres.AdSet{
		CampaignID:  campaignID,
		Name:        body.Name,
		Targeting:   targeting,
		Placement:   body.Placement,
		BidType:     body.BidType,
		BidAmount:   body.BidAmount,
		DailyBudget: body.DailyBudget,
	})
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, as, nil)
}

func (h *Handler) CreateAdCreative(c *gin.Context) {
	_, ok := requireUser(c)
	if !ok {
		return
	}
	campaignID, ok := parseUUIDParam(c, "campaignId", "INVALID_CAMPAIGN_ID")
	if !ok {
		return
	}
	var body struct {
		ContentType string `json:"content_type"`
		PostID      string `json:"post_id"`
		Headline    string `json:"headline"`
		BodyText    string `json:"body_text"`
		CTAType     string `json:"cta_type"`
		CTAURL      string `json:"cta_url"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	creative := &postgres.AdCreative{
		CampaignID:  campaignID,
		ContentType: body.ContentType,
		Headline:    body.Headline,
		BodyText:    body.BodyText,
		CTAType:     body.CTAType,
		CTAURL:      body.CTAURL,
	}
	if body.PostID != "" {
		pid, err := uuid.Parse(body.PostID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_POST_ID", "invalid post_id", nil, nil)
			return
		}
		creative.PostID = &pid
	}
	cr, err := h.svc.CreateAdCreative(c.Request.Context(), creative)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "CREATE_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, cr, nil)
}

func (h *Handler) GetAdPerformance(c *gin.Context) {
	campaignID, ok := parseUUIDParam(c, "campaignId", "INVALID_CAMPAIGN_ID")
	if !ok {
		return
	}
	startStr := c.DefaultQuery("start_date", time.Now().AddDate(0, -1, 0).Format("2006-01-02"))
	endStr := c.DefaultQuery("end_date", time.Now().Format("2006-01-02"))

	startDate, err := time.Parse("2006-01-02", startStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_DATE", "invalid start_date", nil, nil)
		return
	}
	endDate, err := time.Parse("2006-01-02", endStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_DATE", "invalid end_date", nil, nil)
		return
	}

	perf, err := h.svc.GetAdPerformance(c.Request.Context(), campaignID, startDate, endDate)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "FETCH_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]interface{}{"items": perf}, nil)
}

func (h *Handler) SetAdFrequencyCap(c *gin.Context) {
	_, ok := requireUser(c)
	if !ok {
		return
	}
	campaignID, ok := parseUUIDParam(c, "campaignId", "INVALID_CAMPAIGN_ID")
	if !ok {
		return
	}
	var body struct {
		PerDay  int `json:"per_day"`
		PerWeek int `json:"per_week"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil, nil)
		return
	}
	if err := h.svc.SetAdFrequencyCap(c.Request.Context(), campaignID, body.PerDay, body.PerWeek); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "SET_CAP_FAILED", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}
