package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// AccessScopeAnonymous marks an asset attached to an anonymous group post:
// its record is its uploader's alone and its bytes are streamed, never
// redirected to a URL that names the uploader (migration 018).
const AccessScopeAnonymous = "anonymous"

// ErrScopeConflict is returned when an asset already carries a different,
// incompatible scope (a dating photo is never also a group attachment).
var ErrScopeConflict = fmt.Errorf("media access scope conflict")

// MarkAnonymous puts the asset into the anonymous scope. Idempotent; a
// missing asset is (false, nil).
func (s *MediaAssetStore) MarkAnonymous(ctx context.Context, id uuid.UUID) (bool, error) {
	var current *string
	err := s.db.QueryRow(ctx, `SELECT access_scope FROM media_assets WHERE id = $1`, id).Scan(&current)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if current != nil && *current != "" && *current != AccessScopeAnonymous {
		return false, ErrScopeConflict
	}
	if current != nil && *current == AccessScopeAnonymous {
		return true, nil
	}
	tag, err := s.db.Exec(ctx,
		`UPDATE media_assets SET access_scope = $2, updated_at = NOW() WHERE id = $1`,
		id, AccessScopeAnonymous)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}
