package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
)

// ErrDocumentNotConfirmable: MarkDocumentReady found no confirmed document
// row to move to ready (not a document, or past the confirm step).
var ErrDocumentNotConfirmable = errors.New("document is not awaiting readiness")

// MarkDocumentReady finishes a private document's confirm (service/document.go):
// ready and released in one statement, with no renditions, scan or URL
// columns written. The WHERE names the file type, so an image or video row can
// never be moved to ready through this path, and the step after
// MarkUploadConfirmed only ('uploaded'), so a rejected or failed row is never
// revived.
//
// moderation_status 'passed' means released for the document's only use: a
// commerce reviewer reading it through the token-gated document-bytes route.
// No viewer route serves a document whatever this column says.
func (s *MediaAssetStore) MarkDocumentReady(ctx context.Context, id uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_assets
		   SET processing_status = 'ready',
		       moderation_status = 'passed',
		       updated_at        = NOW()
		 WHERE id = $1
		   AND file_type = 'document'
		   AND processing_status = 'uploaded'
		   AND storage_key NOT LIKE 'public/%'`, id)
	if err != nil {
		return fmt.Errorf("mark document %s ready: %w", id, err)
	}
	if tag.RowsAffected() != 1 {
		return fmt.Errorf("mark document %s ready: %w", id, ErrDocumentNotConfirmable)
	}
	return nil
}
