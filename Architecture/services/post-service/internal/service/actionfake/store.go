// Package actionfake is an in-memory Action-button link store for tests
// (service and http). It mirrors store/postgres/action_links.go's rules —
// one live link per post / profile, a new id when the URL changes, active
// links only resolve, the report threshold, the recheck claim — so the
// service and route tests exercise the same behaviour the SQL implements.
// The SQL itself is covered by the tagged integration test.
package actionfake

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/atpost/post-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Store is the fake. Posts maps post id -> author (a post absent from it
// does not exist); Deleted marks soft-deleted posts; Tiers gates posts.
type Store struct {
	mu       sync.Mutex
	Links    map[uuid.UUID]*postgres.ActionLink
	Posts    map[uuid.UUID]uuid.UUID
	Deleted  map[uuid.UUID]bool
	Tiers    map[uuid.UUID]uuid.UUID
	Daily    map[uuid.UUID]map[string]int64
	Reports  map[uuid.UUID]map[uuid.UUID]time.Time
	Claims   map[uuid.UUID]time.Time
	Reviews  []string
	Err      error
	Now      func() time.Time
	Lookups  int
	Replaces int
}

// New builds an empty store.
func New() *Store {
	return &Store{
		Links: map[uuid.UUID]*postgres.ActionLink{}, Posts: map[uuid.UUID]uuid.UUID{},
		Deleted: map[uuid.UUID]bool{}, Tiers: map[uuid.UUID]uuid.UUID{},
		Daily: map[uuid.UUID]map[string]int64{}, Reports: map[uuid.UUID]map[uuid.UUID]time.Time{},
		Claims: map[uuid.UUID]time.Time{},
	}
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func cp(l *postgres.ActionLink) *postgres.ActionLink {
	if l == nil {
		return nil
	}
	c := *l
	return &c
}

func (s *Store) live(match func(*postgres.ActionLink) bool) *postgres.ActionLink {
	for _, l := range s.Links {
		if l.Status != postgres.ActionStatusRemoved && match(l) {
			return l
		}
	}
	return nil
}

func (s *Store) replace(cur *postgres.ActionLink, postID, ownerID *uuid.UUID, author uuid.UUID, w *postgres.ActionLinkWrite) *postgres.ActionLink {
	s.Replaces++
	now := s.now()
	if cur != nil && (w == nil || cur.URL != w.URL) {
		cur.Status, cur.UpdatedAt = postgres.ActionStatusRemoved, now
	}
	if w == nil {
		return nil
	}
	if cur != nil && cur.URL == w.URL {
		cur.Label, cur.SafetyStatus, cur.SafetyCheckedAt, cur.UpdatedAt = w.Label, w.SafetyStatus, w.SafetyCheckedAt, now
		if cur.Status == postgres.ActionStatusBlocked && cur.StatusReason != nil && *cur.StatusReason == postgres.ActionReasonUnsafe && w.SafetyStatus == postgres.ActionSafetySafe {
			cur.Status, cur.StatusReason = postgres.ActionStatusActive, nil
		}
		return cp(cur)
	}
	id := w.ID
	if id == uuid.Nil {
		id = uuid.New()
	}
	l := &postgres.ActionLink{ID: id, PostID: postID, ProfileOwnerID: ownerID, AuthorID: author,
		URL: w.URL, Domain: w.Domain, Label: w.Label, Status: postgres.ActionStatusActive,
		SafetyStatus: w.SafetyStatus, SafetyCheckedAt: w.SafetyCheckedAt, CreatedAt: now, UpdatedAt: now}
	s.Links[id] = l
	return cp(l)
}

// Insert adds a live link directly (fixtures).
func (s *Store) Insert(l postgres.ActionLink) *postgres.ActionLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	if l.ID == uuid.Nil {
		l.ID = uuid.New()
	}
	if l.Status == "" {
		l.Status = postgres.ActionStatusActive
	}
	if l.CreatedAt.IsZero() {
		l.CreatedAt = s.now().Add(-time.Hour)
	}
	c := l
	s.Links[c.ID] = &c
	return cp(&c)
}

// Get returns a copy of one link.
func (s *Store) Get(id uuid.UUID) *postgres.ActionLink {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cp(s.Links[id])
}

func (s *Store) ReplacePostActionLink(_ context.Context, postID, authorID uuid.UUID, w *postgres.ActionLinkWrite) (*postgres.ActionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	owner, ok := s.Posts[postID]
	if !ok || s.Deleted[postID] {
		return nil, pgx.ErrNoRows
	}
	if owner != authorID {
		return nil, postgres.ErrPostEditNotOwned
	}
	cur := s.live(func(l *postgres.ActionLink) bool { return l.PostID != nil && *l.PostID == postID })
	pid := postID
	return s.replace(cur, &pid, nil, authorID, w), nil
}

func (s *Store) ReplaceProfileActionLink(_ context.Context, ownerID uuid.UUID, w *postgres.ActionLinkWrite) (*postgres.ActionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	cur := s.live(func(l *postgres.ActionLink) bool { return l.ProfileOwnerID != nil && *l.ProfileOwnerID == ownerID })
	o := ownerID
	return s.replace(cur, nil, &o, ownerID, w), nil
}

func (s *Store) ActionLinksForPosts(_ context.Context, ids []uuid.UUID) (map[uuid.UUID]*postgres.ActionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Lookups++
	if s.Err != nil {
		return nil, s.Err
	}
	want := map[uuid.UUID]bool{}
	for _, id := range ids {
		want[id] = true
	}
	out := map[uuid.UUID]*postgres.ActionLink{}
	for _, l := range s.Links {
		if l.PostID != nil && want[*l.PostID] && l.Status != postgres.ActionStatusRemoved {
			out[*l.PostID] = cp(l)
		}
	}
	return out, nil
}

func (s *Store) ActionLinkForProfile(_ context.Context, ownerID uuid.UUID) (*postgres.ActionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	return cp(s.live(func(l *postgres.ActionLink) bool { return l.ProfileOwnerID != nil && *l.ProfileOwnerID == ownerID })), nil
}

func (s *Store) GetResolvableActionLink(_ context.Context, id uuid.UUID) (*postgres.ResolvableActionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Err != nil {
		return nil, s.Err
	}
	l := s.Links[id]
	if l == nil || l.Status != postgres.ActionStatusActive {
		return nil, nil
	}
	out := &postgres.ResolvableActionLink{Link: cp(l)}
	if l.PostID != nil {
		author, ok := s.Posts[*l.PostID]
		if !ok || s.Deleted[*l.PostID] {
			return nil, nil
		}
		out.PostAuthorID = author
		if t, ok := s.Tiers[*l.PostID]; ok {
			tier := t
			out.PostTierID = &tier
		}
	}
	return out, nil
}

func (s *Store) RecordActionLinkClick(_ context.Context, id uuid.UUID, day time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.Links[id]
	if l == nil || l.Status != postgres.ActionStatusActive {
		return nil
	}
	l.ClicksTotal++
	if s.Daily[id] == nil {
		s.Daily[id] = map[string]int64{}
	}
	s.Daily[id][day.UTC().Format("2006-01-02")]++
	return nil
}

func (s *Store) ActionLinkStats(_ context.Context, postID, ownerID *uuid.UUID, since time.Time) (int64, int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var total, week int64
	sinceDay := since.UTC().Format("2006-01-02")
	for _, l := range s.Links {
		if (postID != nil && l.PostID != nil && *l.PostID == *postID) || (ownerID != nil && l.ProfileOwnerID != nil && *l.ProfileOwnerID == *ownerID) {
			total += l.ClicksTotal
			for day, n := range s.Daily[l.ID] {
				if day >= sinceDay {
					week += n
				}
			}
		}
	}
	return total, week, nil
}

func (s *Store) ApplyActionLinkReport(_ context.Context, in postgres.ActionLinkReportInput) (postgres.ActionLinkReportOutcome, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out postgres.ActionLinkReportOutcome
	var l *postgres.ActionLink
	switch in.TargetType {
	case "post":
		l = s.live(func(x *postgres.ActionLink) bool { return x.PostID != nil && *x.PostID == in.TargetID })
	case "user":
		l = s.live(func(x *postgres.ActionLink) bool { return x.ProfileOwnerID != nil && *x.ProfileOwnerID == in.TargetID })
	}
	if l == nil {
		return out, nil
	}
	out.Found, out.LinkID, out.PostID = true, l.ID, l.PostID
	if in.ReporterID == l.AuthorID || in.ReportedAt.Before(l.CreatedAt) {
		return out, nil
	}
	if s.Reports[l.ID] == nil {
		s.Reports[l.ID] = map[uuid.UUID]time.Time{}
	}
	if prev, ok := s.Reports[l.ID][in.ReporterID]; !ok || in.ReportedAt.After(prev) {
		s.Reports[l.ID][in.ReporterID] = in.ReportedAt
	}
	out.Counted = true
	since := in.Now.Add(-in.Window)
	if l.ModeratedAt != nil && l.ModeratedAt.After(since) {
		since = *l.ModeratedAt
	}
	for _, at := range s.Reports[l.ID] {
		if at.After(since) {
			out.Reporters++
		}
	}
	if out.Reporters >= in.Threshold && l.Status == postgres.ActionStatusActive {
		r := postgres.ActionReasonReports
		l.Status, l.StatusReason = postgres.ActionStatusUnderReview, &r
		out.Held = true
	}
	return out, nil
}

func (s *Store) ReviewActionLink(_ context.Context, id, actor uuid.UUID, decision, note string) (*postgres.ActionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if actor == uuid.Nil {
		return nil, postgres.ErrAdminAuditActor
	}
	l := s.Links[id]
	if l == nil || l.Status == postgres.ActionStatusRemoved {
		return nil, postgres.ErrActionLinkNotFound
	}
	now := s.now()
	switch decision {
	case postgres.ActionDecisionRestore:
		if l.Status != postgres.ActionStatusBlocked && l.Status != postgres.ActionStatusUnderReview {
			return nil, postgres.ErrActionLinkTransition
		}
		l.Status, l.StatusReason = postgres.ActionStatusActive, nil
	case postgres.ActionDecisionBlock:
		if l.Status != postgres.ActionStatusActive && l.Status != postgres.ActionStatusUnderReview {
			return nil, postgres.ErrActionLinkTransition
		}
		r := postgres.ActionReasonModerator
		l.Status, l.StatusReason = postgres.ActionStatusBlocked, &r
	default:
		return nil, postgres.ErrActionLinkTransition
	}
	l.ModeratedAt = &now
	s.Reviews = append(s.Reviews, actor.String()+" "+decision+" "+id.String())
	return cp(l), nil
}

func (s *Store) ListActionLinksByStatus(_ context.Context, status string, before time.Time, limit int) ([]*postgres.ActionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []*postgres.ActionLink
	for _, l := range s.Links {
		if l.Status == status && (before.IsZero() || l.UpdatedAt.Before(before)) {
			out = append(out, cp(l))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *Store) ClaimActionLinksForRecheck(_ context.Context, now, staleBefore, claimUntil time.Time, limit int) ([]*postgres.ActionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []*postgres.ActionLink
	for _, l := range s.Links {
		if l.Status != postgres.ActionStatusActive && l.Status != postgres.ActionStatusUnderReview {
			continue
		}
		if l.SafetyCheckedAt != nil && !l.SafetyCheckedAt.Before(staleBefore) {
			continue
		}
		if c, ok := s.Claims[l.ID]; ok && !c.Before(now) {
			continue
		}
		due = append(due, l)
	}
	sort.Slice(due, func(i, j int) bool { return due[i].ID.String() < due[j].ID.String() })
	if len(due) > limit {
		due = due[:limit]
	}
	out := make([]*postgres.ActionLink, 0, len(due))
	for _, l := range due {
		s.Claims[l.ID] = claimUntil
		out = append(out, cp(l))
	}
	return out, nil
}

func (s *Store) RecordActionLinkVerdict(_ context.Context, id uuid.UUID, safety string, checkedAt time.Time) (*postgres.ActionLink, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	l := s.Links[id]
	if l == nil || l.Status == postgres.ActionStatusRemoved {
		return nil, postgres.ErrActionLinkNotFound
	}
	at := checkedAt
	l.SafetyStatus, l.SafetyCheckedAt = safety, &at
	delete(s.Claims, id)
	if safety == postgres.ActionSafetyUnsafe && (l.Status == postgres.ActionStatusActive || l.Status == postgres.ActionStatusUnderReview) {
		r := postgres.ActionReasonUnsafe
		l.Status, l.StatusReason = postgres.ActionStatusBlocked, &r
	}
	return cp(l), nil
}

// ErrDown is a convenient store outage.
var ErrDown = errors.New("store down")
