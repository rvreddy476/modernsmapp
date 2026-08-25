package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/google/uuid"
)

var ErrMediaNotAllowed = errors.New("media attachment is not owned, ready, and approved")

func (s *Service) reserveChatAttachment(ctx context.Context, referenceID, uploaderID, mediaID uuid.UUID) error {
	if s.mediaServiceURL == "" || s.internalServiceKey == "" {
		return fmt.Errorf("%w: media authority is not configured", ErrMediaNotAllowed)
	}
	body, err := json.Marshal(map[string]string{
		"reference_id": referenceID.String(),
		"uploader_id":  uploaderID.String(),
		"media_id":     mediaID.String(),
	})
	if err != nil {
		return err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.mediaServiceURL+"/v1/media/internal/chat-attachment/reserve", bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Internal-Service-Key", s.internalServiceKey)
	response, err := s.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("media authority unavailable: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNoContent {
		return nil
	}
	if response.StatusCode == http.StatusForbidden || response.StatusCode == http.StatusNotFound {
		return ErrMediaNotAllowed
	}
	return fmt.Errorf("media authority returned %d", response.StatusCode)
}
