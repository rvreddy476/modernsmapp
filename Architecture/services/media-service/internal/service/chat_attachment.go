package service

import (
	"context"
	"errors"
	"fmt"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

var ErrChatAttachmentDenied = errors.New("chat attachment is not allowed")

// ReserveChatAttachment proves ownership/readiness and creates a media-owned
// reference in the same transaction. Chat never accepts a client URL or
// creates a second media record.
func (s *Service) ReserveChatAttachment(ctx context.Context, referenceID, uploaderID, mediaID uuid.UUID) error {
	err := s.pgStore.ReserveChatAttachment(ctx, referenceID, uploaderID, mediaID)
	if errors.Is(err, postgres.ErrChatAttachmentReservationDenied) {
		return ErrChatAttachmentDenied
	}
	if err != nil {
		return fmt.Errorf("reserve chat attachment: %w", err)
	}
	return nil
}
