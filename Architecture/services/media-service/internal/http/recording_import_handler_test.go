package http

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// POST /v1/media/internal/recordings/import over httptest, through the REAL
// RegisterRoutes, with a fake importer: caller authentication, the answer
// shape and the error mapping.

const recordingImportKey = "recording-import-internal-key"

type fakeRecordingImporter struct {
	calls  []service.RecordingImportInput
	result *service.RecordingImportResult
	err    error
}

func (f *fakeRecordingImporter) Import(_ context.Context, in service.RecordingImportInput) (*service.RecordingImportResult, error) {
	f.calls = append(f.calls, in)
	return f.result, f.err
}

type recordingImportFixture struct {
	imp                                  *fakeRecordingImporter
	liveSigner, commerceSigner, rogueSig *servicetoken.Signer
	verifier                             *servicetoken.Verifier
}

func newRecordingImportFixture(t *testing.T) *recordingImportFixture {
	t.Helper()
	f := &recordingImportFixture{imp: &fakeRecordingImporter{
		result: &service.RecordingImportResult{MediaID: uuid.New(), ProcessingStatus: "processing", Created: true},
	}}
	var livePub, commercePub string
	f.liveSigner, livePub = mustSigner(t, IssuerLiveService, "l1")
	f.commerceSigner, commercePub = mustSigner(t, IssuerCommerceService, "c1")
	f.rogueSig, _ = mustSigner(t, IssuerLiveService, "l1") // same name, unregistered key
	f.verifier = servicetoken.NewVerifier(AudienceMedia)
	if err := f.verifier.RegisterBase64(IssuerLiveService, "l1", livePub, []string{OpRecordingImport}, nil); err != nil {
		t.Fatal(err)
	}
	// commerce-service may even hold the import operation: the issuer check
	// still refuses it.
	if err := f.verifier.RegisterBase64(IssuerCommerceService, "c1", commercePub, []string{OpImageBytesRead, OpRecordingImport}, nil); err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *recordingImportFixture) router(t *testing.T, legacyKey bool) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	h := New(service.New(postgres.New(nil), nil)).WithInternalKey(recordingImportKey).
		WithRecordingImport(f.imp, f.verifier, legacyKey)
	h.RegisterRoutes(r, passthrough, passthrough)
	return r
}

const recordingImportPath = "/v1/media/internal/recordings/import"

func recordingImportBody() string {
	stream := uuid.NewString()
	return fmt.Sprintf(`{"owner_user_id":%q,"bucket":"live-recordings","key":"recordings/%s.mp4","content_type":"video/mp4","duration_ms":61000,"source":"live_recording","source_ref":%q}`,
		uuid.NewString(), stream, stream)
}

func postRecordingImport(r *gin.Engine, key, token, body string, extra map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, recordingImportPath, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if key != "" {
		req.Header.Set("X-Internal-Service-Key", key)
	}
	if token != "" {
		req.Header.Set(ServiceAuthHeader, "Bearer "+token)
	}
	for k, v := range extra {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func recordingErrorCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	return body.Error.Code
}

func TestRecordingImport_RouteOnlyWithKeyAndImporter(t *testing.T) {
	gin.SetMode(gin.TestMode)
	imp := &fakeRecordingImporter{result: &service.RecordingImportResult{MediaID: uuid.New(), Created: true}}

	noKey := gin.New()
	New(service.New(postgres.New(nil), nil)).WithRecordingImport(imp, nil, true).RegisterRoutes(noKey, passthrough, passthrough)
	if w := postRecordingImport(noKey, "", "", recordingImportBody(), nil); w.Code != http.StatusNotFound {
		t.Fatalf("without an internal key: %d, want 404", w.Code)
	}

	noImporter := gin.New()
	New(service.New(postgres.New(nil), nil)).WithInternalKey("k").RegisterRoutes(noImporter, passthrough, passthrough)
	if w := postRecordingImport(noImporter, "k", "", recordingImportBody(), nil); w.Code != http.StatusNotFound {
		t.Fatalf("without an importer: %d, want 404", w.Code)
	}
	if len(imp.calls) != 0 {
		t.Fatal("importer reached")
	}
}

func TestRecordingImport_CallerAuthentication(t *testing.T) {
	f := newRecordingImportFixture(t)
	live := func() string { return mint(t, f.liveSigner, AudienceMedia, OpRecordingImport) }

	strict := f.router(t, false)
	dev := f.router(t, true)
	cases := []struct {
		name   string
		r      *gin.Engine
		key    string
		token  string
		extra  map[string]string
		status int
		code   string
	}{
		{"no internal key", dev, "", "", nil, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"wrong internal key", dev, "nope", "", nil, http.StatusUnauthorized, "UNAUTHORIZED"},
		{"user identity header", dev, recordingImportKey, "", map[string]string{"X-User-Id": uuid.NewString()}, http.StatusForbidden, CodeUserCallerRefused},
		{"admin role header", strict, recordingImportKey, live(), map[string]string{"X-Admin-Role": "superadmin"}, http.StatusForbidden, CodeUserCallerRefused},
		{"bare key off dev", strict, recordingImportKey, "", nil, http.StatusUnauthorized, CodeServiceTokenRequired},
		{"commerce token", strict, recordingImportKey, mint(t, f.commerceSigner, AudienceMedia, OpRecordingImport), nil, http.StatusForbidden, CodeServiceTokenRejected},
		{"wrong operation", strict, recordingImportKey, mint(t, f.liveSigner, AudienceMedia, OpImageBytesRead), nil, http.StatusForbidden, CodeServiceTokenRejected},
		{"wrong audience", strict, recordingImportKey, mint(t, f.liveSigner, "payments", OpRecordingImport), nil, http.StatusForbidden, CodeServiceTokenRejected},
		{"unregistered key", strict, recordingImportKey, mint(t, f.rogueSig, AudienceMedia, OpRecordingImport), nil, http.StatusForbidden, CodeServiceTokenRejected},
		{"bad token on dev", dev, recordingImportKey, "garbage", nil, http.StatusForbidden, CodeServiceTokenRejected},
		{"live token", strict, recordingImportKey, live(), nil, http.StatusCreated, ""},
		{"bare key on dev", dev, recordingImportKey, "", nil, http.StatusCreated, ""},
	}
	for _, tc := range cases {
		before := len(f.imp.calls)
		w := postRecordingImport(tc.r, tc.key, tc.token, recordingImportBody(), tc.extra)
		if w.Code != tc.status {
			t.Fatalf("%s: status %d, want %d (%s)", tc.name, w.Code, tc.status, w.Body.String())
		}
		if tc.code != "" {
			if got := recordingErrorCode(t, w); got != tc.code {
				t.Fatalf("%s: code %q, want %q", tc.name, got, tc.code)
			}
			if len(f.imp.calls) != before {
				t.Fatalf("%s: importer reached by a refused caller", tc.name)
			}
		} else if len(f.imp.calls) != before+1 {
			t.Fatalf("%s: importer not reached", tc.name)
		}
	}
}

func TestRecordingImport_NoVerifierRefusesTokens(t *testing.T) {
	f := newRecordingImportFixture(t)
	gin.SetMode(gin.TestMode)
	r := gin.New()
	New(service.New(postgres.New(nil), nil)).WithInternalKey(recordingImportKey).
		WithRecordingImport(f.imp, nil, false).RegisterRoutes(r, passthrough, passthrough)
	w := postRecordingImport(r, recordingImportKey, mint(t, f.liveSigner, AudienceMedia, OpRecordingImport), recordingImportBody(), nil)
	if w.Code != http.StatusForbidden || recordingErrorCode(t, w) != CodeServiceTokenRejected {
		t.Fatalf("status %d %s", w.Code, w.Body.String())
	}
}

func TestRecordingImport_AnswerShapeAndBody(t *testing.T) {
	f := newRecordingImportFixture(t)
	r := f.router(t, true)
	body := recordingImportBody()
	w := postRecordingImport(r, recordingImportKey, "", body, nil)
	if w.Code != http.StatusCreated {
		t.Fatalf("status %d", w.Code)
	}
	var got struct {
		Data struct {
			MediaID          string `json:"media_id"`
			ProcessingStatus string `json:"processing_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Data.MediaID != f.imp.result.MediaID.String() || got.Data.ProcessingStatus != "processing" {
		t.Fatalf("answer = %s", w.Body.String())
	}
	var sent map[string]any
	_ = json.Unmarshal([]byte(body), &sent)
	call := f.imp.calls[len(f.imp.calls)-1]
	if call.OwnerUserID != sent["owner_user_id"] || call.Bucket != "live-recordings" || call.SourceRef != sent["source_ref"] ||
		call.Key != sent["key"] || call.Source != "live_recording" || call.ContentType != "video/mp4" || call.DurationMs != 61000 {
		t.Fatalf("importer got %+v", call)
	}

	// A repeat answers 200 with the same id.
	f.imp.result = &service.RecordingImportResult{MediaID: f.imp.result.MediaID, ProcessingStatus: "ready"}
	if w := postRecordingImport(r, recordingImportKey, "", body, nil); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `"ready"`) {
		t.Fatalf("repeat: %d %s", w.Code, w.Body.String())
	}

	if w := postRecordingImport(r, recordingImportKey, "", "{not json", nil); w.Code != http.StatusBadRequest {
		t.Fatalf("bad json: %d", w.Code)
	}
}

func TestRecordingImport_ErrorMapping(t *testing.T) {
	f := newRecordingImportFixture(t)
	r := f.router(t, true)
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{fmt.Errorf("%w: x", service.ErrRecordingInvalid), http.StatusBadRequest, CodeRecordingInvalid},
		{fmt.Errorf("%w: x", service.ErrRecordingKeyInvalid), http.StatusBadRequest, CodeRecordingKeyInvalid},
		{service.ErrRecordingBucketNotAllowed, http.StatusForbidden, CodeRecordingBucketNotAllowed},
		{service.ErrRecordingNotFound, http.StatusNotFound, CodeRecordingNotFound},
		{service.ErrRecordingTooLarge, http.StatusRequestEntityTooLarge, CodeRecordingTooLarge},
		{service.ErrRecordingNotVideo, http.StatusUnprocessableEntity, CodeRecordingNotVideo},
		{service.ErrRecordingOwnerConflict, http.StatusConflict, CodeRecordingOwnerConflict},
		{service.ErrRecordingChanged, http.StatusConflict, CodeRecordingChanged},
		{fmt.Errorf("pg: connection refused password=hunter2"), http.StatusServiceUnavailable, CodeMediaUnavailable},
	}
	for _, tc := range cases {
		f.imp.err = tc.err
		w := postRecordingImport(r, recordingImportKey, "", recordingImportBody(), nil)
		if w.Code != tc.status || recordingErrorCode(t, w) != tc.code {
			t.Fatalf("%v: %d %s, want %d %s", tc.err, w.Code, w.Body.String(), tc.status, tc.code)
		}
		if tc.status >= 500 && strings.Contains(w.Body.String(), "hunter2") {
			t.Fatal("an internal error leaked into the answer")
		}
	}
}
