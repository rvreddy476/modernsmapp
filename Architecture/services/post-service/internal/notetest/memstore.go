// Package notetest is an in-memory stand-in for the Echo note store
// (service.noteStore, *postgres.Store in production), shared by the
// service and http tests. Never imported by production code.
package notetest

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Store holds notes, media, idempotency claims and the writes made.
type Store struct {
	mu      sync.Mutex
	Notes   map[uuid.UUID]*postgres.Note
	Deleted map[uuid.UUID]bool
	Media   map[uuid.UUID]postgres.NoteMediaRow
	Claims  map[string]claim
	// Writes is every CreateNotes call that committed.
	Writes []*postgres.NoteWrite
	// Reposts are (user, post) pairs holding an active repost.
	Reposts map[[2]uuid.UUID]string
	// Hidden marks notes no viewer but their author may see (blocked,
	// private, held), for the BatchPosts stand-in.
	Hidden map[uuid.UUID]bool
	// Fail makes every call answer this error.
	Fail error
}

type claim struct {
	postID      uuid.UUID
	fingerprint string
}

// New returns an empty Store.
func New() *Store {
	return &Store{
		Notes: map[uuid.UUID]*postgres.Note{}, Deleted: map[uuid.UUID]bool{},
		Media: map[uuid.UUID]postgres.NoteMediaRow{}, Claims: map[string]claim{},
		Reposts: map[[2]uuid.UUID]string{}, Hidden: map[uuid.UUID]bool{},
	}
}

// Put adds a note row (a copy) and returns it.
func (s *Store) Put(n postgres.Note) *postgres.Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	if n.Post.ContentType == "" {
		n.Post.ContentType = postgres.NoteContentType
	}
	if n.Post.ReviewStatus == "" {
		n.Post.ReviewStatus = "approved"
	}
	if n.Post.Visibility == "" {
		n.Post.Visibility = "public"
	}
	if n.ReplyPolicy == "" {
		n.ReplyPolicy = "everyone"
	}
	if n.ConversationID == nil {
		id := n.Post.ID
		n.ConversationID = &id
	}
	c := n
	s.Notes[n.Post.ID] = &c
	return &c
}

// Note returns the stored row (nil when absent).
func (s *Store) Note(id uuid.UUID) *postgres.Note {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Notes[id]
}

// Visible is the BatchPosts stand-in's decision: live, not hidden to
// anyone but the author.
func (s *Store) Visible(id uuid.UUID, viewer *uuid.UUID) (*postgres.Post, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.Notes[id]
	if n == nil || s.Deleted[id] {
		return nil, false
	}
	if s.Hidden[id] && (viewer == nil || *viewer != n.Post.AuthorID) {
		return nil, false
	}
	p := n.Post
	return &p, true
}

func (s *Store) GetNotes(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return nil, s.Fail
	}
	out := map[uuid.UUID]*postgres.Note{}
	for _, id := range ids {
		if n := s.Notes[id]; n != nil && !s.Deleted[id] {
			c := *n
			out[id] = &c
		}
	}
	return out, nil
}

func (s *Store) GetNoteThread(_ context.Context, rootID uuid.UUID) ([]postgres.Note, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []postgres.Note
	for id, n := range s.Notes {
		if !s.Deleted[id] && n.Post.ThreadRootID != nil && *n.Post.ThreadRootID == rootID {
			out = append(out, *n)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Post.ThreadSeq < out[j].Post.ThreadSeq })
	return out, nil
}

func (s *Store) ListNotes(_ context.Context, q postgres.NoteListQuery) ([]postgres.Note, string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return nil, "", s.Fail
	}
	var out []postgres.Note
	for id, n := range s.Notes {
		if s.Deleted[id] {
			continue
		}
		switch {
		case q.ParentID != nil:
			if n.ReplyToID == nil || *n.ReplyToID != *q.ParentID {
				continue
			}
		case q.Tab == postgres.NoteTabReposts:
			if _, ok := s.Reposts[[2]uuid.UUID{*q.AuthorID, id}]; !ok {
				continue
			}
		default:
			if n.Post.AuthorID != *q.AuthorID {
				continue
			}
			if (q.Tab == postgres.NoteTabReplies) != (n.ReplyToID != nil) {
				continue
			}
		}
		out = append(out, *n)
	}
	sort.Slice(out, func(i, j int) bool {
		if q.ParentID != nil {
			return out[i].Post.CreatedAt.Before(out[j].Post.CreatedAt)
		}
		return out[i].Post.CreatedAt.After(out[j].Post.CreatedAt)
	})
	if q.Limit > 0 && len(out) > q.Limit {
		return out[:q.Limit], "next", nil
	}
	return out, "", nil
}

func (s *Store) NoteMedia(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]postgres.NoteMediaRow, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := map[uuid.UUID]postgres.NoteMediaRow{}
	for _, id := range ids {
		if m, ok := s.Media[id]; ok {
			out[id] = m
		}
	}
	return out, nil
}

func (s *Store) CreateNotes(_ context.Context, w *postgres.NoteWrite) (*postgres.NoteWriteResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail != nil {
		return nil, s.Fail
	}
	if w.Idem != nil {
		key := w.Idem.ActorID.String() + "|" + w.Idem.ClientKey
		if c, ok := s.Claims[key]; ok {
			if c.fingerprint != w.Idem.Fingerprint {
				return nil, postgres.ErrCreateKeyConflict
			}
			return nil, postgres.ErrCreateKeyReplay{PostID: c.postID}
		}
		s.Claims[key] = claim{postID: w.Notes[0].ID, fingerprint: w.Idem.Fingerprint}
	}
	for i, p := range w.Notes {
		n := postgres.Note{Post: *p, NoteFields: w.Fields[i]}
		if n.ReplyPolicy == "" {
			n.ReplyPolicy = "everyone"
		}
		s.Notes[p.ID] = &n
	}
	if w.ReplyParent != nil {
		if parent := s.Notes[*w.ReplyParent]; parent != nil {
			parent.ReplyCount++
		}
	}
	res := &postgres.NoteWriteResult{}
	if r := w.QuoteRepost; r != nil {
		k := [2]uuid.UUID{r.UserID, r.OriginalPostID}
		if _, taken := s.Reposts[k]; !taken {
			r.ID = uuid.New()
			r.RepostType = "quote"
			s.Reposts[k] = "quote"
			res.QuoteRepostWritten = true
		}
	}
	s.Writes = append(s.Writes, w)
	return res, nil
}

func (s *Store) LookupCreateIdempotency(_ context.Context, actorID uuid.UUID, key string) (uuid.UUID, string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.Claims[actorID.String()+"|"+key]
	return c.postID, c.fingerprint, ok, nil
}

func (s *Store) UpdateNote(_ context.Context, id, authorID uuid.UUID, e postgres.NoteEdit) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.Notes[id]
	if n == nil || s.Deleted[id] || n.Post.AuthorID != authorID {
		return false, nil
	}
	if e.Text != nil {
		n.Post.Text = *e.Text
		n.Post.Hashtags = e.Hashtags
		n.Post.MentionUsernames = e.Mentions
		at := e.EditedAt
		n.EditedAt = &at
	}
	if e.ReplyPolicy != nil {
		n.ReplyPolicy = *e.ReplyPolicy
	}
	return true, nil
}

func (s *Store) DecrementNoteReplyCount(_ context.Context, parentID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if p := s.Notes[parentID]; p != nil && p.ReplyCount > 0 {
		p.ReplyCount--
	}
	return nil
}

// Delete soft-deletes id (the service's delete stand-in).
func (s *Store) Delete(id uuid.UUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Deleted[id] = true
}

// LastWrite is the latest committed write (nil when none).
func (s *Store) LastWrite() *postgres.NoteWrite {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.Writes) == 0 {
		return nil
	}
	return s.Writes[len(s.Writes)-1]
}

// EventTypes lists the outbox event types of w.
func EventTypes(w *postgres.NoteWrite) []string {
	var out []string
	for _, e := range w.Events {
		out = append(out, e.EventType)
	}
	return out
}

// Limiter counts Allow calls per key against the limit it is given.
type Limiter struct {
	mu     sync.Mutex
	Counts map[string]int64
}

func NewLimiter() *Limiter { return &Limiter{Counts: map[string]int64{}} }

func (l *Limiter) Allow(_ context.Context, key string, limit int64, _ time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.Counts[key]++
	return l.Counts[key] <= limit
}

// Premium answers from a set; Err fails every call.
type Premium struct {
	Users map[uuid.UUID]bool
	Err   error
	Calls int
}

func (p *Premium) IsPremium(_ context.Context, id uuid.UUID) (bool, error) {
	p.Calls++
	if p.Err != nil {
		return false, p.Err
	}
	return p.Users[id], nil
}

// Relations answers the reply-policy questions from sets.
type Relations struct {
	// Follows[author][user] = author follows user.
	Follows map[uuid.UUID]map[uuid.UUID]bool
	// Usernames maps a lower-cased username to its account.
	Usernames map[string]uuid.UUID
	Err       error
}

func (r *Relations) AuthorFollows(_ context.Context, author, user uuid.UUID) (bool, error) {
	if r.Err != nil {
		return false, r.Err
	}
	return r.Follows[author][user], nil
}

func (r *Relations) MentionedIDs(_ context.Context, usernames []string) ([]uuid.UUID, error) {
	if r.Err != nil {
		return nil, r.Err
	}
	var out []uuid.UUID
	for _, u := range usernames {
		if id, ok := r.Usernames[strings.ToLower(u)]; ok {
			out = append(out, id)
		}
	}
	return out, nil
}
