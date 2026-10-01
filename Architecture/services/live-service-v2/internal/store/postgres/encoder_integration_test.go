package postgres

// Encoder streams against a real Postgres (migration 004). Same rule as
// integration_test.go: LIVE_V2_TEST_DSN, and only a database whose name ends
// in _test.

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

func itEncoderStream(t *testing.T, s *Store, creator uuid.UUID) *LiveStream {
	t.Helper()
	st, err := s.CreateStream(context.Background(), CreateStreamParams{
		CreatorUserID: creator, LiveKitRoom: "stream_" + uuid.NewString(), Title: "it", Visibility: "public", Source: SourceEncoder,
	})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

// TestIntegrationEncoderSourceColumn: source defaults to device, is stored
// and returned by every read, and the CHECK refuses anything else.
func TestIntegrationEncoderSourceColumn(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	dev := itStream(t, s, uuid.New())
	enc := itEncoderStream(t, s, uuid.New())
	if dev.Source != SourceDevice || enc.Source != SourceEncoder {
		t.Fatalf("created sources = %q, %q", dev.Source, enc.Source)
	}
	if dev.IngressID != nil || enc.IngressID != nil || enc.EncoderIdentity != nil {
		t.Fatalf("a new stream has an ingress: %+v", enc)
	}
	byID, err := s.GetByID(ctx, enc.ID)
	if err != nil || byID.Source != SourceEncoder {
		t.Fatalf("GetByID: %+v %v", byID, err)
	}
	byRoom, err := s.GetByRoom(ctx, enc.LiveKitRoom)
	if err != nil || byRoom.ID != enc.ID || byRoom.Source != SourceEncoder {
		t.Fatalf("GetByRoom: %+v %v", byRoom, err)
	}
	// A row written without the column (every row before migration 004).
	var src string
	if err := pool.QueryRow(ctx, `
		INSERT INTO live_streams (creator_user_id, livekit_room, title) VALUES ($1, $2, 'old')
		RETURNING source`, uuid.New(), "stream_"+uuid.NewString()).Scan(&src); err != nil || src != SourceDevice {
		t.Fatalf("column default = %q %v", src, err)
	}
	if _, err := pool.Exec(ctx, `UPDATE live_streams SET source = 'rtmp' WHERE id = $1`, enc.ID); err == nil {
		t.Fatal("the source check accepted 'rtmp'")
	}
	if _, err := s.CreateStream(ctx, CreateStreamParams{
		CreatorUserID: uuid.New(), LiveKitRoom: "stream_" + uuid.NewString(), Title: "it", Visibility: "public", Source: "rtmp",
	}); err == nil {
		t.Fatal("CreateStream stored source 'rtmp'")
	}
	// The transition's RETURNING carries the new columns.
	res, err := s.ApplyTransition(ctx, enc.ID, to(StatusStarting, ""), nil, nil)
	if err != nil || res.Next.Source != SourceEncoder {
		t.Fatalf("transition row: %+v %v", res, err)
	}
}

// TestIntegrationIngressOnTheRow: SetIngress stores once, only on an encoder
// stream that is not on air; ClearIngress clears only the ingress it names
// and keeps the encoder identity; ended streams that still carry one are
// listed for the sweeper.
func TestIntegrationIngressOnTheRow(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	enc := itEncoderStream(t, s, uuid.New())
	ident := "encoder_" + enc.ID.String()

	if _, err := s.SetIngress(ctx, enc.ID, "", ident); err == nil {
		t.Fatal("an empty ingress id was accepted")
	}
	dev := itStream(t, s, uuid.New())
	if ok, err := s.SetIngress(ctx, dev.ID, "IN_dev", "encoder_"+dev.ID.String()); err != nil || ok {
		t.Fatalf("a device stream took an ingress: %v %v", ok, err)
	}
	if ok, err := s.SetIngress(ctx, uuid.New(), "IN_none", "encoder_x"); err != nil || ok {
		t.Fatalf("a missing stream took an ingress: %v %v", ok, err)
	}

	if ok, err := s.SetIngress(ctx, enc.ID, "IN_1", ident); err != nil || !ok {
		t.Fatalf("first SetIngress: %v %v", ok, err)
	}
	if ok, err := s.SetIngress(ctx, enc.ID, "IN_2", ident); err != nil || ok {
		t.Fatalf("a second ingress replaced the first: %v %v", ok, err)
	}
	got, _ := s.GetByID(ctx, enc.ID)
	if got.IngressID == nil || *got.IngressID != "IN_1" || got.EncoderIdentity == nil || *got.EncoderIdentity != ident {
		t.Fatalf("row = %+v", got)
	}

	// Clearing another ingress id changes nothing.
	if err := s.ClearIngress(ctx, enc.ID, "IN_2"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.GetByID(ctx, enc.ID); got.IngressID == nil {
		t.Fatal("ClearIngress cleared an ingress it was not given")
	}
	if err := s.ClearIngress(ctx, enc.ID, "IN_1"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetByID(ctx, enc.ID)
	if got.IngressID != nil || got.EncoderIdentity == nil || *got.EncoderIdentity != ident {
		t.Fatalf("after clear: ingress=%v identity=%v", got.IngressID, got.EncoderIdentity)
	}

	// By status: every status but ended takes one.
	for status, want := range map[string]bool{
		StatusScheduled: true, StatusStarting: true, StatusFailed: true,
		StatusLive: true, StatusReconnecting: true, StatusEnded: false,
	} {
		st := itEncoderStream(t, s, uuid.New())
		if status != StatusScheduled {
			reason := ""
			switch status {
			case StatusFailed:
				reason = "no_media"
			case StatusEnded:
				reason = "host_ended"
			}
			if _, err := s.ApplyTransition(ctx, st.ID, to(status, reason), nil, nil); err != nil {
				t.Fatal(err)
			}
		}
		ok, err := s.SetIngress(ctx, st.ID, "IN_"+status, "encoder_"+st.ID.String())
		if err != nil || ok != want {
			t.Fatalf("SetIngress in %s = %v %v, want %v", status, ok, err, want)
		}
	}

	// The sweeper's list: ended with an ingress, or failed for longer than
	// failedKeep by the database clock; never a fresh failure or a stream on air.
	ended := itEncoderStream(t, s, uuid.New())
	failed := itEncoderStream(t, s, uuid.New())
	failedOld := itEncoderStream(t, s, uuid.New())
	live := itEncoderStream(t, s, uuid.New())
	for _, st := range []*LiveStream{ended, failed, failedOld, live} {
		if ok, err := s.SetIngress(ctx, st.ID, "IN_"+st.ID.String(), "encoder_"+st.ID.String()); err != nil || !ok {
			t.Fatal(ok, err)
		}
		_, _ = s.ApplyTransition(ctx, st.ID, to(StatusStarting, ""), nil, nil)
	}
	res, err := s.ApplyTransition(ctx, ended.ID, to(StatusEnded, "host_ended"), nil, nil)
	if err != nil || res.Next.IngressID == nil {
		t.Fatalf("the ended row lost its ingress id: %+v %v", res, err)
	}
	_, _ = s.ApplyTransition(ctx, failed.ID, to(StatusFailed, "no_media"), nil, nil)
	_, _ = s.ApplyTransition(ctx, failedOld.ID, to(StatusFailed, "no_media"), nil, nil)
	if _, err := pool.Exec(ctx, `UPDATE live_streams SET status_changed_at = NOW() - interval '25 hours' WHERE id = $1`, failedOld.ID); err != nil {
		t.Fatal(err)
	}
	_, _ = s.ApplyTransition(ctx, live.ID, to(StatusLive, ""), nil, nil)
	left, err := s.ListEndedWithIngress(ctx, 24*time.Hour, 100000)
	if err != nil {
		t.Fatal(err)
	}
	listed := map[uuid.UUID]string{}
	for _, si := range left {
		listed[si.StreamID] = si.IngressID
	}
	if listed[ended.ID] != "IN_"+ended.ID.String() {
		t.Fatalf("the ended stream's ingress is not listed: %v", listed[ended.ID])
	}
	if listed[failedOld.ID] != "IN_"+failedOld.ID.String() {
		t.Fatal("a stream failed for 25 hours keeps its ingress")
	}
	if _, ok := listed[failed.ID]; ok {
		t.Fatal("a freshly failed stream's ingress is listed for deletion")
	}
	if _, ok := listed[live.ID]; ok {
		t.Fatal("a live stream's ingress is listed for deletion")
	}
	_ = s.ClearIngress(ctx, ended.ID, "IN_"+ended.ID.String())
	left, _ = s.ListEndedWithIngress(ctx, 24*time.Hour, 100000)
	for _, si := range left {
		if si.StreamID == ended.ID {
			t.Fatal("a cleared ingress is still listed")
		}
	}
}

// TestIntegrationEncoderStartTimeout: by the database clock a device stream
// is due after startTimeout and an encoder stream only after
// encoderStartTimeout.
func TestIntegrationEncoderStartTimeout(t *testing.T) {
	s, pool := integrationStore(t)
	ctx := context.Background()
	dev := itStream(t, s, uuid.New())
	encYoung := itEncoderStream(t, s, uuid.New())
	encOld := itEncoderStream(t, s, uuid.New())
	for id, age := range map[uuid.UUID]string{dev.ID: "121 seconds", encYoung.ID: "121 seconds", encOld.ID: "601 seconds"} {
		if _, err := s.ApplyTransition(ctx, id, to(StatusStarting, ""), nil, nil); err != nil {
			t.Fatal(err)
		}
		if _, err := pool.Exec(ctx, `UPDATE live_streams SET status_changed_at = NOW() - $2::interval WHERE id = $1`, id, age); err != nil {
			t.Fatal(err)
		}
	}
	ids, err := s.ListDueForTimeout(ctx, 120*time.Second, 600*time.Second, 60*time.Second, 100000)
	if err != nil {
		t.Fatal(err)
	}
	due := map[uuid.UUID]bool{}
	for _, id := range ids {
		due[id] = true
	}
	if !due[dev.ID] || due[encYoung.ID] || !due[encOld.ID] {
		t.Fatalf("due: device(121s)=%v encoder(121s)=%v encoder(601s)=%v", due[dev.ID], due[encYoung.ID], due[encOld.ID])
	}
}
