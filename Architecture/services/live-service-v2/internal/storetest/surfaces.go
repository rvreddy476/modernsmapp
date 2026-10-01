package storetest

// The in-memory mirror of internal/store/postgres/surfaces.go (migration
// 005): the same filters, orders, keysets, locks-as-one-mutex and rules, so
// the service tests exercise the service's decisions.

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// HeartRow is one live_stream_hearts row.
type HeartRow struct {
	Hearts    int
	CreatedAt time.Time
}

// BadgeRow is one live_creator_badges row (the founding creator badge).
type BadgeRow struct {
	Badge     string
	GrantedAt time.Time
	StreamID  uuid.UUID
	RevokedAt *time.Time
}

// idLess is Postgres's uuid order (bytewise, which the canonical text
// preserves).
func idLess(a, b uuid.UUID) bool { return a.String() < b.String() }

// timeDesc orders by t DESC NULLS LAST, then id DESC.
func timeDesc(ta, tb *time.Time, a, b uuid.UUID) bool {
	switch {
	case ta == nil && tb == nil:
		return idLess(b, a)
	case ta == nil:
		return false
	case tb == nil:
		return true
	case !ta.Equal(*tb):
		return ta.After(*tb)
	}
	return idLess(b, a)
}

func (m *MemStore) listLiveLocked(p postgres.ListLiveParams) []*postgres.LiveStream {
	limit := p.Limit
	if limit <= 0 || limit > 500 {
		limit = 20
	}
	hasCursor := p.StartedBefore != nil && p.IDBefore != nil
	var out []*postgres.LiveStream
	for _, st := range m.Streams {
		if st.Status != postgres.StatusLive && st.Status != postgres.StatusReconnecting {
			continue
		}
		if !p.Filter.Matches(st) {
			continue
		}
		if p.Sort == postgres.SortViewers {
			if hasCursor && p.ViewersBefore != nil {
				// (viewer_count, started_at, id) < cursor; a NULL started_at
				// compares as unknown, which the WHERE drops.
				if st.StartedAt == nil {
					continue
				}
				switch {
				case st.ViewerCount != *p.ViewersBefore:
					if st.ViewerCount > *p.ViewersBefore {
						continue
					}
				case !st.StartedAt.Equal(*p.StartedBefore):
					if st.StartedAt.After(*p.StartedBefore) {
						continue
					}
				default:
					if !idLess(st.ID, *p.IDBefore) {
						continue
					}
				}
			}
		} else if hasCursor {
			if st.StartedAt == nil {
				continue
			}
			if st.StartedAt.After(*p.StartedBefore) || (st.StartedAt.Equal(*p.StartedBefore) && !idLess(st.ID, *p.IDBefore)) {
				continue
			}
		}
		out = append(out, clone(st))
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if p.Sort == postgres.SortViewers && a.ViewerCount != b.ViewerCount {
			return a.ViewerCount > b.ViewerCount
		}
		return timeDesc(a.StartedAt, b.StartedAt, a.ID, b.ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *MemStore) listScheduledLocked(p postgres.ListScheduledParams) []*postgres.LiveStream {
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var out []*postgres.LiveStream
	for _, st := range m.Streams {
		if st.Status != postgres.StatusScheduled {
			continue
		}
		// Upcoming: a time still ahead. Unstarted: every scheduled stream.
		if !p.Unstarted && (st.ScheduledAt == nil || !st.ScheduledAt.After(p.Now)) {
			continue
		}
		if !p.Filter.Matches(st) {
			continue
		}
		if p.ScheduledAfter != nil && p.IDAfter != nil {
			key := postgres.ScheduledSortKey(st)
			if key.Before(*p.ScheduledAfter) || (key.Equal(*p.ScheduledAfter) && !idLess(*p.IDAfter, st.ID)) {
				continue
			}
		}
		out = append(out, clone(st))
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := postgres.ScheduledSortKey(out[i]), postgres.ScheduledSortKey(out[j])
		if !a.Equal(b) {
			return a.Before(b)
		}
		return idLess(out[i].ID, out[j].ID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

func (m *MemStore) ListPast(_ context.Context, p postgres.ListPastParams) ([]*postgres.LiveStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	limit := p.Limit
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	var out []*postgres.LiveStream
	for _, st := range m.Streams {
		if st.Status != postgres.StatusEnded || st.StartedAt == nil || st.EndedAt == nil {
			continue
		}
		if !p.Filter.Matches(st) {
			continue
		}
		if p.EndedBefore != nil && p.IDBefore != nil {
			if st.EndedAt.After(*p.EndedBefore) || (st.EndedAt.Equal(*p.EndedBefore) && !idLess(st.ID, *p.IDBefore)) {
				continue
			}
		}
		out = append(out, clone(st))
	}
	sort.Slice(out, func(i, j int) bool { return timeDesc(out[i].EndedAt, out[j].EndedAt, out[i].ID, out[j].ID) })
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) UpdateScheduled(_ context.Context, id uuid.UUID, p postgres.StreamPatch) (*postgres.LiveStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.Streams[id]
	if !ok {
		return nil, postgres.ErrNotFound
	}
	if st.Status != postgres.StatusScheduled {
		return nil, postgres.ErrStateConflict
	}
	p.Apply(st)
	st.UpdatedAt = m.Now()
	return clone(st), nil
}

// --- reminders ---

func (m *MemStore) SetReminder(_ context.Context, streamID, userID uuid.UUID, set bool) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.Streams[streamID]
	if !ok {
		return 0, postgres.ErrNotFound
	}
	if st.Status != postgres.StatusScheduled {
		return 0, postgres.ErrStateConflict
	}
	if set {
		if m.Reminders[streamID] == nil {
			m.Reminders[streamID] = map[uuid.UUID]bool{}
		}
		m.Reminders[streamID][userID] = true
	} else {
		delete(m.Reminders[streamID], userID)
	}
	return len(m.Reminders[streamID]), nil
}

func (m *MemStore) ReminderStats(_ context.Context, streamIDs []uuid.UUID, viewerID uuid.UUID) (map[uuid.UUID]postgres.ReminderStat, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[uuid.UUID]postgres.ReminderStat{}
	for _, id := range streamIDs {
		if n := len(m.Reminders[id]); n > 0 {
			out[id] = postgres.ReminderStat{Count: n, Set: m.Reminders[id][viewerID]}
		}
	}
	return out, nil
}

func (m *MemStore) ListReminderUserIDs(_ context.Context, streamID, after uuid.UUID, limit int) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []uuid.UUID{}
	for id := range m.Reminders[streamID] {
		if idLess(after, id) {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return idLess(out[i], out[j]) })
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// --- hearts ---

func (m *MemStore) AddHearts(_ context.Context, streamID, userID uuid.UUID, n, perUserCap int) (postgres.HeartResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var res postgres.HeartResult
	if n <= 0 {
		return res, errors.New("hearts: n must be positive")
	}
	st, ok := m.Streams[streamID]
	if !ok {
		return res, postgres.ErrNotFound
	}
	res.Total = st.HeartCount
	if st.Status != postgres.StatusLive && st.Status != postgres.StatusReconnecting {
		return res, postgres.ErrNotOnAir
	}
	have := 0
	if row := m.Hearts[streamID][userID]; row != nil {
		have = row.Hearts
	}
	res.Added = postgres.HeartsUnderCap(have, n, perUserCap)
	if res.Added > 0 {
		if m.Hearts[streamID] == nil {
			m.Hearts[streamID] = map[uuid.UUID]*HeartRow{}
		}
		row := m.Hearts[streamID][userID]
		if row == nil {
			row = &HeartRow{CreatedAt: m.Now()}
			m.Hearts[streamID][userID] = row
		}
		row.Hearts += res.Added
		st.HeartCount += int64(res.Added)
		res.Total = st.HeartCount
	}
	return res, nil
}

func (m *MemStore) ListSupporters(_ context.Context, streamID uuid.UUID, limit int) ([]postgres.Supporter, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	st, ok := m.Streams[streamID]
	if !ok {
		return []postgres.Supporter{}, nil
	}
	agg := map[uuid.UUID]*postgres.Supporter{}
	first := func(id uuid.UUID, at time.Time) *postgres.Supporter {
		sp := agg[id]
		if sp == nil {
			sp = &postgres.Supporter{UserID: id, FirstAt: at}
			agg[id] = sp
		}
		if at.Before(sp.FirstAt) {
			sp.FirstAt = at
		}
		return sp
	}
	for id, row := range m.Hearts[streamID] {
		if row.Hearts > 0 {
			first(id, row.CreatedAt).Hearts = row.Hearts
		}
	}
	for _, msg := range m.Messages {
		if msg.StreamID == streamID && msg.RemovedAt == nil {
			first(msg.UserID, msg.CreatedAt).Messages++
		}
	}
	out := []postgres.Supporter{}
	for id, sp := range agg {
		if id == st.CreatorUserID {
			continue
		}
		if _, banned := m.Bans[streamID][id]; banned {
			continue
		}
		if _, banned := m.PlatformBans[id]; banned {
			continue
		}
		out = append(out, *sp)
	}
	sort.Slice(out, func(i, j int) bool {
		a, b := out[i], out[j]
		switch {
		case a.Hearts != b.Hearts:
			return a.Hearts > b.Hearts
		case a.Messages != b.Messages:
			return a.Messages > b.Messages
		case !a.FirstAt.Equal(b.FirstAt):
			return a.FirstAt.Before(b.FirstAt)
		}
		return idLess(a.UserID, b.UserID)
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// --- creator badges ---

// grantFoundingLocked is the INSERT ... ON CONFLICT DO NOTHING: a creator who
// has the badge, or had it revoked, is left as they are.
func (m *MemStore) grantFoundingLocked(st *postgres.LiveStream) {
	if _, exists := m.Badges[st.CreatorUserID]; exists {
		return
	}
	m.Badges[st.CreatorUserID] = &BadgeRow{Badge: postgres.BadgeFoundingCreator, GrantedAt: *st.EndedAt, StreamID: st.ID}
}

func (m *MemStore) visibleBadgeLocked(userID uuid.UUID) *BadgeRow {
	b := m.Badges[userID]
	if b == nil || b.RevokedAt != nil {
		return nil
	}
	if _, banned := m.PlatformBans[userID]; banned {
		return nil
	}
	return b
}

func (m *MemStore) BadgesFor(_ context.Context, userIDs []uuid.UUID) (map[uuid.UUID][]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[uuid.UUID][]string{}
	for _, id := range userIDs {
		if b := m.visibleBadgeLocked(id); b != nil {
			out[id] = []string{b.Badge}
		}
	}
	return out, nil
}

func (m *MemStore) ListBadges(_ context.Context, userID uuid.UUID) ([]postgres.Badge, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []postgres.Badge{}
	if b := m.visibleBadgeLocked(userID); b != nil {
		out = append(out, postgres.Badge{Badge: b.Badge, GrantedAt: b.GrantedAt})
	}
	return out, nil
}

func (m *MemStore) AdminRevokeBadge(_ context.Context, userID uuid.UUID, badge, _ string, audit postgres.AuditEntry) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b := m.Badges[userID]
	if b == nil || b.Badge != badge {
		return false, postgres.ErrNotFound
	}
	if b.RevokedAt != nil {
		return false, nil
	}
	now := m.Now()
	b.RevokedAt = &now
	m.Audits = append(m.Audits, audit)
	return true, nil
}
