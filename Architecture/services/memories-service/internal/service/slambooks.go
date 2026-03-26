package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/atpost/memories-service/internal/store/postgres"
	"github.com/google/uuid"
)

type CreateSlambookCardInput struct {
	Title           string
	Prompt          string
	ResponseType    string
	PlaceholderText string
	HelpText        string
	IsRequired      bool
}

type CreateSlambookInput struct {
	OwnerUserID          uuid.UUID
	Title                string
	Subtitle             string
	Description          string
	Category             string
	ThemeKey             string
	Visibility           string
	ResponseIdentityMode string
	ApprovalRequired     bool
	TemplatePackKey      string
	CustomCards          []CreateSlambookCardInput
	ClosesAt             *time.Time
}

type SlambookDetail struct {
	Slambook      *postgres.Slambook                `json:"slambook"`
	Cards         []postgres.SlambookCard           `json:"cards"`
	ViewerSession *postgres.SlambookResponseSession `json:"viewer_session,omitempty"`
}

type RespondToSlambookAnswerInput struct {
	CardID     uuid.UUID
	AnswerText string
	AnswerJSON map[string]any
}

type RespondToSlambookInput struct {
	DisplayName string
	Anonymous   bool
	ShareToken  *uuid.UUID
	Submit      bool
	Answers     []RespondToSlambookAnswerInput
}

func (s *Service) ListSlambookTemplatePacks(ctx context.Context) ([]postgres.SlambookTemplatePack, error) {
	return s.store.ListSlambookTemplatePacks(ctx)
}

func (s *Service) CreateSlambook(ctx context.Context, input *CreateSlambookInput) (*postgres.Slambook, error) {
	title := strings.TrimSpace(input.Title)
	if title == "" {
		return nil, fmt.Errorf("title is required")
	}
	if input.OwnerUserID == uuid.Nil {
		return nil, fmt.Errorf("owner user id is required")
	}
	category := strings.TrimSpace(input.Category)
	if category == "" {
		category = "personal"
	}
	themeKey := strings.TrimSpace(input.ThemeKey)
	if themeKey == "" {
		themeKey = "classic"
	}
	visibility := strings.TrimSpace(input.Visibility)
	if visibility == "" {
		visibility = "invited_only"
	}
	if visibility != "private" && visibility != "invited_only" && visibility != "public" {
		return nil, fmt.Errorf("unsupported visibility for v1: %s", visibility)
	}
	identityMode := strings.TrimSpace(input.ResponseIdentityMode)
	if identityMode == "" {
		identityMode = "named"
	}
	switch identityMode {
	case "named", "anonymous_allowed", "anonymous_owner_only", "fully_anonymous":
	default:
		return nil, fmt.Errorf("invalid response identity mode")
	}
	approvalRequired := input.ApprovalRequired
	if identityMode != "named" {
		approvalRequired = true
	}
	if strings.TrimSpace(input.TemplatePackKey) == "" && len(input.CustomCards) == 0 {
		return nil, fmt.Errorf("choose a template pack or add at least one custom card")
	}

	customCards := make([]postgres.SlambookCard, 0, len(input.CustomCards))
	for _, rawCard := range input.CustomCards {
		prompt := strings.TrimSpace(rawCard.Prompt)
		if prompt == "" {
			continue
		}
		title := strings.TrimSpace(rawCard.Title)
		if title == "" {
			title = prompt
		}
		responseType := strings.TrimSpace(rawCard.ResponseType)
		if responseType == "" {
			responseType = "text"
		}
		if responseType != "text" && responseType != "long_text" {
			return nil, fmt.Errorf("custom card response type %s is not supported in v1", responseType)
		}
		var placeholder *string
		if v := strings.TrimSpace(rawCard.PlaceholderText); v != "" {
			placeholder = &v
		}
		var helpText *string
		if v := strings.TrimSpace(rawCard.HelpText); v != "" {
			helpText = &v
		}
		customCards = append(customCards, postgres.SlambookCard{
			Title:           title,
			Prompt:          prompt,
			ResponseType:    responseType,
			PlaceholderText: placeholder,
			HelpText:        helpText,
			Config:          map[string]any{},
			IsRequired:      rawCard.IsRequired,
		})
	}

	var subtitle *string
	if v := strings.TrimSpace(input.Subtitle); v != "" {
		subtitle = &v
	}
	var description *string
	if v := strings.TrimSpace(input.Description); v != "" {
		description = &v
	}
	now := time.Now()
	slambook := &postgres.Slambook{
		ID:                   uuid.New(),
		OwnerUserID:          input.OwnerUserID,
		ContextType:          "profile",
		Title:                title,
		Subtitle:             subtitle,
		Description:          description,
		Category:             category,
		ThemeKey:             themeKey,
		Visibility:           visibility,
		ResponseIdentityMode: identityMode,
		ApprovalRequired:     approvalRequired,
		AllowCustomCards:     true,
		AllowReactions:       false,
		AllowComments:        false,
		AllowShareLink:       true,
		MaxResponsesPerUser:  1,
		OpensAt:              now,
		ClosesAt:             input.ClosesAt,
		Status:               "active",
		LastActivityAt:       now,
		CreatedAt:            now,
		UpdatedAt:            now,
	}
	if err := s.store.CreateSlambook(ctx, slambook, strings.TrimSpace(input.TemplatePackKey), customCards); err != nil {
		return nil, err
	}
	return s.store.GetSlambook(ctx, slambook.ID)
}

func (s *Service) ListSlambooks(ctx context.Context, viewerUserID, ownerUserID uuid.UUID) ([]postgres.Slambook, error) {
	var (
		items []postgres.Slambook
		err   error
	)
	if viewerUserID == ownerUserID {
		items, err = s.store.ListOwnedSlambooks(ctx, ownerUserID)
	} else {
		items, err = s.store.ListPublicSlambooksByOwner(ctx, ownerUserID)
	}
	if err != nil {
		return nil, err
	}
	for i := range items {
		items[i].ViewerCanModerate = viewerUserID == items[i].OwnerUserID
		items[i].ViewerCanRespond = viewerUserID != uuid.Nil && viewerUserID != items[i].OwnerUserID && items[i].Status == "active"
	}
	return items, nil
}

func (s *Service) GetSlambookDetail(ctx context.Context, slambookID uuid.UUID, viewerUserID *uuid.UUID) (*SlambookDetail, error) {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return nil, err
	}
	viewerInvite, err := s.lookupViewerInvite(ctx, slambookID, viewerUserID)
	if err != nil {
		return nil, err
	}
	if !canViewSlambook(slambook, viewerUserID, viewerInvite != nil) {
		return nil, fmt.Errorf("not authorized")
	}
	cards, err := s.store.GetSlambookCards(ctx, slambookID)
	if err != nil {
		return nil, err
	}
	detail := &SlambookDetail{
		Slambook: slambook,
		Cards:    cards,
	}
	if viewerUserID != nil && *viewerUserID != uuid.Nil {
		session, err := s.store.GetViewerResponseSession(ctx, slambookID, *viewerUserID)
		if err != nil {
			return nil, err
		}
		detail.ViewerSession = session
		if session != nil {
			detail.Slambook.ViewerSessionID = &session.ID
			detail.Slambook.ViewerResponseStatus = &session.Status
		}
		detail.Slambook.ViewerCanRespond = canRespondToSlambook(slambook, *viewerUserID, viewerInvite != nil)
		detail.Slambook.ViewerCanModerate = *viewerUserID == slambook.OwnerUserID
	}
	return detail, nil
}

func (s *Service) GetSlambookByShareToken(ctx context.Context, token uuid.UUID, viewerUserID *uuid.UUID) (*SlambookDetail, error) {
	invite, err := s.store.GetInviteByToken(ctx, token)
	if err != nil {
		return nil, err
	}
	slambook, err := s.store.GetSlambook(ctx, invite.SlambookID)
	if err != nil {
		return nil, err
	}
	if !slambook.AllowShareLink {
		return nil, fmt.Errorf("share link is disabled")
	}
	cards, err := s.store.GetSlambookCards(ctx, slambook.ID)
	if err != nil {
		return nil, err
	}
	detail := &SlambookDetail{
		Slambook: slambook,
		Cards:    cards,
	}
	slambook.ShareToken = invite.ShareToken
	if viewerUserID != nil && *viewerUserID != uuid.Nil {
		session, err := s.store.GetViewerResponseSession(ctx, slambook.ID, *viewerUserID)
		if err != nil {
			return nil, err
		}
		detail.ViewerSession = session
		if session != nil {
			detail.Slambook.ViewerSessionID = &session.ID
			detail.Slambook.ViewerResponseStatus = &session.Status
		}
		detail.Slambook.ViewerCanRespond = canRespondToSlambook(slambook, *viewerUserID, true)
		detail.Slambook.ViewerCanModerate = *viewerUserID == slambook.OwnerUserID
	}
	return detail, nil
}

func (s *Service) CreateSlambookShareLink(ctx context.Context, slambookID, ownerUserID uuid.UUID) (*postgres.SlambookInvite, error) {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return nil, err
	}
	if slambook.OwnerUserID != ownerUserID {
		return nil, fmt.Errorf("not authorized")
	}
	if !slambook.AllowShareLink {
		return nil, fmt.Errorf("share link is disabled")
	}
	return s.store.EnsureShareLinkInvite(ctx, slambookID, ownerUserID)
}

func (s *Service) CreateSlambookInvites(ctx context.Context, slambookID, ownerUserID uuid.UUID, targetUserIDs []uuid.UUID, message *string) ([]postgres.SlambookInvite, error) {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return nil, err
	}
	if slambook.OwnerUserID != ownerUserID {
		return nil, fmt.Errorf("not authorized")
	}
	return s.store.CreateUserInvites(ctx, slambookID, ownerUserID, targetUserIDs, message)
}

func (s *Service) SaveSlambookResponse(ctx context.Context, slambookID, responderUserID uuid.UUID, input *RespondToSlambookInput) (*postgres.SlambookResponseSession, error) {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return nil, err
	}

	var inviteID *uuid.UUID
	hasInvite := false
	if input.ShareToken != nil {
		invite, err := s.store.GetInviteByToken(ctx, *input.ShareToken)
		if err != nil {
			return nil, err
		}
		if invite.SlambookID != slambookID {
			return nil, fmt.Errorf("share token does not belong to this slambook")
		}
		inviteID = &invite.ID
		hasInvite = true
	} else {
		invite, err := s.store.GetInviteByTargetUser(ctx, slambookID, responderUserID)
		if err != nil {
			return nil, err
		}
		if invite != nil {
			inviteID = &invite.ID
			hasInvite = true
		}
	}
	if !canRespondToSlambook(slambook, responderUserID, hasInvite) {
		return nil, fmt.Errorf("not allowed to respond to this slambook")
	}

	existingSession, err := s.store.GetViewerResponseSession(ctx, slambookID, responderUserID)
	if err != nil {
		return nil, err
	}
	if existingSession != nil && existingSession.Status != "draft" {
		return nil, fmt.Errorf("you have already submitted a response")
	}

	cards, err := s.store.GetSlambookCards(ctx, slambookID)
	if err != nil {
		return nil, err
	}
	cardMap := make(map[uuid.UUID]postgres.SlambookCard, len(cards))
	for _, card := range cards {
		cardMap[card.ID] = card
	}

	answers := make([]postgres.SlambookSubmittedItem, 0, len(input.Answers))
	answered := make(map[uuid.UUID]bool, len(input.Answers))
	for _, answer := range input.Answers {
		card, ok := cardMap[answer.CardID]
		if !ok {
			return nil, fmt.Errorf("card %s does not belong to this slambook", answer.CardID)
		}
		text := strings.TrimSpace(answer.AnswerText)
		if input.Submit && text == "" && len(answer.AnswerJSON) == 0 {
			return nil, fmt.Errorf("answer is required for %s", card.Title)
		}
		var answerText *string
		if text != "" {
			answerText = &text
		}
		answers = append(answers, postgres.SlambookSubmittedItem{
			CardID:       answer.CardID,
			ResponseType: card.ResponseType,
			AnswerText:   answerText,
			AnswerJSON:   answer.AnswerJSON,
		})
		answered[answer.CardID] = true
	}

	if input.Submit {
		for _, card := range cards {
			if card.IsRequired && !answered[card.ID] {
				return nil, fmt.Errorf("required card %s is missing", card.Title)
			}
		}
	}

	identityMode := "named"
	if input.Anonymous && slambook.ResponseIdentityMode != "named" {
		identityMode = slambook.ResponseIdentityMode
	}
	displayName := strings.TrimSpace(input.DisplayName)
	if displayName == "" && identityMode == "named" {
		displayName = "PostBook User"
	}

	return s.store.SaveResponseSession(ctx, slambookID, responderUserID, inviteID, displayName, identityMode, answers, input.Submit, slambook.ApprovalRequired)
}

func (s *Service) ListSlambookModerationQueue(ctx context.Context, slambookID, ownerUserID uuid.UUID) ([]postgres.SlambookResponseSession, error) {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return nil, err
	}
	if slambook.OwnerUserID != ownerUserID {
		return nil, fmt.Errorf("not authorized")
	}
	return s.store.ListPendingSlambookSessions(ctx, slambookID)
}

func (s *Service) ModerateSlambookSession(ctx context.Context, slambookID, sessionID, ownerUserID uuid.UUID, action, reason string) error {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return err
	}
	if slambook.OwnerUserID != ownerUserID {
		return fmt.Errorf("not authorized")
	}
	return s.store.ModerateSlambookSession(ctx, slambookID, sessionID, ownerUserID, action, reason)
}

func (s *Service) ListSlambookOpinionSpace(ctx context.Context, slambookID uuid.UUID, viewerUserID *uuid.UUID) ([]postgres.OpinionSpaceItem, error) {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return nil, err
	}
	viewerInvite, err := s.lookupViewerInvite(ctx, slambookID, viewerUserID)
	if err != nil {
		return nil, err
	}
	if !canViewSlambook(slambook, viewerUserID, viewerInvite != nil) {
		return nil, fmt.Errorf("not authorized")
	}
	return s.store.ListVisibleOpinionSpaceItems(ctx, slambookID)
}

func (s *Service) PinSlambookOpinionItem(ctx context.Context, slambookID, itemID, ownerUserID uuid.UUID, pinned bool) error {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return err
	}
	if slambook.OwnerUserID != ownerUserID {
		return fmt.Errorf("not authorized")
	}
	return s.store.SetOpinionSpacePinned(ctx, slambookID, itemID, pinned)
}

func (s *Service) ReorderSlambookOpinionItems(ctx context.Context, slambookID, ownerUserID uuid.UUID, itemIDs []uuid.UUID) error {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return err
	}
	if slambook.OwnerUserID != ownerUserID {
		return fmt.Errorf("not authorized")
	}
	return s.store.ReorderOpinionSpaceItems(ctx, slambookID, itemIDs)
}

func (s *Service) ArchiveSlambook(ctx context.Context, slambookID, ownerUserID uuid.UUID) error {
	slambook, err := s.store.GetSlambook(ctx, slambookID)
	if err != nil {
		return err
	}
	if slambook.OwnerUserID != ownerUserID {
		return fmt.Errorf("not authorized")
	}
	return s.store.ArchiveSlambook(ctx, slambookID, ownerUserID)
}

func (s *Service) lookupViewerInvite(ctx context.Context, slambookID uuid.UUID, viewerUserID *uuid.UUID) (*postgres.SlambookInvite, error) {
	if viewerUserID == nil || *viewerUserID == uuid.Nil {
		return nil, nil
	}
	return s.store.GetInviteByTargetUser(ctx, slambookID, *viewerUserID)
}

func canViewSlambook(slambook *postgres.Slambook, viewerUserID *uuid.UUID, hasInvite bool) bool {
	if slambook == nil {
		return false
	}
	if viewerUserID != nil && *viewerUserID == slambook.OwnerUserID {
		return true
	}
	if slambook.Visibility == "public" {
		return true
	}
	switch slambook.Visibility {
	case "invited_only", "friends_only", "group_members_only", "community_members_only":
		return hasInvite
	default:
		return false
	}
}

func canRespondToSlambook(slambook *postgres.Slambook, responderUserID uuid.UUID, hasInvite bool) bool {
	if slambook == nil || responderUserID == uuid.Nil {
		return false
	}
	if responderUserID == slambook.OwnerUserID {
		return false
	}
	if slambook.Status != "active" {
		return false
	}
	now := time.Now()
	if now.Before(slambook.OpensAt) {
		return false
	}
	if slambook.ClosesAt != nil && now.After(*slambook.ClosesAt) {
		return false
	}
	switch slambook.Visibility {
	case "public":
		return true
	case "invited_only", "friends_only", "group_members_only", "community_members_only":
		return hasInvite
	default:
		return false
	}
}
