package postgres

import (
	"context"
	"fmt"

	"github.com/google/uuid"
)

// Ordered carousel reads. Creator Studio P0-A, errata E-2.1.
//
// # WHY THIS FILE EXISTS
//
// Before this, eight separate call sites each ran their own `post_media` query
// and appended rows in whatever order PostgreSQL happened to return them. That
// is fine for one attachment and silently wrong for a carousel: the ordinal a
// creator chose is a product promise, and physical row order is not a contract.
//
// Ordering AND ordinal normalization now live in exactly one place, so a new
// read surface cannot quietly opt out of either.
//
// # THE PHASE-A NULL
//
// Migration 036 adds `post_media.position` as NULLABLE on purpose, so that old
// pods still writing `(post_id, media_id, kind)` keep working during a rolling
// deploy. That means a row read here may legitimately have no ordinal yet, so
// the column is scanned into *int and never into int. Scanning a NULL into int
// is a runtime error, and it would be an error on the ordinary upgrade path.
//
// After release B (037) `position` is NOT NULL, and the fallback branch in
// normalizePostMediaPositions is deleted rather than left dormant. See
// database/migrations_release_b/README.md.

// postMediaOrder is the single-post ordering.
//
// `NULLS LAST, pm.media_id` is the phase-A tiebreak: it makes an un-backfilled
// slice deterministic instead of arbitrary, which is what lets the all-absent
// branch below emit stable slice indices.
const postMediaOrder = ` ORDER BY pm.position NULLS LAST, pm.media_id`

// postMediaBatchOrder additionally groups by post so each accumulated per-post
// slice is contiguous and internally ordered.
const postMediaBatchOrder = ` ORDER BY pm.post_id, pm.position NULLS LAST, pm.media_id`

// scannedMedia is one row before normalization, with the ordinal still nullable.
type scannedMedia struct {
	media    PostMedia
	position *int
}

// normalizePostMediaPositions turns a deterministically ordered slice into one
// with an ordinal on every item, or fails closed.
//
// Three cases, and only three:
//
//   - ALL PRESENT: the stored ordinals are authoritative, after proving they are
//     unique and contiguous 0..N-1. A gap or a duplicate means the create-time
//     invariant was violated, and returning a "best effort" carousel would hide
//     the corruption from the only code positioned to notice it.
//   - ALL ABSENT: a legacy or old-writer slice. The deterministic sort above
//     already fixed the order, so the slice index IS the ordinal.
//   - MIXED: fail closed. No legitimate writer produces this — a create
//     transaction writes every ordinal or none — so a mixed slice means either
//     data corruption or a writer nobody has accounted for. Guessing would
//     publish a silently reordered carousel.
func normalizePostMediaPositions(postID uuid.UUID, rows []scannedMedia) ([]PostMedia, error) {
	if len(rows) == 0 {
		return nil, nil
	}

	present := 0
	for _, r := range rows {
		if r.position != nil {
			present++
		}
	}

	switch present {
	case 0:
		// Legacy slice: the ordinal is the position in the deterministic order.
		out := make([]PostMedia, 0, len(rows))
		for i, r := range rows {
			m := r.media
			m.Position = i
			out = append(out, m)
		}
		return out, nil

	case len(rows):
		seen := make(map[int]struct{}, len(rows))
		out := make([]PostMedia, 0, len(rows))
		for _, r := range rows {
			p := *r.position
			if p < 0 || p >= len(rows) {
				return nil, fmt.Errorf(
					"post_media: post %s ordinal %d out of range 0..%d", postID, p, len(rows)-1)
			}
			if _, dup := seen[p]; dup {
				return nil, fmt.Errorf("post_media: post %s has duplicate ordinal %d", postID, p)
			}
			seen[p] = struct{}{}
			m := r.media
			m.Position = p
			out = append(out, m)
		}
		return out, nil

	default:
		return nil, fmt.Errorf(
			"post_media: post %s has %d of %d ordinals set; mixed presence is not a legitimate writer state",
			postID, present, len(rows))
	}
}

// loadPostMediaForPost reads one post's ordered, normalized attachments.
func (s *Store) loadPostMediaForPost(ctx context.Context, postID uuid.UUID) ([]PostMedia, error) {
	rows, err := s.db.Query(ctx,
		`SELECT `+postMediaColumns+` `+postMediaSource+` WHERE pm.post_id = $1`+postMediaOrder, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var scanned []scannedMedia
	for rows.Next() {
		var m PostMedia
		var pos *int
		if err := rows.Scan(&m.MediaID, &m.Kind, &m.AltText, &m.AltDecorative, &pos); err != nil {
			return nil, err
		}
		scanned = append(scanned, scannedMedia{media: m, position: pos})
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	return normalizePostMediaPositions(postID, scanned)
}

// attachPostMedia fills Media on every post in the batch, ordered and normalized.
//
// This replaces seven byte-identical inline blocks that each swallowed their
// query error with `if err == nil`. They are now one call that RETURNS the
// error, because a mixed-ordinal slice must fail closed rather than render a
// reordered carousel — the whole point of E-2.1.
func (s *Store) attachPostMedia(ctx context.Context, posts []Post) error {
	if len(posts) == 0 {
		return nil
	}

	postIDs := make([]uuid.UUID, len(posts))
	for i, p := range posts {
		postIDs[i] = p.ID
	}

	rows, err := s.db.Query(ctx, `
		SELECT pm.post_id, `+postMediaColumns+`
		`+postMediaSource+` WHERE pm.post_id = ANY($1)`+postMediaBatchOrder, postIDs)
	if err != nil {
		return err
	}
	defer rows.Close()

	scannedByPost := make(map[uuid.UUID][]scannedMedia, len(posts))
	for rows.Next() {
		var postID uuid.UUID
		var m PostMedia
		var pos *int
		if err := rows.Scan(&postID, &m.MediaID, &m.Kind, &m.AltText, &m.AltDecorative, &pos); err != nil {
			return err
		}
		scannedByPost[postID] = append(scannedByPost[postID], scannedMedia{media: m, position: pos})
	}
	if err := rows.Err(); err != nil {
		return err
	}

	for i := range posts {
		normalized, err := normalizePostMediaPositions(posts[i].ID, scannedByPost[posts[i].ID])
		if err != nil {
			return err
		}
		posts[i].Media = normalized
	}
	return nil
}
