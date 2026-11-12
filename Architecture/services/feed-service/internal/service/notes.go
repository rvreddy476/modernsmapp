package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/atpost/feed-service/internal/store/postgres"
	"github.com/atpost/feed-service/internal/store/scylla"
	"github.com/gocql/gocql"
	"github.com/google/uuid"
)

// Echo notes in feed-service (docs/designs/echo-contract.md, "feed-service";
// short-posts-design.md §5.5).
//
// WRITE. On PostCreated with content_type "note" and no reply_to_id, a note
// is written to its author's and every follower's notes_timeline (never to
// Home or the author timeline). Replies are recorded and go nowhere. An
// author who is celeb-flagged, or whose follower count passes
// ECHO_FANOUT_MAX_FOLLOWERS, is not pushed: their notes are pulled at read
// time from echo_notes. Fan-out runs whatever ECHO_ENABLED says.
//
// LIFECYCLE mirrors Home: delete removes every notes_timeline row through the
// post's reverse index (DeletePostFromTimelines, kind "notes"); restore and a
// visibility change re-fan-out through FanoutPost, which routes a note here;
// unfollow purges the ex-follower's rows unless they follow again; follow
// backfills the followee's recent notes.
//
// READ. Following is the viewer's notes_timeline plus pulled authors, newest
// first, keyset-paged by timeline key exactly like Home (homeWindow). For You
// ranks a pool of following notes, notes people you follow liked or
// reposted, and fast-rising notes from the last 48 hours. Both apply the
// same block / mute / hidden-author filters as Home before the page is cut,
// and the handler hydrates through HydratePosts, which applies the keyword,
// privacy, listing-visibility and "not interested" filters.

// NoteContentType is the content_type of an Echo note.
const NoteContentType = "note"

// defaultEchoMaxFollowers is ECHO_FANOUT_MAX_FOLLOWERS when unset.
const defaultEchoMaxFollowers = 100000

type echoConfig struct {
	enabled      bool
	maxFollowers int
}

func loadEchoConfig() echoConfig {
	return parseEchoConfig(os.Getenv("ECHO_ENABLED"), os.Getenv("ECHO_FANOUT_MAX_FOLLOWERS"))
}

// parseEchoConfig applies the contract's defaults: off unless ECHO_ENABLED
// is "true" (or "1"); a positive ECHO_FANOUT_MAX_FOLLOWERS, else 100000.
func parseEchoConfig(enabledRaw, maxRaw string) echoConfig {
	c := echoConfig{maxFollowers: defaultEchoMaxFollowers}
	switch strings.ToLower(strings.TrimSpace(enabledRaw)) {
	case "true", "1":
		c.enabled = true
	}
	if n, err := strconv.Atoi(strings.TrimSpace(maxRaw)); err == nil && n > 0 {
		c.maxFollowers = n
	}
	return c
}

// EchoEnabled reports the ECHO_ENABLED switch: off means the notes read
// routes answer 503 ECHO_NOT_ENABLED. It never stops the fan-out.
func (s *Service) EchoEnabled() bool { return s.echo.enabled }

// notesTimelineStore is the Scylla slice the notes paths use; the
// TimelineStore implements it.
type notesTimelineStore interface {
	AddToNotesTimeline(ctx context.Context, ownerID, postID, authorID uuid.UUID, createdAt time.Time) error
	GetNotesTimelineBefore(ctx context.Context, ownerID uuid.UUID, beforeToken string, limit int) ([]scylla.FeedItem, error)
	DeleteNotesTimelineEntriesByAuthorForUser(ctx context.Context, userID, authorID uuid.UUID, bucketLookback int) (int, error)
	UserInteractedPosts(ctx context.Context, userID uuid.UUID, limit int) ([]uuid.UUID, error)
	GetHomeTimelineByContentTypes(ctx context.Context, userID uuid.UUID, contentTypes []string, limit int) ([]scylla.FeedItem, error)
}

// notesMetaStore is the Postgres slice (migration 009); the MetaStore
// implements it.
type notesMetaStore interface {
	RecordNote(ctx context.Context, n postgres.NoteRow) error
	NoteMeta(ctx context.Context, postID uuid.UUID) (postgres.NoteRow, bool, error)
	SetNoteDeleted(ctx context.Context, postID uuid.UUID, deleted bool) error
	SetNoteVisibility(ctx context.Context, postID uuid.UUID, visibility string) error
	LiveNotesByAuthors(ctx context.Context, authorIDs []uuid.UUID, before time.Time, limit int) ([]postgres.NoteRow, error)
	RecentLiveNotes(ctx context.Context, since time.Time, limit int) ([]postgres.NoteRow, error)
	LiveNotesAmong(ctx context.Context, postIDs []uuid.UUID) ([]postgres.NoteRow, error)
	MarkNotesPullAuthor(ctx context.Context, authorID uuid.UUID, followers int) error
	IsNotesPullAuthor(ctx context.Context, authorID uuid.UUID) (bool, error)
	NotesPullAuthorsAmong(ctx context.Context, authorIDs []uuid.UUID) ([]uuid.UUID, error)
	CelebsAmong(ctx context.Context, authorIDs []uuid.UUID) ([]uuid.UUID, error)
}

var errNotesStoresMissing = errors.New("echo notes: timeline or metadata store not configured")

// dropNotes removes Echo notes from a Home candidate list. Notes are never
// written to Home; this is the read-side backstop for any that arrive by
// another path.
func dropNotes(candidates []FeedItem) []FeedItem {
	n := 0
	for _, c := range candidates {
		if c.ContentType == NoteContentType {
			n++
		}
	}
	if n == 0 {
		return candidates
	}
	out := make([]FeedItem, 0, len(candidates)-n)
	for _, c := range candidates {
		if c.ContentType != NoteContentType {
			out = append(out, c)
		}
	}
	return out
}

// ── Write path ──────────────────────────────────────────────────────────────

// NoteCreated is what the PostCreated consumer hands FanoutNote.
type NoteCreated struct {
	PostID     uuid.UUID
	AuthorID   uuid.UUID
	CreatedAt  time.Time
	Visibility string
	ReplyToID  string
}

func normalizeNoteVisibility(v string) string {
	v = strings.ToLower(strings.TrimSpace(v))
	if v == "" {
		return "public"
	}
	return v
}

// FanoutNote handles PostCreated for a note: the note is recorded, and a
// top-level note is written to its author's and followers' notes timelines.
// A reply is recorded and goes to no timeline. Any error is returned so the
// event is retried; every write is an upsert.
func (s *Service) FanoutNote(ctx context.Context, n NoteCreated) error {
	if s.notesMeta == nil || s.notesTimelines == nil {
		return errNotesStoresMissing
	}
	topLevel := strings.TrimSpace(n.ReplyToID) == ""
	visibility := normalizeNoteVisibility(n.Visibility)
	if err := s.notesMeta.RecordNote(ctx, postgres.NoteRow{
		PostID: n.PostID, AuthorID: n.AuthorID, CreatedAt: n.CreatedAt,
		TopLevel: topLevel, Visibility: visibility,
	}); err != nil {
		return fmt.Errorf("record note %s: %w", n.PostID, err)
	}
	if !topLevel {
		return nil // replies never go to timelines
	}
	return s.pushNote(ctx, n.PostID, n.AuthorID, n.CreatedAt, visibility)
}

// refanoutNote is FanoutPost for a note (PostRestored, an audience change).
// Those events carry no reply_to_id, so echo_notes decides: a reply, or a
// note this service never recorded, gets no rows.
func (s *Service) refanoutNote(ctx context.Context, postID, authorID uuid.UUID, createdAt time.Time, visibility string) error {
	if s.notesMeta == nil || s.notesTimelines == nil {
		return errNotesStoresMissing
	}
	meta, found, err := s.notesMeta.NoteMeta(ctx, postID)
	if err != nil {
		return fmt.Errorf("note %s: read metadata: %w", postID, err)
	}
	if !found || !meta.TopLevel {
		log.Printf("echo: note %s re-fan-out skipped (recorded=%v top_level=%v)", postID, found, meta.TopLevel)
		return nil
	}
	visibility = normalizeNoteVisibility(visibility)
	if err := s.notesMeta.SetNoteVisibility(ctx, postID, visibility); err != nil {
		return fmt.Errorf("note %s: record visibility: %w", postID, err)
	}
	return s.pushNote(ctx, postID, authorID, createdAt, visibility)
}

// errNotesPullThreshold stops the notes follower leg once the author turns
// out to have more than ECHO_FANOUT_MAX_FOLLOWERS followers.
var errNotesPullThreshold = errors.New("echo fan-out threshold reached")

// pushNote writes the author's own row and, for a listable note whose
// author is pushed, one row per follower, chunk by chunk with FanoutPost's
// resumable progress.
func (s *Service) pushNote(ctx context.Context, postID, authorID uuid.UUID, createdAt time.Time, visibility string) error {
	if err := s.notesTimelines.AddToNotesTimeline(ctx, authorID, postID, authorID, createdAt); err != nil {
		return fmt.Errorf("note %s: author's own row: %w", postID, err)
	}
	if !listableVisibility(visibility) {
		return nil // the author's alone (listingvisibility.go)
	}
	pulled, err := s.notesAuthorPulled(ctx, authorID)
	if err != nil {
		return fmt.Errorf("note %s: pull model check: %w", postID, err)
	}
	if pulled {
		return nil // read at Following time (pullNoteRows)
	}
	threshold := s.echo.maxFollowers
	if threshold <= 0 {
		threshold = defaultEchoMaxFollowers
	}
	if n, ok := s.followerCount(ctx, authorID); ok && n > threshold {
		if err := s.notesMeta.MarkNotesPullAuthor(ctx, authorID, n); err != nil {
			return fmt.Errorf("note %s: flag pulled author: %w", postID, err)
		}
		return nil
	}

	prog := s.loadFanoutProgress(ctx, postID)
	dedupe := newRecipientDedupe()
	dedupe.first(authorID) // the author's own row is already written
	write := func(ids []uuid.UUID) error {
		fresh := make([]uuid.UUID, 0, len(ids))
		for _, id := range ids {
			if dedupe.first(id) {
				fresh = append(fresh, id)
			}
		}
		return pushChunkWith(fresh, func(id uuid.UUID) error {
			return s.notesTimelines.AddToNotesTimeline(ctx, id, postID, authorID, createdAt)
		})
	}
	pageFilter := func(seen int, ids []uuid.UUID) ([]uuid.UUID, error) {
		if seen > threshold {
			if err := s.notesMeta.MarkNotesPullAuthor(ctx, authorID, seen); err != nil {
				return nil, fmt.Errorf("flag pulled author: %w", err)
			}
			return nil, errNotesPullThreshold
		}
		return ids, nil
	}
	err = s.graphLeg(ctx, prog, "notes_followers", "followers", authorID, pageFilter, write)
	switch {
	case errors.Is(err, errNotesPullThreshold):
		// Pulled from now on; rows already pushed merge by post id.
	case err != nil:
		return fmt.Errorf("note %s fan-out incomplete, will be retried: %w", postID, err)
	}
	prog.clear(ctx)
	return nil
}

// notesAuthorPulled reports whether an author's notes are pulled at read
// time: celeb-flagged, or flagged by the notes threshold. Fails safe: an
// unreadable flag is an error, never "push".
func (s *Service) notesAuthorPulled(ctx context.Context, authorID uuid.UUID) (bool, error) {
	if s.celebs != nil {
		celeb, err := s.celebs.IsCeleb(ctx, authorID)
		if err != nil {
			return false, err
		}
		if celeb {
			return true, nil
		}
	}
	return s.notesMeta.IsNotesPullAuthor(ctx, authorID)
}

// MarkNoteDeleted follows PostDeleted (true) and PostRestored (false) in
// echo_notes, so a deleted note is no pull or For You candidate. The
// timeline rows themselves go through the reverse index.
func (s *Service) MarkNoteDeleted(ctx context.Context, postID uuid.UUID, deleted bool) error {
	if s.notesMeta == nil {
		return nil
	}
	return s.notesMeta.SetNoteDeleted(ctx, postID, deleted)
}

// NotesUnfollowLookback is how many monthly buckets the unfollow purge
// walks, as for Home.
const NotesUnfollowLookback = 3

// PurgeNotesAfterUnfollow removes the followee's notes from the ex-
// follower's notes timeline. Notes fan out to followers only, so the rows
// stay only if the graph says the follow exists again; a relationship that
// cannot be established purges nothing and is an error (retried).
func (s *Service) PurgeNotesAfterUnfollow(ctx context.Context, followerID, followeeID uuid.UUID) (int, error) {
	if s.notesTimelines == nil {
		return 0, nil
	}
	if followerID == uuid.Nil || followeeID == uuid.Nil {
		return 0, fmt.Errorf("empty follower or followee id")
	}
	cctx, cancel := context.WithTimeout(ctx, fanoutClaimTimeout)
	rels, err := s.fetchRelationships(cctx, followerID, []uuid.UUID{followeeID})
	cancel()
	if err != nil {
		return 0, fmt.Errorf("notes unfollow purge: relationship not established, nothing purged: %w", err)
	}
	rel, ok := rels[followeeID]
	if !ok {
		return 0, fmt.Errorf("notes unfollow purge: graph-service returned no relationship for %s", followeeID)
	}
	if rel.Follows {
		return 0, nil // followed again: the rows are still owed
	}
	if s.rdb != nil {
		s.rdb.Del(ctx, notesForYouKey(followerID))
	}
	return s.notesTimelines.DeleteNotesTimelineEntriesByAuthorForUser(ctx, followerID, followeeID, NotesUnfollowLookback)
}

// notesBackfillLimit bounds the notes copied on a new follow, as Home's
// backfill does.
const notesBackfillLimit = 50

// BackfillNotesOnFollow copies the followee's recent live notes into the
// new follower's notes timeline, unless the followee is pulled (their notes
// are read at Following time anyway). Idempotent: rows upsert.
func (s *Service) BackfillNotesOnFollow(ctx context.Context, followerID, followeeID uuid.UUID) (int, error) {
	if s.notesMeta == nil || s.notesTimelines == nil {
		return 0, nil
	}
	pulled, err := s.notesAuthorPulled(ctx, followeeID)
	if err != nil {
		return 0, fmt.Errorf("notes backfill: pull model check: %w", err)
	}
	if pulled {
		return 0, nil
	}
	notes, err := s.notesMeta.LiveNotesByAuthors(ctx, []uuid.UUID{followeeID}, time.Now().Add(time.Minute), notesBackfillLimit)
	if err != nil {
		return 0, fmt.Errorf("notes backfill: read notes: %w", err)
	}
	written := 0
	for _, n := range notes {
		if err := s.notesTimelines.AddToNotesTimeline(ctx, followerID, n.PostID, n.AuthorID, n.CreatedAt); err != nil {
			return written, fmt.Errorf("notes backfill: write %s: %w", n.PostID, err)
		}
		written++
	}
	return written, nil
}

// ── Following ───────────────────────────────────────────────────────────────

// notesFetchLimit is the read headroom over a page, as GetHomeFeed's.
func notesFetchLimit(limit int) int {
	n := limit + limit/2
	if n < limit+10 {
		n = limit + 10
	}
	if n > 200 {
		n = 200
	}
	return n
}

// GetNotesFollowingPage is one page of the Following timeline: the viewer's
// notes_timeline merged with pulled authors' notes, newest first, block /
// mute / hidden filtered (fail closed), cut with homeWindow so the cursor is
// the exact timeline key the page ended on.
func (s *Service) GetNotesFollowingPage(ctx context.Context, viewerID uuid.UUID, limit int, before string) ([]FeedItem, string, error) {
	if s.notesTimelines == nil {
		return nil, "", errNotesStoresMissing
	}
	blocked, err := s.resolveBlockedSet(ctx, viewerID)
	if err != nil {
		return nil, "", err
	}
	fetch := notesFetchLimit(limit)
	raw, err := s.notesFollowingRows(ctx, viewerID, before, fetch)
	if err != nil {
		return nil, "", err
	}
	candidates := notesRowsToItems(raw, sourceTimeline)
	candidates = applyBlockFilter(candidates, blocked)
	if candidates, err = s.applyHiddenAuthorFilter(ctx, candidates); err != nil {
		return nil, "", err
	}
	page, next := homeWindow(candidates, raw, fetch, limit)
	return page, next, nil
}

// notesRowsToItems converts timeline rows to candidates, keeping notes only.
func notesRowsToItems(rows []scylla.FeedItem, source string) []FeedItem {
	out := make([]FeedItem, 0, len(rows))
	for _, r := range rows {
		if r.ContentType != NoteContentType {
			continue
		}
		out = append(out, FeedItem{
			PostID: r.PostID, AuthorID: r.AuthorID, CreatedAt: r.CreatedAt,
			ContentType: NoteContentType, CursorToken: r.CursorToken, Source: source,
		})
	}
	return out
}

// notesFollowingRows reads the viewer's notes_timeline below `before` and
// merges in the pulled authors' notes by timeline key. A failed pull is
// logged and the page is served from the pushed rows, as Home does.
func (s *Service) notesFollowingRows(ctx context.Context, viewerID uuid.UUID, before string, fetch int) ([]scylla.FeedItem, error) {
	rows, err := s.notesTimelines.GetNotesTimelineBefore(ctx, viewerID, before, fetch)
	if err != nil {
		return nil, err
	}
	pulled, err := s.pullNoteRows(ctx, viewerID, before, fetch)
	if err != nil {
		log.Printf("echo: notes pull unavailable for %s, page served from the pushed timeline only: %v", viewerID, err)
	}
	if len(pulled) > 0 {
		rows = mergeTimelineRows(rows, pulled, fetch)
	}
	return rows, nil
}

// pulledNoteAuthors returns the accounts the viewer follows whose notes are
// pulled (celeb-flagged or over the notes threshold), at most
// maxPulledCelebs.
func (s *Service) pulledNoteAuthors(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, error) {
	if s.notesMeta == nil {
		return nil, nil
	}
	following, err := s.viewerFollowing(ctx, viewerID)
	if err != nil || len(following) == 0 {
		return nil, err
	}
	celebs, err := s.notesMeta.CelebsAmong(ctx, following)
	if err != nil {
		return nil, err
	}
	flagged, err := s.notesMeta.NotesPullAuthorsAmong(ctx, following)
	if err != nil {
		return nil, err
	}
	seen := make(map[uuid.UUID]struct{}, len(celebs)+len(flagged))
	var out []uuid.UUID
	for _, id := range append(celebs, flagged...) {
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
		if len(out) == maxPulledCelebs {
			break
		}
	}
	return out, nil
}

// pullNoteRows reads the pulled authors' live notes strictly older than
// `before` (a timeline key; "" = newest), up to limit, keyed by TimelineTS
// so they merge into and page with the pushed rows.
func (s *Service) pullNoteRows(ctx context.Context, viewerID uuid.UUID, before string, limit int) ([]scylla.FeedItem, error) {
	authors, err := s.pulledNoteAuthors(ctx, viewerID)
	if err != nil || len(authors) == 0 {
		return nil, err
	}
	upper := time.Now().Add(time.Minute)
	if before != "" {
		parsed, err := gocql.ParseUUID(before)
		if err != nil {
			return nil, err
		}
		// One millisecond past the key's time: the key is millisecond
		// precision, so same-millisecond notes are read and the exact key
		// comparison below decides.
		upper = parsed.Time().Add(time.Millisecond)
	}
	notes, err := s.notesMeta.LiveNotesByAuthors(ctx, authors, upper, limit)
	if err != nil {
		return nil, err
	}
	out := make([]scylla.FeedItem, 0, len(notes))
	for _, n := range notes {
		token := scylla.TimelineTS(n.PostID, n.CreatedAt).String()
		if before != "" && !timelineKeyAfter(before, token) {
			continue // not strictly older than the cursor
		}
		out = append(out, scylla.FeedItem{
			PostID: n.PostID, AuthorID: n.AuthorID, CreatedAt: n.CreatedAt,
			ContentType: NoteContentType, CursorToken: token,
		})
	}
	return out, nil
}

// ── For You ─────────────────────────────────────────────────────────────────

const (
	// forYouPoolMax bounds one viewer's ranked pool.
	forYouPoolMax = 300
	// forYouFollowingRows is how much of Following joins the pool.
	forYouFollowingRows = 150
	// forYouNetworkAccounts is how many followed accounts' likes are read.
	forYouNetworkAccounts = 30
	// forYouLikesPerAccount bounds each followed account's like read.
	forYouLikesPerAccount = 100
	// forYouRisingWindow is how far back the fast-rising source looks.
	forYouRisingWindow = 48 * time.Hour
	// forYouRisingPool / forYouRisingKeep: recent notes read, and the
	// fastest of them kept.
	forYouRisingPool = 500
	forYouRisingKeep = 50
	// forYouSnapshotTTL is how long a ranked pool serves later pages.
	forYouSnapshotTTL = 15 * time.Minute
	// forYouRepostRows bounds the repost rows read off the home timeline
	// (each read walks months until it finds this many).
	forYouRepostRows = 30
	// sourceEchoNetwork marks a note liked or reposted by someone the
	// viewer follows.
	sourceEchoNetwork = "echo_network"
)

func notesForYouKey(viewerID uuid.UUID) string {
	return fmt.Sprintf("feed:echo:foryou:%s", viewerID)
}

// forYouSnapshot is a viewer's ranked pool, kept so later pages page the
// same order.
type forYouSnapshot struct {
	ID    string          `json:"id"`
	Items []forYouPoolRow `json:"items"`
}

type forYouPoolRow struct {
	PostID    uuid.UUID `json:"post_id"`
	AuthorID  uuid.UUID `json:"author_id"`
	CreatedAt time.Time `json:"created_at"`
	Score     float64   `json:"score"`
	Source    string    `json:"source"`
}

// ForYouCursor is a position in a ranked pool: the snapshot it belongs to
// and the offset of the next item.
type ForYouCursor struct {
	Snapshot string
	Offset   int
}

// GetNotesForYouPage serves one page of the ranked For You list. The first
// page (no snapshot) builds and ranks the pool and stores it; later pages
// read the stored pool, and rebuild it if it has expired. Block, mute and
// hidden-author filters run on the pool and again on every page.
func (s *Service) GetNotesForYouPage(ctx context.Context, viewerID uuid.UUID, limit int, cursor ForYouCursor) ([]FeedItem, *ForYouCursor, error) {
	if s.notesTimelines == nil || s.notesMeta == nil {
		return nil, nil, errNotesStoresMissing
	}
	blocked, blockErr := s.resolveBlockedSet(ctx, viewerID)
	if blockErr != nil {
		return nil, nil, blockErr // fail closed, as Home does
	}
	var err error
	var snap *forYouSnapshot
	if cursor.Snapshot != "" {
		snap = s.loadForYouSnapshot(ctx, viewerID, cursor.Snapshot)
	}
	if snap == nil {
		if snap, err = s.buildForYouSnapshot(ctx, viewerID, blocked); err != nil {
			return nil, nil, err
		}
		s.storeForYouSnapshot(ctx, viewerID, snap)
	}
	offset := cursor.Offset
	if offset < 0 || offset > len(snap.Items) {
		offset = len(snap.Items)
	}
	end := offset + limit
	if end > len(snap.Items) {
		end = len(snap.Items)
	}
	page := make([]FeedItem, 0, end-offset)
	for _, r := range snap.Items[offset:end] {
		page = append(page, FeedItem{
			PostID: r.PostID, AuthorID: r.AuthorID, CreatedAt: r.CreatedAt,
			Score: r.Score, ContentType: NoteContentType, Source: r.Source,
		})
	}
	page = applyBlockFilter(page, blocked)
	if page, err = s.applyHiddenAuthorFilter(ctx, page); err != nil {
		return nil, nil, err
	}
	var next *ForYouCursor
	if end < len(snap.Items) {
		next = &ForYouCursor{Snapshot: snap.ID, Offset: end}
	}
	return page, next, nil
}

// buildForYouSnapshot gathers, filters and ranks the pool.
func (s *Service) buildForYouSnapshot(ctx context.Context, viewerID uuid.UUID, blocked map[uuid.UUID]struct{}) (*forYouSnapshot, error) {
	pool, err := s.forYouCandidates(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	pool = filterForYouPool(pool, viewerID, blocked)
	if pool, err = s.applyHiddenAuthorFilter(ctx, pool); err != nil {
		return nil, err
	}
	if len(pool) > forYouPoolMax {
		pool = pool[:forYouPoolMax]
	}
	ranked := s.rankNotes(ctx, viewerID, pool)
	snap := &forYouSnapshot{ID: newSnapshotID(), Items: make([]forYouPoolRow, 0, len(ranked))}
	for _, c := range ranked {
		snap.Items = append(snap.Items, forYouPoolRow{
			PostID: c.PostID, AuthorID: c.AuthorID, CreatedAt: c.CreatedAt, Score: c.Score, Source: c.Source,
		})
	}
	return snap, nil
}

// filterForYouPool drops the viewer's own notes and blocked / muted
// authors, and keeps one candidate per note (the first source wins).
func filterForYouPool(pool []FeedItem, viewerID uuid.UUID, blocked map[uuid.UUID]struct{}) []FeedItem {
	seen := make(map[uuid.UUID]struct{}, len(pool))
	out := make([]FeedItem, 0, len(pool))
	for _, c := range pool {
		if c.AuthorID == viewerID {
			continue
		}
		if _, dup := seen[c.PostID]; dup {
			continue
		}
		seen[c.PostID] = struct{}{}
		out = append(out, c)
	}
	return applyBlockFilter(out, blocked)
}

// rankNotes orders the pool with the ranking package; without a ranker, or
// when it fails, newest first.
func (s *Service) rankNotes(ctx context.Context, viewerID uuid.UUID, pool []FeedItem) []FeedItem {
	if len(pool) == 0 {
		return pool
	}
	if s.ranker != nil {
		ranked, err := s.ranker.Rank(ctx, viewerID, feedItemsToCandidates(pool), len(pool))
		if err == nil {
			return wholeWindow(pool, candidatesToFeedItems(ranked))
		}
		log.Printf("echo: For You ranking failed for %s, newest first: %v", viewerID, err)
		rankFallbacks.WithLabelValues("echo_for_you").Inc()
	}
	out := append([]FeedItem(nil), pool...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out
}

// forYouCandidates gathers the three contract sources. Following is
// required (an error fails the page); the other two are best effort.
func (s *Service) forYouCandidates(ctx context.Context, viewerID uuid.UUID) ([]FeedItem, error) {
	rows, err := s.notesFollowingRows(ctx, viewerID, "", forYouFollowingRows)
	if err != nil {
		return nil, err
	}
	pool := notesRowsToItems(rows, sourceTimeline)
	if network, err := s.forYouNetworkNotes(ctx, viewerID); err != nil {
		log.Printf("echo: For You network source unavailable for %s: %v", viewerID, err)
	} else {
		pool = append(pool, network...)
	}
	if rising, err := s.forYouRisingNotes(ctx); err != nil {
		log.Printf("echo: For You rising source unavailable: %v", err)
	} else {
		pool = append(pool, rising...)
	}
	return pool, nil
}

// forYouNetworkNotes is source 2: notes liked (user_post_interactions, from
// PostReacted) by up to forYouNetworkAccounts accounts the viewer follows,
// and notes reposted by them (the repost rows FanoutRepost wrote into the
// viewer's home timeline), kept only if they are live top-level notes.
func (s *Service) forYouNetworkNotes(ctx context.Context, viewerID uuid.UUID) ([]FeedItem, error) {
	following, err := s.viewerFollowing(ctx, viewerID)
	if err != nil {
		return nil, err
	}
	if len(following) > forYouNetworkAccounts {
		following = following[:forYouNetworkAccounts]
	}
	ids := map[uuid.UUID]struct{}{}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, 8)
	for _, f := range following {
		wg.Add(1)
		sem <- struct{}{}
		go func(f uuid.UUID) {
			defer wg.Done()
			defer func() { <-sem }()
			liked, err := s.notesTimelines.UserInteractedPosts(ctx, f, forYouLikesPerAccount)
			if err != nil {
				log.Printf("echo: likes of %s unavailable: %v", f, err)
				return
			}
			mu.Lock()
			for _, id := range liked {
				ids[id] = struct{}{}
			}
			mu.Unlock()
		}(f)
	}
	wg.Wait()
	if reposts, err := s.notesTimelines.GetHomeTimelineByContentTypes(ctx, viewerID, []string{"repost"}, forYouRepostRows); err != nil {
		log.Printf("echo: repost rows of %s unavailable: %v", viewerID, err)
	} else {
		for _, r := range reposts {
			ids[r.PostID] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return nil, nil
	}
	list := make([]uuid.UUID, 0, len(ids))
	for id := range ids {
		list = append(list, id)
	}
	notes, err := s.notesMeta.LiveNotesAmong(ctx, list)
	if err != nil {
		return nil, err
	}
	return noteRowsToItems(notes, sourceEchoNetwork), nil
}

// forYouRisingNotes is source 3: live public top-level notes from the last
// 48 hours, the fastest by the velocity tracker's score
// (post:velocity:ranked, pipeline/velocity.go), newest first among equals.
func (s *Service) forYouRisingNotes(ctx context.Context) ([]FeedItem, error) {
	notes, err := s.notesMeta.RecentLiveNotes(ctx, time.Now().Add(-forYouRisingWindow), forYouRisingPool)
	if err != nil {
		return nil, err
	}
	if len(notes) == 0 {
		return nil, nil
	}
	velocity := make([]float64, len(notes))
	if s.rdb != nil {
		members := make([]string, len(notes))
		for i, n := range notes {
			members[i] = n.PostID.String()
		}
		if scores, err := s.rdb.ZMScore(ctx, "post:velocity:ranked", members...).Result(); err == nil {
			copy(velocity, scores)
		}
	}
	idx := make([]int, len(notes))
	for i := range idx {
		idx[i] = i
	}
	sort.SliceStable(idx, func(a, b int) bool { return velocity[idx[a]] > velocity[idx[b]] })
	if len(idx) > forYouRisingKeep {
		idx = idx[:forYouRisingKeep]
	}
	kept := make([]postgres.NoteRow, 0, len(idx))
	for _, i := range idx {
		kept = append(kept, notes[i])
	}
	return noteRowsToItems(kept, sourceColdStart), nil
}

func noteRowsToItems(rows []postgres.NoteRow, source string) []FeedItem {
	out := make([]FeedItem, 0, len(rows))
	for _, n := range rows {
		out = append(out, FeedItem{
			PostID: n.PostID, AuthorID: n.AuthorID, CreatedAt: n.CreatedAt,
			ContentType: NoteContentType, Source: source,
		})
	}
	return out
}

func (s *Service) loadForYouSnapshot(ctx context.Context, viewerID uuid.UUID, id string) *forYouSnapshot {
	if s.rdb == nil {
		return nil
	}
	raw, err := s.rdb.Get(ctx, notesForYouKey(viewerID)).Bytes()
	if err != nil {
		return nil
	}
	var snap forYouSnapshot
	if json.Unmarshal(raw, &snap) != nil || snap.ID != id {
		return nil
	}
	return &snap
}

func (s *Service) storeForYouSnapshot(ctx context.Context, viewerID uuid.UUID, snap *forYouSnapshot) {
	if s.rdb == nil {
		return
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		return
	}
	if err := s.rdb.Set(ctx, notesForYouKey(viewerID), raw, forYouSnapshotTTL).Err(); err != nil {
		log.Printf("echo: For You snapshot for %s not stored: %v", viewerID, err)
	}
}

func newSnapshotID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return hex.EncodeToString(b[:])
}

