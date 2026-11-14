package notetest

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Echo v2 stand-ins (service.noteV2Store), mirroring store/postgres
// notes_v2.go's rules in memory: published = PublishAt nil; a scheduled
// note is in no listing.

// ScheduleState is note_schedules.
type ScheduleState struct {
	PublishError  *string
	Attempts      int
	FirstFailedAt *time.Time
	NextAttemptAt *time.Time
	FailedAt      *time.Time
}

// Publish is one committed PublishScheduledNote.
type Publish struct {
	ID     uuid.UUID
	Events []postgres.ThreadOutboxEvent
	Quote  *postgres.Repost
}

func (s *Store) putPoll(id uuid.UUID, p *postgres.PollData) {
	c := *p
	c.Options = append([]postgres.PollOption(nil), p.Options...)
	for i := range c.Options {
		if c.Options[i].ID == uuid.Nil {
			c.Options[i].ID = uuid.New()
		}
		c.Options[i].SortOrder = i
		p.Options[i].ID = c.Options[i].ID
	}
	s.Polls[id] = &c
}

// PutPoll attaches a poll to note id (tests).
func (s *Store) PutPoll(id uuid.UUID, p *postgres.PollData) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.putPoll(id, p)
}

func (s *Store) published(id uuid.UUID) *postgres.Note {
	n := s.Notes[id]
	if n == nil || s.Deleted[id] || n.Post.PublishAt != nil {
		return nil
	}
	return n
}

func (s *Store) GetScheduledNote(_ context.Context, id uuid.UUID) (*postgres.ScheduledNote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return nil, s.Fail
	}
	return s.scheduled(id), nil
}

func (s *Store) scheduled(id uuid.UUID) *postgres.ScheduledNote {
	n := s.Notes[id]
	if n == nil || s.Deleted[id] || n.Post.PublishAt == nil {
		return nil
	}
	out := &postgres.ScheduledNote{Note: *n}
	if st := s.Schedules[id]; st != nil {
		out.PublishError = st.PublishError
		out.Failed = st.FailedAt != nil
	}
	return out
}

func (s *Store) ListScheduledNotes(_ context.Context, authorID uuid.UUID, _ string, limit int) ([]postgres.ScheduledNote, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []postgres.ScheduledNote
	for id, n := range s.Notes {
		if n.Post.AuthorID != authorID {
			continue
		}
		if sn := s.scheduled(id); sn != nil {
			out = append(out, *sn)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Post.PublishAt.Before(*out[j].Post.PublishAt) })
	if limit > 0 && len(out) > limit {
		return out[:limit], "next", nil
	}
	return out, "", nil
}

func (s *Store) RescheduleNote(_ context.Context, id, authorID uuid.UUID, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sn := s.scheduled(id)
	if sn == nil || sn.Post.AuthorID != authorID {
		return false, nil
	}
	n := s.Notes[id]
	if p := s.Polls[id]; p != nil && p.EndsAt != nil {
		ends := at.Add(p.EndsAt.Sub(*n.Post.PublishAt))
		p.EndsAt = &ends
	}
	n.Post.PublishAt = &at
	delete(s.Schedules, id)
	return true, nil
}

func (s *Store) ClaimDueNotes(_ context.Context, now, leaseUntil time.Time, limit int) ([]uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []*postgres.Note
	for id, n := range s.Notes {
		if s.Deleted[id] || n.Post.PublishAt == nil || n.Post.PublishAt.After(now) {
			continue
		}
		if st := s.Schedules[id]; st != nil && (st.FailedAt != nil || (st.NextAttemptAt != nil && st.NextAttemptAt.After(now))) {
			continue
		}
		due = append(due, n)
	}
	sort.Slice(due, func(i, j int) bool { return due[i].Post.PublishAt.Before(*due[j].Post.PublishAt) })
	var out []uuid.UUID
	for _, n := range due {
		if limit > 0 && len(out) >= limit {
			break
		}
		st := s.Schedules[n.Post.ID]
		if st == nil {
			st = &ScheduleState{}
			s.Schedules[n.Post.ID] = st
		}
		lease := leaseUntil
		st.NextAttemptAt = &lease
		out = append(out, n.Post.ID)
	}
	return out, nil
}

func (s *Store) PublishScheduledNote(_ context.Context, w *postgres.NotePublish) (*postgres.NotePublished, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.Notes[w.ID]
	if n == nil || s.Deleted[w.ID] || n.Post.PublishAt == nil || n.Post.PublishAt.After(w.Now) {
		return nil, nil
	}
	if p := s.Polls[w.ID]; p != nil && p.EndsAt != nil {
		ends := w.Now.Add(p.EndsAt.Sub(*n.Post.PublishAt))
		p.EndsAt = &ends
	}
	n.Post.PublishAt = nil
	n.Post.IsScheduled = false
	n.Post.CreatedAt = w.Now
	out := &postgres.NotePublished{SearchRev: 2}
	if r := w.QuoteRepost; r != nil {
		k := [2]uuid.UUID{r.UserID, r.OriginalPostID}
		if _, taken := s.Reposts[k]; !taken {
			r.ID = uuid.New()
			r.RepostType = "quote"
			s.Reposts[k] = "quote"
			out.QuoteRepostWritten = true
		}
	}
	pub := Publish{ID: w.ID, Quote: w.QuoteRepost}
	if w.Events != nil {
		pub.Events = w.Events(out.SearchRev)
	}
	s.Publishes = append(s.Publishes, pub)
	delete(s.Schedules, w.ID)
	return out, nil
}

func (s *Store) RecordNotePublishFailure(_ context.Context, id uuid.UUID, reason string, now, retryAt, giveUpBefore time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st := s.Schedules[id]
	if st == nil {
		st = &ScheduleState{}
		s.Schedules[id] = st
	}
	r := reason
	st.PublishError = &r
	st.Attempts++
	if st.FirstFailedAt == nil {
		at := now
		st.FirstFailedAt = &at
	}
	next := retryAt
	st.NextAttemptAt = &next
	st.FailedAt = nil
	if !st.FirstFailedAt.After(giveUpBefore) {
		at := now
		st.FailedAt = &at
	}
	return st.FailedAt != nil, nil
}

func (s *Store) GetPollsByPostIDs(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.PollData, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[uuid.UUID]*postgres.PollData{}
	for _, id := range ids {
		p := s.Polls[id]
		if p == nil {
			continue
		}
		c := *p
		c.Options = append([]postgres.PollOption(nil), p.Options...)
		c.TotalVotes = 0
		for i := range c.Options {
			c.Options[i].VoteCount = 0
			for k, opts := range s.Votes {
				if k[0] != id {
					continue
				}
				for _, o := range opts {
					if o == c.Options[i].ID {
						c.Options[i].VoteCount++
					}
				}
			}
			c.TotalVotes += c.Options[i].VoteCount
		}
		out[id] = &c
	}
	return out, nil
}

func (s *Store) GetUserPollVotesBatch(_ context.Context, ids []uuid.UUID, userID uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[uuid.UUID][]uuid.UUID{}
	for _, id := range ids {
		if v := s.Votes[[2]uuid.UUID{id, userID}]; len(v) > 0 {
			out[id] = append([]uuid.UUID(nil), v...)
		}
	}
	return out, nil
}

func (s *Store) InsertPollVote(_ context.Context, postID, optionID, userID uuid.UUID, allowsMultiple bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.Polls[postID]
	if p == nil {
		return false, nil
	}
	found := false
	for _, o := range p.Options {
		if o.ID == optionID {
			found = true
		}
	}
	k := [2]uuid.UUID{postID, userID}
	if !found || (!allowsMultiple && len(s.Votes[k]) > 0) {
		return false, nil
	}
	s.Votes[k] = append(s.Votes[k], optionID)
	return true, nil
}

func (s *Store) NoteCountsFor(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.NoteCounts, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[uuid.UUID]postgres.NoteCounts{}
	for _, id := range ids {
		var c postgres.NoteCounts
		for qid, n := range s.Notes {
			if n.QuoteOfID != nil && *n.QuoteOfID == id && s.published(qid) != nil {
				c.Quotes++
			}
		}
		for k := range s.Bookmarks {
			if k[1] == id {
				c.Bookmarks++
			}
		}
		out[id] = c
	}
	return out, nil
}

func (s *Store) BatchIsBookmarked(_ context.Context, userID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[uuid.UUID]bool{}
	for _, id := range ids {
		if _, ok := s.Bookmarks[[2]uuid.UUID{userID, id}]; ok {
			out[id] = true
		}
	}
	return out, nil
}

// Clock stamps bookmark saves (tests may set it).
var Clock = func() time.Time { return time.Now() }

func (s *Store) AddBookmark(_ context.Context, userID, postID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	k := [2]uuid.UUID{userID, postID}
	if _, ok := s.Bookmarks[k]; !ok {
		s.Bookmarks[k] = Clock()
	}
	return nil
}

func (s *Store) RemoveBookmark(_ context.Context, userID, postID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.Bookmarks, [2]uuid.UUID{userID, postID})
	return nil
}

func (s *Store) ListNoteBookmarks(_ context.Context, userID uuid.UUID, _ string, limit int) ([]postgres.Note, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type row struct {
		n  postgres.Note
		at time.Time
	}
	var rows []row
	for k, at := range s.Bookmarks {
		if k[0] != userID {
			continue
		}
		if n := s.published(k[1]); n != nil {
			rows = append(rows, row{*n, at})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].at.After(rows[j].at) })
	out := make([]postgres.Note, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.n)
	}
	if limit > 0 && len(out) > limit {
		return out[:limit], "next", nil
	}
	return out, "", nil
}

func (s *Store) RecordNoteViews(_ context.Context, viewerID uuid.UUID, ids []uuid.UUID, now time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var counted int64
	for _, id := range ids {
		n := s.published(id)
		if n == nil || n.Post.AuthorID == viewerID {
			continue
		}
		k := [2]uuid.UUID{id, viewerID}
		if last, ok := s.Views[k]; ok && last.After(now.Add(-24*time.Hour)) {
			continue
		}
		s.Views[k] = now
		n.ViewCount++
		counted++
	}
	return counted, nil
}

func (s *Store) DiscoverNotes(_ context.Context, q postgres.NoteDiscoverQuery) ([]postgres.Note, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []postgres.Note
	for id := range s.Notes {
		n := s.published(id)
		if n == nil || n.Post.Visibility != "public" {
			continue
		}
		switch {
		case q.QuoteOfID != nil:
			if n.QuoteOfID == nil || *n.QuoteOfID != *q.QuoteOfID {
				continue
			}
		case q.Hashtag != "":
			has := false
			for _, t := range n.Post.Hashtags {
				if strings.EqualFold(t, q.Hashtag) {
					has = true
				}
			}
			if !has {
				continue
			}
		default:
			if !strings.Contains(strings.ToLower(n.Post.Text), strings.ToLower(q.Search)) {
				continue
			}
		}
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool {
		if q.Top && out[i].ReplyCount != out[j].ReplyCount {
			return out[i].ReplyCount > out[j].ReplyCount
		}
		return out[i].Post.CreatedAt.After(out[j].Post.CreatedAt)
	})
	if q.Limit > 0 && len(out) > q.Limit {
		return out[:q.Limit], "next", nil
	}
	return out, "", nil
}

func (s *Store) NoteTrends(_ context.Context, since time.Time, minAuthors, limit int) ([]postgres.NoteTrend, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	type agg struct {
		notes   int64
		authors map[uuid.UUID]bool
	}
	tags := map[string]*agg{}
	for id := range s.Notes {
		n := s.published(id)
		if n == nil || n.ReplyToID != nil || n.Post.Visibility != "public" || n.Post.CreatedAt.Before(since) {
			continue
		}
		for _, t := range n.Post.Hashtags {
			t = strings.ToLower(t)
			a := tags[t]
			if a == nil {
				a = &agg{authors: map[uuid.UUID]bool{}}
				tags[t] = a
			}
			a.notes++
			a.authors[n.Post.AuthorID] = true
		}
	}
	out := []postgres.NoteTrend{}
	for t, a := range tags {
		if len(a.authors) >= minAuthors {
			out = append(out, postgres.NoteTrend{Tag: t, NoteCount: a.notes, Authors: int64(len(a.authors))})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Authors != out[j].Authors {
			return out[i].Authors > out[j].Authors
		}
		if out[i].NoteCount != out[j].NoteCount {
			return out[i].NoteCount > out[j].NoteCount
		}
		return out[i].Tag < out[j].Tag
	})
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
