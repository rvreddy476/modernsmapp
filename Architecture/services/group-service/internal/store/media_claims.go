package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/atpost/group-service/internal/claimsync"
	"github.com/atpost/group-service/internal/mediaclaims"
	"github.com/atpost/group-service/internal/mediaclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Media claim support (internal/mediaclaims, internal/claimsync).
//
// The holders: groups.avatar_media_id and cover_media_id (ref = group id),
// group_events.cover_media_id (ref = event id) and group_posts.attachments
// (ref = group post id). A group is dead once deleted (archived groups are
// still shown); an event once cancelled, or once its group is deleted; a post
// once deleted or rejected, or once its group is deleted (a pending post is
// live: approving it shows it).

// LockMediaHolder serialises every write that changes which media one row
// holds, for as long as the caller holds it. The claim protocol claims,
// writes and releases in three steps; without this, two saves of the same
// row that each read it first can interleave so that one's release removes a
// claim the other's write relies on. Transaction-scoped, so a crash or a lost
// connection frees it. unlock is idempotent.
//
// Callers pass the in-process holderGate before taking a connection, so lock
// holders can never exhaust the pool (media_holder_gate.go).
func (s *Store) LockMediaHolder(ctx context.Context, ref uuid.UUID) (unlock func(), err error) {
	return lockMediaHolder(ctx, s.holders, s.db, mediaHolderLockKey(ref))
}

// lockMediaHolderSQL waits (never try_) for an exclusive, transaction-scoped
// lock. Pinned by TestMediaHolderLockWaitsAndIsTransactionScoped; proven live
// by TestMediaClaimsIT_HolderLockSerialises (CI).
const lockMediaHolderSQL = `SELECT pg_advisory_xact_lock($1)`

// mediaHolderLockKey namespaces the advisory lock so it cannot collide with
// another lock taken on the same id for a different purpose.
func mediaHolderLockKey(ref uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("group-media-holder:"))
	_, _ = h.Write(ref[:])
	return int64(h.Sum64())
}

// ListEventIDsWithCover returns every event of the group that names a cover,
// cancelled ones included: releasing is idempotent, and a cancel whose
// release failed is exactly the one to retry.
func (s *Store) ListEventIDsWithCover(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id FROM group_events WHERE group_id = $1 AND cover_media_id IS NOT NULL ORDER BY id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GroupMediaClaimRows pages through every group with an avatar or a cover,
// deleted ones included, in id order after `after`; it returns the last group
// id read (the next cursor), since one group yields up to two rows.
func (s *Store) GroupMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]claimsync.Row, uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, avatar_media_id, cover_media_id, status <> 'deleted'
		FROM groups
		WHERE (avatar_media_id IS NOT NULL OR cover_media_id IS NOT NULL) AND id > $1
		ORDER BY id
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []claimsync.Row
	last := after
	for rows.Next() {
		var id uuid.UUID
		var avatar, cover *uuid.UUID
		var live bool
		if err := rows.Scan(&id, &avatar, &cover, &live); err != nil {
			return nil, after, err
		}
		last = id
		if avatar != nil {
			out = append(out, claimsync.Row{Kind: mediaclient.KindGroupAvatar, Ref: id, MediaIDs: []uuid.UUID{*avatar}, Live: live})
		}
		if cover != nil {
			out = append(out, claimsync.Row{Kind: mediaclient.KindGroupCover, Ref: id, MediaIDs: []uuid.UUID{*cover}, Live: live})
		}
	}
	return out, last, rows.Err()
}

// eventLiveSQL is when an event's cover counts as shown: the event is not
// cancelled and its group exists and is not deleted. group_events.group_id
// has no foreign key, so a missing group is possible and is dead.
const eventLiveSQL = `(COALESCE(e.status, '') <> 'cancelled' AND COALESCE(g.status <> 'deleted', false))`

// GroupEventMediaClaimRows pages through every event with a cover, cancelled
// ones included, in id order after `after`.
func (s *Store) GroupEventMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]claimsync.Row, uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT e.id, e.cover_media_id, `+eventLiveSQL+`
		FROM group_events e LEFT JOIN groups g ON g.id = e.group_id
		WHERE e.cover_media_id IS NOT NULL AND e.id > $1
		ORDER BY e.id
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []claimsync.Row
	last := after
	for rows.Next() {
		r := claimsync.Row{Kind: mediaclient.KindGroupEventCover}
		var cover uuid.UUID
		if err := rows.Scan(&r.Ref, &cover, &r.Live); err != nil {
			return nil, after, err
		}
		r.MediaIDs = []uuid.UUID{cover}
		last = r.Ref
		out = append(out, r)
	}
	return out, last, rows.Err()
}

// MediaClaimRowFor re-reads one holder row, for the reconcile tool, under
// LockMediaHolder. A row that no longer exists is not live and holds nothing.
func (s *Store) MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (claimsync.Row, error) {
	row := claimsync.Row{Kind: kind, Ref: ref}
	var id *uuid.UUID
	var err error
	switch kind {
	case mediaclient.KindGroupAvatar, mediaclient.KindGroupCover:
		col := "avatar_media_id"
		if kind == mediaclient.KindGroupCover {
			col = "cover_media_id"
		}
		err = s.db.QueryRow(ctx, `SELECT `+col+`, status <> 'deleted' FROM groups WHERE id = $1`, ref).
			Scan(&id, &row.Live)
	case mediaclient.KindGroupEventCover:
		err = s.db.QueryRow(ctx, `
			SELECT e.cover_media_id, `+eventLiveSQL+`
			FROM group_events e LEFT JOIN groups g ON g.id = e.group_id
			WHERE e.id = $1`, ref).Scan(&id, &row.Live)
	case mediaclient.KindGroupPostAttachment:
		var raw []byte
		err = s.db.QueryRow(ctx, `
			SELECT p.attachments, `+postLiveSQL+`
			FROM group_posts p LEFT JOIN groups g ON g.id = p.group_id
			WHERE p.id = $1`, ref).Scan(&raw, &row.Live)
		if errors.Is(err, pgx.ErrNoRows) {
			return claimsync.Row{Kind: kind, Ref: ref}, nil
		}
		row.MediaIDs = mediaclaims.StoredAttachmentIDs(raw)
		return row, err
	default:
		return row, fmt.Errorf("unknown media holder kind %q", kind)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return claimsync.Row{Kind: kind, Ref: ref}, nil
	}
	if id != nil {
		row.MediaIDs = []uuid.UUID{*id}
	}
	return row, err
}

// postLiveSQL is when a group post's attachments count as shown: the post is
// neither deleted nor rejected (a pending post is shown once approved), and
// its group exists and is not deleted. An unknown status counts as live: an
// extra claim is repaired by a later pass, a missing one is not noticed.
const postLiveSQL = `(COALESCE(p.status, 'published') NOT IN ('deleted', 'rejected') AND COALESCE(g.status <> 'deleted', false))`

// hasAttachmentsSQL is true when group_posts.attachments is a non-empty
// list. jsonb_array_length errors on a non-array, so the type is checked in
// a CASE (SQL does not promise AND short-circuits).
const hasAttachmentsSQL = `(CASE WHEN jsonb_typeof(attachments) = 'array' THEN jsonb_array_length(attachments) > 0 ELSE false END)`

// PostIDsWithAttachments returns every post of the group that names
// attachments, deleted and rejected ones included: releasing is idempotent,
// and a delete whose release failed is exactly the one to retry.
func (s *Store) PostIDsWithAttachments(ctx context.Context, groupID uuid.UUID) ([]uuid.UUID, error) {
	rows, err := s.db.Query(ctx,
		`SELECT id FROM group_posts WHERE group_id = $1 AND `+hasAttachmentsSQL+` ORDER BY id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []uuid.UUID
	for rows.Next() {
		var id uuid.UUID
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// GroupPostMediaClaimRows pages through every group post that names
// attachments, dead ones included, in id order after `after`.
func (s *Store) GroupPostMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]claimsync.Row, uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT p.id, p.attachments, `+postLiveSQL+`
		FROM group_posts p LEFT JOIN groups g ON g.id = p.group_id
		WHERE (CASE WHEN jsonb_typeof(p.attachments) = 'array' THEN jsonb_array_length(p.attachments) > 0 ELSE false END)
		  AND p.id > $1
		ORDER BY p.id
		LIMIT $2`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []claimsync.Row
	last := after
	for rows.Next() {
		r := claimsync.Row{Kind: mediaclient.KindGroupPostAttachment}
		var raw []byte
		if err := rows.Scan(&r.Ref, &raw, &r.Live); err != nil {
			return nil, after, err
		}
		r.MediaIDs = mediaclaims.StoredAttachmentIDs(raw)
		last = r.Ref
		out = append(out, r)
	}
	return out, last, rows.Err()
}
