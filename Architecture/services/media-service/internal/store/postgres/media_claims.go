package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// ── Cross-service media claims (migration 031) ─────────────────────────────
//
// Other services keep media ids in their own tables with no foreign key into
// media_assets (channel_updates.media_ids, broadcast_channels.avatar_media_id,
// ...). Nothing in this database can see those rows, and even in a shared
// database a plain UUID write takes no lock on the media row, so a reference
// check inside a delete transaction cannot see a concurrent attach coming
// (C-CLB-1). The owner's DELETE /v1/media/{id} therefore left them dangling.
//
// A claim is this service's own record of such a reference. The protocol:
//
//  1. the claimant CLAIMS (ClaimMedia) before it writes its row;
//  2. it writes the row; if that write fails it RELEASES what it just claimed;
//  3. when the row stops referencing the asset (edit, delete) it releases.
//
// ClaimMedia locks every media row FOR KEY SHARE and inserts the claim, whose
// RESTRICT foreign key holds that lock until commit. DeleteMedia locks the
// row FOR UPDATE, which conflicts with KEY SHARE, and then reads the claims.
// So an attach racing a delete ends one of two ways, never a third:
//
//   - the claim commits first: the delete waits on the row, then sees the
//     claim (READ COMMITTED re-reads after the lock) and refuses;
//   - the delete locks first: the claim waits, then finds no row and is
//     refused with ErrMediaNotFound, so the claimant never writes its row.
//
// A failure between the claimant's steps leaves an extra claim, never a
// missing one: the asset is retained a little longer, never deleted under a
// live reference. The claimant's reconcile tool releases stale claims.

// The two row locks the protocol rests on. FOR UPDATE conflicts with FOR KEY
// SHARE, so a claim and a physical delete of the same asset cannot both pass
// their checks. KEY SHARE (not SHARE) so a claim does not stall the
// transcoder's status updates on the same row. Pinned by
// TestClaimAndDeleteLocksConflict; proven live by
// TestOwnerDeleteAndClaimRaceNeverDangle (CI).
const (
	lockMediaForClaimSQL  = `SELECT processing_status FROM media_assets WHERE id = $1 FOR KEY SHARE`
	lockMediaForDeleteSQL = `SELECT storage_key FROM media_assets WHERE id = $1 FOR UPDATE`
)

// MediaClaim names one holder of a reference: which service, what kind of row
// and which row. Together with a media id it is the claim's identity, so
// writing it twice is a no-op.
type MediaClaim struct {
	Claimant string
	Kind     string
	Ref      string
}

// ErrMediaInUse is the owner delete's refusal: a claim exists. errors.As a
// *MediaInUseError for who holds it.
var ErrMediaInUse = errors.New("media is in use")

// ClaimHolder is one (claimant, kind) group holding an asset, counted. The
// holding row ids are deliberately not part of it: the owner is told WHERE the
// asset is used, not which other rows in another service exist.
type ClaimHolder struct {
	Claimant string `json:"claimant"`
	Kind     string `json:"kind"`
	Count    int    `json:"count"`
}

// MediaInUseError carries the holders that refused a delete.
type MediaInUseError struct {
	Holders []ClaimHolder
}

func (e *MediaInUseError) Error() string {
	parts := make([]string, 0, len(e.Holders))
	for _, h := range e.Holders {
		parts = append(parts, fmt.Sprintf("%s/%s x%d", h.Claimant, h.Kind, h.Count))
	}
	return "media is in use: " + strings.Join(parts, ", ")
}

func (e *MediaInUseError) Unwrap() error { return ErrMediaInUse }

// ClaimMedia records claim c on every id, all or nothing.
//
// Each media row is locked FOR KEY SHARE first, in id order, and an asset that
// is gone refuses the whole call with ErrMediaNotFound: the claimant must not
// write a reference to it. Whether the asset may be attached at all (owner,
// upload finished, moderation) is the claimant's decision, made before it
// claims; media-service records the reference, it does not authorise it.
// Idempotent: an existing claim is left as it is.
func (s *MediaAssetStore) ClaimMedia(ctx context.Context, c MediaClaim, mediaIDs []uuid.UUID) error {
	ids := uniqueSortedIDs(mediaIDs)
	if len(ids) == 0 {
		return nil
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("claim media: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after Commit

	for _, id := range ids {
		var status string
		err := tx.QueryRow(ctx, lockMediaForClaimSQL, id).Scan(&status)
		if errors.Is(err, pgx.ErrNoRows) {
			return fmt.Errorf("%w: %s", ErrMediaNotFound, id)
		}
		if err != nil {
			return fmt.Errorf("claim media: lock %s: %w", id, err)
		}
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO media_claims (media_id, claimant, claim_kind, claim_ref)
		SELECT id, $2, $3, $4 FROM unnest($1::uuid[]) AS t(id)
		ON CONFLICT (media_id, claimant, claim_kind, claim_ref) DO NOTHING`,
		ids, c.Claimant, c.Kind, c.Ref); err != nil {
		if isForeignKeyViolation(err) {
			return fmt.Errorf("%w: removed while claiming", ErrMediaNotFound)
		}
		return fmt.Errorf("claim media: insert: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("claim media: commit: %w", err)
	}
	return nil
}

// ReleaseMediaClaims removes holder c's claims on mediaIDs, or every claim c
// holds when mediaIDs is empty. Releasing a claim that does not exist is not an
// error, so a retried release is safe. Returns the number removed.
func (s *MediaAssetStore) ReleaseMediaClaims(ctx context.Context, c MediaClaim, mediaIDs []uuid.UUID) (int64, error) {
	var (
		tag pgconn.CommandTag
		err error
	)
	if len(mediaIDs) == 0 {
		tag, err = s.db.Exec(ctx, `
			DELETE FROM media_claims
			WHERE claimant = $1 AND claim_kind = $2 AND claim_ref = $3`,
			c.Claimant, c.Kind, c.Ref)
	} else {
		tag, err = s.db.Exec(ctx, `
			DELETE FROM media_claims
			WHERE claimant = $1 AND claim_kind = $2 AND claim_ref = $3
			  AND media_id = ANY($4::uuid[])`,
			c.Claimant, c.Kind, c.Ref, uniqueSortedIDs(mediaIDs))
	}
	if err != nil {
		return 0, fmt.Errorf("release media claims: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ReleaseMediaClaimsExcept removes every claim holder c has EXCEPT those on
// keep: "this row now holds exactly keep". The reconcile path, for a release
// that was lost after an edit. Claim keep first, then call this: the other
// order would leave a moment where the row names an unclaimed asset.
func (s *MediaAssetStore) ReleaseMediaClaimsExcept(ctx context.Context, c MediaClaim, keep []uuid.UUID) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM media_claims
		WHERE claimant = $1 AND claim_kind = $2 AND claim_ref = $3
		  AND NOT (media_id = ANY($4::uuid[]))`,
		c.Claimant, c.Kind, c.Ref, uniqueSortedIDs(keep))
	if err != nil {
		return 0, fmt.Errorf("release media claims except: %w", err)
	}
	return tag.RowsAffected(), nil
}

// mediaClaimHoldersTx reads who holds mediaID, inside the caller's
// transaction (which must already hold the row FOR UPDATE).
func mediaClaimHoldersTx(ctx context.Context, tx pgx.Tx, mediaID uuid.UUID) ([]ClaimHolder, error) {
	rows, err := tx.Query(ctx, `
		SELECT claimant, claim_kind, count(*)::int
		FROM media_claims WHERE media_id = $1
		GROUP BY claimant, claim_kind
		ORDER BY claimant, claim_kind`, mediaID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ClaimHolder
	for rows.Next() {
		var h ClaimHolder
		if err := rows.Scan(&h.Claimant, &h.Kind, &h.Count); err != nil {
			return nil, err
		}
		out = append(out, h)
	}
	return out, rows.Err()
}

// refuseClaimedDelete is the owner-delete decision on the holders read under
// the row lock. Pure, so the rule is tested without a database.
func refuseClaimedDelete(holders []ClaimHolder) error {
	if len(holders) > 0 {
		return &MediaInUseError{Holders: holders}
	}
	return nil
}

func uniqueSortedIDs(ids []uuid.UUID) []uuid.UUID {
	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out
}
