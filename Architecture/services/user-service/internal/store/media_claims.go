package store

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/atpost/user-service/internal/mediaclient"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ── Media claims: the rows that hold media, for the claim protocol ─────────
//
// internal/service/media_claims.go has the protocol; internal/claimsync the
// reconcile. This file is what both read and the lock they serialise on.

// MediaClaimRow is one holder of media (a channel picture, a portfolio item,
// a business page cover...) as the table holds it now. Live is false when the
// row is gone or terminal (a disabled page): its claims should be released.
// Invalid lists values a TEXT column holds that are not media ids at all
// (business_pages predates UUID columns); they are reported, never claimed.
type MediaClaimRow struct {
	Kind     string
	Ref      uuid.UUID
	MediaIDs []uuid.UUID
	Invalid  []string
	Live     bool
}

// PendingMediaClaimHolder is one media_claim_pending row (migration 010).
type PendingMediaClaimHolder struct {
	Kind       string
	Ref        uuid.UUID
	EnqueuedAt time.Time
}

// ── The holder lock ──────────────────────────────────────────────────────

// LockMediaHolder serialises every write that changes which media one row
// holds, for as long as the caller holds it. The claim protocol claims,
// writes and releases in three steps; two edits of the same row that each
// read it first could otherwise interleave so that one's release removes a
// claim the other's write relies on. Transaction-scoped, so a crash or a lost
// connection frees it. unlock is idempotent.
//
// The lock's transaction pins one pool connection for as long as it is held,
// and the holder then needs a SECOND connection for its own reads and writes.
// Unbounded, that deadlocks the pool: with N connections, N callers each
// holding (or waiting in Postgres for) a lock leave none for the holder's
// next query, and nobody ever unlocks. So before a connection is taken the
// caller passes the holderGate: same-ref callers queue in process (only one
// of them waits in Postgres, the advisory lock still serialises across
// replicas), and at most holderSlotsFor(MaxConns) locks are held at once, so
// a holder always finds a free connection.
func (s *Store) LockMediaHolder(ctx context.Context, ref uuid.UUID) (unlock func(), err error) {
	leave, err := s.holders.enter(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	tx, err := s.db.Begin(ctx)
	if err != nil {
		leave()
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	if _, err := tx.Exec(ctx, lockMediaHolderSQL, mediaHolderLockKey(ref)); err != nil {
		_ = tx.Rollback(context.WithoutCancel(ctx))
		leave()
		return nil, fmt.Errorf("lock media holder: %w", err)
	}
	var once sync.Once
	return func() {
		once.Do(func() {
			_ = tx.Rollback(context.WithoutCancel(ctx))
			leave()
		})
	}, nil
}

// holderSlotsFor is how many holder locks may be held at once on a pool of
// maxConns connections: half, so lock holders can never take the last
// connection (each needs one more than it pins) and ordinary traffic keeps
// the rest. At least one.
func holderSlotsFor(maxConns int32) int {
	if n := int(maxConns / 2); n > 1 {
		return n
	}
	return 1
}

// holderGate is the in-process half of the holder lock (see LockMediaHolder).
type holderGate struct {
	slots chan struct{} // one token per held lock
	mu    sync.Mutex
	refs  map[uuid.UUID]*holderRef
}

type holderRef struct {
	turn  chan struct{} // capacity 1: the token is this ref's turn
	users int           // callers holding or waiting for the turn
}

func newHolderGate(slots int) *holderGate {
	if slots < 1 {
		slots = 1
	}
	return &holderGate{slots: make(chan struct{}, slots), refs: map[uuid.UUID]*holderRef{}}
}

// enter waits for ref's turn and then for a slot, in that order (so callers
// queued behind the same ref hold no slot). leave gives both back; it must be
// called exactly once. A cancelled ctx stops the wait with ctx.Err().
func (g *holderGate) enter(ctx context.Context, ref uuid.UUID) (leave func(), err error) {
	g.mu.Lock()
	r := g.refs[ref]
	if r == nil {
		r = &holderRef{turn: make(chan struct{}, 1)}
		g.refs[ref] = r
	}
	r.users++
	g.mu.Unlock()

	done := func() {
		g.mu.Lock()
		r.users--
		if r.users == 0 {
			delete(g.refs, ref)
		}
		g.mu.Unlock()
	}
	select {
	case r.turn <- struct{}{}:
	case <-ctx.Done():
		done()
		return nil, ctx.Err()
	}
	select {
	case g.slots <- struct{}{}:
	case <-ctx.Done():
		<-r.turn
		done()
		return nil, ctx.Err()
	}
	return func() {
		<-g.slots
		<-r.turn
		done()
	}, nil
}

// lockMediaHolderSQL waits (never try_) for an exclusive, transaction-scoped
// lock. Pinned by TestMediaHolderLockWaitsAndIsTransactionScoped; proven live
// by TestMediaClaimsIT_HolderLockSerialises (CI).
const lockMediaHolderSQL = `SELECT pg_advisory_xact_lock($1)`

// mediaHolderLockKey namespaces the advisory lock so it cannot collide with
// another lock on the same id (channel-service uses its own namespace).
// Every holder ref here is a fresh UUID of its own table, so one namespace
// covers channels, portfolio items and pages.
func mediaHolderLockKey(ref uuid.UUID) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte("user-media-holder:"))
	_, _ = h.Write(ref[:])
	return int64(h.Sum64())
}

// ── Pending holders (migration 010) ──────────────────────────────────────

// enqueueMediaClaimPendingSQL is shared by every writer that enqueues: the
// INSERT ... SELECT form lets a delete enqueue exactly the rows it is about
// to remove, in its own transaction. $1 is the kind; the SELECT supplies refs.
const enqueueMediaClaimPendingSQL = `
	INSERT INTO media_claim_pending (claim_kind, claim_ref)
	SELECT $1::text, ref FROM (%s) AS holders(ref)
	ON CONFLICT (claim_kind, claim_ref) DO UPDATE SET enqueued_at = clock_timestamp()`

type pendingEnqueue struct {
	kind  string
	table string // the holder table sel reads (purge probes it first)
	// sel selects the holder ids (one column) and may use $2.. placeholders.
	sel string
}

func enqueuePendingTx(ctx context.Context, tx pgx.Tx, steps []pendingEnqueue, args ...any) error {
	for _, st := range steps {
		all := append([]any{st.kind}, args...)
		if _, err := tx.Exec(ctx, fmt.Sprintf(enqueueMediaClaimPendingSQL, st.sel), all...); err != nil {
			return fmt.Errorf("enqueue media claim release (%s): %w", st.kind, err)
		}
	}
	return nil
}

// The holder sets each delete enqueues. $2 is the row id, $3 the owner.
var (
	channelDeletePending = []pendingEnqueue{
		{kind: mediaclient.KindChannelAvatar, table: "channels", sel: `SELECT id FROM channels WHERE id = $2 AND user_id = $3 AND avatar_media_id IS NOT NULL`},
		{kind: mediaclient.KindChannelBanner, table: "channels", sel: `SELECT id FROM channels WHERE id = $2 AND user_id = $3 AND banner_media_id IS NOT NULL`},
	}
	portfolioDeletePending = []pendingEnqueue{
		{kind: mediaclient.KindPortfolioItem, table: "portfolio_items", sel: `SELECT id FROM portfolio_items WHERE id = $2 AND user_id = $3 AND media_id IS NOT NULL`},
	}
	pageDisablePending = []pendingEnqueue{
		{kind: mediaclient.KindBusinessPageAvatar, table: "business_pages", sel: `SELECT id FROM business_pages WHERE id = $2 AND avatar_media_id <> ''`},
		{kind: mediaclient.KindBusinessPageCover, table: "business_pages", sel: `SELECT id FROM business_pages WHERE id = $2 AND cover_media_id <> ''`},
	}
	// $2 is the purged user.
	purgePending = []pendingEnqueue{
		{kind: mediaclient.KindChannelAvatar, table: "channels", sel: `SELECT id FROM channels WHERE user_id = $2 AND avatar_media_id IS NOT NULL`},
		{kind: mediaclient.KindChannelBanner, table: "channels", sel: `SELECT id FROM channels WHERE user_id = $2 AND banner_media_id IS NOT NULL`},
		{kind: mediaclient.KindPortfolioItem, table: "portfolio_items", sel: `SELECT id FROM portfolio_items WHERE user_id = $2 AND media_id IS NOT NULL`},
		{kind: mediaclient.KindBusinessPageAvatar, table: "business_pages", sel: `SELECT id FROM business_pages WHERE user_id = $2 AND avatar_media_id <> ''`},
		{kind: mediaclient.KindBusinessPageCover, table: "business_pages", sel: `SELECT id FROM business_pages WHERE user_id = $2 AND cover_media_id <> ''`},
	}
)

// MarkMediaClaimHolderPending records that kind/ref's claims need settling
// (a best-effort release failed). Idempotent.
func (s *Store) MarkMediaClaimHolderPending(ctx context.Context, kind string, ref uuid.UUID) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO media_claim_pending (claim_kind, claim_ref) VALUES ($1, $2)
		ON CONFLICT (claim_kind, claim_ref) DO UPDATE SET enqueued_at = clock_timestamp()`, kind, ref)
	return err
}

// PendingMediaClaimHolders returns up to limit holders waiting to be settled,
// oldest first.
func (s *Store) PendingMediaClaimHolders(ctx context.Context, limit int) ([]PendingMediaClaimHolder, error) {
	rows, err := s.db.Query(ctx, `
		SELECT claim_kind, claim_ref, enqueued_at FROM media_claim_pending
		ORDER BY enqueued_at, claim_kind, claim_ref LIMIT $1`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PendingMediaClaimHolder
	for rows.Next() {
		var p PendingMediaClaimHolder
		if err := rows.Scan(&p.Kind, &p.Ref, &p.EnqueuedAt); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// ClearMediaClaimHolderPending removes the entry p, unless it was re-enqueued
// after p was read (enqueued_at moved): that later entry still needs settling.
func (s *Store) ClearMediaClaimHolderPending(ctx context.Context, p PendingMediaClaimHolder) error {
	_, err := s.db.Exec(ctx, `
		DELETE FROM media_claim_pending
		WHERE claim_kind = $1 AND claim_ref = $2 AND enqueued_at = $3`, p.Kind, p.Ref, p.EnqueuedAt)
	return err
}

// ── Reading the holders ──────────────────────────────────────────────────

// MediaClaimRowFor re-reads one holder row, under LockMediaHolder. A row that
// no longer exists is not live and holds nothing.
func (s *Store) MediaClaimRowFor(ctx context.Context, kind string, ref uuid.UUID) (MediaClaimRow, error) {
	row := MediaClaimRow{Kind: kind, Ref: ref}
	var (
		id  *uuid.UUID
		txt string
		err error
	)
	switch kind {
	case mediaclient.KindChannelAvatar, mediaclient.KindChannelBanner:
		col := "avatar_media_id"
		if kind == mediaclient.KindChannelBanner {
			col = "banner_media_id"
		}
		err = s.db.QueryRow(ctx, `SELECT `+col+` FROM channels WHERE id = $1`, ref).Scan(&id)
		row.Live = err == nil
	case mediaclient.KindPortfolioItem:
		err = s.db.QueryRow(ctx, `SELECT media_id FROM portfolio_items WHERE id = $1`, ref).Scan(&id)
		row.Live = err == nil
	case mediaclient.KindBusinessPageAvatar, mediaclient.KindBusinessPageCover:
		col := "avatar_media_id"
		if kind == mediaclient.KindBusinessPageCover {
			col = "cover_media_id"
		}
		err = s.db.QueryRow(ctx, `SELECT `+col+`, `+pageLiveSQL+` FROM business_pages WHERE id = $1`, ref).
			Scan(&txt, &row.Live)
		if err == nil {
			addPageMedia(&row, txt)
		}
	default:
		return row, fmt.Errorf("unknown media holder kind %q", kind)
	}
	if errors.Is(err, pgx.ErrNoRows) {
		return MediaClaimRow{Kind: kind, Ref: ref}, nil
	}
	if err != nil {
		return row, err
	}
	if id != nil && *id != uuid.Nil {
		row.MediaIDs = []uuid.UUID{*id}
	}
	return row, nil
}

// pageLiveSQL is the one definition of a page that still shows its media:
// `disabled` is terminal (pages/statemachine.go), so its claims go.
const pageLiveSQL = `(status <> 'disabled')`

// ParsePageMediaID reads a business_pages media column, which is TEXT: ""
// holds nothing; a UUID is a media id; anything else is not a media id.
func ParsePageMediaID(v string) (id uuid.UUID, ok bool, present bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return uuid.Nil, false, false
	}
	parsed, err := uuid.Parse(v)
	if err != nil || parsed == uuid.Nil {
		return uuid.Nil, false, true
	}
	return parsed, true, true
}

func addPageMedia(row *MediaClaimRow, v string) {
	id, ok, present := ParsePageMediaID(v)
	switch {
	case ok:
		row.MediaIDs = []uuid.UUID{id}
	case present:
		row.Invalid = []string{v}
	}
}

// ChannelMediaClaimRows pages through every channel with a picture or banner
// in id order after `after`; the second value is the last channel id read
// (the next cursor), since one channel yields up to two rows.
func (s *Store) ChannelMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]MediaClaimRow, uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, avatar_media_id, banner_media_id FROM channels
		WHERE (avatar_media_id IS NOT NULL OR banner_media_id IS NOT NULL) AND id > $1
		ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []MediaClaimRow
	last := after
	for rows.Next() {
		var id uuid.UUID
		var avatar, banner *uuid.UUID
		if err := rows.Scan(&id, &avatar, &banner); err != nil {
			return nil, after, err
		}
		last = id
		if avatar != nil {
			out = append(out, MediaClaimRow{Kind: mediaclient.KindChannelAvatar, Ref: id, MediaIDs: []uuid.UUID{*avatar}, Live: true})
		}
		if banner != nil {
			out = append(out, MediaClaimRow{Kind: mediaclient.KindChannelBanner, Ref: id, MediaIDs: []uuid.UUID{*banner}, Live: true})
		}
	}
	return out, last, rows.Err()
}

// PortfolioMediaClaimRows pages through every portfolio item with media.
func (s *Store) PortfolioMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]MediaClaimRow, uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, media_id FROM portfolio_items
		WHERE media_id IS NOT NULL AND id > $1
		ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []MediaClaimRow
	last := after
	for rows.Next() {
		var id, media uuid.UUID
		if err := rows.Scan(&id, &media); err != nil {
			return nil, after, err
		}
		last = id
		out = append(out, MediaClaimRow{Kind: mediaclient.KindPortfolioItem, Ref: id, MediaIDs: []uuid.UUID{media}, Live: true})
	}
	return out, last, rows.Err()
}

// BusinessPageMediaClaimRows pages through every page with an avatar or
// cover, disabled ones included (their claims are released).
func (s *Store) BusinessPageMediaClaimRows(ctx context.Context, after uuid.UUID, limit int) ([]MediaClaimRow, uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, avatar_media_id, cover_media_id, `+pageLiveSQL+` FROM business_pages
		WHERE (avatar_media_id <> '' OR cover_media_id <> '') AND id > $1
		ORDER BY id LIMIT $2`, after, limit)
	if err != nil {
		return nil, after, err
	}
	defer rows.Close()
	var out []MediaClaimRow
	last := after
	for rows.Next() {
		var id uuid.UUID
		var avatar, cover string
		var live bool
		if err := rows.Scan(&id, &avatar, &cover, &live); err != nil {
			return nil, after, err
		}
		last = id
		if strings.TrimSpace(avatar) != "" {
			r := MediaClaimRow{Kind: mediaclient.KindBusinessPageAvatar, Ref: id, Live: live}
			addPageMedia(&r, avatar)
			out = append(out, r)
		}
		if strings.TrimSpace(cover) != "" {
			r := MediaClaimRow{Kind: mediaclient.KindBusinessPageCover, Ref: id, Live: live}
			addPageMedia(&r, cover)
			out = append(out, r)
		}
	}
	return out, last, rows.Err()
}
