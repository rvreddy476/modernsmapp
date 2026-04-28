package http

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/atpost/memories-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

func (h *Handler) ListSlambookTemplatePacks(c *gin.Context) {
	packs, err := h.svc.ListSlambookTemplatePacks(c.Request.Context())
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusInternalServerError, "SLAMBOOK_TEMPLATE_PACKS_FAILED", err.Error(), nil)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": packs}, nil)
}

func (h *Handler) CreateSlambook(c *gin.Context) {
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		Title                string     `json:"title"`
		Subtitle             string     `json:"subtitle"`
		Description          string     `json:"description"`
		Category             string     `json:"category"`
		ThemeKey             string     `json:"theme_key"`
		Visibility           string     `json:"visibility"`
		ResponseIdentityMode string     `json:"response_identity_mode"`
		ApprovalRequired     bool       `json:"approval_required"`
		TemplatePackKey      string     `json:"template_pack_key"`
		ClosesAt             *time.Time `json:"closes_at"`
		CustomCards          []struct {
			Title           string `json:"title"`
			Prompt          string `json:"prompt"`
			ResponseType    string `json:"response_type"`
			PlaceholderText string `json:"placeholder_text"`
			HelpText        string `json:"help_text"`
			IsRequired      bool   `json:"is_required"`
		} `json:"custom_cards"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	customCards := make([]service.CreateSlambookCardInput, 0, len(body.CustomCards))
	for _, card := range body.CustomCards {
		customCards = append(customCards, service.CreateSlambookCardInput{
			Title:           card.Title,
			Prompt:          card.Prompt,
			ResponseType:    card.ResponseType,
			PlaceholderText: card.PlaceholderText,
			HelpText:        card.HelpText,
			IsRequired:      card.IsRequired,
		})
	}

	slambook, err := h.svc.CreateSlambook(c.Request.Context(), &service.CreateSlambookInput{
		OwnerUserID:          userID,
		Title:                body.Title,
		Subtitle:             body.Subtitle,
		Description:          body.Description,
		Category:             body.Category,
		ThemeKey:             body.ThemeKey,
		Visibility:           body.Visibility,
		ResponseIdentityMode: body.ResponseIdentityMode,
		ApprovalRequired:     body.ApprovalRequired,
		TemplatePackKey:      body.TemplatePackKey,
		CustomCards:          customCards,
		ClosesAt:             body.ClosesAt,
	})
	if err != nil {
		writeSlambookError(c, "CREATE_SLAMBOOK_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, slambook, nil)
}

func (h *Handler) ListSlambooks(c *gin.Context) {
	viewerUserID, ok := parseOptionalUserID(c)
	if !ok {
		return
	}

	ownerParam := strings.TrimSpace(c.Query("owner_user_id"))
	if ownerParam == "" {
		requiredUserID, ok := parseUserID(c)
		if !ok {
			return
		}
		viewerUserID = &requiredUserID
	}

	ownerUserID, err := parseUUIDString(ownerParam)
	if err != nil {
		if ownerParam == "" && viewerUserID != nil {
			ownerUserID = *viewerUserID
		} else {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_OWNER_USER_ID", "invalid owner user id", nil)
			return
		}
	}

	resolvedViewerID := uuid.Nil
	if viewerUserID != nil {
		resolvedViewerID = *viewerUserID
	}

	items, err := h.svc.ListSlambooks(c.Request.Context(), resolvedViewerID, ownerUserID)
	if err != nil {
		writeSlambookError(c, "LIST_SLAMBOOKS_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": items}, nil)
}

func (h *Handler) GetSlambook(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	viewerUserID, ok := parseOptionalUserID(c)
	if !ok {
		return
	}

	detail, err := h.svc.GetSlambookDetail(c.Request.Context(), slambookID, viewerUserID)
	if err != nil {
		writeSlambookError(c, "GET_SLAMBOOK_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, detail, nil)
}

func (h *Handler) CreateSlambookShareLink(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	invite, err := h.svc.CreateSlambookShareLink(c.Request.Context(), slambookID, userID)
	if err != nil {
		writeSlambookError(c, "CREATE_SLAMBOOK_SHARE_LINK_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, invite, nil)
}

func (h *Handler) CreateSlambookInvites(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		TargetUserIDs []string `json:"target_user_ids"`
		Message       *string  `json:"message"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	targetUserIDs, err := parseUUIDStrings(body.TargetUserIDs)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_TARGET_USER_IDS", err.Error(), nil)
		return
	}

	invites, err := h.svc.CreateSlambookInvites(c.Request.Context(), slambookID, userID, targetUserIDs, body.Message)
	if err != nil {
		writeSlambookError(c, "CREATE_SLAMBOOK_INVITES_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusCreated, map[string]any{"items": invites}, nil)
}

func (h *Handler) SaveSlambookResponse(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		DisplayName string `json:"display_name"`
		Anonymous   bool   `json:"anonymous"`
		ShareToken  string `json:"share_token"`
		Submit      bool   `json:"submit"`
		Answers     []struct {
			CardID     string         `json:"card_id"`
			AnswerText string         `json:"answer_text"`
			AnswerJSON map[string]any `json:"answer_json"`
		} `json:"answers"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	answers := make([]service.RespondToSlambookAnswerInput, 0, len(body.Answers))
	for _, answer := range body.Answers {
		cardID, err := parseUUIDString(answer.CardID)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_CARD_ID", "invalid card id", nil)
			return
		}
		answers = append(answers, service.RespondToSlambookAnswerInput{
			CardID:     cardID,
			AnswerText: answer.AnswerText,
			AnswerJSON: answer.AnswerJSON,
		})
	}

	var shareToken *uuid.UUID
	if token := strings.TrimSpace(body.ShareToken); token != "" {
		parsedToken, err := parseUUIDString(token)
		if err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SHARE_TOKEN", "invalid share token", nil)
			return
		}
		shareToken = &parsedToken
	}

	session, err := h.svc.SaveSlambookResponse(c.Request.Context(), slambookID, userID, &service.RespondToSlambookInput{
		DisplayName: body.DisplayName,
		Anonymous:   body.Anonymous,
		ShareToken:  shareToken,
		Submit:      body.Submit,
		Answers:     answers,
	})
	if err != nil {
		writeSlambookError(c, "SAVE_SLAMBOOK_RESPONSE_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, session, nil)
}

func (h *Handler) ListSlambookOpinionSpace(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	viewerUserID, ok := parseOptionalUserID(c)
	if !ok {
		return
	}

	items, err := h.svc.ListSlambookOpinionSpace(c.Request.Context(), slambookID, viewerUserID)
	if err != nil {
		writeSlambookError(c, "LIST_SLAMBOOK_OPINION_SPACE_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": items}, nil)
}

func (h *Handler) ListSlambookModerationQueue(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	items, err := h.svc.ListSlambookModerationQueue(c.Request.Context(), slambookID, userID)
	if err != nil {
		writeSlambookError(c, "LIST_SLAMBOOK_MODERATION_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"items": items}, nil)
}

func (h *Handler) ModerateSlambookSession(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	sessionID, err := parseUUIDParam(c, "sessionId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SESSION_ID", "invalid session id", nil)
		return
	}
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		Action string `json:"action"`
		Reason string `json:"reason"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}

	if err := h.svc.ModerateSlambookSession(c.Request.Context(), slambookID, sessionID, userID, body.Action, body.Reason); err != nil {
		writeSlambookError(c, "MODERATE_SLAMBOOK_SESSION_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) SetSlambookOpinionPinned(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	itemID, err := parseUUIDParam(c, "itemId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ITEM_ID", "invalid item id", nil)
		return
	}
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		Pinned *bool `json:"pinned"`
	}
	if c.Request.ContentLength != 0 {
		if err := c.ShouldBindJSON(&body); err != nil {
			api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
			return
		}
	}
	pinned := true
	if body.Pinned != nil {
		pinned = *body.Pinned
	}

	if err := h.svc.PinSlambookOpinionItem(c.Request.Context(), slambookID, itemID, userID, pinned); err != nil {
		writeSlambookError(c, "SET_SLAMBOOK_OPINION_PIN_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]any{"pinned": pinned}, nil)
}

func (h *Handler) ReorderSlambookOpinionItems(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	var body struct {
		ItemIDs []string `json:"item_ids"`
	}
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_BODY", err.Error(), nil)
		return
	}
	itemIDs, err := parseUUIDStrings(body.ItemIDs)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_ITEM_IDS", err.Error(), nil)
		return
	}

	if err := h.svc.ReorderSlambookOpinionItems(c.Request.Context(), slambookID, userID, itemIDs); err != nil {
		writeSlambookError(c, "REORDER_SLAMBOOK_OPINION_ITEMS_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "updated"}, nil)
}

func (h *Handler) ArchiveSlambook(c *gin.Context) {
	slambookID, err := parseUUIDParam(c, "slambookId")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SLAMBOOK_ID", "invalid slambook id", nil)
		return
	}
	userID, ok := parseUserID(c)
	if !ok {
		return
	}

	if err := h.svc.ArchiveSlambook(c.Request.Context(), slambookID, userID); err != nil {
		writeSlambookError(c, "ARCHIVE_SLAMBOOK_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, map[string]string{"status": "archived"}, nil)
}

func (h *Handler) GetSlambookByShareToken(c *gin.Context) {
	token, err := parseUUIDParam(c, "token")
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_SHARE_TOKEN", "invalid share token", nil)
		return
	}
	viewerUserID, ok := parseOptionalUserID(c)
	if !ok {
		return
	}

	detail, err := h.svc.GetSlambookByShareToken(c.Request.Context(), token, viewerUserID)
	if err != nil {
		writeSlambookError(c, "GET_SLAMBOOK_SHARE_FAILED", err)
		return
	}
	api.JSON(c.Writer, http.StatusOK, detail, nil)
}

func parseOptionalUserID(c *gin.Context) (*uuid.UUID, bool) {
	userID := strings.TrimSpace(c.GetHeader("X-User-Id"))
	if userID == "" {
		return nil, true
	}
	uid, err := uuid.Parse(userID)
	if err != nil {
		api.ErrorWithContext(c.Request.Context(), c.Writer, http.StatusBadRequest, "INVALID_USER_ID", "invalid user id", nil)
		return nil, false
	}
	return &uid, true
}

func parseUUIDParam(c *gin.Context, key string) (uuid.UUID, error) {
	return parseUUIDString(c.Param(key))
}

func parseUUIDStrings(values []string) ([]uuid.UUID, error) {
	parsed := make([]uuid.UUID, 0, len(values))
	for _, value := range values {
		id, err := parseUUIDString(value)
		if err != nil {
			return nil, err
		}
		parsed = append(parsed, id)
	}
	return parsed, nil
}

func parseUUIDString(value string) (uuid.UUID, error) {
	return uuid.Parse(strings.TrimSpace(value))
}

func writeSlambookError(c *gin.Context, code string, err error) {
	status := http.StatusBadRequest
	switch {
	case errors.Is(err, pgx.ErrNoRows), strings.Contains(err.Error(), "no rows"):
		status = http.StatusNotFound
	case strings.Contains(err.Error(), "not authorized"), strings.Contains(err.Error(), "not allowed"):
		status = http.StatusForbidden
	}
	api.ErrorWithContext(c.Request.Context(), c.Writer, status, code, err.Error(), nil)
}
