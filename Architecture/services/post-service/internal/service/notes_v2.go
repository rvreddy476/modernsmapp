// notes_v2.go is Echo v2's server side (docs/designs/echo-v2-contract.md
// §1-3): the Note JSON's new fields, polls on notes, scheduled notes and
// their publisher, bookmarks, views, quotes, trends, hashtag pages and
// search.
//
// Storage reuse: polls are the post poll tables (polls, poll_options,
// poll_votes, one vote per person through InsertPollVote's advisory lock);
// bookmarks are saved_items rows (target_type 'post'); scheduling is
// posts.publish_at (migration 042), published by internal/noteschedule
// (the ordinary post schedule worker skips notes). Views and the
// publisher's retry state are migration 083.
package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/atpost/post-service/internal/store/scylla"
	"github.com/atpost/shared/events"
	"github.com/google/uuid"
)

// The v2 contract's numbers.
const (
	NotePollMinOptions     = 2
	NotePollMaxOptions     = 4
	NotePollLabelMaxRunes  = 25
	NotePollMinDuration    = 5 * time.Minute
	NotePollMaxDuration    = 7 * 24 * time.Hour
	NoteScheduleMinLead    = 5 * time.Minute
	NoteScheduleMaxMonths  = 18
	NoteViewsMaxBatch      = 50
	NoteTrendsDefault      = 10
	NoteTrendsMax          = 30
	NoteTrendsWindow       = 24 * time.Hour
	NoteTrendsMinAuthors   = 3
	notePublishRetry       = time.Hour
	notePublishRetryBrief  = 5 * time.Minute
	notePublishGiveUpAfter = 24 * time.Hour
	notePublishLease       = 10 * time.Minute
)

// Note statuses.
const (
	NoteStatusPublished = "published"
	NoteStatusScheduled = "scheduled"
	NoteStatusFailed    = "failed"
)

// Publish failure reasons (publish_error).
const (
	NotePublishPremiumRequired    = "premium_required"
	NotePublishPremiumUnavailable = "premium_check_unavailable"
	NotePublishSuspended          = "suspended"
	NotePublishStandingUnknown    = "standing_unavailable"
)

// v2 errors; the HTTP layer maps each to the contract's code.
var (
	ErrInvalidNotePoll  = errors.New("invalid poll")
	ErrNotePollMedia    = errors.New("a poll note may not carry media")
	ErrInvalidSchedule  = errors.New("invalid schedule")
	ErrNotePollClosed   = errors.New("this poll has closed")
	ErrNoteAlreadyVoted = errors.New("you already voted on this poll")
	ErrOwnNotePoll      = errors.New("you cannot vote on your own poll")
)

// NotePollInput is POST /v1/notes' poll.
type NotePollInput struct {
	Options         []string
	DurationMinutes int
}

// NotePoll is the Note JSON's poll.
type NotePoll struct {
	Options    []NotePollOption `json:"options"`
	TotalVotes int64            `json:"total_votes"`
	EndsAt     *time.Time       `json:"ends_at"`
	Closed     bool             `json:"closed"`
	ViewerVote *int             `json:"viewer_vote"`
}

// NotePollOption is one choice. Votes is null until the viewer may see
// the counts.
type NotePollOption struct {
	Index int    `json:"index"`
	Label string `json:"label"`
	Votes *int64 `json:"votes"`
}

// NoteTrend is one row of GET /v1/notes/trends.
type NoteTrend struct {
	Tag       string `json:"tag"`
	NoteCount int64  `json:"note_count"`
	Rank      int    `json:"rank"`
}

// BookmarkState is POST/DELETE /v1/notes/:id/bookmark.
type BookmarkState struct {
	Bookmarked bool  `json:"bookmarked"`
	Count      int64 `json:"count"`
}

// noteV2Store is what the v2 paths need beyond noteStore; *postgres.Store
// and the in-memory notetest.Store both have it.
type noteV2Store interface {
	GetScheduledNote(ctx context.Context, id uuid.UUID) (*postgres.ScheduledNote, error)
	ListScheduledNotes(ctx context.Context, authorID uuid.UUID, cursor string, limit int) ([]postgres.ScheduledNote, string, error)
	RescheduleNote(ctx context.Context, id, authorID uuid.UUID, at time.Time) (bool, error)
	ClaimDueNotes(ctx context.Context, now, leaseUntil time.Time, limit int) ([]uuid.UUID, error)
	PublishScheduledNote(ctx context.Context, w *postgres.NotePublish) (*postgres.NotePublished, error)
	RecordNotePublishFailure(ctx context.Context, id uuid.UUID, reason string, now, retryAt, giveUpBefore time.Time) (bool, error)

	GetPollsByPostIDs(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.PollData, error)
	GetUserPollVotesBatch(ctx context.Context, ids []uuid.UUID, userID uuid.UUID) (map[uuid.UUID][]uuid.UUID, error)
	InsertPollVote(ctx context.Context, postID, optionID, userID uuid.UUID, allowsMultiple bool) (bool, error)

	NoteCountsFor(ctx context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.NoteCounts, error)
	BatchIsBookmarked(ctx context.Context, userID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error)
	AddBookmark(ctx context.Context, userID, postID uuid.UUID) error
	RemoveBookmark(ctx context.Context, userID, postID uuid.UUID) error
	ListNoteBookmarks(ctx context.Context, userID uuid.UUID, cursor string, limit int) ([]postgres.Note, string, error)
	RecordNoteViews(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID, now time.Time) (int64, error)

	DiscoverNotes(ctx context.Context, q postgres.NoteDiscoverQuery) ([]postgres.Note, string, error)
	NoteTrends(ctx context.Context, since time.Time, minAuthors, limit int) ([]postgres.NoteTrend, error)
}

func (s *Service) notesV2() (noteV2Store, error) {
	if s.noteStoreSeam != nil {
		if v, ok := s.noteStoreSeam.(noteV2Store); ok {
			return v, nil
		}
		return nil, ErrNoteStoreUnavailable
	}
	if s.pgStore != nil {
		return s.pgStore, nil
	}
	return nil, ErrNoteStoreUnavailable
}

// ── Polls ───────────────────────────────────────────────────────────────

// validateNotePoll checks the poll rule and returns the poll row, running
// from start. A poll note carries no media (X rule).
func validateNotePoll(in *NotePollInput, mediaCount int, start time.Time) (*postgres.PollData, error) {
	if in == nil {
		return nil, nil
	}
	if mediaCount > 0 {
		return nil, ErrNotePollMedia
	}
	if n := len(in.Options); n < NotePollMinOptions || n > NotePollMaxOptions {
		return nil, fmt.Errorf("%w: a poll has %d-%d choices", ErrInvalidNotePoll, NotePollMinOptions, NotePollMaxOptions)
	}
	d := time.Duration(in.DurationMinutes) * time.Minute
	if d < NotePollMinDuration || d > NotePollMaxDuration {
		return nil, fmt.Errorf("%w: a poll runs 5 minutes to 7 days", ErrInvalidNotePoll)
	}
	seen := map[string]bool{}
	opts := make([]postgres.PollOption, 0, len(in.Options))
	for i, raw := range in.Options {
		label := strings.TrimSpace(raw)
		if label == "" || utf8.RuneCountInString(label) > NotePollLabelMaxRunes {
			return nil, fmt.Errorf("%w: choice %d must be 1-%d characters", ErrInvalidNotePoll, i+1, NotePollLabelMaxRunes)
		}
		key := strings.ToLower(label)
		if seen[key] {
			return nil, fmt.Errorf("%w: choice %d repeats another", ErrInvalidNotePoll, i+1)
		}
		seen[key] = true
		opts = append(opts, postgres.PollOption{Label: label, SortOrder: i})
	}
	ends := start.Add(d)
	return &postgres.PollData{Question: "", AllowsMultiple: false, EndsAt: &ends, Options: opts}, nil
}

// notePollView is the poll as viewer sees it: counts only after they
// voted, once the poll closed, or for the note's author.
func notePollView(poll *postgres.PollData, author uuid.UUID, viewer *uuid.UUID, viewerVotes []uuid.UUID, now time.Time) *NotePoll {
	if poll == nil {
		return nil
	}
	out := &NotePoll{Options: make([]NotePollOption, 0, len(poll.Options)), EndsAt: poll.EndsAt}
	out.Closed = poll.EndsAt != nil && !now.Before(*poll.EndsAt)
	for i, o := range poll.Options {
		out.TotalVotes += o.VoteCount
		for _, v := range viewerVotes {
			if v == o.ID && v != uuid.Nil {
				idx := i
				out.ViewerVote = &idx
			}
		}
	}
	reveal := out.Closed || out.ViewerVote != nil || (viewer != nil && *viewer == author)
	for i, o := range poll.Options {
		opt := NotePollOption{Index: i, Label: o.Label}
		if reveal {
			votes := o.VoteCount
			opt.Votes = &votes
		}
		out.Options = append(out.Options, opt)
	}
	return out
}

// attachNoteExtras fills the v2 fields of a page of published notes:
// quote and bookmark counts, the viewer's bookmark and the poll.
// Best-effort, like author hydration: a failed read leaves zeros.
func (s *Service) attachNoteExtras(ctx context.Context, views []*NoteView, viewerID *uuid.UUID) {
	if len(views) == 0 {
		return
	}
	store, err := s.notesV2()
	if err != nil {
		return
	}
	ids := make([]uuid.UUID, 0, len(views))
	for _, v := range views {
		ids = append(ids, v.Post.ID)
	}
	if counts, err := store.NoteCountsFor(ctx, ids); err == nil {
		for _, v := range views {
			c := counts[v.Post.ID]
			v.QuoteCount, v.BookmarkCount = c.Quotes, c.Bookmarks
			v.ThreadParts = c.ThreadParts
		}
	} else {
		slog.Debug("note counts skipped", "err", err)
	}
	if viewerID != nil {
		if marks, err := store.BatchIsBookmarked(ctx, *viewerID, ids); err == nil {
			for _, v := range views {
				v.ViewerHasBookmarked = marks[v.Post.ID]
			}
		}
	}
	s.attachNotePolls(ctx, store, views, viewerID)
}

func (s *Service) attachNotePolls(ctx context.Context, store noteV2Store, views []*NoteView, viewerID *uuid.UUID) {
	ids := make([]uuid.UUID, 0, len(views))
	for _, v := range views {
		ids = append(ids, v.Post.ID)
	}
	polls, err := store.GetPollsByPostIDs(ctx, ids)
	if err != nil || len(polls) == 0 {
		return
	}
	var votes map[uuid.UUID][]uuid.UUID
	if viewerID != nil {
		pollIDs := make([]uuid.UUID, 0, len(polls))
		for id := range polls {
			pollIDs = append(pollIDs, id)
		}
		votes, _ = store.GetUserPollVotesBatch(ctx, pollIDs, *viewerID)
	}
	now := s.clock()
	for _, v := range views {
		if p := polls[v.Post.ID]; p != nil {
			v.Poll = notePollView(p, v.Post.AuthorID, viewerID, votes[v.Post.ID], now)
		}
	}
}

// VoteNotePoll is POST /v1/notes/:id/poll/vote: one vote per person, final.
// The note must be one the voter may see; its author may not vote, and a
// closed poll takes no vote.
func (s *Service) VoteNotePoll(ctx context.Context, id, voter uuid.UUID, optionIndex int) (*NoteView, error) {
	v, err := s.GetNote(ctx, id, &voter)
	if err != nil {
		return nil, err
	}
	store, err := s.notesV2()
	if err != nil {
		return nil, err
	}
	polls, err := store.GetPollsByPostIDs(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	poll := polls[id]
	if poll == nil {
		return nil, fmt.Errorf("%w: this note has no poll", ErrInvalidNotePoll)
	}
	if v.Post.AuthorID == voter {
		return nil, ErrOwnNotePoll
	}
	if poll.EndsAt != nil && !s.clock().Before(*poll.EndsAt) {
		return nil, ErrNotePollClosed
	}
	if optionIndex < 0 || optionIndex >= len(poll.Options) {
		return nil, fmt.Errorf("%w: option_index out of range", ErrInvalidNotePoll)
	}
	inserted, err := store.InsertPollVote(ctx, id, poll.Options[optionIndex].ID, voter, false)
	if err != nil {
		return nil, err
	}
	if !inserted {
		return nil, ErrNoteAlreadyVoted
	}
	return s.GetNote(ctx, id, &voter)
}

// ── Scheduling ──────────────────────────────────────────────────────────

// ValidateNoteSchedule is the schedule window: 5 minutes to 18 months
// after now.
func ValidateNoteSchedule(at, now time.Time) error {
	if at.IsZero() || at.Before(now.Add(NoteScheduleMinLead)) || at.After(now.AddDate(0, NoteScheduleMaxMonths, 0)) {
		return fmt.Errorf("%w: scheduled_at must be 5 minutes to 18 months ahead", ErrInvalidSchedule)
	}
	return nil
}

// scheduledNoteView is the author's view of a scheduled note.
func (s *Service) scheduledNoteView(ctx context.Context, n *postgres.ScheduledNote) (*NoteView, error) {
	p := n.Post
	if s.pgStore != nil {
		if err := s.attachMediaState(ctx, []*postgres.Post{&p}); err != nil {
			return nil, err
		}
	}
	d := &PostDetail{Post: &p, Counts: &scylla.Counts{}}
	v := buildNoteView(d, &postgres.Note{Post: p, NoteFields: n.NoteFields})
	v.ViewerCanReply = true
	v.Status = NoteStatusScheduled
	if n.Failed {
		v.Status = NoteStatusFailed
	}
	v.ScheduledAt = p.PublishAt
	v.PublishError = n.PublishError
	author := p.AuthorID
	if a := s.noteAuthors(ctx, &author, []uuid.UUID{author}); a != nil {
		v.Author = a[author]
	}
	if store, err := s.notesV2(); err == nil {
		s.attachNotePolls(ctx, store, []*NoteView{v}, &author)
	}
	if v.Poll == nil && p.Poll != nil {
		v.Poll = notePollView(p.Poll, author, &author, nil, s.clock())
	}
	return v, nil
}

// loadOwnScheduledNote is the author's scheduled note id; anyone else's,
// or a published one, reads as not found (a scheduled note is invisible).
func (s *Service) loadOwnScheduledNote(ctx context.Context, store noteV2Store, id, caller uuid.UUID) (*postgres.ScheduledNote, error) {
	n, err := store.GetScheduledNote(ctx, id)
	if err != nil {
		return nil, err
	}
	if n == nil || n.Post.AuthorID != caller {
		return nil, ErrNoteNotFound
	}
	return n, nil
}

// ListMyScheduledNotes is GET /v1/notes/me/scheduled, soonest first.
func (s *Service) ListMyScheduledNotes(ctx context.Context, authorID uuid.UUID, cursor string, limit int) (*NotePage, error) {
	store, err := s.notesV2()
	if err != nil {
		return nil, err
	}
	rows, next, err := store.ListScheduledNotes(ctx, authorID, cursor, clampNoteLimit(limit))
	if err != nil {
		return nil, err
	}
	out := &NotePage{Items: make([]*NoteView, 0, len(rows)), NextCursor: next}
	for i := range rows {
		if rows[i].Post.AuthorID != authorID {
			continue
		}
		v, err := s.scheduledNoteView(ctx, &rows[i])
		if err != nil {
			return nil, err
		}
		out.Items = append(out.Items, v)
	}
	return out, nil
}

// RescheduleNote is PATCH /v1/notes/:id/schedule. A failed note is armed
// again (its publisher state is cleared).
func (s *Service) RescheduleNote(ctx context.Context, id, caller uuid.UUID, at time.Time) (*NoteView, error) {
	if err := ValidateNoteSchedule(at, s.clock()); err != nil {
		return nil, err
	}
	store, err := s.notesV2()
	if err != nil {
		return nil, err
	}
	if _, err := s.loadOwnScheduledNote(ctx, store, id, caller); err != nil {
		return nil, err
	}
	ok, err := store.RescheduleNote(ctx, id, caller, at.UTC())
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrNoteNotFound
	}
	n, err := s.loadOwnScheduledNote(ctx, store, id, caller)
	if err != nil {
		return nil, err
	}
	return s.scheduledNoteView(ctx, n)
}

// DeleteScheduledNote is DELETE /v1/notes/:id/schedule: the author's
// scheduled note goes (the ordinary soft delete; it never published, so
// nothing was announced).
func (s *Service) DeleteScheduledNote(ctx context.Context, id, caller uuid.UUID) error {
	store, err := s.notesV2()
	if err != nil {
		return err
	}
	if _, err := s.loadOwnScheduledNote(ctx, store, id, caller); err != nil {
		return err
	}
	if err := s.deleteOne(ctx, id, caller); err != nil {
		if errors.Is(err, ErrPostNotFound) || errors.Is(err, ErrPostForbidden) {
			return ErrNoteNotFound
		}
		return err
	}
	return nil
}

// NotePublishReport is one publisher pass.
type NotePublishReport struct {
	Claimed, Published, Deferred int
}

// PublishDueNotes is one pass of the note publisher: claim the due notes
// (SKIP LOCKED, leased) and publish each.
func (s *Service) PublishDueNotes(ctx context.Context, limit int) (NotePublishReport, error) {
	var rep NotePublishReport
	store, err := s.notesV2()
	if err != nil {
		return rep, err
	}
	now := s.clock().UTC()
	ids, err := store.ClaimDueNotes(ctx, now, now.Add(notePublishLease), limit)
	if err != nil {
		return rep, err
	}
	rep.Claimed = len(ids)
	for _, id := range ids {
		if ctx.Err() != nil {
			return rep, ctx.Err()
		}
		ok, err := s.publishScheduledNote(ctx, store, id, now)
		if err != nil {
			slog.Error("note publish failed; retried when the lease ends", "note_id", id, "err", err)
		}
		if ok {
			rep.Published++
		} else {
			rep.Deferred++
		}
	}
	return rep, nil
}

// notePublishCheck re-checks standing and Premium at publication. ""
// passes; otherwise the reason, and whether it is a transient one.
func (s *Service) notePublishCheck(ctx context.Context, author uuid.UUID) (string, bool) {
	if err := s.requirePublishStanding(ctx, author); err != nil {
		if errors.Is(err, ErrAuthorSuspended) {
			return NotePublishSuspended, false
		}
		return NotePublishStandingUnknown, true
	}
	if err := s.requirePremium(ctx, author); err != nil {
		if errors.Is(err, ErrPremiumRequired) {
			return NotePublishPremiumRequired, false
		}
		return NotePublishPremiumUnavailable, true
	}
	return "", false
}

// publishScheduledNote publishes one due note. PostCreated, the quote's
// repost row and note.quoted, mentions and the outbox nudge all happen
// here, never at create. A failed check keeps the note scheduled with a
// publish_error, retried hourly (5 minutes when the check could not
// answer) for 24 hours, then failed.
func (s *Service) publishScheduledNote(ctx context.Context, store noteV2Store, id uuid.UUID, now time.Time) (bool, error) {
	n, err := store.GetScheduledNote(ctx, id)
	if err != nil {
		return false, err
	}
	if n == nil || n.Failed || n.Post.PublishAt == nil || n.Post.PublishAt.After(now) {
		return false, nil
	}
	author := n.Post.AuthorID
	if reason, transient := s.notePublishCheck(ctx, author); reason != "" {
		retry := notePublishRetry
		if transient {
			retry = notePublishRetryBrief
		}
		failed, err := store.RecordNotePublishFailure(ctx, id, reason, now, now.Add(retry), now.Add(-notePublishGiveUpAfter))
		if err != nil {
			return false, err
		}
		slog.Info("scheduled note held", "note_id", id, "reason", reason, "failed", failed)
		return false, nil
	}

	var target *postgres.Note
	if n.QuoteOfID != nil {
		baseStore, err := s.notesStore()
		if err != nil {
			return false, err
		}
		target, err = s.loadNoteTarget(ctx, baseStore, *n.QuoteOfID, author)
		if errors.Is(err, ErrNoteNotFound) {
			target = nil // gone since: publish without the repost row
		} else if err != nil {
			return false, err
		}
	}
	live := n.Post
	live.PublishAt = nil
	live.IsScheduled = false
	live.CreatedAt = now
	live.UpdatedAt = now
	live.PublishedAt = &now
	f := n.NoteFields
	pol, _, err := noteDistribution()
	if err != nil {
		return false, err
	}
	w := &postgres.NotePublish{ID: id, Now: now}
	if target != nil {
		w.QuoteRepost = &postgres.Repost{UserID: author, OriginalPostID: target.Post.ID,
			QuoteText: live.Text, Visibility: target.Post.Visibility}
	}
	if s.noteEventsOn() {
		w.Events = func(rev int64) []postgres.ThreadOutboxEvent {
			evts := []postgres.ThreadOutboxEvent{{EventType: events.PostCreated, PostID: id,
				Payload: s.notePostCreatedRev(ctx, &live, pol, f, rev)}}
			return append(evts, noteReplyEvents(&live, f, target)...)
		}
	}
	res, err := store.PublishScheduledNote(ctx, w)
	if err != nil {
		return false, err
	}
	if res == nil {
		return false, nil
	}
	s.InvalidatePostBodyCache(ctx, id)
	s.afterNoteCreated(ctx, &live, live.MentionUsernames)
	if res.QuoteRepostWritten && w.QuoteRepost != nil {
		s.afterQuoteRepost(ctx, w.QuoteRepost, target)
	}
	slog.Info("scheduled note published", "note_id", id, "author_id", author)
	return true, nil
}

// ── Bookmarks and views ─────────────────────────────────────────────────

// SetNoteBookmark is POST (on=true) / DELETE (on=false)
// /v1/notes/:id/bookmark. Saving needs a note the viewer may see; removing
// never does (a saver can always clean up their own row). Bookmarks are
// private: only the count is public.
func (s *Service) SetNoteBookmark(ctx context.Context, id, viewer uuid.UUID, on bool) (*BookmarkState, error) {
	store, err := s.notesV2()
	if err != nil {
		return nil, err
	}
	if on {
		if _, err := s.GetNote(ctx, id, &viewer); err != nil {
			return nil, err
		}
		if err := store.AddBookmark(ctx, viewer, id); err != nil {
			return nil, err
		}
	} else if err := store.RemoveBookmark(ctx, viewer, id); err != nil {
		return nil, err
	}
	counts, err := store.NoteCountsFor(ctx, []uuid.UUID{id})
	if err != nil {
		return nil, err
	}
	return &BookmarkState{Bookmarked: on, Count: counts[id].Bookmarks}, nil
}

// ListNoteBookmarks is GET /v1/notes/bookmarks: the viewer's own saved
// notes, newest save first.
func (s *Service) ListNoteBookmarks(ctx context.Context, viewer uuid.UUID, cursor string, limit int) (*NotePage, error) {
	store, err := s.notesV2()
	if err != nil {
		return nil, err
	}
	rows, next, err := store.ListNoteBookmarks(ctx, viewer, cursor, clampNoteLimit(limit))
	if err != nil {
		return nil, err
	}
	return s.notePage(ctx, rows, next, &viewer)
}

// RecordNoteViews is POST /v1/notes/views. A signed-out batch is accepted
// and not counted; a signed-in viewer counts once per note per 24 hours,
// never on their own notes (the store applies both).
func (s *Service) RecordNoteViews(ctx context.Context, viewer *uuid.UUID, ids []uuid.UUID) error {
	if len(ids) > NoteViewsMaxBatch {
		return fmt.Errorf("%w: at most %d note_ids", ErrInvalidNoteRequest, NoteViewsMaxBatch)
	}
	if viewer == nil || len(ids) == 0 {
		return nil
	}
	store, err := s.notesV2()
	if err != nil {
		return err
	}
	_, err = store.RecordNoteViews(ctx, *viewer, dedupeUUIDs(ids), s.clock().UTC())
	return err
}

// ── Quotes, trends, hashtag pages, search ───────────────────────────────

// ListNoteQuotes is GET /v1/notes/:id/quotes, newest first.
func (s *Service) ListNoteQuotes(ctx context.Context, id uuid.UUID, viewer *uuid.UUID, cursor string, limit int) (*NotePage, error) {
	if _, err := s.GetNote(ctx, id, viewer); err != nil {
		return nil, err
	}
	return s.discoverNotes(ctx, postgres.NoteDiscoverQuery{QuoteOfID: &id, Cursor: cursor, Limit: clampNoteLimit(limit)}, viewer)
}

func noteTop(tab string) (bool, error) {
	switch tab {
	case "", "top":
		return true, nil
	case "latest":
		return false, nil
	}
	return false, ErrInvalidNoteTab
}

// NotesByHashtag is GET /v1/notes/hashtags/:tag?tab=top|latest.
func (s *Service) NotesByHashtag(ctx context.Context, tag, tab string, viewer *uuid.UUID, cursor string, limit int) (*NotePage, error) {
	top, err := noteTop(tab)
	if err != nil {
		return nil, err
	}
	tag = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(tag), "#"))
	if tag == "" {
		return nil, fmt.Errorf("%w: empty hashtag", ErrInvalidNoteRequest)
	}
	return s.discoverNotes(ctx, postgres.NoteDiscoverQuery{Hashtag: tag, Top: top, Cursor: cursor, Limit: clampNoteLimit(limit)}, viewer)
}

// SearchNotes is GET /v1/notes/search?q&tab=top|latest.
func (s *Service) SearchNotes(ctx context.Context, q, tab string, viewer *uuid.UUID, cursor string, limit int) (*NotePage, error) {
	top, err := noteTop(tab)
	if err != nil {
		return nil, err
	}
	q = strings.TrimSpace(q)
	if q == "" {
		return &NotePage{Items: []*NoteView{}}, nil
	}
	if utf8.RuneCountInString(q) > 200 {
		return nil, fmt.Errorf("%w: q is longer than 200 characters", ErrInvalidNoteRequest)
	}
	return s.discoverNotes(ctx, postgres.NoteDiscoverQuery{Search: q, Top: top, Cursor: cursor, Limit: clampNoteLimit(limit)}, viewer)
}

func (s *Service) discoverNotes(ctx context.Context, q postgres.NoteDiscoverQuery, viewer *uuid.UUID) (*NotePage, error) {
	store, err := s.notesV2()
	if err != nil {
		return nil, err
	}
	rows, next, err := store.DiscoverNotes(ctx, q)
	if err != nil {
		return nil, err
	}
	return s.notePage(ctx, rows, next, viewer)
}

// NoteTrends is GET /v1/notes/trends.
func (s *Service) NoteTrends(ctx context.Context, limit int) ([]NoteTrend, error) {
	if limit <= 0 {
		limit = NoteTrendsDefault
	}
	if limit > NoteTrendsMax {
		limit = NoteTrendsMax
	}
	store, err := s.notesV2()
	if err != nil {
		return nil, err
	}
	rows, err := store.NoteTrends(ctx, s.clock().Add(-NoteTrendsWindow), NoteTrendsMinAuthors, limit)
	if err != nil {
		return nil, err
	}
	out := make([]NoteTrend, 0, len(rows))
	for _, r := range rows {
		if r.Authors < NoteTrendsMinAuthors {
			continue
		}
		out = append(out, NoteTrend{Tag: r.Tag, NoteCount: r.NoteCount, Rank: len(out) + 1})
	}
	return out, nil
}

// HydrateNotes is the internal batch read (GET /internal/notes/batch):
// the Note JSON of ids for viewer, in order, dropping what they may not
// see. feed-service hydrates the notes timelines with it.
func (s *Service) HydrateNotes(ctx context.Context, ids []uuid.UUID, viewer *uuid.UUID) ([]*NoteView, error) {
	if len(ids) > NoteListMax {
		ids = ids[:NoteListMax]
	}
	views, err := s.loadNoteViews(ctx, ids, viewer, true)
	if err != nil {
		return nil, err
	}
	out := make([]*NoteView, 0, len(ids))
	for _, id := range ids {
		if v := views[id]; v != nil {
			out = append(out, v)
		}
	}
	return out, nil
}
