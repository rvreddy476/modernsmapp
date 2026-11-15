package postgres

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestIntegrationRecordingParts: the part protocol of migration 008 —
// reserve only while live and nothing records, attach, finish by egress id
// (duplicates ignored), no part for an ended stream, and one import of the
// longest complete part once nothing records.
func TestIntegrationRecordingParts(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	st := itStream(t, s, uuid.New())
	keyFor := func(n int) string { return fmt.Sprintf("recordings/%s_part%d.mp4", st.ID, n) }
	params := ReservePartParams{KeyFor: keyFor, MaxParts: 3, StaleStart: time.Minute}
	setStatus := func(status string) {
		t.Helper()
		if _, err := pool.Exec(ctx, `UPDATE live_streams SET status = $2, status_changed_at = NOW(),
			ended_at = CASE WHEN $2 = 'ended' THEN NOW() ELSE ended_at END WHERE id = $1`, st.ID, status); err != nil {
			t.Fatal(err)
		}
	}

	if _, ok, err := s.ReserveRecordingPart(ctx, st.ID, params); err != nil || ok {
		t.Fatalf("reserved a part of a scheduled stream: ok=%v err=%v", ok, err)
	}
	setStatus(StatusLive)
	p1, ok, err := s.ReserveRecordingPart(ctx, st.ID, params)
	if err != nil || !ok || p1.Part != 1 || p1.ObjectKey != keyFor(1) {
		t.Fatalf("reserve 1 = %+v %v %v", p1, ok, err)
	}
	if _, ok, _ := s.ReserveRecordingPart(ctx, st.ID, params); ok {
		t.Fatal("reserved a second part while one is starting")
	}
	if attached, err := s.AttachRecordingEgress(ctx, st.ID, 1, "EG_A"); err != nil || !attached {
		t.Fatalf("attach = %v %v", attached, err)
	}
	if ids, _ := s.ActiveRecordingEgresses(ctx, st.ID); len(ids) != 1 || ids[0] != "EG_A" {
		t.Fatalf("active = %v", ids)
	}
	fin := PartFinish{EgressID: "EG_A", Complete: true, RecordingURL: "https://s3/a", DurationMs: 120000}
	if m, has, err := s.FinishRecordingPart(ctx, st.ID, fin); err != nil || !m || !has {
		t.Fatalf("finish = %v %v %v", m, has, err)
	}
	// A duplicate (even as failed) leaves the part complete.
	if m, _, _ := s.FinishRecordingPart(ctx, st.ID, PartFinish{EgressID: "EG_A", DurationMs: 1}); !m {
		t.Fatal("duplicate not matched")
	}
	p2, ok, err := s.ReserveRecordingPart(ctx, st.ID, params)
	if err != nil || !ok || p2.Part != 2 {
		t.Fatalf("reserve 2 = %+v %v %v", p2, ok, err)
	}
	if _, err := s.AttachRecordingEgress(ctx, st.ID, 2, "EG_B"); err != nil {
		t.Fatal(err)
	}

	// Ended: no part, no import while EG_B records; then the longest part.
	setStatus(StatusEnded)
	if _, ok, _ := s.ReserveRecordingPart(ctx, st.ID, params); ok {
		t.Fatal("reserved a part of an ended stream")
	}
	q := QueueImportParams{Bucket: "live-recordings", URLFor: func(k string) string { return "u/" + k }, WaitActive: time.Hour}
	if queued, err := s.QueueRecordingImport(ctx, st.ID, q); err != nil || queued {
		t.Fatalf("queued while a part records: %v %v", queued, err)
	}
	// Matched by key: an egress whose id was never attached.
	if m, _, err := s.FinishRecordingPart(ctx, st.ID, PartFinish{EgressID: "EG_B", ObjectKey: keyFor(2), Complete: true, DurationMs: 30000}); err != nil || !m {
		t.Fatalf("finish 2 = %v %v", m, err)
	}
	if queued, err := s.QueueRecordingImport(ctx, st.ID, q); err != nil || !queued {
		t.Fatalf("not queued: %v %v", queued, err)
	}
	if queued, _ := s.QueueRecordingImport(ctx, st.ID, q); queued {
		t.Fatal("queued twice")
	}
	j, err := s.GetImport(ctx, st.ID)
	if err != nil || j.ObjectKey != keyFor(1) || j.DurationMs != 120000 || j.RecordingURL != "https://s3/a" {
		t.Fatalf("import = %+v %v", j, err)
	}

	// A reservation whose stream ended before the attach is failed.
	st2 := itStream(t, s, uuid.New())
	if _, err := pool.Exec(ctx, `UPDATE live_streams SET status = 'live' WHERE id = $1`, st2.ID); err != nil {
		t.Fatal(err)
	}
	params.KeyFor = func(n int) string { return fmt.Sprintf("recordings/%s_part%d.mp4", st2.ID, n) }
	if _, ok, _ := s.ReserveRecordingPart(ctx, st2.ID, params); !ok {
		t.Fatal("reserve st2")
	}
	if _, err := pool.Exec(ctx, `UPDATE live_streams SET status = 'ended' WHERE id = $1`, st2.ID); err != nil {
		t.Fatal(err)
	}
	if attached, err := s.AttachRecordingEgress(ctx, st2.ID, 1, "EG_C"); err != nil || attached {
		t.Fatalf("attached to an ended stream: %v %v", attached, err)
	}
	if parts, _ := s.ListRecordingParts(ctx, st2.ID); len(parts) != 1 || parts[0].State != PartFailed {
		t.Fatalf("parts = %+v", parts)
	}
}
