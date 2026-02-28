package http

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/facebook-like/shared/api"
	"github.com/facebook-like/user-service/internal/service"
	"github.com/facebook-like/user-service/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type Handler struct {
	svc      *service.Service
	graphURL string
}

func New(svc *service.Service) *Handler {
	graphURL := os.Getenv("GRAPH_SERVICE_URL")
	if graphURL == "" {
		graphURL = "http://graph-service:8083"
	}
	return &Handler{svc: svc, graphURL: graphURL}
}

func (h *Handler) RegisterRoutes(r *gin.Engine) {
	v1 := r.Group("/v1/users")
	{
		v1.GET("/by-username/:username", h.GetUserByUsername)
		v1.GET("/:userId", h.GetUser)
		v1.GET("/:userId/links", h.GetUserLinks)
		v1.PUT("/me", h.UpdateMe)
		v1.GET("/me", h.GetMe)
		v1.PUT("/me/links", h.UpdateMyLinks)
		v1.GET("/me/settings", h.GetMySettings)
		v1.PUT("/me/settings", h.UpdateMySettings)

		// About
		v1.GET("/:userId/about", h.GetAbout)
		v1.GET("/:userId/about/:section", h.GetAboutSection)
		v1.PUT("/me/about/:section", h.UpsertAboutItem)
		v1.DELETE("/me/about/:section/:itemId", h.DeleteAboutItem)
	}
}

func (h *Handler) GetUser(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if u == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetUserByUsername(c *gin.Context) {
	username := c.Param("username")
	if username == "" {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", "Username is required", nil, nil)
		return
	}

	u, err := h.svc.GetUserByUsername(c.Request.Context(), username)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if u == nil {
		api.Error(c.Writer, http.StatusNotFound, "NOT_FOUND", "User not found", nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetMe(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	if userIDStr == "" {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Missing user ID", nil, nil)
		return
	}
	userID, _ := uuid.Parse(userIDStr)

	u, err := h.svc.GetUser(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to fetch profile", nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, u, nil)
}

type UpdateProfileRequest struct {
	DisplayName   string     `json:"display_name"`
	Bio           string     `json:"bio"`
	AvatarMediaID *uuid.UUID `json:"avatar_media_id"`
	CoverMediaID  *uuid.UUID `json:"cover_media_id"`
	FirstName     *string    `json:"first_name"`
	LastName      *string    `json:"last_name"`
	Gender        *string    `json:"gender"`
	DoB           *time.Time `json:"dob"`
	Username      *string    `json:"username"`
	Category      *string    `json:"category"`
	Profession    *string    `json:"profession"`
	Website       *string    `json:"website"`
	Location      *string    `json:"location"`
}

func (h *Handler) UpdateMe(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req UpdateProfileRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	u, err := h.svc.UpdateUser(c.Request.Context(), userID,
		req.DisplayName, req.Bio, req.AvatarMediaID, req.CoverMediaID,
		req.FirstName, req.LastName, req.Gender,
		req.Username, req.Category, req.Profession, req.Website, req.Location,
		req.DoB,
	)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, u, nil)
}

func (h *Handler) GetUserLinks(c *gin.Context) {
	userIDStr := c.Param("userId")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	links, err := h.svc.GetUserLinks(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if links == nil {
		links = []store.UserLink{}
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

type UpdateLinksRequest struct {
	Links []LinkItem `json:"links"`
}

type LinkItem struct {
	Platform     string `json:"platform"`
	URL          string `json:"url"`
	DisplayLabel string `json:"display_label"`
	SortOrder    int    `json:"sort_order"`
}

func (h *Handler) UpdateMyLinks(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req UpdateLinksRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	links := make([]store.UserLink, len(req.Links))
	for i, l := range req.Links {
		links[i] = store.UserLink{
			UserID:       userID,
			Platform:     l.Platform,
			URL:          l.URL,
			DisplayLabel: l.DisplayLabel,
			SortOrder:    l.SortOrder,
		}
	}

	if err := h.svc.UpdateUserLinks(c.Request.Context(), userID, links); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, links, nil)
}

func (h *Handler) GetMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	s, err := h.svc.GetSettings(c.Request.Context(), userID)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

func (h *Handler) UpdateMySettings(c *gin.Context) {
	userIDStr := c.GetHeader("X-User-Id")
	userID, err := uuid.Parse(userIDStr)
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}

	var req store.UserSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}
	req.UserID = userID

	s, err := h.svc.UpdateSettings(c.Request.Context(), &req)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, s, nil)
}

// --- About ---

// resolveViewerAccess determines the viewer's access level by calling graph-service.
func (h *Handler) resolveViewerAccess(ctx *gin.Context, ownerID uuid.UUID) service.ViewerAccess {
	viewerIDStr := ctx.GetHeader("X-User-Id")
	viewerID, err := uuid.Parse(viewerIDStr)
	if err != nil {
		return service.ViewerAccess{}
	}
	if viewerID == ownerID {
		return service.ViewerAccess{IsSelf: true}
	}

	url := fmt.Sprintf("%s/v1/graph/relationship?user_id=%s&other_id=%s", h.graphURL, viewerID, ownerID)
	resp, err := http.Get(url)
	if err != nil {
		return service.ViewerAccess{}
	}
	defer resp.Body.Close()

	var body struct {
		Data struct {
			FollowedBy bool `json:"followed_by"`
			IsFriend   bool `json:"is_friend"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return service.ViewerAccess{}
	}

	return service.ViewerAccess{
		IsFollower: body.Data.FollowedBy,
		IsFriend:   body.Data.IsFriend,
	}
}

func (h *Handler) GetAbout(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}

	access := h.resolveViewerAccess(c, userID)
	items, err := h.svc.GetAllAbout(c.Request.Context(), userID, access)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if items == nil {
		items = make(map[string][]store.AboutItem)
	}

	api.JSON(c.Writer, http.StatusOK, items, nil)
}

func (h *Handler) GetAboutSection(c *gin.Context) {
	userID, err := uuid.Parse(c.Param("userId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid user ID", nil, nil)
		return
	}
	section := c.Param("section")

	access := h.resolveViewerAccess(c, userID)
	items, err := h.svc.GetAboutSection(c.Request.Context(), userID, section, access)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}
	if items == nil {
		items = []store.AboutItem{}
	}

	api.JSON(c.Writer, http.StatusOK, items, nil)
}

type UpsertAboutRequest struct {
	ItemID     *string         `json:"item_id"`
	Data       json.RawMessage `json:"data" binding:"required"`
	Visibility string          `json:"visibility"`
	SortOrder  int             `json:"sort_order"`
}

func (h *Handler) UpsertAboutItem(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	section := c.Param("section")

	var req UpsertAboutRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_REQUEST", err.Error(), nil, nil)
		return
	}

	visibility := req.Visibility
	if visibility == "" {
		visibility = "public"
	}

	var itemID uuid.UUID
	if req.ItemID != nil {
		itemID, err = uuid.Parse(*req.ItemID)
		if err != nil {
			api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid item_id", nil, nil)
			return
		}
	}

	item := &store.AboutItem{
		UserID:     userID,
		Section:    section,
		ItemID:     itemID,
		Data:       req.Data,
		Visibility: visibility,
		SortOrder:  req.SortOrder,
	}

	result, err := h.svc.UpsertAboutItem(c.Request.Context(), item)
	if err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, result, nil)
}

func (h *Handler) DeleteAboutItem(c *gin.Context) {
	userID, err := uuid.Parse(c.GetHeader("X-User-Id"))
	if err != nil {
		api.Error(c.Writer, http.StatusUnauthorized, "UNAUTHORIZED", "Invalid user ID", nil, nil)
		return
	}
	section := c.Param("section")
	itemID, err := uuid.Parse(c.Param("itemId"))
	if err != nil {
		api.Error(c.Writer, http.StatusBadRequest, "INVALID_ID", "Invalid item ID", nil, nil)
		return
	}

	if err := h.svc.DeleteAboutItem(c.Request.Context(), userID, section, itemID); err != nil {
		api.Error(c.Writer, http.StatusInternalServerError, "INTERNAL_ERROR", err.Error(), nil, nil)
		return
	}

	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "deleted"}, nil)
}
