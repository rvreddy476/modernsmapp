package service

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/atpost/group-service/internal/store"
	"github.com/google/uuid"
)

func (s *Service) ensureGroupChat(ctx context.Context, group *store.Group) error {
	if group.ChatConversationID != nil {
		return nil
	}
	conversationID, err := s.createGroupChat(ctx, group.CreatorID, group.ID, group.Name)
	if err != nil {
		return fmt.Errorf("provision group chat: %w", err)
	}
	if err := s.store.SetChatConversationID(ctx, group.ID, conversationID); err != nil {
		return fmt.Errorf("persist group chat identity: %w", err)
	}
	group.ChatConversationID = &conversationID
	s.invalidateGroupCache(ctx, group.ID)
	return nil
}

func (s *Service) createGroupChat(ctx context.Context, creatorID uuid.UUID, groupID uuid.UUID, groupName string) (uuid.UUID, error) {
	url := fmt.Sprintf("%s/internal/v1/chat/groups/conversations", s.messageServiceURL)
	body, _ := json.Marshal(map[string]interface{}{
		"title":           groupName,
		"creator_user_id": creatorID.String(),
		"group_id":        groupID.String(),
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return uuid.Nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)

	resp, err := s.chatClient.Do(req)
	if err != nil {
		return uuid.Nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return uuid.Nil, fmt.Errorf("message-service error (status %d): %s", resp.StatusCode, string(respBody))
	}

	var convResp struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&convResp); err != nil {
		return uuid.Nil, err
	}

	return uuid.Parse(convResp.Data.ID)
}

func (s *Service) syncMemberToChat(ctx context.Context, conversationID, userID uuid.UUID, add bool) error {
	var method string
	var url string
	var bodyReader io.Reader
	if add {
		method = http.MethodPost
		url = fmt.Sprintf("%s/internal/v1/chat/groups/conversations/%s/members", s.messageServiceURL, conversationID)
		body, _ := json.Marshal(map[string]string{"user_id": userID.String()})
		bodyReader = bytes.NewReader(body)
	} else {
		method = http.MethodDelete
		url = fmt.Sprintf("%s/internal/v1/chat/groups/conversations/%s/members/%s", s.messageServiceURL, conversationID, userID)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	resp, err := s.chatClient.Do(req)
	if err != nil {
		return fmt.Errorf("sync chat membership: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		responseBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("chat sync returned %d: %s", resp.StatusCode, strings.TrimSpace(string(responseBody)))
	}
	return nil
}
