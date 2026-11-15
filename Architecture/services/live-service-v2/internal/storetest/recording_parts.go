package storetest

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// PartRow is one live_recording_parts row.
type PartRow struct {
	postgres.RecordingPart
	CreatedAt time.Time
}

// RecordingParts returns copies of a stream's parts, in order.
func (m *MemStore) RecordingParts(streamID uuid.UUID) []postgres.RecordingPart {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []postgres.RecordingPart
	for _, p := range m.Parts[streamID] {
		out = append(out, p.RecordingPart)
	}
	return out
}

func (m *MemStore) ReserveRecordingPart(_ context.Context, streamID uuid.UUID, p postgres.ReservePartParams) (*postgres.RecordingPart, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.Streams[streamID]
	if !ok {
		return nil, false, postgres.ErrNotFound
	}
	if st.Status != postgres.StatusLive {
		return nil, false, nil
	}
	now := m.Now()
	parts := m.Parts[streamID]
	active, maxPart := 0, 0
	for _, pr := range parts {
		if pr.State == postgres.PartStarting && now.Sub(pr.CreatedAt) > p.StaleStart {
			msg := "start abandoned"
			pr.State, pr.LastError = postgres.PartFailed, &msg
		}
		if pr.State == postgres.PartStarting || pr.State == postgres.PartRecording {
			active++
		}
		if pr.Part > maxPart {
			maxPart = pr.Part
		}
	}
	if active > 0 || len(parts) >= p.MaxParts || (len(parts) == 0 && st.EgressID != nil && *st.EgressID != "") {
		return nil, false, nil
	}
	row := &PartRow{RecordingPart: postgres.RecordingPart{StreamID: streamID, Part: maxPart + 1, State: postgres.PartStarting}, CreatedAt: now}
	row.ObjectKey = p.KeyFor(row.Part)
	if m.Parts == nil {
		m.Parts = map[uuid.UUID][]*PartRow{}
	}
	m.Parts[streamID] = append(parts, row)
	cp := row.RecordingPart
	return &cp, true, nil
}

func (m *MemStore) part(streamID uuid.UUID, part int) *PartRow {
	for _, p := range m.Parts[streamID] {
		if p.Part == part {
			return p
		}
	}
	return nil
}

func (m *MemStore) AttachRecordingEgress(_ context.Context, streamID uuid.UUID, part int, egressID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.Streams[streamID]
	if !ok {
		return false, postgres.ErrNotFound
	}
	p := m.part(streamID, part)
	if p == nil || p.State != postgres.PartStarting {
		return false, nil
	}
	id := egressID
	p.EgressID = &id
	if postgres.IsTerminal(st.Status) {
		msg := "stream ended before the recording started"
		p.State, p.LastError = postgres.PartFailed, &msg
		return false, nil
	}
	p.State = postgres.PartRecording
	return true, nil
}

func (m *MemStore) FailRecordingPart(_ context.Context, streamID uuid.UUID, part int, msg string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.part(streamID, part); p != nil && p.State == postgres.PartStarting {
		e := msg
		p.State, p.LastError = postgres.PartFailed, &e
	}
	return nil
}

func (m *MemStore) FinishRecordingPart(_ context.Context, streamID uuid.UUID, f postgres.PartFinish) (matched, hasParts bool, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	parts := m.Parts[streamID]
	byID := func(p *PartRow) bool { return f.EgressID != "" && p.EgressID != nil && *p.EgressID == f.EgressID }
	byKey := func(p *PartRow) bool { return f.ObjectKey != "" && p.ObjectKey == f.ObjectKey }
	for _, p := range parts {
		active := p.State == postgres.PartStarting || p.State == postgres.PartRecording
		if !active || !(byID(p) || (p.EgressID == nil && byKey(p))) {
			continue
		}
		if p.EgressID == nil && f.EgressID != "" {
			id := f.EgressID
			p.EgressID = &id
		}
		p.State = postgres.PartFailed
		if f.Complete {
			p.State = postgres.PartComplete
		}
		if f.RecordingURL != "" {
			u := f.RecordingURL
			p.RecordingURL = &u
		}
		p.DurationMs = f.DurationMs
		if f.Error != "" {
			e := f.Error
			p.LastError = &e
		}
		return true, true, nil
	}
	for _, p := range parts {
		if byID(p) || byKey(p) {
			return true, true, nil // duplicate or late: left alone
		}
	}
	return false, len(parts) > 0, nil
}

func (m *MemStore) ActiveRecordingEgresses(_ context.Context, streamID uuid.UUID) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, p := range m.Parts[streamID] {
		if p.State == postgres.PartRecording && p.EgressID != nil {
			out = append(out, *p.EgressID)
		}
	}
	return out, nil
}

func (m *MemStore) QueueRecordingImport(_ context.Context, streamID uuid.UUID, p postgres.QueueImportParams) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	st, ok := m.Streams[streamID]
	if !ok {
		return false, postgres.ErrNotFound
	}
	if !postgres.IsTerminal(st.Status) {
		return false, nil
	}
	if _, queued := m.Imports[streamID]; queued {
		return false, nil
	}
	ended := st.StatusChangedAt
	if st.EndedAt != nil {
		ended = *st.EndedAt
	}
	endedLongAgo := m.Now().Sub(ended) > p.WaitActive
	var best *PartRow
	for _, pr := range m.Parts[streamID] {
		if (pr.State == postgres.PartStarting || pr.State == postgres.PartRecording) && !endedLongAgo {
			return false, nil
		}
		if pr.State == postgres.PartComplete && (best == nil || pr.DurationMs > best.DurationMs) {
			best = pr
		}
	}
	if best == nil {
		return false, nil
	}
	url := ""
	if best.RecordingURL != nil {
		url = *best.RecordingURL
	}
	if url == "" && p.URLFor != nil {
		url = p.URLFor(best.ObjectKey)
	}
	d := int(best.DurationMs / 1000)
	st.RecordingURL, st.RecordingDurationSeconds = &url, &d
	m.Imports[streamID] = &postgres.RecordingImport{
		StreamID: streamID, OwnerUserID: st.CreatorUserID, Bucket: p.Bucket, ObjectKey: best.ObjectKey,
		RecordingURL: url, DurationMs: best.DurationMs, State: postgres.ImportPending,
	}
	m.ImportDue[streamID] = m.Now()
	m.ImportCreated[streamID] = m.Now()
	return true, nil
}

func (m *MemStore) ListAwaitingImport(_ context.Context, within time.Duration, limit int) ([]uuid.UUID, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.Now()
	var out []uuid.UUID
	for id, parts := range m.Parts {
		st := m.Streams[id]
		if st == nil || !postgres.IsTerminal(st.Status) || len(out) >= limit {
			continue
		}
		if _, queued := m.Imports[id]; queued {
			continue
		}
		for _, p := range parts {
			if p.State == postgres.PartComplete && now.Sub(p.CreatedAt) < within {
				out = append(out, id)
				break
			}
		}
	}
	return out, nil
}
