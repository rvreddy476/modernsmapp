package postgres

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pulse dating clips (migration 026): a ≤30 s voice or video answer to a
// dating profile prompt.

// AccessScopeDatingClip marks an audio or video asset dating-service has
// attached to a prompt answer. Like a dating photo, the public read routes
// refuse it to anyone but its uploader; other viewers reach it only through
// a short-lived URL dating-service requests after its own audience decision.
const AccessScopeDatingClip = "dating_clip"

// IsDatingScope reports whether scope is one of the uploader-only dating
// scopes (a dating photo or a dating clip).
func IsDatingScope(scope string) bool {
	return scope == AccessScopeDatingPhoto || scope == AccessScopeDatingClip
}

// MarkDatingClip puts the uploader's asset into the dating_clip scope.
// Idempotent. A missing asset, or one uploaded by someone else, is
// pgx.ErrNoRows; an asset already carrying another scope (anonymous, a
// dating photo) is ErrScopeConflict and is left unchanged.
func (s *MediaAssetStore) MarkDatingClip(ctx context.Context, id, uploaderID uuid.UUID) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE media_assets
		SET access_scope = 'dating_clip', updated_at = NOW()
		WHERE id = $1 AND uploader_id = $2
		  AND (access_scope IS NULL OR access_scope = '' OR access_scope = 'dating_clip')`,
		id, uploaderID)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	var current *string
	err = s.db.QueryRow(ctx,
		`SELECT access_scope FROM media_assets WHERE id = $1 AND uploader_id = $2`, id, uploaderID).Scan(&current)
	if errors.Is(err, pgx.ErrNoRows) {
		return pgx.ErrNoRows
	}
	if err != nil {
		return err
	}
	return ErrScopeConflict
}
