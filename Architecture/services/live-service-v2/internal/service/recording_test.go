package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/shared/servicetoken"

	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// fakeImporter answers ImportRecording from a func.
type fakeImporter struct {
	calls []ImportRecordingRequest
	fn    func() (ImportResult, error)
}

func (f *fakeImporter) ImportRecording(_ context.Context, req ImportRecordingRequest) (ImportResult, error) {
	f.calls = append(f.calls, req)
	return f.fn()
}

func egressEnded(st *postgres.LiveStream, status string) WebhookEvent {
	return WebhookEvent{ID: uuid.NewString(), Event: "egress_ended", Egress: &EgressResult{
		EgressID: "EG_1", RoomName: st.LiveKitRoom, Status: status,
		Location: "https://s3/live-recordings/recordings/" + st.ID.String() + ".mp4",
		Filename: "recordings/" + st.ID.String() + ".mp4", DurationNs: int64(90 * time.Second),
	}}
}

func importRig(t *testing.T) (*rig, *postgres.LiveStream) {
	t.Helper()
	host := uuid.New()
	r := newRig(host)
	r.svc.s3Bucket = "live-recordings"
	st := r.store.AddStream(host)
	if err := r.svc.HandleWebhook(ctx, egressEnded(st, "EGRESS_COMPLETE")); err != nil {
		t.Fatal(err)
	}
	return r, st
}

// TestVODReadyOnlyWhenTheAssetIsReady: an imported asset that is still
// processing is re-asked every 30s; vod_ready goes out once, with the media
// id, only when media-service says ready.
func TestVODReadyOnlyWhenTheAssetIsReady(t *testing.T) {
	r, st := importRig(t)
	host := st.CreatorUserID

	// No importer configured: nothing goes out, the job waits.
	r.svc.RunImports(ctx)
	if len(r.store.Outbox) != 0 || r.store.Imports[st.ID].State != postgres.ImportPending {
		t.Fatalf("outbox=%v job=%+v", outboxTypes(r), r.store.Imports[st.ID])
	}

	mediaID := uuid.New()
	status := "processing"
	imp := &fakeImporter{fn: func() (ImportResult, error) { return ImportResult{MediaID: mediaID, ProcessingStatus: status}, nil }}
	r.svc.media = imp
	r.clock.Advance(importBackoff(1) + time.Second)
	r.svc.RunImports(ctx)
	if len(imp.calls) != 1 || len(r.store.Outbox) != 0 {
		t.Fatalf("processing asset: calls=%d outbox=%v", len(imp.calls), outboxTypes(r))
	}
	r.svc.RunImports(ctx) // inside the 30s poll interval
	if len(imp.calls) != 1 {
		t.Fatalf("re-asked inside the poll interval")
	}
	r.clock.Advance(importPollInterval + time.Second)
	r.svc.RunImports(ctx)
	if len(imp.calls) != 2 || len(r.store.Outbox) != 0 {
		t.Fatalf("still processing: calls=%d outbox=%v", len(imp.calls), outboxTypes(r))
	}
	status = "ready"
	r.clock.Advance(importPollInterval + time.Second)
	r.svc.RunImports(ctx)
	if len(r.store.Outbox) != 1 || r.store.Outbox[0].EventType != "live.stream.vod_ready" {
		t.Fatalf("outbox = %v", outboxTypes(r))
	}
	req := imp.calls[len(imp.calls)-1]
	if req.OwnerUserID != host.String() || req.Bucket != "live-recordings" || req.Key != "recordings/"+st.ID.String()+".mp4" ||
		req.ContentType != "video/mp4" || req.DurationMs != 90000 || req.Source != "live_recording" || req.SourceRef != st.ID.String() {
		t.Fatalf("import request = %+v", req)
	}
	var env struct {
		Payload struct {
			MediaAssetID string `json:"media_asset_id"`
			StreamID     string `json:"stream_id"`
			DurationSec  int    `json:"duration_sec"`
			Title        string `json:"title"`
		} `json:"payload"`
	}
	_ = json.Unmarshal(r.store.Outbox[0].Payload, &env)
	if env.Payload.MediaAssetID != mediaID.String() || env.Payload.StreamID != st.ID.String() || env.Payload.DurationSec != 90 || env.Payload.Title == "" {
		t.Fatalf("vod_ready payload = %+v", env.Payload)
	}
	r.clock.Advance(time.Hour)
	r.svc.RunImports(ctx)
	if len(imp.calls) != 3 || len(r.store.Outbox) != 1 {
		t.Fatalf("a done job was re-imported: calls=%d", len(imp.calls))
	}
}

// TestImportTerminalAndRetryPaths.
func TestImportTerminalAndRetryPaths(t *testing.T) {
	for _, tc := range []struct {
		name     string
		res      ImportResult
		err      error
		terminal bool
	}{
		{"asset failed", ImportResult{MediaID: uuid.New(), ProcessingStatus: "failed"}, nil, true},
		{"asset rejected", ImportResult{MediaID: uuid.New(), ProcessingStatus: "rejected"}, nil, true},
		{"asset deleted", ImportResult{MediaID: uuid.New(), ProcessingStatus: "deleted"}, nil, true},
		{"key invalid", ImportResult{}, &ImportError{HTTPStatus: 400, Code: "RECORDING_KEY_INVALID", Terminal: true}, true},
		{"owner conflict", ImportResult{}, &ImportError{HTTPStatus: 409, Code: "RECORDING_OWNER_CONFLICT", Terminal: true}, true},
		{"not found yet", ImportResult{}, &ImportError{HTTPStatus: 404, Code: "RECORDING_NOT_FOUND"}, false},
		{"changed", ImportResult{}, &ImportError{HTTPStatus: 409, Code: "RECORDING_CHANGED"}, false},
		{"unavailable", ImportResult{}, &ImportError{HTTPStatus: 503}, false},
		{"network", ImportResult{}, errors.New("dial tcp: refused"), false},
	} {
		r, st := importRig(t)
		r.svc.media = &fakeImporter{fn: func() (ImportResult, error) { return tc.res, tc.err }}
		r.svc.RunImports(ctx)
		j := r.store.Imports[st.ID]
		wantState := postgres.ImportPending
		if tc.terminal {
			wantState = postgres.ImportFailed
		}
		if j.State != wantState || len(r.store.Outbox) != 0 {
			t.Fatalf("%s: state=%s outbox=%v", tc.name, j.State, outboxTypes(r))
		}
	}
	// Six hours without a ready asset: given up, no vod_ready.
	r, st := importRig(t)
	r.svc.media = &fakeImporter{fn: func() (ImportResult, error) {
		return ImportResult{MediaID: uuid.New(), ProcessingStatus: "processing"}, nil
	}}
	for i := 0; i < 800 && r.store.Imports[st.ID].State == postgres.ImportPending; i++ {
		r.svc.RunImports(ctx)
		r.clock.Advance(importPollInterval + time.Second)
	}
	if j := r.store.Imports[st.ID]; j.State != postgres.ImportFailed || len(r.store.Outbox) != 0 {
		t.Fatalf("after 6h: %+v outbox=%v", j, outboxTypes(r))
	}
	// A failed egress records nothing.
	r2, st2 := importRig(t)
	other := r2.store.AddStream(st2.CreatorUserID)
	_ = r2.svc.HandleWebhook(ctx, egressEnded(other, "EGRESS_FAILED"))
	if r2.store.Imports[other.ID] != nil {
		t.Fatal("a failed egress queued an import")
	}
}

// TestHTTPMediaImporter against an httptest media-service: the body, the
// internal key, no user identity, and the error classification.
func TestHTTPMediaImporter(t *testing.T) {
	mediaID := uuid.New()
	var gotKey, gotPath, gotUser, gotTok string
	var gotBody ImportRecordingRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey, gotPath, gotUser = r.Header.Get("X-Internal-Service-Key"), r.URL.Path, r.Header.Get("X-User-Id")
		gotTok = strings.TrimPrefix(r.Header.Get("X-Service-Authorization"), "Bearer ")
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		switch gotBody.SourceRef {
		case "notfound":
			w.WriteHeader(404)
			fmt.Fprint(w, `{"error":{"code":"RECORDING_NOT_FOUND","message":"no object"}}`)
		case "owner":
			w.WriteHeader(409)
			fmt.Fprint(w, `{"error":{"code":"RECORDING_OWNER_CONFLICT"}}`)
		case "changed":
			w.WriteHeader(409)
			fmt.Fprint(w, `{"error":{"code":"RECORDING_CHANGED"}}`)
		case "big":
			w.WriteHeader(413)
			fmt.Fprint(w, `{"error":{"code":"RECORDING_TOO_LARGE"}}`)
		case "down":
			w.WriteHeader(503)
		case "empty":
			fmt.Fprint(w, `{"data":{}}`)
		default:
			w.WriteHeader(201)
			fmt.Fprintf(w, `{"data":{"media_id":%q,"processing_status":"processing"}}`, mediaID)
		}
	}))
	defer srv.Close()
	pub, priv, _ := servicetoken.GenerateKeypair()
	signer, err := ImportSignerFromEnv(func(k string) string {
		return map[string]string{"LIVE_SERVICE_TOKEN_KID": "l1", "LIVE_SERVICE_TOKEN_PRIVKEY": priv}[k]
	})
	if err != nil {
		t.Fatal(err)
	}
	imp := NewHTTPMediaImporter(srv.URL+"/", "k1", signer)
	res, err := imp.ImportRecording(ctx, ImportRecordingRequest{SourceRef: "s1", Bucket: "b", Key: "k"})
	if err != nil || res.MediaID != mediaID || res.ProcessingStatus != "processing" || gotKey != "k1" || gotUser != "" ||
		gotPath != "/v1/media/internal/recordings/import" || gotBody.Bucket != "b" {
		t.Fatalf("res=%+v err=%v key=%q user=%q path=%q body=%+v", res, err, gotKey, gotUser, gotPath, gotBody)
	}
	for ref, terminal := range map[string]bool{"notfound": false, "owner": true, "changed": false, "big": true, "down": false} {
		_, err := imp.ImportRecording(ctx, ImportRecordingRequest{SourceRef: ref})
		var ie *ImportError
		if !errors.As(err, &ie) || ie.Terminal != terminal {
			t.Fatalf("%s: %v (terminal want %v)", ref, err, terminal)
		}
	}
	if _, err := imp.ImportRecording(ctx, ImportRecordingRequest{SourceRef: "empty"}); err == nil {
		t.Fatal("an answer without media_id was taken as success")
	}
	// The token is what media-service verifies: issuer live-service-v2,
	// audience media, operation media:recording.import.
	v := servicetoken.NewVerifier(AudienceMedia)
	if err := v.RegisterBase64(IssuerLiveService, "l1", pub, []string{OpRecordingImport}, nil); err != nil {
		t.Fatal(err)
	}
	if ver, err := v.Verify(gotTok, OpRecordingImport, ""); err != nil || ver.Issuer != IssuerLiveService {
		t.Fatalf("token: %v %+v", err, ver)
	}
	// No signer: no token header (dev path).
	gotTok = "unset"
	_, _ = NewHTTPMediaImporter(srv.URL, "k1", nil).ImportRecording(ctx, ImportRecordingRequest{SourceRef: "s2"})
	if gotTok != "" {
		t.Fatalf("a token was sent without a signer: %q", gotTok)
	}
	if _, err := ImportSignerFromEnv(func(k string) string { return map[string]string{"LIVE_SERVICE_TOKEN_KID": "l1"}[k] }); err == nil {
		t.Fatal("half a signer config was accepted")
	}
	if s, err := ImportSignerFromEnv(func(string) string { return "" }); s != nil || err != nil {
		t.Fatalf("unset signer: %v %v", s, err)
	}
	if NewHTTPMediaImporter("  ", "k", nil) != nil {
		t.Fatal("an empty URL must mean not configured")
	}
}
