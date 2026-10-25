package postgres

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Media holders: the rows whose media ids take a media-service claim ──────
//
// internal/mediaclaims explains which columns need a claim (no foreign key)
// and which do not (a real foreign key already refuses the owner delete);
// internal/service/media_claims.go is the protocol. This file is the store
// half: a per-row lock so the claim, the write and the release of one row are
// never interleaved with another writer's, and the re-reads the protocol and
// the reconcile tool (cmd/media-claims) decide on.

// Holder kinds, as media-service registers them for "post-service". They
// duplicate mediaclaims.Kind* (the store does not import the client); the
// service pins them equal (TestHolderKindsMatchClaimKinds).
const (
	HolderPostCover  = "post_cover"
	HolderReelDraft  = "reel_draft"
	HolderEventCover = "event_cover"
)

// MediaHolding is what one holder row holds right now. Live is false when the
// row is gone, or is a reel draft that will never be shown again (deleted or
// published): its claims should be released. A soft-deleted POST is live: it
// can be restored until the purge removes the row.
type MediaHolding struct {
	Kind     string
	Ref      uuid.UUID
	MediaIDs []uuid.UUID
	Live     bool
}

// reelDraftLiveSQL is the reel draft liveness rule, shared by the single-row
// read and the reconcile pager. A published draft hands its media to the post
// (post_media's foreign key, the post's own cover claim); a deleted one is
// never shown. Every other state can still be edited and published.
const reelDraftLiveSQL = `status NOT IN ('deleted', 'published')`

// LockMediaHolder serialises every write that changes which media one holder
// row names, for as long as the caller holds it. The claim protocol claims,
// writes and releases in three steps; without this, two edits of the same row
// can interleave so that one's release removes a claim the other's write
// relies on. Transaction-scoped, so a crash or a lost connection frees it;
// unlock is idempotent.
func (s *Store) LockMediaHolder(ctx context.Context, kind string, ref uuid.UUID) (unlock func(), err error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	if _, err := tx.Exec(ctx, lockMediaHolderSQL, mediaHolderLockKey(kind, ref)); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	done := false
	return func() {
		if !done {
			done = true
			_ = tx.Rollback(context.WithoutCancel(ctx))
		}
	}, nil
}

// lockMediaHolderSQL waits (never try_) for an exclusive, transaction-scoped
// lock. Pinned by TestMediaHolderLockWaitsAndIsTransactionScoped; proven live
// by TestMediaClaimsIT_HolderLockSerialises (CI).
const lockMediaHolderSQL = `SELECT pg_advisory_xact_lock($1)`

// mediaHolderLockKey namespaces the advisory lock by service and kind: a reel
// draft publishes as a post with the SAME id, and the two holders must not
// wait on each other, nor on a lock another service takes on the same id.
func mediaHolderLockKey(kind string, ref uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("post-media-holder:" + kind + ":"))
	_, _ = h.Write(ref[:])
	return int64(h.Sum64()) //nolint:gosec // G115: a hash, any int64 is a valid key
}

// MediaHeldBy re-reads one holder row. A row that does not exist is not live
// and holds nothing.
func (s *Store) MediaHeldBy(ctx context.Context, kind string, ref uuid.UUID) (MediaHolding, error) {
	h := MediaHolding{Kind: kind, Ref: ref}
	var a, b *uuid.UUID
	var err error
	switch kind {
	case HolderPostCover:
		err = s.db.QueryRow(ctx, `SELECT cover_media_id, TRUE FROM posts WHERE id = $1`, ref).Scan(&a, &h.Live)
	case HolderReelDraft:
		err = s.db.QueryRow(ctx, `SELECT media_id, cover_media_id, `+reelDraftLiveSQL+`
			FROM reel_drafts WHERE id = $1`, ref).Scan(&a, &b, &h.Live)
	case HolderEventCover:
		err = s.db.QueryRow(ctx, `SELECT cover_media_id, TRUE FROM events WHERE id = $1`, ref).Scan(&a, &h.Live)
	default:
		return h, fmt.Errorf("unknown media holder kind %q", kind)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return MediaHolding{Kind: kind, Ref: ref}, nil
	}
	if err != nil {
		return h, fmt.Errorf("read media holder %s %s: %w", kind, ref, err)
	}
	h.MediaIDs = holdingIDs(a, b)
	return h, nil
}

// MediaHoldingPage pages through every row of kind that names media, in id
// order after `after`, dead rows included (their claims are released).
func (s *Store) MediaHoldingPage(ctx context.Context, kind string, after uuid.UUID, limit int) ([]MediaHolding, error) {
	var q string
	switch kind {
	case HolderPostCover:
		q = `SELECT id, cover_media_id, NULL::uuid, TRUE FROM posts
			WHERE cover_media_id IS NOT NULL AND id > $1 ORDER BY id LIMIT $2`
	case HolderReelDraft:
		q = `SELECT id, media_id, cover_media_id, ` + reelDraftLiveSQL + ` FROM reel_drafts
			WHERE (media_id IS NOT NULL OR cover_media_id IS NOT NULL) AND id > $1 ORDER BY id LIMIT $2`
	case HolderEventCover:
		q = `SELECT id, cover_media_id, NULL::uuid, TRUE FROM events
			WHERE cover_media_id IS NOT NULL AND id > $1 ORDER BY id LIMIT $2`
	default:
		return nil, fmt.Errorf("unknown media holder kind %q", kind)
	}
	rows, err := s.db.Query(ctx, q, after, limit)
	if err != nil {
		return nil, fmt.Errorf("page media holders %s: %w", kind, err)
	}
	defer rows.Close()
	var out []MediaHolding
	for rows.Next() {
		h := MediaHolding{Kind: kind}
		var a, b *uuid.UUID
		if err := rows.Scan(&h.Ref, &a, &b, &h.Live); err != nil {
			return nil, err
		}
		h.MediaIDs = holdingIDs(a, b)
		out = append(out, h)
	}
	return out, rows.Err()
}

// ClaimedRef is one (kind, ref) post-service holds claims under, read from
// media-service's media_claims table.
type ClaimedRef struct {
	Kind string
	Ref  uuid.UUID
}

// ErrClaimsTableAbsent: media_claims is not visible from this database (a
// deployment where media-service has its own database, or one that predates
// media-service migration 031). The orphan half of the reconcile is skipped.
var ErrClaimsTableAbsent = errors.New("media_claims is not visible from this database")

// ClaimedRefsPage pages through the (kind, ref) pairs claimant holds claims
// under in media-service's media_claims, after the cursor (afterKind,
// afterRef; both "" to start). It READS the table (posts and media share the
// app database; post-service already joins media_assets); every change still
// goes through media-service's claim routes. A ref that is not a canonical
// UUID is skipped: this service only ever sends uuid.String().
//
// This is how the reconcile tool finds claims whose holder row is gone
// (a purged post, an erased account's drafts), which no page of post-service's
// own tables can list. next is the cursor for the following page; more is
// false on the last one.
func (s *Store) ClaimedRefsPage(ctx context.Context, claimant string, after ClaimCursor, limit int) (refs []ClaimedRef, next ClaimCursor, more bool, err error) {
	var present bool
	if err := s.db.QueryRow(ctx, `SELECT to_regclass('public.media_claims') IS NOT NULL`).Scan(&present); err != nil {
		return nil, after, false, fmt.Errorf("probe media_claims: %w", err)
	}
	if !present {
		return nil, after, false, ErrClaimsTableAbsent
	}
	// Text, not a ::uuid cast: SQL does not promise to filter before it
	// casts, and one malformed ref must not fail the whole page.
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT claim_kind, claim_ref
		FROM media_claims
		WHERE claimant = $1 AND (claim_kind, claim_ref) > ($2, $3)
		ORDER BY claim_kind, claim_ref
		LIMIT $4`, claimant, after.Kind, after.Ref, limit)
	if err != nil {
		return nil, after, false, fmt.Errorf("page media claims: %w", err)
	}
	defer rows.Close()
	next = after
	n := 0
	for rows.Next() {
		var kind, ref string
		if err := rows.Scan(&kind, &ref); err != nil {
			return nil, after, false, err
		}
		n++
		next = ClaimCursor{Kind: kind, Ref: ref}
		id, perr := uuid.Parse(ref)
		if perr != nil || id.String() != ref {
			continue // not a ref this service wrote; the cursor moves past it
		}
		refs = append(refs, ClaimedRef{Kind: kind, Ref: id})
	}
	if err := rows.Err(); err != nil {
		return nil, after, false, err
	}
	return refs, next, n == limit, nil
}

// ClaimCursor is ClaimedRefsPage's position: the last (claim_kind, claim_ref)
// read, as text.
type ClaimCursor struct{ Kind, Ref string }

func holdingIDs(a, b *uuid.UUID) []uuid.UUID {
	var out []uuid.UUID
	for _, p := range []*uuid.UUID{a, b} {
		if p == nil || *p == uuid.Nil {
			continue
		}
		dup := false
		for _, have := range out {
			if have == *p {
				dup = true
			}
		}
		if !dup {
			out = append(out, *p)
		}
	}
	return out
}
