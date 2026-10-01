package service

// Live surfaces (2 Oct 2026): live in PostTube (wide) and in the Reels Live
// tab (vertical). A stream carries an orientation and a category, every row
// carries its host card, and discovery — live now, upcoming, by category,
// live creators, one creator's streams — goes through the same viewer check
// as the single read (canSee: visibility and blocks; a signed-out caller
// sees public streams only).

import (
	"context"
	"errors"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// --- orientation and category ---

// normalizeOrientation returns "landscape" for empty, the value for a known
// orientation, and "" for anything else.
func normalizeOrientation(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", postgres.OrientationLandscape:
		return postgres.OrientationLandscape
	case postgres.OrientationPortrait:
		return postgres.OrientationPortrait
	default:
		return ""
	}
}

const (
	// categoryCacheTTL is how long a fetched taxonomy is used without
	// asking post-service again.
	categoryCacheTTL = 10 * time.Minute
	// categoryRetryAfter spaces the retries while post-service is failing.
	categoryRetryAfter = 30 * time.Second
	categoryTimeout    = 3 * time.Second
	maxCategorySlugLen = 64
)

// categoryCache is the last taxonomy post-service answered with. It is kept
// past its TTL: while post-service is unreachable, exactly the slugs seen
// before stay acceptable.
type categoryCache struct {
	mu        sync.Mutex
	labels    map[string]string // slug -> label; nil until a first answer
	fetchedAt time.Time
	failedAt  time.Time
}

// categoryLabels returns the taxonomy as slug -> label: fresh when
// post-service answers (or answered within categoryCacheTTL), otherwise
// whatever was seen last, which is nil when it never answered.
func (s *Service) categoryLabels(ctx context.Context) map[string]string {
	c := &s.catalog
	now := s.clock()
	c.mu.Lock()
	labels := c.labels
	fresh := labels != nil && now.Sub(c.fetchedAt) < categoryCacheTTL
	backoff := !c.failedAt.IsZero() && now.Sub(c.failedAt) < categoryRetryAfter
	c.mu.Unlock()
	if fresh || backoff || s.categories == nil {
		return labels
	}
	fctx, cancel := context.WithTimeout(ctx, categoryTimeout)
	defer cancel()
	list, err := s.categories.Categories(fctx)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		c.failedAt = now
		slog.Warn("live-v2: category taxonomy not refreshed; using the last one seen", "known", len(c.labels), "err", err)
		return c.labels
	}
	next := make(map[string]string, len(list))
	for _, cat := range list {
		next[cat.Slug] = cat.Label
	}
	c.labels, c.fetchedAt, c.failedAt = next, now, time.Time{}
	return next
}

// validCategory normalises a stream's category. Empty is allowed (no
// category); anything else must be a slug of the taxonomy as last seen.
func (s *Service) validCategory(ctx context.Context, raw string) (string, error) {
	slug := strings.ToLower(strings.TrimSpace(raw))
	if slug == "" {
		return "", nil
	}
	if len(slug) > maxCategorySlugLen {
		return "", ErrInvalidCategory
	}
	if _, ok := s.categoryLabels(ctx)[slug]; !ok {
		return "", ErrInvalidCategory
	}
	return slug, nil
}

// --- user cards ---

const (
	cardCacheTTL   = 60 * time.Second
	cardCacheMax   = 20000
	profileTimeout = 2 * time.Second
)

// cardCache remembers profile lookups (found or not) for cardCacheTTL.
type cardCache struct {
	mu sync.Mutex
	m  map[uuid.UUID]cardEntry
}

type cardEntry struct {
	profile Profile
	found   bool
	at      time.Time
}

// userCards builds the card of each of ids: the user id always, the profile
// when the lookup answered (cached 60s), and the creator badges (read from
// this service's own table every time, so a badge just earned or revoked
// shows at once). A failed profile lookup leaves the cards with the user id
// only; it is never an error.
func (s *Service) userCards(ctx context.Context, ids []uuid.UUID) map[uuid.UUID]*postgres.UserCard {
	out := make(map[uuid.UUID]*postgres.UserCard, len(ids))
	uniq := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if _, dup := out[id]; dup || id == uuid.Nil {
			continue
		}
		out[id] = &postgres.UserCard{UserID: id}
		uniq = append(uniq, id)
	}
	if len(uniq) == 0 {
		return out
	}
	now := s.clock()
	var missing []uuid.UUID
	s.cards.mu.Lock()
	for _, id := range uniq {
		e, ok := s.cards.m[id]
		if !ok || now.Sub(e.at) >= cardCacheTTL {
			missing = append(missing, id)
			continue
		}
		applyProfile(out[id], e)
	}
	s.cards.mu.Unlock()
	if len(missing) > 0 && s.profiles != nil {
		pctx, cancel := context.WithTimeout(ctx, profileTimeout)
		found, err := s.profiles.Profiles(pctx, missing)
		cancel()
		if err != nil {
			slog.Warn("live-v2: profile lookup failed; cards carry the user id only", "users", len(missing), "err", err)
		} else {
			s.cards.mu.Lock()
			if s.cards.m == nil || len(s.cards.m) > cardCacheMax {
				s.cards.m = map[uuid.UUID]cardEntry{}
			}
			for _, id := range missing {
				p, ok := found[id]
				e := cardEntry{profile: p, found: ok, at: now}
				s.cards.m[id] = e
				applyProfile(out[id], e)
			}
			s.cards.mu.Unlock()
		}
	}
	if badges, err := s.store.BadgesFor(ctx, uniq); err != nil {
		slog.Warn("live-v2: badge lookup failed; cards carry no badges", "err", err)
	} else {
		for id, list := range badges {
			if card := out[id]; card != nil && len(list) > 0 {
				card.Badges = append([]string{}, list...)
			}
		}
	}
	return out
}

func applyProfile(card *postgres.UserCard, e cardEntry) {
	if !e.found {
		return
	}
	card.Name, card.Handle, card.AvatarURL = e.profile.Name, e.profile.Handle, e.profile.AvatarURL
}

// hydrateCreators puts the host card on every row. Rows are copied, never
// mutated.
func (s *Service) hydrateCreators(ctx context.Context, rows []*postgres.LiveStream) []*postgres.LiveStream {
	if len(rows) == 0 {
		return rows
	}
	ids := make([]uuid.UUID, len(rows))
	for i, st := range rows {
		ids[i] = st.CreatorUserID
	}
	cards := s.userCards(ctx, ids)
	out := make([]*postgres.LiveStream, len(rows))
	for i, st := range rows {
		cp := *st
		if card := cards[st.CreatorUserID]; card != nil {
			c := *card
			cp.Creator = &c
		}
		out[i] = &cp
	}
	return out
}

// withCreator is hydrateCreators for one row.
func (s *Service) withCreator(ctx context.Context, st *postgres.LiveStream) *postgres.LiveStream {
	return s.hydrateCreators(ctx, []*postgres.LiveStream{st})[0]
}

// decorate is what every listed or read row gets: moderator_user_ids and
// has_ingress for the host and moderators, and the host card for everyone.
func (s *Service) decorate(ctx context.Context, viewerID uuid.UUID, rows []*postgres.LiveStream) []*postgres.LiveStream {
	return s.hydrateCreators(ctx, s.decorateModerators(ctx, viewerID, rows))
}

// withReminders sets reminder_count on every row and reminder_set for a
// signed-in viewer. Rows are copied. Best effort: a lookup error leaves the
// fields off.
func (s *Service) withReminders(ctx context.Context, viewerID uuid.UUID, rows []*postgres.LiveStream) []*postgres.LiveStream {
	if len(rows) == 0 {
		return rows
	}
	ids := make([]uuid.UUID, len(rows))
	for i, st := range rows {
		ids[i] = st.ID
	}
	stats, err := s.store.ReminderStats(ctx, ids, viewerID)
	if err != nil {
		slog.Warn("live-v2: reminder lookup failed", "err", err)
		return rows
	}
	out := make([]*postgres.LiveStream, len(rows))
	for i, st := range rows {
		cp := *st
		stat := stats[st.ID]
		n := stat.Count
		cp.ReminderCount = &n
		if viewerID != uuid.Nil {
			set := stat.Set
			cp.ReminderSet = &set
		}
		out[i] = &cp
	}
	return out
}

// --- visibility over a list ---

// visible answers canSee for every row. A signed-in viewer's relationships
// to the rows' creators are read in one batch when the graph client can;
// a failed batch hides every row that needed it (fail closed, as canSee
// does on an error).
func (s *Service) visible(ctx context.Context, viewerID uuid.UUID, rows []*postgres.LiveStream) []bool {
	out := make([]bool, len(rows))
	batch, canBatch := s.graph.(batchGraph)
	if viewerID == uuid.Nil || !canBatch {
		for i, st := range rows {
			out[i] = s.canSee(ctx, st, viewerID) == nil
		}
		return out
	}
	var creators []uuid.UUID
	seen := map[uuid.UUID]bool{}
	for _, st := range rows {
		if st.CreatorUserID != viewerID && !seen[st.CreatorUserID] {
			seen[st.CreatorUserID] = true
			creators = append(creators, st.CreatorUserID)
		}
	}
	var rels map[uuid.UUID]Relationship // nil after a failed batch: nobody has an answer
	if len(creators) > 0 {
		var err error
		if rels, err = batch.Relationships(ctx, viewerID, creators); err != nil {
			slog.Warn("live-v2: relationship batch failed; other people's streams are hidden from this list", "err", err)
			rels = nil
		}
	}
	lookup := func(creatorID uuid.UUID) (Relationship, error) {
		rel, ok := rels[creatorID]
		if !ok {
			return Relationship{}, errors.New("no relationship answer for the creator")
		}
		return rel, nil
	}
	for i, st := range rows {
		out[i] = canSeeBy(st, viewerID, lookup) == nil
	}
	return out
}

// pageRounds bounds how many batches one page may read while it looks for
// rows the viewer may see.
const pageRounds = 5

// pageVisible fills one page with rows the viewer may see. It reads
// over-fetched batches from `cursor` on, keeps the visible rows up to limit,
// and resumes after the last row READ (visible or not), so a run of hidden
// streams neither ends a listing early nor repeats. A page comes back short
// (with a cursor to carry on from) only after pageRounds batches.
// NextCursor is empty when the store has nothing more.
func (s *Service) pageVisible(ctx context.Context, viewerID uuid.UUID, limit int, cursor string,
	fetch func(batch int, cursor string) ([]*postgres.LiveStream, error), cursorOf func(*postgres.LiveStream) string) (*ListLiveResult, error) {
	batch := limit * 2
	out := make([]*postgres.LiveStream, 0, limit)
	next := ""
	for round := 0; round < pageRounds; round++ {
		rows, err := fetch(batch, cursor)
		if err != nil {
			return nil, err
		}
		vis := s.visible(ctx, viewerID, rows)
		var last *postgres.LiveStream
		readAll := true
		for i, st := range rows {
			if len(out) == limit {
				readAll = false
				break
			}
			last = st
			if vis[i] {
				out = append(out, st)
			}
		}
		// More may follow when rows of this batch were left unread, or the
		// store handed back a full batch.
		if last == nil || (readAll && len(rows) < batch) {
			next = ""
			break
		}
		next = cursorOf(last)
		cursor = next
		if len(out) == limit || next == "" {
			break
		}
	}
	return &ListLiveResult{Streams: out, NextCursor: next}, nil
}

// --- discovery ---

// DiscoverParams are the filters of the live and upcoming listings.
type DiscoverParams struct {
	Orientation string // "" = any
	Category    string // "" = any
	// Following keeps only streams by users the viewer follows or whose
	// channel the viewer subscribes to.
	Following bool
	Sort      string // live only: "viewers" (default) or "recent"
	Limit     int
	Cursor    string
}

func pageLimit(limit int) int {
	if limit <= 0 || limit > 50 {
		return 20
	}
	return limit
}

// streamFilter turns the request's filters into the store's. nothing=true
// means the answer is the empty list without asking the store: the Following
// filter for a signed-out caller, for a viewer who follows nobody, or when
// the follow / subscription lookup failed (fail closed).
func (s *Service) streamFilter(ctx context.Context, viewerID uuid.UUID, p DiscoverParams) (f postgres.StreamFilter, nothing bool, err error) {
	if o := strings.ToLower(strings.TrimSpace(p.Orientation)); o != "" {
		if o != postgres.OrientationLandscape && o != postgres.OrientationPortrait {
			return f, false, ErrInvalidOrientation
		}
		f.Orientation = o
	}
	f.Category = strings.ToLower(strings.TrimSpace(p.Category))
	if p.Following {
		ids, ok := s.followedCreators(ctx, viewerID)
		if !ok || len(ids) == 0 {
			return f, true, nil
		}
		f.CreatorIDs = ids
	}
	return f, false, nil
}

// followedCreators is who the viewer follows or subscribes to. ok=false when
// that is unknown: signed out, not configured, or a failed lookup.
func (s *Service) followedCreators(ctx context.Context, viewerID uuid.UUID) ([]uuid.UUID, bool) {
	if viewerID == uuid.Nil || s.following == nil {
		return nil, false
	}
	ids, err := s.following.FollowedCreatorIDs(ctx, viewerID)
	if err != nil {
		slog.Warn("live-v2: following lookup failed; the Following filter lists nothing", "err", err)
		return nil, false
	}
	return ids, true
}

// viewersCursorPrefix tags a cursor of the most-watched order:
// "v<viewer_count>:<unix_micros>:<uuid>".
const viewersCursorPrefix = "v"

func encodeViewersCursor(st *postgres.LiveStream) string {
	if st.StartedAt == nil {
		return ""
	}
	return viewersCursorPrefix + strconv.Itoa(st.ViewerCount) + ":" + encodeCursor(*st.StartedAt, st.ID)
}

// parseViewersCursor is forgiving like parseCursor: anything malformed is
// the head of the list.
func parseViewersCursor(cursor string) (*int, *time.Time, *uuid.UUID) {
	if !strings.HasPrefix(cursor, viewersCursorPrefix) {
		return nil, nil, nil
	}
	parts := strings.SplitN(strings.TrimPrefix(cursor, viewersCursorPrefix), ":", 2)
	if len(parts) != 2 {
		return nil, nil, nil
	}
	n, err := strconv.Atoi(parts[0])
	if err != nil {
		return nil, nil, nil
	}
	t, id, _ := parseCursor(parts[1])
	if t == nil || id == nil {
		return nil, nil, nil
	}
	return &n, t, id
}

// DiscoverLive is GET /streams?status=live: streams on air the viewer may
// see, most-watched first (or newest first), filtered by orientation,
// category and Following.
//
// The most-watched order pages on a count that moves while people join and
// leave, so a stream can repeat or be missed across pages; a client that
// needs a stable walk uses sort=recent.
func (s *Service) DiscoverLive(ctx context.Context, viewerID uuid.UUID, p DiscoverParams) (*ListLiveResult, error) {
	limit := pageLimit(p.Limit)
	sortBy := strings.ToLower(strings.TrimSpace(p.Sort))
	switch sortBy {
	case "":
		sortBy = postgres.SortViewers
	case postgres.SortViewers, postgres.SortRecent:
	default:
		return nil, ErrInvalidSort
	}
	filter, nothing, err := s.streamFilter(ctx, viewerID, p)
	if err != nil {
		return nil, err
	}
	if nothing {
		return &ListLiveResult{Streams: []*postgres.LiveStream{}}, nil
	}
	res, err := s.pageLive(ctx, viewerID, sortBy, filter, limit, p.Cursor)
	if err != nil {
		return nil, err
	}
	res.Streams = s.decorate(ctx, viewerID, res.Streams)
	return res, nil
}

// startedCursor is the newest-first keyset of a stream on air.
func startedCursor(st *postgres.LiveStream) string {
	if st.StartedAt == nil {
		return ""
	}
	return encodeCursor(*st.StartedAt, st.ID)
}

// pageLive pages the streams on air in sortBy's order.
func (s *Service) pageLive(ctx context.Context, viewerID uuid.UUID, sortBy string, filter postgres.StreamFilter, limit int, cursor string) (*ListLiveResult, error) {
	cursorOf := startedCursor
	if sortBy == postgres.SortViewers {
		cursorOf = encodeViewersCursor
	}
	return s.pageVisible(ctx, viewerID, limit, cursor, func(batch int, cur string) ([]*postgres.LiveStream, error) {
		lp := postgres.ListLiveParams{Sort: sortBy, Filter: filter, Limit: batch}
		if sortBy == postgres.SortViewers {
			lp.ViewersBefore, lp.StartedBefore, lp.IDBefore = parseViewersCursor(cur)
		} else {
			lp.StartedBefore, lp.IDBefore, _ = parseCursor(cur)
		}
		return s.store.ListLive(ctx, lp)
	}, cursorOf)
}

// upcoming pages scheduled streams whose scheduled_at is after `after`,
// soonest first, with reminder_count / reminder_set on every row. With
// unstarted it pages EVERY scheduled stream instead — overdue and timeless
// ones too, the timeless last (`after` is then unused).
func (s *Service) upcoming(ctx context.Context, viewerID uuid.UUID, filter postgres.StreamFilter, after time.Time, unstarted bool, limit int, cursor string) (*ListLiveResult, error) {
	res, err := s.pageVisible(ctx, viewerID, limit, cursor, func(batch int, cur string) ([]*postgres.LiveStream, error) {
		sp := postgres.ListScheduledParams{Now: after, Filter: filter, Limit: batch, Unstarted: unstarted}
		sp.ScheduledAfter, sp.IDAfter, _ = parseCursor(cur)
		return s.store.ListScheduled(ctx, sp)
	}, func(st *postgres.LiveStream) string {
		return encodeCursor(postgres.ScheduledSortKey(st), st.ID)
	})
	if err != nil {
		return nil, err
	}
	res.Streams = s.withReminders(ctx, viewerID, s.decorate(ctx, viewerID, res.Streams))
	return res, nil
}

// DiscoverUpcoming is GET /streams/upcoming: scheduled streams whose
// scheduled_at is still in the future, soonest first.
func (s *Service) DiscoverUpcoming(ctx context.Context, viewerID uuid.UUID, p DiscoverParams) (*ListLiveResult, error) {
	filter, nothing, err := s.streamFilter(ctx, viewerID, p)
	if err != nil {
		return nil, err
	}
	if nothing {
		return &ListLiveResult{Streams: []*postgres.LiveStream{}}, nil
	}
	return s.upcoming(ctx, viewerID, filter, s.clock().UTC(), false, pageLimit(p.Limit), p.Cursor)
}

// aggregateScan bounds how many on-air streams the category and creator
// summaries read (the most-watched first).
const aggregateScan = 500

// onAirVisible is the on-air streams the viewer may see, most-watched first.
func (s *Service) onAirVisible(ctx context.Context, viewerID uuid.UUID, filter postgres.StreamFilter) ([]*postgres.LiveStream, error) {
	rows, err := s.store.ListLive(ctx, postgres.ListLiveParams{Limit: aggregateScan, Sort: postgres.SortViewers, Filter: filter})
	if err != nil {
		return nil, err
	}
	vis := s.visible(ctx, viewerID, rows)
	out := rows[:0:0]
	for i, st := range rows {
		if vis[i] {
			out = append(out, st)
		}
	}
	return out, nil
}

// LiveCategory is one category that has a stream on air.
type LiveCategory struct {
	Slug        string `json:"slug"`
	Label       string `json:"label"`
	LiveCount   int    `json:"live_count"`
	ViewerCount int    `json:"viewer_count"`
}

// LiveCategories is GET /categories/live: the categories with at least one
// stream on air that the viewer may see, by viewers. A stream without a
// category is in none. The label is post-service's; the slug stands in when
// the taxonomy is not known. orientation ("" = any) counts only streams of
// that orientation.
func (s *Service) LiveCategories(ctx context.Context, viewerID uuid.UUID, orientation string) ([]LiveCategory, error) {
	filter, _, err := s.streamFilter(ctx, viewerID, DiscoverParams{Orientation: orientation})
	if err != nil {
		return nil, err
	}
	rows, err := s.onAirVisible(ctx, viewerID, filter)
	if err != nil {
		return nil, err
	}
	agg := map[string]*LiveCategory{}
	for _, st := range rows {
		if st.Category == "" {
			continue
		}
		c := agg[st.Category]
		if c == nil {
			c = &LiveCategory{Slug: st.Category}
			agg[st.Category] = c
		}
		c.LiveCount++
		c.ViewerCount += st.ViewerCount
	}
	out := make([]LiveCategory, 0, len(agg))
	if len(agg) == 0 {
		return out, nil
	}
	labels := s.categoryLabels(ctx)
	for _, c := range agg {
		c.Label = labels[c.Slug]
		if c.Label == "" {
			c.Label = c.Slug
		}
		out = append(out, *c)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.ViewerCount != b.ViewerCount:
			return a.ViewerCount > b.ViewerCount
		case a.LiveCount != b.LiveCount:
			return a.LiveCount > b.LiveCount
		}
		return a.Slug < b.Slug
	})
	return out, nil
}

// LiveCreator is one creator who is on air.
type LiveCreator struct {
	Creator     *postgres.UserCard `json:"creator"`
	StreamID    uuid.UUID          `json:"stream_id"`
	ViewerCount int                `json:"viewer_count"`
	Orientation string             `json:"orientation"`
}

// LiveCreators is GET /creators/live: creators on air the viewer may see,
// by viewers, one row per creator (their most-watched stream). orientation
// ("" = any) keeps only streams of that orientation.
func (s *Service) LiveCreators(ctx context.Context, viewerID uuid.UUID, limit int, orientation string) ([]LiveCreator, error) {
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}
	filter, _, err := s.streamFilter(ctx, viewerID, DiscoverParams{Orientation: orientation})
	if err != nil {
		return nil, err
	}
	rows, err := s.onAirVisible(ctx, viewerID, filter)
	if err != nil {
		return nil, err
	}
	seen := map[uuid.UUID]bool{}
	picked := make([]*postgres.LiveStream, 0, limit)
	for _, st := range rows {
		if len(picked) == limit {
			break
		}
		if seen[st.CreatorUserID] {
			continue
		}
		seen[st.CreatorUserID] = true
		picked = append(picked, st)
	}
	ids := make([]uuid.UUID, len(picked))
	for i, st := range picked {
		ids[i] = st.CreatorUserID
	}
	cards := s.userCards(ctx, ids)
	out := make([]LiveCreator, 0, len(picked))
	for _, st := range picked {
		out = append(out, LiveCreator{
			Creator: cards[st.CreatorUserID], StreamID: st.ID,
			ViewerCount: st.ViewerCount, Orientation: st.Orientation,
		})
	}
	return out, nil
}

// A creator's streams (the channel Live tab).
const (
	UserStreamsLive     = "live"
	UserStreamsUpcoming = "upcoming"
	UserStreamsPast     = "past"
)

// ErrInvalidUserStreamsStatus is a ?status= the creator listing does not know.
var ErrInvalidUserStreamsStatus = errors.New("invalid: status must be live, upcoming or past")

// UserStreams is GET /users/:userId/streams?status=: one creator's streams
// that the viewer may see.
//
//	live      on air, newest first.
//	upcoming  scheduled, soonest first, with reminder_set / reminder_count.
//	          For everyone but the creator only streams whose time is still
//	          ahead. The creator gets ALL their unstarted streams: overdue
//	          ones and ones with no time too, the timeless last, to start
//	          or edit them.
//	past      ended streams that were live, newest end first.
func (s *Service) UserStreams(ctx context.Context, viewerID, userID uuid.UUID, status string, limit int, cursor string) (*ListLiveResult, error) {
	limit = pageLimit(limit)
	filter := postgres.StreamFilter{CreatorIDs: []uuid.UUID{userID}}
	switch status {
	case "", UserStreamsLive:
		res, err := s.pageLive(ctx, viewerID, postgres.SortRecent, filter, limit, cursor)
		if err != nil {
			return nil, err
		}
		res.Streams = s.decorate(ctx, viewerID, res.Streams)
		return res, nil
	case UserStreamsUpcoming:
		mine := viewerID != uuid.Nil && viewerID == userID
		return s.upcoming(ctx, viewerID, filter, s.clock().UTC(), mine, limit, cursor)
	case UserStreamsPast:
		res, err := s.pageVisible(ctx, viewerID, limit, cursor, func(batch int, cur string) ([]*postgres.LiveStream, error) {
			pp := postgres.ListPastParams{Filter: filter, Limit: batch}
			pp.EndedBefore, pp.IDBefore, _ = parseCursor(cur)
			return s.store.ListPast(ctx, pp)
		}, func(st *postgres.LiveStream) string {
			if st.EndedAt == nil {
				return ""
			}
			return encodeCursor(*st.EndedAt, st.ID)
		})
		if err != nil {
			return nil, err
		}
		res.Streams = s.decorate(ctx, viewerID, res.Streams)
		return res, nil
	default:
		return nil, ErrInvalidUserStreamsStatus
	}
}

// --- editing a scheduled stream ---

// UpdateStreamParams is PATCH /streams/:id. A nil field is left alone;
// CoverMediaID and ScheduledAt are written (cleared when nil) only when
// their Set flag is true.
type UpdateStreamParams struct {
	Title       *string
	Description *string
	Category    *string
	Visibility  *string
	Orientation *string

	SetCoverMediaID bool
	CoverMediaID    *uuid.UUID
	SetScheduledAt  bool
	ScheduledAt     *time.Time
}

// UpdateStream edits a stream that has not started: only the host, only
// while it is 'scheduled' (ErrStateConflict otherwise). Paid visibility is
// refused as on create.
func (s *Service) UpdateStream(ctx context.Context, streamID, hostID uuid.UUID, p UpdateStreamParams) (*postgres.LiveStream, error) {
	st, err := s.requireCreator(ctx, streamID, hostID)
	if err != nil {
		return nil, err
	}
	// Refused here, before the fields are looked at (a stream that started
	// answers 409 whatever the body says), and again by the store's UPDATE,
	// whose WHERE covers a stream that starts in between.
	if st.Status != postgres.StatusScheduled {
		return nil, ErrStateConflict
	}
	patch := postgres.StreamPatch{
		SetCoverMediaID: p.SetCoverMediaID, CoverMediaID: p.CoverMediaID,
		SetScheduledAt: p.SetScheduledAt, ScheduledAt: p.ScheduledAt,
	}
	if p.Title != nil {
		title := strings.TrimSpace(*p.Title)
		if title == "" {
			return nil, ErrInvalidTitle
		}
		patch.Title = &title
	}
	if p.Description != nil {
		d := strings.TrimSpace(*p.Description)
		patch.Description = &d
	}
	if p.Category != nil {
		c, err := s.validCategory(ctx, *p.Category)
		if err != nil {
			return nil, err
		}
		patch.Category = &c
	}
	if p.Visibility != nil {
		// Unlike create, an empty value is not "public": a field that is
		// sent says what it means.
		v := strings.TrimSpace(*p.Visibility)
		if v == "" || normalizeVisibility(v) == "" {
			return nil, ErrInvalidVisibility
		}
		v = normalizeVisibility(v)
		if v == visibilityPaid {
			return nil, ErrPaidVisibility
		}
		patch.Visibility = &v
	}
	if p.Orientation != nil {
		o := strings.TrimSpace(*p.Orientation)
		if o == "" || normalizeOrientation(o) == "" {
			return nil, ErrInvalidOrientation
		}
		o = normalizeOrientation(o)
		patch.Orientation = &o
	}
	next, err := s.store.UpdateScheduled(ctx, streamID, patch)
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		return nil, ErrStreamNotFound
	case errors.Is(err, postgres.ErrStateConflict):
		return nil, ErrStateConflict
	case err != nil:
		return nil, err
	}
	rows := s.withReminders(ctx, hostID, s.decorate(ctx, hostID, []*postgres.LiveStream{next}))
	return rows[0], nil
}

// --- reminders ---

// ReminderResult is the answer of PUT / DELETE /streams/:id/reminder.
type ReminderResult struct {
	ReminderSet   bool `json:"reminder_set"`
	ReminderCount int  `json:"reminder_count"`
}

// SetReminder adds or removes the viewer's "Notify me" on a stream they may
// watch (the viewer token's gate: visibility, blocks, stream ban) that is
// still 'scheduled'. Idempotent both ways.
func (s *Service) SetReminder(ctx context.Context, streamID, viewerID uuid.UUID, set bool) (*ReminderResult, error) {
	st, err := s.store.GetByID(ctx, streamID)
	if err != nil {
		return nil, mapStoreErr(err)
	}
	if err := s.authorizeViewer(ctx, st, viewerID); err != nil {
		return nil, err
	}
	// "Only while scheduled" is the store's check, made on the locked row so
	// a stream that starts at this moment cannot gain a reminder.
	n, err := s.store.SetReminder(ctx, streamID, viewerID, set)
	switch {
	case errors.Is(err, postgres.ErrNotFound):
		return nil, ErrStreamNotFound
	case errors.Is(err, postgres.ErrStateConflict):
		return nil, ErrStateConflict
	case err != nil:
		return nil, err
	}
	return &ReminderResult{ReminderSet: set, ReminderCount: n}, nil
}

// ReminderPage is one page of the viewers to remind.
type ReminderPage struct {
	UserIDs   []string `json:"user_ids"`
	NextAfter string   `json:"next_after"`
	HasMore   bool     `json:"has_more"`
}

// ReminderPageMax is the largest page of the internal reminders route.
const ReminderPageMax = 1000

// ReminderUserIDs pages who set a reminder on the stream, by user id, after
// `after` (uuid.Nil = from the start): notification-service's read when it
// consumes live.stream.started. The shape is post-service's subscriber-ids
// page: has_more is true when the page came back full.
func (s *Service) ReminderUserIDs(ctx context.Context, streamID, after uuid.UUID, limit int) (*ReminderPage, error) {
	if limit <= 0 || limit > ReminderPageMax {
		limit = ReminderPageMax
	}
	if _, err := s.store.GetByID(ctx, streamID); err != nil {
		return nil, mapStoreErr(err)
	}
	ids, err := s.store.ListReminderUserIDs(ctx, streamID, after, limit)
	if err != nil {
		return nil, err
	}
	page := &ReminderPage{UserIDs: make([]string, 0, len(ids)), HasMore: len(ids) == limit}
	for _, id := range ids {
		page.UserIDs = append(page.UserIDs, id.String())
	}
	if len(ids) > 0 {
		page.NextAfter = ids[len(ids)-1].String()
	}
	return page, nil
}
