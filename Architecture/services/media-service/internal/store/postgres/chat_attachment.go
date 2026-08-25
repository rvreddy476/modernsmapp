package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrChatAttachmentReservationDenied = errors.New("chat attachment reservation denied")

// ReserveChatAttachment atomically validates and pins one canonical asset for
// a chat send. The foreign key takes a key-share lock on media_assets; a
// concurrent physical delete either wins before this insert (and the insert
// fails closed) or waits and is refused by the committed reference.
func (s *MediaAssetStore) ReserveChatAttachment(ctx context.Context, referenceID, uploaderID, mediaID uuid.UUID) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	_, err = tx.Exec(ctx, `
		INSERT INTO media_chat_attachment_reservations (reference_id, media_id, uploader_id)
		SELECT $1, id, uploader_id
		FROM media_assets
		WHERE id=$2 AND uploader_id=$3
		  AND processing_status='ready' AND moderation_status='passed'
		ON CONFLICT (reference_id) DO NOTHING
	`, referenceID, mediaID, uploaderID)
	if err != nil {
		return fmt.Errorf("reserve chat attachment: %w", err)
	}

	var reservedMediaID, reservedUploaderID uuid.UUID
	err = tx.QueryRow(ctx, `
		SELECT r.media_id, r.uploader_id
		FROM media_chat_attachment_reservations r
		JOIN media_assets a ON a.id=r.media_id
		WHERE r.reference_id=$1
		  AND a.processing_status='ready' AND a.moderation_status='passed'
	`, referenceID).Scan(&reservedMediaID, &reservedUploaderID)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrChatAttachmentReservationDenied
	}
	if err != nil {
		return fmt.Errorf("read chat attachment reservation: %w", err)
	}
	if reservedMediaID != mediaID || reservedUploaderID != uploaderID {
		return ErrChatAttachmentReservationDenied
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit chat attachment reservation: %w", err)
	}
	return nil
}
