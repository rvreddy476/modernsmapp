package http

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/atpost/media-service/internal/service"
	"github.com/atpost/shared/api"
	"github.com/atpost/shared/servicetoken"
	"github.com/gin-gonic/gin"
)

// Live recordings become media assets (1 Oct 2026).
//
// POST /v1/media/internal/recordings/import
//
//	{"owner_user_id","bucket","key","content_type":"video/mp4",
//	 "duration_ms","source":"live_recording","source_ref":"<stream id>"}
//	-> 201 {"data":{"media_id","processing_status"}}   created by this call
//	-> 200 {"data":{"media_id","processing_status"}}   already imported
//
// The work is service.RecordingImporter (internal/service/recording_import.go).
//
// Who may call (caller live-service-v2):
//
//   - the route is in the /v1/media/internal group: the internal service key
//     first (401), and the api-gateway does not route the group;
//   - a request carrying a gateway-set end-user identity is refused (403
//     USER_CALLER_REFUSED);
//   - a service token in X-Service-Authorization (audience "media",
//     operation media:recording.import) must verify AND be issued by
//     live-service-v2, else 403 SERVICE_TOKEN_REJECTED;
//   - with no token the bare internal key is accepted on local/dev only
//     (processing.IsLocalDevEnv); elsewhere 401 SERVICE_TOKEN_REQUIRED.
//
// duration_ms is checked (not negative) and otherwise advisory: the worker
// measures the duration with ffprobe like any upload.

// RecordingImportRoute is the route within the /v1/media/internal group.
const RecordingImportRoute = "/recordings/import"

// OpRecordingImport is the operation a token must carry for the import.
const OpRecordingImport = "media:recording.import"

// IssuerLiveService is the only issuer the import accepts.
const IssuerLiveService = "live-service-v2"

// Error codes of the import route.
const (
	CodeRecordingInvalid          = "INVALID_REQUEST"
	CodeRecordingBucketNotAllowed = "RECORDING_BUCKET_NOT_ALLOWED"
	CodeRecordingKeyInvalid       = "RECORDING_KEY_INVALID"
	CodeRecordingNotFound         = "RECORDING_NOT_FOUND"
	CodeRecordingTooLarge         = "RECORDING_TOO_LARGE"
	CodeRecordingNotVideo         = "RECORDING_NOT_VIDEO"
	CodeRecordingOwnerConflict    = "RECORDING_OWNER_CONFLICT"
	CodeRecordingChanged          = "RECORDING_CHANGED"
)

// recordingImportService is the importer; an interface so the route's
// wiring and authentication are testable without stores.
type recordingImportService interface {
	Import(ctx context.Context, in service.RecordingImportInput) (*service.RecordingImportResult, error)
}

// WithRecordingImport wires the import route. A nil importer leaves the
// route unregistered (404). verifier is the SERVICE_CALLERS verifier (nil
// accepts no token); acceptLegacyKey admits the bare internal key (local/dev
// only, decided in main).
func (h *Handler) WithRecordingImport(imp recordingImportService, verifier *servicetoken.Verifier, acceptLegacyKey bool) *Handler {
	h.recordings = imp
	// A nil *Verifier must stay a nil interface, or Verify would be called
	// on it.
	h.recordingsVerifier = nil
	if verifier != nil {
		h.recordingsVerifier = verifier
	}
	h.recordingsLegacyKey = acceptLegacyKey
	return h
}

// requireRecordingImportCaller admits live-service-v2 (token) or, on
// local/dev, the bare internal key. It runs after the group's key check.
func (h *Handler) requireRecordingImportCaller() gin.HandlerFunc {
	return func(c *gin.Context) {
		ctx := c.Request.Context()
		raw := strings.TrimSpace(strings.TrimPrefix(c.GetHeader(ServiceAuthHeader), "Bearer "))
		if raw == "" {
			if !h.recordingsLegacyKey {
				api.ErrorWithContext(ctx, c.Writer, http.StatusUnauthorized, CodeServiceTokenRequired,
					"a live-service-v2 token is required", nil)
				c.Abort()
				return
			}
			c.Next()
			return
		}
		if h.recordingsVerifier == nil {
			slog.WarnContext(ctx, "media-service: recording import token refused: no service callers configured")
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		verified, err := h.recordingsVerifier.Verify(raw, OpRecordingImport, "")
		if err != nil {
			slog.WarnContext(ctx, "media-service: recording import token refused", "reason", err.Error())
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		if verified.Issuer != IssuerLiveService {
			slog.WarnContext(ctx, "media-service: recording import called by another service", "issuer", verified.Issuer)
			api.ErrorWithContext(ctx, c.Writer, http.StatusForbidden, CodeServiceTokenRejected, "service token rejected", nil)
			c.Abort()
			return
		}
		c.Next()
	}
}

type recordingImportRequest struct {
	OwnerUserID string `json:"owner_user_id"`
	Bucket      string `json:"bucket"`
	Key         string `json:"key"`
	ContentType string `json:"content_type"`
	DurationMs  int64  `json:"duration_ms"`
	Source      string `json:"source"`
	SourceRef   string `json:"source_ref"`
}

// ImportRecording — POST /v1/media/internal/recordings/import
func (h *Handler) ImportRecording(c *gin.Context) {
	ctx := c.Request.Context()
	var body recordingImportRequest
	if err := c.ShouldBindJSON(&body); err != nil {
		api.ErrorWithContext(ctx, c.Writer, http.StatusBadRequest, CodeRecordingInvalid, "body must be the import JSON", nil)
		return
	}
	res, err := h.recordings.Import(ctx, service.RecordingImportInput{
		OwnerUserID: body.OwnerUserID,
		Bucket:      body.Bucket,
		Key:         body.Key,
		ContentType: body.ContentType,
		DurationMs:  body.DurationMs,
		Source:      body.Source,
		SourceRef:   body.SourceRef,
	})
	if err != nil {
		status, code := recordingImportError(err)
		if status >= 500 {
			slog.ErrorContext(ctx, "media-service: recording import failed", "source_ref", body.SourceRef, "error", err.Error())
			api.ErrorWithContext(ctx, c.Writer, status, code, "media is unavailable; retry", nil)
			return
		}
		api.ErrorWithContext(ctx, c.Writer, status, code, err.Error(), nil)
		return
	}
	status := http.StatusOK
	if res.Created {
		status = http.StatusCreated
	}
	c.JSON(status, gin.H{"data": gin.H{
		"media_id":          res.MediaID.String(),
		"processing_status": res.ProcessingStatus,
	}})
}

// recordingImportError maps an importer error to a status and code.
func recordingImportError(err error) (int, string) {
	switch {
	case errors.Is(err, service.ErrRecordingInvalid):
		return http.StatusBadRequest, CodeRecordingInvalid
	case errors.Is(err, service.ErrRecordingKeyInvalid):
		return http.StatusBadRequest, CodeRecordingKeyInvalid
	case errors.Is(err, service.ErrRecordingBucketNotAllowed):
		return http.StatusForbidden, CodeRecordingBucketNotAllowed
	case errors.Is(err, service.ErrRecordingNotFound):
		return http.StatusNotFound, CodeRecordingNotFound
	case errors.Is(err, service.ErrRecordingTooLarge):
		return http.StatusRequestEntityTooLarge, CodeRecordingTooLarge
	case errors.Is(err, service.ErrRecordingNotVideo):
		return http.StatusUnprocessableEntity, CodeRecordingNotVideo
	case errors.Is(err, service.ErrRecordingOwnerConflict):
		return http.StatusConflict, CodeRecordingOwnerConflict
	case errors.Is(err, service.ErrRecordingChanged):
		return http.StatusConflict, CodeRecordingChanged
	default:
		return http.StatusServiceUnavailable, CodeMediaUnavailable
	}
}
