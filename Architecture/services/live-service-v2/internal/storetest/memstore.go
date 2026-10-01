// Package storetest is an in-memory stand-in for *postgres.Store that
// satisfies service.Store, for the service and http unit tests. It applies
// the same rules the SQL does (locked read-decide-write transitions with the
// status age from a settable clock, presence counting without the host,
// one report per reporter per target, append-only audit) so the tests
// exercise the service's decisions, not a mock's.
//
// Imported only by tests.
package storetest

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// MemStore is the in-memory store.
type MemStore struct {
	mu sync.Mutex
	// Now is the "database clock"; default time.Now.
	Now func() time.Time

	Streams       map[uuid.UUID]*postgres.LiveStream
	Mutes         map[uuid.UUID]map[uuid.UUID]bool
	WordFilters   map[uuid.UUID]map[string]bool
	Messages      map[uuid.UUID]*postgres.ChatMessage
	Pinned        map[uuid.UUID]uuid.UUID
	Presence      map[uuid.UUID]map[uuid.UUID]bool
	Mods          map[uuid.UUID][]uuid.UUID
	Bans          map[uuid.UUID]map[uuid.UUID]postgres.StreamBan
	PlatformBans  map[uuid.UUID]postgres.PlatformBan
	Reports       []*postgres.Report
	Audits        []postgres.AuditEntry
	Outbox        []postgres.OutboxEvent
	Webhooks      map[string]bool
	Imports       map[uuid.UUID]*postgres.RecordingImport
	ImportDue     map[uuid.UUID]time.Time
	ImportCreated map[uuid.UUID]time.Time
	ViewerEvents  int

	// FailOutbox makes every outbox enqueue fail (the transaction rolls back).
	FailOutbox bool
}

// New returns an empty store.
func New() *MemStore {
	return &MemStore{
		Now:           time.Now,
		Streams:       map[uuid.UUID]*postgres.LiveStream{},
		Mutes:         map[uuid.UUID]map[uuid.UUID]bool{},
		WordFilters:   map[uuid.UUID]map[string]bool{},
		Messages:      map[uuid.UUID]*postgres.ChatMessage{},
		Pinned:        map[uuid.UUID]uuid.UUID{},
		Presence:      map[uuid.UUID]map[uuid.UUID]bool{},
		Mods:          map[uuid.UUID][]uuid.UUID{},
		Bans:          map[uuid.UUID]map[uuid.UUID]postgres.StreamBan{},
		PlatformBans:  map[uuid.UUID]postgres.PlatformBan{},
		Webhooks:      map[string]bool{},
		Imports:       map[uuid.UUID]*postgres.RecordingImport{},
		ImportDue:     map[uuid.UUID]time.Time{},
		ImportCreated: map[uuid.UUID]time.Time{},
	}
}

// AddStream adds a public stream in status live (started now) for creator.
func (m *MemStore) AddStream(creator uuid.UUID) *postgres.LiveStream {
	return m.AddStreamStatus(creator, postgres.StatusLive)
}

// AddStreamStatus adds a public stream in the given status.
func (m *MemStore) AddStreamStatus(creator uuid.UUID, status string) *postgres.LiveStream {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	id := uuid.New()
	st := &postgres.LiveStream{
		ID: id, CreatorUserID: creator, Status: status, Visibility: "public",
		LiveKitRoom: "stream_" + id.String(), Title: "t", StatusChangedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	if status == postgres.StatusLive || status == postgres.StatusReconnecting {
		st.StartedAt = &now
	}
	m.Streams[id] = st
	return clone(st)
}

// Stream returns a copy of the stored stream.
func (m *MemStore) Stream(id uuid.UUID) *postgres.LiveStream {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.Streams[id]; ok {
		return clone(st)
	}
	return nil
}

func clone(st *postgres.LiveStream) *postgres.LiveStream {
	cp := *st
	return &cp
}

// --- streams ---

func (m *MemStore) CreateStream(_ context.Context, p postgres.CreateStreamParams) (*postgres.LiveStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	st := &postgres.LiveStream{
		ID: uuid.New(), CreatorUserID: p.CreatorUserID, LiveKitRoom: p.LiveKitRoom, Title: p.Title,
		Description: p.Description, CoverMediaID: p.CoverMediaID, Status: postgres.StatusScheduled,
		Visibility: p.Visibility, ScheduledAt: p.ScheduledAt, StatusChangedAt: now, CreatedAt: now, UpdatedAt: now,
	}
	st.LiveKitRoom = "stream_" + st.ID.String()
	m.Streams[st.ID] = st
	return clone(st), nil
}

func (m *MemStore) GetByID(_ context.Context, id uuid.UUID) (*postgres.LiveStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.Streams[id]
	if !ok {
		return nil, postgres.ErrNotFound
	}
	return clone(st), nil
}

func (m *MemStore) ListLive(_ context.Context, p postgres.ListLiveParams) ([]*postgres.LiveStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*postgres.LiveStream
	for _, st := range m.Streams {
		if st.Status == postgres.StatusLive || st.Status == postgres.StatusReconnecting {
			out = append(out, clone(st))
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID.String() > out[j].ID.String() })
	if p.Limit > 0 && len(out) > p.Limit {
		out = out[:p.Limit]
	}
	return out, nil
}

func (m *MemStore) ListScheduled(_ context.Context, _ postgres.ListScheduledParams) ([]*postgres.LiveStream, error) {
	return nil, nil
}

// --- lifecycle ---

func (m *MemStore) ApplyTransition(_ context.Context, id uuid.UUID, decide postgres.TransitionFunc, events postgres.EventsFunc, audit *postgres.AuditEntry) (*postgres.TransitionResult, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	cur, ok := m.Streams[id]
	if !ok {
		return nil, postgres.ErrNotFound
	}
	prev := clone(cur)
	d, ok := decide(prev, m.Now().Sub(prev.StatusChangedAt))
	if !ok {
		return nil, postgres.ErrStateConflict
	}
	if d.To == prev.Status {
		if audit != nil {
			m.Audits = append(m.Audits, *audit)
		}
		return &postgres.TransitionResult{Prev: prev, Next: prev}, nil
	}
	now := m.Now()
	next := clone(prev)
	next.Status = d.To
	next.StatusChangedAt = now
	next.UpdatedAt = now
	next.EndedReason = nil
	if postgres.IsTerminal(d.To) {
		if d.Reason == "" {
			return nil, errors.New("transition to a terminal status needs a reason")
		}
		r := d.Reason
		next.EndedReason = &r
		if next.EndedAt == nil {
			next.EndedAt = &now
		}
		next.ViewerCount = 0
	}
	if d.To == postgres.StatusLive && next.StartedAt == nil {
		next.StartedAt = &now
	}
	if d.To == postgres.StatusStarting {
		next.EndedAt = nil
	}
	var evs []postgres.OutboxEvent
	if events != nil {
		var err error
		if evs, err = events(prev, next); err != nil {
			return nil, err // rolled back: nothing written
		}
	}
	if len(evs) > 0 && m.FailOutbox {
		return nil, errors.New("outbox insert failed")
	}
	// commit
	m.Streams[id] = next
	if postgres.IsTerminal(d.To) {
		delete(m.Presence, id)
	}
	m.enqueue(evs...)
	if audit != nil {
		m.Audits = append(m.Audits, *audit)
	}
	return &postgres.TransitionResult{Prev: prev, Next: clone(next), Changed: true}, nil
}

func (m *MemStore) enqueue(evs ...postgres.OutboxEvent) {
	for _, e := range evs {
		dup := false
		if e.IdempotencyKey != "" {
			for _, o := range m.Outbox {
				if o.IdempotencyKey == e.IdempotencyKey {
					dup = true
				}
			}
		}
		if !dup {
			m.Outbox = append(m.Outbox, e)
		}
	}
}

func (m *MemStore) SetEgressID(_ context.Context, id uuid.UUID, egressID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if st, ok := m.Streams[id]; ok && st.EgressID == nil && egressID != "" {
		e := egressID
		st.EgressID = &e
	}
	return nil
}

func (m *MemStore) SetRecording(_ context.Context, id uuid.UUID, url string, durationSec int, job postgres.RecordingImport) (*postgres.LiveStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.Streams[id]
	if !ok {
		return nil, postgres.ErrNotFound
	}
	u := url
	d := durationSec
	st.RecordingURL, st.RecordingDurationSeconds = &u, &d
	if _, exists := m.Imports[id]; !exists {
		j := job
		j.StreamID, j.OwnerUserID, j.RecordingURL, j.State = id, st.CreatorUserID, url, postgres.ImportPending
		m.Imports[id] = &j
		m.ImportDue[id] = m.Now()
		m.ImportCreated[id] = m.Now()
	}
	return clone(st), nil
}

func (m *MemStore) ClaimDueImports(_ context.Context, limit int, lease time.Duration) ([]postgres.RecordingImport, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []postgres.RecordingImport
	now := m.Now()
	for id, j := range m.Imports {
		if j.State != postgres.ImportPending || m.ImportDue[id].After(now) || len(out) >= limit {
			continue
		}
		m.ImportDue[id] = now.Add(lease)
		cp := *j
		cp.Age = now.Sub(m.ImportCreated[id])
		out = append(out, cp)
	}
	return out, nil
}

func (m *MemStore) CompleteImport(_ context.Context, streamID, mediaID uuid.UUID, events func(*postgres.LiveStream, postgres.RecordingImport) ([]postgres.OutboxEvent, error)) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.Imports[streamID]
	if !ok || j.State != postgres.ImportPending {
		return nil
	}
	done := *j
	now := m.Now()
	id := mediaID
	ready := "ready"
	done.MediaID, done.DoneAt, done.State, done.ProcessingStatus = &id, &now, postgres.ImportDone, &ready
	done.Attempts++
	evs, err := events(clone(m.Streams[streamID]), done)
	if err != nil {
		return err
	}
	if len(evs) > 0 && m.FailOutbox {
		return errors.New("outbox insert failed")
	}
	*j = done
	m.enqueue(evs...)
	return nil
}

func (m *MemStore) RetryImport(_ context.Context, streamID uuid.UUID, mediaID *uuid.UUID, status, msg string, retryIn time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.Imports[streamID]; ok && j.State == postgres.ImportPending {
		m.noteAttempt(j, mediaID, status, msg)
		m.ImportDue[streamID] = m.Now().Add(retryIn)
	}
	return nil
}

func (m *MemStore) TerminateImport(_ context.Context, streamID uuid.UUID, mediaID *uuid.UUID, status, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j, ok := m.Imports[streamID]; ok && j.State == postgres.ImportPending {
		m.noteAttempt(j, mediaID, status, msg)
		now := m.Now()
		j.State, j.DoneAt = postgres.ImportFailed, &now
	}
	return nil
}

func (m *MemStore) noteAttempt(j *postgres.RecordingImport, mediaID *uuid.UUID, status, msg string) {
	j.Attempts++
	if msg != "" {
		e := msg
		j.LastError = &e
	}
	if mediaID != nil {
		id := *mediaID
		j.MediaID = &id
	}
	if status != "" {
		s := status
		j.ProcessingStatus = &s
	}
}

func (m *MemStore) ApplyPresence(_ context.Context, streamID, userID uuid.UUID, present bool) (*postgres.LiveStream, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.Streams[streamID]
	if !ok {
		return nil, false, postgres.ErrNotFound
	}
	if m.Presence[streamID] == nil {
		m.Presence[streamID] = map[uuid.UUID]bool{}
	}
	m.Presence[streamID][userID] = present
	if userID != st.CreatorUserID {
		m.ViewerEvents++
	}
	if postgres.IsTerminal(st.Status) {
		return clone(st), false, nil
	}
	n := 0
	for uid, p := range m.Presence[streamID] {
		if p && uid != st.CreatorUserID {
			n++
		}
	}
	old := st.ViewerCount
	st.ViewerCount = n
	if n > st.ViewerPeak {
		st.ViewerPeak = n
	}
	return clone(st), n != old, nil
}

func (m *MemStore) ListDueForTimeout(_ context.Context, startTimeout, grace time.Duration, limit int) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	var out []uuid.UUID
	for id, st := range m.Streams {
		age := now.Sub(st.StatusChangedAt)
		if (st.Status == postgres.StatusStarting && age > startTimeout) ||
			(st.Status == postgres.StatusReconnecting && age > grace) {
			out = append(out, id)
		}
	}
	return out, nil
}

func (m *MemStore) ListByStatuses(_ context.Context, statuses []string, _ int) ([]*postgres.AdminStream, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*postgres.AdminStream{}
	for _, st := range m.Streams {
		for _, s := range statuses {
			if st.Status == s {
				open := 0
				for _, r := range m.Reports {
					if r.StreamID == st.ID && r.Status == "open" {
						open++
					}
				}
				out = append(out, &postgres.AdminStream{LiveStream: *clone(st), OpenReports: open})
			}
		}
	}
	return out, nil
}

func (m *MemStore) WebhookSeen(_ context.Context, id string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Webhooks[id], nil
}

func (m *MemStore) MarkWebhook(_ context.Context, id, _ string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Webhooks[id] = true
	return nil
}

func (m *MemStore) PruneWebhookEvents(context.Context, time.Duration) error { return nil }

// --- chat ---

func (m *MemStore) InsertChatMessage(_ context.Context, streamID, userID uuid.UUID, text string) (*postgres.ChatMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg := &postgres.ChatMessage{ID: uuid.New(), StreamID: streamID, UserID: userID, Text: text, CreatedAt: m.Now()}
	m.Messages[msg.ID] = msg
	cp := *msg
	return &cp, nil
}

func (m *MemStore) ListRecentChatMessages(_ context.Context, streamID uuid.UUID, _ int) ([]*postgres.ChatMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*postgres.ChatMessage{}
	for _, msg := range m.Messages {
		if msg.StreamID == streamID && msg.RemovedAt == nil {
			cp := *msg
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *MemStore) GetChatMessage(_ context.Context, streamID, messageID uuid.UUID) (*postgres.ChatMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.Messages[messageID]
	if !ok || msg.StreamID != streamID {
		return nil, postgres.ErrNotFound
	}
	cp := *msg
	return &cp, nil
}

func (m *MemStore) RemoveChatMessage(_ context.Context, streamID, messageID, _ uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.removeLocked(streamID, messageID)
}

func (m *MemStore) removeLocked(streamID, messageID uuid.UUID) (bool, error) {
	msg, ok := m.Messages[messageID]
	if !ok || msg.StreamID != streamID {
		return false, postgres.ErrNotFound
	}
	if msg.RemovedAt != nil {
		return false, nil
	}
	now := m.Now()
	msg.RemovedAt = &now
	msg.IsPinned, msg.PinnedAt = false, nil
	if m.Pinned[streamID] == messageID {
		delete(m.Pinned, streamID)
	}
	return true, nil
}

// --- Phase B moderation ---

func (m *MemStore) MuteUser(_ context.Context, streamID, userID, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.Mutes[streamID] == nil {
		m.Mutes[streamID] = map[uuid.UUID]bool{}
	}
	m.Mutes[streamID][userID] = true
	return nil
}

func (m *MemStore) UnmuteUser(_ context.Context, streamID, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Mutes[streamID], userID)
	return nil
}

func (m *MemStore) IsUserMuted(_ context.Context, streamID, userID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.Mutes[streamID][userID], nil
}

func (m *MemStore) ListMutedUsers(_ context.Context, streamID uuid.UUID) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []uuid.UUID{}
	for u := range m.Mutes[streamID] {
		out = append(out, u)
	}
	return out, nil
}

func (m *MemStore) AddWordFilter(_ context.Context, streamID uuid.UUID, word string, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.WordFilters[streamID] == nil {
		m.WordFilters[streamID] = map[string]bool{}
	}
	m.WordFilters[streamID][strings.ToLower(strings.TrimSpace(word))] = true
	return nil
}

func (m *MemStore) RemoveWordFilter(_ context.Context, streamID uuid.UUID, word string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.WordFilters[streamID], strings.ToLower(strings.TrimSpace(word)))
	return nil
}

func (m *MemStore) ListWordFilters(_ context.Context, streamID uuid.UUID) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []string{}
	for w := range m.WordFilters[streamID] {
		out = append(out, w)
	}
	return out, nil
}

func (m *MemStore) MatchesWordFilter(_ context.Context, streamID uuid.UUID, text string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	lt := strings.ToLower(text)
	for w := range m.WordFilters[streamID] {
		if strings.Contains(lt, w) {
			return true, nil
		}
	}
	return false, nil
}

func (m *MemStore) PinMessage(_ context.Context, streamID, messageID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	msg, ok := m.Messages[messageID]
	if !ok || msg.StreamID != streamID || msg.RemovedAt != nil {
		return postgres.ErrNotFound
	}
	if prev, ok := m.Pinned[streamID]; ok {
		if pm, ok := m.Messages[prev]; ok {
			pm.IsPinned, pm.PinnedAt = false, nil
		}
	}
	now := m.Now()
	msg.IsPinned, msg.PinnedAt = true, &now
	m.Pinned[streamID] = messageID
	return nil
}

func (m *MemStore) UnpinMessage(_ context.Context, streamID, messageID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if msg, ok := m.Messages[messageID]; ok && msg.StreamID == streamID {
		msg.IsPinned, msg.PinnedAt = false, nil
	}
	if m.Pinned[streamID] == messageID {
		delete(m.Pinned, streamID)
	}
	return nil
}

func (m *MemStore) GetPinnedMessage(_ context.Context, streamID uuid.UUID) (*postgres.ChatMessage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	id, ok := m.Pinned[streamID]
	if !ok {
		return nil, nil
	}
	cp := *m.Messages[id]
	return &cp, nil
}

// --- moderation v2 ---

func (m *MemStore) IsModerator(_ context.Context, streamID, userID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, id := range m.Mods[streamID] {
		if id == userID {
			return true, nil
		}
	}
	return false, nil
}

func (m *MemStore) ListModerators(_ context.Context, streamID uuid.UUID) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]uuid.UUID{}, m.Mods[streamID]...), nil
}

func (m *MemStore) ModeratorsFor(_ context.Context, ids []uuid.UUID) (map[uuid.UUID][]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := map[uuid.UUID][]uuid.UUID{}
	for _, id := range ids {
		if l := m.Mods[id]; len(l) > 0 {
			out[id] = append([]uuid.UUID{}, l...)
		}
	}
	return out, nil
}

func (m *MemStore) ReplaceModerators(_ context.Context, streamID uuid.UUID, userIDs []uuid.UUID, _ uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Mods[streamID] = append([]uuid.UUID{}, userIDs...)
	return nil
}

func (m *MemStore) BanFromStream(_ context.Context, streamID, userID, by uuid.UUID, reason string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.banLocked(streamID, userID, by, reason)
	return nil
}

func (m *MemStore) banLocked(streamID, userID, by uuid.UUID, reason string) {
	if m.Bans[streamID] == nil {
		m.Bans[streamID] = map[uuid.UUID]postgres.StreamBan{}
	}
	m.Bans[streamID][userID] = postgres.StreamBan{StreamID: streamID, UserID: userID, BannedBy: by, Reason: reason, BannedAt: m.Now()}
	kept := m.Mods[streamID][:0]
	for _, id := range m.Mods[streamID] {
		if id != userID {
			kept = append(kept, id)
		}
	}
	m.Mods[streamID] = kept
}

func (m *MemStore) UnbanFromStream(_ context.Context, streamID, userID uuid.UUID) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.Bans[streamID], userID)
	return nil
}

func (m *MemStore) IsBannedFromStream(_ context.Context, streamID, userID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.Bans[streamID][userID]
	return ok, nil
}

func (m *MemStore) ListStreamBans(_ context.Context, streamID uuid.UUID) ([]postgres.StreamBan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []postgres.StreamBan{}
	for _, b := range m.Bans[streamID] {
		out = append(out, b)
	}
	return out, nil
}

func (m *MemStore) IsPlatformBanned(_ context.Context, userID uuid.UUID) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.PlatformBans[userID]
	return ok, nil
}

func (m *MemStore) ListPlatformBans(_ context.Context, limit, offset int) ([]postgres.PlatformBan, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []postgres.PlatformBan{}
	for _, b := range m.PlatformBans {
		out = append(out, b)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BannedAt.After(out[j].BannedAt) })
	if offset >= len(out) {
		return []postgres.PlatformBan{}, nil
	}
	out = out[offset:]
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *MemStore) AdminSetPlatformBan(_ context.Context, userID uuid.UUID, banned bool, reason string, audit postgres.AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if banned {
		m.PlatformBans[userID] = postgres.PlatformBan{UserID: userID, Reason: reason, BannedBy: audit.ActorID, BannedAt: m.Now()}
	} else {
		delete(m.PlatformBans, userID)
	}
	m.Audits = append(m.Audits, audit)
	return nil
}

func (m *MemStore) ActiveStreamsOf(_ context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []uuid.UUID
	for id, st := range m.Streams {
		if st.CreatorUserID == userID && (st.Status == postgres.StatusStarting || st.Status == postgres.StatusLive || st.Status == postgres.StatusReconnecting) {
			out = append(out, id)
		}
	}
	return out, nil
}

func (m *MemStore) AdminRemoveChatMessage(_ context.Context, streamID, messageID uuid.UUID, audit postgres.AuditEntry) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed, err := m.removeLocked(streamID, messageID)
	if err != nil {
		return false, err
	}
	m.Audits = append(m.Audits, audit)
	return removed, nil
}

func (m *MemStore) CreateReport(_ context.Context, r postgres.NewReport, maxPerWindow int, window time.Duration) (*postgres.Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	recent := 0
	for _, x := range m.Reports {
		if x.ReporterID == r.ReporterID && now.Sub(x.CreatedAt) < window {
			recent++
		}
	}
	if recent >= maxPerWindow {
		return nil, postgres.ErrReportRateLimited
	}
	for _, x := range m.Reports {
		sameMsg := (x.MessageID == nil && r.MessageID == nil) || (x.MessageID != nil && r.MessageID != nil && *x.MessageID == *r.MessageID)
		if x.ReporterID == r.ReporterID && x.StreamID == r.StreamID && sameMsg {
			return nil, postgres.ErrDuplicate
		}
	}
	rep := &postgres.Report{
		ID: uuid.New(), StreamID: r.StreamID, ReporterID: r.ReporterID, MessageID: r.MessageID,
		TargetUserID: r.TargetUserID, Reason: r.Reason, Note: r.Note, Status: "open", CreatedAt: now,
	}
	m.Reports = append(m.Reports, rep)
	cp := *rep
	return &cp, nil
}

func (m *MemStore) ListReports(_ context.Context, status string, _ int) ([]*postgres.Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := []*postgres.Report{}
	for _, r := range m.Reports {
		if status == "all" || r.Status == status {
			cp := *r
			out = append(out, &cp)
		}
	}
	return out, nil
}

func (m *MemStore) AdminResolveReport(_ context.Context, reportID uuid.UUID, act postgres.ResolveAction, check func(*postgres.Report) error, audit postgres.AuditEntry) (*postgres.Report, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var rep *postgres.Report
	for _, r := range m.Reports {
		if r.ID == reportID {
			rep = r
		}
	}
	if rep == nil {
		return nil, postgres.ErrNotFound
	}
	if rep.Status != "open" {
		return nil, postgres.ErrReportResolved
	}
	if check != nil {
		cp := *rep
		if err := check(&cp); err != nil {
			return nil, err
		}
	}
	switch act.Action {
	case "remove_message":
		if rep.MessageID != nil {
			if _, err := m.removeLocked(rep.StreamID, *rep.MessageID); err != nil && !errors.Is(err, postgres.ErrNotFound) {
				return nil, err
			}
		}
	case "ban_user":
		m.banLocked(rep.StreamID, rep.TargetUserID, audit.ActorID, act.Reason)
	}
	now := m.Now()
	a, rsn, by := act.Action, act.Reason, audit.ActorID
	rep.Status, rep.Resolution, rep.ResolutionReason, rep.ResolvedBy, rep.ResolvedAt = "resolved", &a, &rsn, &by, &now
	m.Audits = append(m.Audits, audit)
	cp := *rep
	return &cp, nil
}
