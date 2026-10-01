package service

// Recording import (1 Oct 2026). post-service makes the unlisted long video
// from live.stream.vod_ready only when the recording is a registered
// media_assets row that is READY (post-service internal/service/live_vod.go,
// resolveRecordingMedia: an explicit media_asset_id wins; a non-ready asset
// is skipped for good). Nothing registered egress files before, so every
// VOD was skipped.
//
// Now egress_ended (complete) stores the recording and queues an import job
// in one transaction; the sweeper calls media-service
//
//	POST {MEDIA_SERVICE_URL}/v1/media/internal/recordings/import   (internal key, no user identity)
//	{"owner_user_id","bucket","key","content_type":"video/mp4","duration_ms",
//	 "source":"live_recording","source_ref":"<stream id>"}
//	-> 200/201 {"data":{"media_id":"<uuid>","processing_status":"..."}}
//	   (idempotent on (source, source_ref): re-asking returns the current status)
//
// and keeps re-asking every 30s while the asset is processing. Only when it
// answers ready does the transaction that records the media id enqueue
// vod_ready (with media_asset_id). failed / rejected / deleted, a refused
// import (400/403/413/422, 409 RECORDING_OWNER_CONFLICT) or 6 hours without
// a ready asset end the job with no vod_ready. 404 RECORDING_NOT_FOUND
// (egress still uploading), 409 RECORDING_CHANGED, 5xx and network errors
// retry with backoff.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/atpost/shared/servicetoken"

	"github.com/atpost/live-service-v2/internal/events"
	"github.com/atpost/live-service-v2/internal/store/postgres"
)

// MediaImporter registers a recording object as a media asset and reports
// its processing status.
type MediaImporter interface {
	ImportRecording(ctx context.Context, req ImportRecordingRequest) (ImportResult, error)
}

// ImportRecordingRequest is the media-service import body.
type ImportRecordingRequest struct {
	OwnerUserID string `json:"owner_user_id"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	DurationMs  int64  `json:"duration_ms"`
	Source      string `json:"source"`
	SourceRef   string `json:"source_ref"`
}

// ImportResult is media-service's answer.
type ImportResult struct {
	MediaID          uuid.UUID
	ProcessingStatus string
}

// ImportError is a refused or failed import call. Terminal errors are never
// retried.
type ImportError struct {
	HTTPStatus int
	Code       string
	Terminal   bool
	Msg        string
}

func (e *ImportError) Error() string {
	return fmt.Sprintf("media import: status %d %s: %s", e.HTTPStatus, e.Code, e.Msg)
}

// terminalImportCodes are refusals a retry cannot change.
var terminalImportCodes = map[string]bool{
	"INVALID_REQUEST":              true,
	"RECORDING_KEY_INVALID":        true,
	"RECORDING_BUCKET_NOT_ALLOWED": true,
	"USER_CALLER_REFUSED":          true,
	"RECORDING_TOO_LARGE":          true,
	"RECORDING_NOT_VIDEO":          true,
	"RECORDING_OWNER_CONFLICT":     true,
}

// classifyImportFailure: terminal by code; otherwise 400/413/422 are
// terminal and everything else (404 RECORDING_NOT_FOUND, 409
// RECORDING_CHANGED, 5xx, 401/403 token or key misconfiguration) is retried.
func classifyImportFailure(status int, code string) bool {
	if terminalImportCodes[code] {
		return true
	}
	// A 403 without a known code (SERVICE_TOKEN_REJECTED: a key or token
	// misconfiguration) is retried until the 6h limit, like a 401.
	switch status {
	case http.StatusBadRequest, http.StatusRequestEntityTooLarge, http.StatusUnprocessableEntity:
		return true
	}
	return false
}

// Service token for the import (media-service recording_import_handler.go):
// issuer live-service-v2, audience "media" (media-service AudienceMedia),
// operation media:recording.import. Without a signer no token is sent —
// accepted by media-service on local/dev only.
const (
	IssuerLiveService = "live-service-v2"
	AudienceMedia     = "media"
	OpRecordingImport = "media:recording.import"
	importTokenTTL    = time.Minute
)

// HTTPMediaImporter calls media-service's internal import route.
type HTTPMediaImporter struct {
	baseURL     string
	internalKey string
	signer      *servicetoken.Signer
	http        *http.Client
}

// NewHTTPMediaImporter returns nil when baseURL is empty (not configured).
// signer may be nil (dev: internal key only).
func NewHTTPMediaImporter(baseURL, internalKey string, signer *servicetoken.Signer) *HTTPMediaImporter {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return nil
	}
	return &HTTPMediaImporter{baseURL: baseURL, internalKey: internalKey, signer: signer, http: &http.Client{Timeout: 15 * time.Second}}
}

// ImportSignerFromEnv builds the token signer from LIVE_SERVICE_TOKEN_KID and
// LIVE_SERVICE_TOKEN_PRIVKEY (base64 ed25519). Both unset = (nil, nil); one
// without the other, or a bad key, is an error.
func ImportSignerFromEnv(getenv func(string) string) (*servicetoken.Signer, error) {
	kid := strings.TrimSpace(getenv("LIVE_SERVICE_TOKEN_KID"))
	priv := strings.TrimSpace(getenv("LIVE_SERVICE_TOKEN_PRIVKEY"))
	if kid == "" && priv == "" {
		return nil, nil
	}
	if kid == "" || priv == "" {
		return nil, errors.New("LIVE_SERVICE_TOKEN_KID and LIVE_SERVICE_TOKEN_PRIVKEY must be set together")
	}
	return servicetoken.NewSignerFromBase64(IssuerLiveService, kid, priv)
}

// ImportRecording posts the import (internal key only — never a user
// identity header) and returns the media id and its processing status.
func (m *HTTPMediaImporter) ImportRecording(ctx context.Context, in ImportRecordingRequest) (ImportResult, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return ImportResult{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+"/v1/media/internal/recordings/import", bytes.NewReader(body))
	if err != nil {
		return ImportResult{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.internalKey != "" {
		req.Header.Set("X-Internal-Service-Key", m.internalKey)
	}
	if m.signer != nil {
		tok, err := m.signer.Mint(AudienceMedia, "recording-import", []string{OpRecordingImport}, nil, importTokenTTL)
		if err != nil {
			return ImportResult{}, fmt.Errorf("media import: mint token: %w", err)
		}
		req.Header.Set("X-Service-Authorization", "Bearer "+tok)
	}
	resp, err := m.http.Do(req)
	if err != nil {
		return ImportResult{}, fmt.Errorf("media import: %w", err) // network: retried
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		var env struct {
			Error struct {
				Code    string `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(raw, &env)
		return ImportResult{}, &ImportError{
			HTTPStatus: resp.StatusCode, Code: env.Error.Code, Msg: env.Error.Message,
			Terminal: classifyImportFailure(resp.StatusCode, env.Error.Code),
		}
	}
	var out struct {
		Data struct {
			MediaID          string `json:"media_id"`
			ProcessingStatus string `json:"processing_status"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return ImportResult{}, fmt.Errorf("media import: decode: %w", err)
	}
	id, err := uuid.Parse(out.Data.MediaID)
	if err != nil || id == uuid.Nil {
		return ImportResult{}, errors.New("media import: answer has no media_id")
	}
	return ImportResult{MediaID: id, ProcessingStatus: strings.ToLower(strings.TrimSpace(out.Data.ProcessingStatus))}, nil
}

// onEgressEnded stores the recording of a COMPLETE egress and queues its
// media import. A failed or aborted egress has no recording to announce.
func (s *Service) onEgressEnded(ctx context.Context, st *postgres.LiveStream, eg *EgressResult) error {
	if eg == nil {
		return nil
	}
	if eg.Status != "" && eg.Status != "EGRESS_COMPLETE" {
		slog.Warn("live-v2: egress ended without a recording", "stream_id", st.ID, "status", eg.Status)
		return nil
	}
	// media-service requires a .mp4 key whose file name carries the stream
	// id; the egress wrote exactly the key StartEgress asked for.
	key := strings.TrimLeft(eg.Filename, "/")
	if !strings.HasSuffix(key, ".mp4") || !strings.Contains(key, st.ID.String()) {
		key = recordingObjectKeyPrefix + st.ID.String() + ".mp4"
	}
	url := eg.Location
	if url == "" {
		url = s.resolveRecordingURL(st.ID)
	}
	_, err := s.store.SetRecording(ctx, st.ID, url, int(eg.DurationNs/int64(time.Second)), postgres.RecordingImport{
		Bucket:     s.s3Bucket,
		ObjectKey:  key,
		DurationMs: eg.DurationNs / int64(time.Millisecond),
	})
	return mapStoreErr(err)
}

// Import cadence.
const (
	importLease        = 2 * time.Minute
	importPollInterval = 30 * time.Second // while the asset is processing
	importGiveUp       = 6 * time.Hour
)

// importBackoff is the wait after the n-th failed call: 15s doubling,
// capped at 10 minutes.
func importBackoff(attempts int) time.Duration {
	d := 15 * time.Second
	for i := 1; i < attempts && d < 10*time.Minute; i++ {
		d *= 2
	}
	if d > 10*time.Minute {
		d = 10 * time.Minute
	}
	return d
}

// Media processing statuses.
const (
	mediaReady = "ready"
)

var terminalMediaStatus = map[string]bool{"failed": true, "rejected": true, "deleted": true}

// RunImports processes due import jobs once.
func (s *Service) RunImports(ctx context.Context) {
	jobs, err := s.store.ClaimDueImports(ctx, 20, importLease)
	if err != nil {
		slog.Warn("live-v2 imports: claim", "err", err)
		return
	}
	for _, j := range jobs {
		s.runImport(ctx, j)
	}
}

func (s *Service) runImport(ctx context.Context, j postgres.RecordingImport) {
	terminate := func(mediaID *uuid.UUID, status, msg string) {
		slog.Warn("live-v2 imports: gave up; no vod_ready", "stream_id", j.StreamID, "reason", msg)
		if err := s.store.TerminateImport(ctx, j.StreamID, mediaID, status, msg); err != nil {
			slog.Warn("live-v2 imports: record terminal state", "stream_id", j.StreamID, "err", err)
		}
	}
	retry := func(mediaID *uuid.UUID, status, msg string, in time.Duration) {
		if j.Age+in > importGiveUp {
			terminate(mediaID, status, "no ready media asset within 6h: "+msg)
			return
		}
		if err := s.store.RetryImport(ctx, j.StreamID, mediaID, status, msg, in); err != nil {
			slog.Warn("live-v2 imports: record retry", "stream_id", j.StreamID, "err", err)
		}
	}
	if s.media == nil {
		retry(nil, "", "media service not configured (MEDIA_SERVICE_URL)", importBackoff(j.Attempts+1))
		return
	}
	res, err := s.media.ImportRecording(ctx, ImportRecordingRequest{
		OwnerUserID: j.OwnerUserID.String(),
		Bucket:      j.Bucket,
		Key:         j.ObjectKey,
		ContentType: "video/mp4",
		DurationMs:  j.DurationMs,
		Source:      "live_recording",
		SourceRef:   j.StreamID.String(),
	})
	var ie *ImportError
	switch {
	case errors.As(err, &ie) && ie.Terminal:
		terminate(nil, "", err.Error())
		return
	case err != nil:
		retry(nil, "", err.Error(), importBackoff(j.Attempts+1))
		return
	}
	mediaID := res.MediaID
	switch {
	case res.ProcessingStatus == mediaReady:
	case terminalMediaStatus[res.ProcessingStatus]:
		terminate(&mediaID, res.ProcessingStatus, "media asset "+res.ProcessingStatus)
		return
	default: // processing, pending, uploaded, ...: ask again
		retry(&mediaID, res.ProcessingStatus, "", importPollInterval)
		return
	}
	err = s.store.CompleteImport(ctx, j.StreamID, mediaID, func(st *postgres.LiveStream, done postgres.RecordingImport) ([]postgres.OutboxEvent, error) {
		e, err := events.VODReady(ctx, st, done.RecordingURL, int(done.DurationMs/1000), mediaID)
		if err != nil {
			return nil, err
		}
		return []postgres.OutboxEvent{e}, nil
	})
	if err != nil {
		retry(&mediaID, res.ProcessingStatus, err.Error(), importBackoff(j.Attempts+1))
	}
}
