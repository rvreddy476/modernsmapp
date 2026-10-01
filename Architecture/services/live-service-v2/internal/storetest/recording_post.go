package storetest

import (
	"context"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// PostLookupRow mirrors the post_lookup_* columns of an import job
// (store/postgres/recording_post.go). A job with no row is pending and due.
type PostLookupRow struct {
	State    string
	Attempts int
	NextAt   time.Time
	LastAt   *time.Time
}

// postLookupLocked returns the lookup row of a stream whose import is done
// (nil otherwise), creating the pending row a fresh job has.
func (m *MemStore) postLookupLocked(streamID uuid.UUID) *PostLookupRow {
	j, ok := m.Imports[streamID]
	if !ok || j.State != postgres.ImportDone {
		return nil
	}
	if m.PostLookups == nil {
		m.PostLookups = map[uuid.UUID]*PostLookupRow{}
	}
	row := m.PostLookups[streamID]
	if row == nil {
		row = &PostLookupRow{State: postgres.PostLookupPending}
		m.PostLookups[streamID] = row
	}
	return row
}

func (m *MemStore) lookupLocked(streamID uuid.UUID, row *PostLookupRow, now time.Time) postgres.PostLookup {
	l := postgres.PostLookup{StreamID: streamID, Attempts: row.Attempts}
	if j := m.Imports[streamID]; j != nil && j.DoneAt != nil {
		l.Age = now.Sub(*j.DoneAt)
	}
	return l
}

func (m *MemStore) ClaimDuePostLookups(_ context.Context, limit int, lease time.Duration) ([]postgres.PostLookup, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	type due struct {
		id  uuid.UUID
		row *PostLookupRow
	}
	var all []due
	for id := range m.Imports {
		row := m.postLookupLocked(id)
		if row == nil || row.State != postgres.PostLookupPending || row.NextAt.After(now) {
			continue
		}
		all = append(all, due{id, row})
	}
	sort.Slice(all, func(i, j int) bool {
		if !all[i].row.NextAt.Equal(all[j].row.NextAt) {
			return all[i].row.NextAt.Before(all[j].row.NextAt)
		}
		return idLess(all[i].id, all[j].id)
	})
	if len(all) > limit {
		all = all[:limit]
	}
	out := make([]postgres.PostLookup, 0, len(all))
	for _, d := range all {
		out = append(out, m.lookupLocked(d.id, d.row, now))
		at := now
		d.row.NextAt, d.row.LastAt = now.Add(lease), &at
	}
	return out, nil
}

func (m *MemStore) ClaimPostLookup(_ context.Context, streamID uuid.UUID, minGap time.Duration) (postgres.PostLookup, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	row := m.postLookupLocked(streamID)
	if row == nil || row.State != postgres.PostLookupPending {
		return postgres.PostLookup{}, false, nil
	}
	if row.LastAt != nil && row.LastAt.After(now.Add(-minGap)) {
		return postgres.PostLookup{}, false, nil
	}
	row.LastAt = &now
	return m.lookupLocked(streamID, row, now), true, nil
}

func (m *MemStore) SetRecordingPost(_ context.Context, streamID, postID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.postLookupLocked(streamID)
	if row == nil || row.State != postgres.PostLookupPending {
		return false, nil
	}
	row.State = postgres.PostLookupFound
	st, ok := m.Streams[streamID]
	if !ok || st.RecordingPostID != nil {
		return false, nil
	}
	id := postID
	st.RecordingPostID = &id
	return true, nil
}

func (m *MemStore) RetryPostLookup(_ context.Context, streamID uuid.UUID, retryIn time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if row := m.postLookupLocked(streamID); row != nil && row.State == postgres.PostLookupPending {
		row.Attempts++
		row.NextAt = m.Now().Add(retryIn)
	}
	return nil
}

func (m *MemStore) StopPostLookup(_ context.Context, streamID uuid.UUID, state string) error {
	if state != postgres.PostLookupDeleted && state != postgres.PostLookupGaveUp {
		return errors.New("stop post lookup: state must be deleted or gave_up")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.postLookupLocked(streamID)
	if row != nil && (row.State == postgres.PostLookupPending ||
		(state == postgres.PostLookupDeleted && row.State == postgres.PostLookupFound)) {
		row.State = state
	}
	if st, ok := m.Streams[streamID]; ok && state == postgres.PostLookupDeleted {
		st.RecordingPostID = nil
	}
	return nil
}

// PostLookup returns a copy of a stream's lookup row (nil: its import is
// not done).
func (m *MemStore) PostLookup(streamID uuid.UUID) *PostLookupRow {
	m.mu.Lock()
	defer m.mu.Unlock()
	row := m.postLookupLocked(streamID)
	if row == nil {
		return nil
	}
	cp := *row
	return &cp
}
