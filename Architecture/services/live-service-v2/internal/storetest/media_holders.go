package storetest

import (
	"context"
	"sort"
	"sync"

	"github.com/google/uuid"
)

// holderLocks is LockMediaHolder's per-stream lock (service/media_claims.go).
var holderLocks sync.Map // uuid.UUID -> *sync.Mutex

// LockMediaHolder mirrors postgres.Store.LockMediaHolder: one writer of a
// stream's cover at a time; unlock is idempotent.
func (m *MemStore) LockMediaHolder(_ context.Context, streamID uuid.UUID) (func(), error) {
	mu, _ := holderLocks.LoadOrStore(streamID, &sync.Mutex{})
	l := mu.(*sync.Mutex)
	l.Lock()
	var once sync.Once
	return func() { once.Do(l.Unlock) }, nil
}

// StreamIDsWithCover mirrors postgres.Store.StreamIDsWithCover.
func (m *MemStore) StreamIDsWithCover(_ context.Context, creatorID uuid.UUID) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for id, st := range m.Streams {
		if st.CreatorUserID == creatorID && st.CoverMediaID != nil {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}

// StreamIDsWithImportMedia mirrors postgres.Store.StreamIDsWithImportMedia.
func (m *MemStore) StreamIDsWithImportMedia(_ context.Context, creatorID uuid.UUID) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for id, j := range m.Imports {
		if st, ok := m.Streams[id]; ok && st.CreatorUserID == creatorID && j.MediaID != nil {
			out = append(out, id)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].String() < out[j].String() })
	return out, nil
}
