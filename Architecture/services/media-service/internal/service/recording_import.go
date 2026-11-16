package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"strconv"
	"strings"

	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

/*
	Live recordings become media assets (1 Oct 2026).

	live-service-v2's LiveKit egress writes one MP4 per finished stream to the
	recordings bucket (dev: MinIO `live-recordings`, key
	recordings/<stream id>.mp4). post-service turns live.stream.vod_ready into
	an unlisted long video only when the recording is a registered
	media_assets row, and nothing registered one.

	POST /v1/media/internal/recordings/import (internal/http/
	recording_import_handler.go) does it:

	  1. validate: owner a uuid, source "live_recording", source_ref the
	     stream id (a uuid), content_type "video/mp4", bucket EXACTLY the
	     configured recordings bucket, key a plain relative path ending .mp4
	     whose file name carries the stream id (no traversal, no odd bytes);
	  2. idempotency: an asset already imported for (source, source_ref) is
	     returned as it is — owned by the same user, or 409;
	  3. the source object must exist, be at most MaxBytes and start with an
	     MP4 ftyp box — the caller's content_type is never taken on trust;
	  4. server-side copy into this service's own layout,
	     user/<owner>/<new media id>/original, preconditioned on the ETag
	     validated in 3 — the asset is never served from the recordings
	     bucket;
	  5. one transaction: the media_assets row (video, owner = stream host,
	     moderation 'pending', upload_purpose 'live_recording', born
	     'processing') and the media.transcode.requested outbox row, so HLS,
	     the thumbnail and the copyright fingerprint run as for any upload.

	Multi-part streams (16 Nov 2026): an encoder reconnect leaves several
	parts, sent as `keys` in part order. Each listed part goes through 1 and
	3; a part missing from the bucket is left out and logged (none found is
	404 RECORDING_NOT_FOUND, retried, as for one key); one found takes the
	single-key path. Two or more must total at most MaxBytes and are copied
	to <prefix>parts/NNN.mp4 (processing.RecordingPartKey) with NO original:
	the worker joins them into the original first (cmd/worker/
	recording_join.go), then transcodes as for any upload.

	Nothing in the recordings bucket is written or deleted. The only object
	this code ever deletes is its OWN copy in the media bucket, when the row
	that would name it was not written (a lost race or a failed insert).
*/

// DefaultLiveRecordingsBucket is live-service-v2's default egress bucket
// (MINIO_BUCKET_LIVE_RECORDINGS there).
const DefaultLiveRecordingsBucket = "live-recordings"

// Env knobs of the import route.
const (
	EnvLiveRecordingsBucket  = "MEDIA_LIVE_RECORDINGS_BUCKET"
	EnvLiveRecordingMaxBytes = "MEDIA_LIVE_RECORDING_MAX_BYTES"
)

// DefaultLiveRecordingMaxBytes bounds an imported recording. The transcode
// worker reads an original whole into memory (cmd/worker transcodeVideo),
// so this is the long-video ceiling, not a free choice.
const DefaultLiveRecordingMaxBytes = MaxVideoSize

// RecordingContentType is the only content type an import accepts.
const RecordingContentType = "video/mp4"

// maxRecordingKeyLen bounds the source key.
const maxRecordingKeyLen = 512

// Import refusals. The handler maps each to one status and code.
var (
	ErrRecordingInvalid          = errors.New("recording import: invalid request")
	ErrRecordingBucketNotAllowed = errors.New("recording import: bucket not allowed")
	ErrRecordingKeyInvalid       = errors.New("recording import: invalid object key")
	ErrRecordingNotFound         = errors.New("recording import: source object not found")
	ErrRecordingTooLarge         = errors.New("recording import: source object too large")
	ErrRecordingNotVideo         = errors.New("recording import: source object is not an MP4 video")
	ErrRecordingOwnerConflict    = errors.New("recording import: already imported for another owner")
	ErrRecordingChanged          = errors.New("recording import: source object changed during import")
)

// RecordingImportInput is the request body, as received.
type RecordingImportInput struct {
	OwnerUserID string
	Bucket      string
	Key         string
	// Keys are a multi-part stream's parts, in order (16 Nov 2026): two or
	// more are copied under the asset's prefix as parts/NNN.mp4 and the
	// worker joins them into the original. Empty: Key alone, as before.
	// When both are set, Key must be one of Keys.
	Keys        []string
	ContentType string
	DurationMs  int64
	Source      string
	SourceRef   string
}

// MaxRecordingParts bounds Keys (live-service caps a stream at 20 parts).
const MaxRecordingParts = processing.MaxJoinParts

// RecordingImportResult is the asset the import resolved to.
type RecordingImportResult struct {
	MediaID          uuid.UUID
	ProcessingStatus string
	// Created is true when this call made the asset (201), false when an
	// earlier import of the same (source, source_ref) did (200).
	Created bool
}

// RecordingImportConfig is the importer's policy.
type RecordingImportConfig struct {
	// AllowedBucket is the one bucket a source may live in.
	AllowedBucket string
	// MaxBytes is the largest source accepted.
	MaxBytes int64
}

// RecordingImportConfigFromEnv reads MEDIA_LIVE_RECORDINGS_BUCKET (default
// live-recordings) and MEDIA_LIVE_RECORDING_MAX_BYTES (default 2 GiB).
func RecordingImportConfigFromEnv(getenv func(string) string) (RecordingImportConfig, error) {
	cfg := RecordingImportConfig{AllowedBucket: DefaultLiveRecordingsBucket, MaxBytes: DefaultLiveRecordingMaxBytes}
	if v := strings.TrimSpace(getenv(EnvLiveRecordingsBucket)); v != "" {
		cfg.AllowedBucket = v
	}
	if v := strings.TrimSpace(getenv(EnvLiveRecordingMaxBytes)); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			return cfg, fmt.Errorf("%s must be a positive byte count, got %q", EnvLiveRecordingMaxBytes, v)
		}
		cfg.MaxBytes = n
	}
	return cfg, nil
}

// recordingImportStore is the slice of the media store the import needs.
type recordingImportStore interface {
	FindImportedMedia(ctx context.Context, source, ref string) (*postgres.ImportedMedia, error)
	CreateImportedVideo(ctx context.Context, in postgres.ImportedVideo) (*postgres.ImportedMedia, bool, error)
}

// recordingImportBlob is the slice of the object store the import needs.
type recordingImportBlob interface {
	StatObjectIn(ctx context.Context, srcBucket, objectKey string) (blob.ObjectInfo, error)
	ReadObjectRangeIn(ctx context.Context, srcBucket, objectKey string, start, end int64) ([]byte, error)
	CopyObjectFrom(ctx context.Context, srcBucket, srcKey, srcETag, dstKey, contentType string) (blob.ObjectInfo, error)
	DeleteObject(ctx context.Context, objectKey string) error
	Bucket() string
}

// RecordingImporter registers live recordings as media assets.
type RecordingImporter struct {
	store recordingImportStore
	blob  recordingImportBlob
	cfg   RecordingImportConfig
}

// NewRecordingImporter refuses a policy that would let the route copy this
// service's OWN objects between owners (allowed bucket = media bucket) or
// that names no bucket at all.
func NewRecordingImporter(store recordingImportStore, b recordingImportBlob, cfg RecordingImportConfig) (*RecordingImporter, error) {
	if store == nil || b == nil {
		return nil, errors.New("recording import: store and object store are required")
	}
	if strings.TrimSpace(cfg.AllowedBucket) == "" {
		return nil, errors.New("recording import: no recordings bucket configured")
	}
	if cfg.AllowedBucket == b.Bucket() {
		return nil, fmt.Errorf("recording import: the recordings bucket must not be the media bucket %q", b.Bucket())
	}
	if cfg.MaxBytes <= 0 {
		cfg.MaxBytes = DefaultLiveRecordingMaxBytes
	}
	return &RecordingImporter{store: store, blob: b, cfg: cfg}, nil
}

// NewRecordingImporterFor builds the importer over the real stores.
func NewRecordingImporterFor(pg *postgres.MediaAssetStore, b *blob.Store, cfg RecordingImportConfig) (*RecordingImporter, error) {
	if pg == nil || b == nil {
		return nil, errors.New("recording import: store and object store are required")
	}
	return NewRecordingImporter(pg, b, cfg)
}

// validRecording is a request that passed every check that needs no I/O.
type validRecording struct {
	owner  uuid.UUID
	ref    string // canonical stream id
	bucket string
	key    string
	// keys are the parts to join, in order; nil = the single key.
	keys []string
}

// validate is step 1 of the package comment. Pure.
func (r *RecordingImporter) validate(in RecordingImportInput) (validRecording, error) {
	var v validRecording
	owner, err := uuid.Parse(strings.TrimSpace(in.OwnerUserID))
	if err != nil || owner == uuid.Nil {
		return v, fmt.Errorf("%w: owner_user_id must be a user id", ErrRecordingInvalid)
	}
	if in.Source != postgres.ImportSourceLiveRecording {
		return v, fmt.Errorf("%w: source must be %q", ErrRecordingInvalid, postgres.ImportSourceLiveRecording)
	}
	streamID, err := uuid.Parse(strings.TrimSpace(in.SourceRef))
	if err != nil || streamID == uuid.Nil {
		return v, fmt.Errorf("%w: source_ref must be the stream id", ErrRecordingInvalid)
	}
	if strings.ToLower(strings.TrimSpace(in.ContentType)) != RecordingContentType {
		return v, fmt.Errorf("%w: content_type must be %s", ErrRecordingInvalid, RecordingContentType)
	}
	if in.DurationMs < 0 {
		return v, fmt.Errorf("%w: duration_ms must not be negative", ErrRecordingInvalid)
	}
	if in.Bucket != r.cfg.AllowedBucket {
		return v, ErrRecordingBucketNotAllowed
	}
	ref := streamID.String()
	if len(in.Keys) == 0 {
		if err := ValidateRecordingKey(in.Key, ref); err != nil {
			return v, err
		}
		return validRecording{owner: owner, ref: ref, bucket: in.Bucket, key: in.Key}, nil
	}
	if len(in.Keys) > MaxRecordingParts {
		return v, fmt.Errorf("%w: at most %d keys", ErrRecordingInvalid, MaxRecordingParts)
	}
	seen := make(map[string]bool, len(in.Keys))
	for _, k := range in.Keys {
		if err := ValidateRecordingKey(k, ref); err != nil {
			return v, err
		}
		if seen[k] {
			return v, fmt.Errorf("%w: key %q listed twice", ErrRecordingInvalid, k)
		}
		seen[k] = true
	}
	if in.Key != "" && !seen[in.Key] {
		return v, fmt.Errorf("%w: key must be one of keys", ErrRecordingInvalid)
	}
	v = validRecording{owner: owner, ref: ref, bucket: in.Bucket, key: in.Key}
	if v.key == "" {
		v.key = in.Keys[0]
	}
	if len(in.Keys) > 1 {
		v.keys = append([]string(nil), in.Keys...)
	} else {
		v.key = in.Keys[0]
	}
	return v, nil
}

// ValidateRecordingKey accepts a plain relative object key: at most 512
// bytes of [A-Za-z0-9._-/], no leading, trailing or doubled slash, no "."
// or ".." segment, ending ".mp4", whose file name contains streamID (so one
// stream's import cannot name another stream's recording). Pure.
func ValidateRecordingKey(key, streamID string) error {
	if key == "" || len(key) > maxRecordingKeyLen {
		return fmt.Errorf("%w: key length", ErrRecordingKeyInvalid)
	}
	for i := 0; i < len(key); i++ {
		c := key[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9',
			c == '.', c == '_', c == '-', c == '/':
		default:
			return fmt.Errorf("%w: character %q", ErrRecordingKeyInvalid, c)
		}
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: path segment %q", ErrRecordingKeyInvalid, seg)
		}
	}
	if !strings.HasSuffix(key, ".mp4") {
		return fmt.Errorf("%w: not an .mp4 object", ErrRecordingKeyInvalid)
	}
	if streamID == "" || !strings.Contains(path.Base(key), streamID) {
		return fmt.Errorf("%w: file name does not name the stream", ErrRecordingKeyInvalid)
	}
	return nil
}

// Import runs the whole import; see the package comment.
func (r *RecordingImporter) Import(ctx context.Context, in RecordingImportInput) (*RecordingImportResult, error) {
	v, err := r.validate(in)
	if err != nil {
		return nil, err
	}
	source := postgres.ImportSourceLiveRecording

	// 2. Idempotency, before any object-store work.
	if existing, err := r.store.FindImportedMedia(ctx, source, v.ref); err != nil {
		return nil, err
	} else if existing != nil {
		return existingResult(existing, v.owner)
	}

	if len(v.keys) > 1 {
		return r.importParts(ctx, v)
	}

	// 3. The source, checked by its bytes.
	info, err := r.checkSource(ctx, v.bucket, v.key)
	if err != nil {
		return nil, err
	}

	// 4. Server-side copy into this service's layout.
	mediaID := uuid.New()
	dstKey := fmt.Sprintf("user/%s/%s/original", v.owner, mediaID)
	copied, err := r.copySource(ctx, v.bucket, v.key, info, dstKey)
	if err != nil {
		return nil, err
	}

	// 5. Row + transcode request, one transaction.
	return r.register(ctx, v, mediaID, dstKey, info.Size, copied.ETag, []string{dstKey})
}

// checkSource is step 3 for one object: it exists, is not empty, at most
// MaxBytes, and starts with an MP4 ftyp box.
func (r *RecordingImporter) checkSource(ctx context.Context, bucket, key string) (blob.ObjectInfo, error) {
	info, err := r.blob.StatObjectIn(ctx, bucket, key)
	if errors.Is(err, blob.ErrObjectNotFound) {
		return info, ErrRecordingNotFound
	}
	if err != nil {
		return info, fmt.Errorf("stat recording: %w", err)
	}
	if info.Size <= 0 {
		return info, fmt.Errorf("%w: empty object", ErrRecordingNotVideo)
	}
	if info.Size > r.cfg.MaxBytes {
		return info, fmt.Errorf("%w: %d bytes (max %d)", ErrRecordingTooLarge, info.Size, r.cfg.MaxBytes)
	}
	headerEnd := int64(63)
	if info.Size-1 < headerEnd {
		headerEnd = info.Size - 1
	}
	header, err := r.blob.ReadObjectRangeIn(ctx, bucket, key, 0, headerEnd)
	if errors.Is(err, blob.ErrObjectNotFound) {
		return info, ErrRecordingNotFound
	}
	if err != nil {
		return info, fmt.Errorf("read recording header: %w", err)
	}
	if kind, ok := processing.ValidateVideoMagicBytes(header); !ok || kind != RecordingContentType {
		return info, ErrRecordingNotVideo
	}
	return info, nil
}

// copySource is step 4 for one object, pinned on the ETag checkSource saw.
// On failure the copy is dropped.
func (r *RecordingImporter) copySource(ctx context.Context, bucket, key string, info blob.ObjectInfo, dstKey string) (blob.ObjectInfo, error) {
	copied, err := r.blob.CopyObjectFrom(ctx, bucket, key, info.ETag, dstKey, RecordingContentType)
	switch {
	case errors.Is(err, blob.ErrObjectChanged):
		r.dropCopy(ctx, dstKey)
		return copied, ErrRecordingChanged
	case errors.Is(err, blob.ErrObjectNotFound):
		r.dropCopy(ctx, dstKey)
		return copied, ErrRecordingNotFound
	case err != nil:
		r.dropCopy(ctx, dstKey)
		return copied, fmt.Errorf("copy recording: %w", err)
	}
	if copied.Size != info.Size {
		r.dropCopy(ctx, dstKey)
		return copied, fmt.Errorf("%w: copied %d bytes of %d", ErrRecordingChanged, copied.Size, info.Size)
	}
	return copied, nil
}

// register is step 5: the row and the transcode request, one transaction.
// owned are the objects this import copied, dropped when the row is not
// written.
func (r *RecordingImporter) register(ctx context.Context, v validRecording, mediaID uuid.UUID, dstKey string,
	size int64, etag string, owned []string) (*RecordingImportResult, error) {
	got, created, err := r.store.CreateImportedVideo(ctx, postgres.ImportedVideo{
		ID:            mediaID,
		OwnerID:       v.owner,
		MimeType:      RecordingContentType,
		SizeBytes:     size,
		StorageBucket: r.blob.Bucket(),
		StorageKey:    dstKey,
		OriginalETag:  etag,
		UploadPurpose: postgres.UploadPurposeLiveRecording,
		Source:        postgres.ImportSourceLiveRecording,
		SourceRef:     v.ref,
	})
	if err != nil {
		r.dropCopies(ctx, owned)
		return nil, err
	}
	if !created {
		// Another import of this stream committed first; ours names nothing.
		r.dropCopies(ctx, owned)
		return existingResult(got, v.owner)
	}
	slog.InfoContext(ctx, "media-service: live recording imported",
		"media_id", got.ID.String(), "owner_id", v.owner.String(), "stream_id", v.ref,
		"size_bytes", size, "parts", len(owned))
	return &RecordingImportResult{MediaID: got.ID, ProcessingStatus: got.ProcessingStatus, Created: true}, nil
}

// importParts imports a multi-part stream. Every listed part is checked; a
// part missing from the recordings bucket is left out and logged, and none
// found is ErrRecordingNotFound (retried, as for one key). One found is the
// single-key import. Two or more are copied, in order, to
// <prefix>parts/NNN.mp4 and registered with NO original: the worker joins
// the copies into the original before it transcodes (and removes them).
// Their total must fit MaxBytes, the worker's cap on one original.
func (r *RecordingImporter) importParts(ctx context.Context, v validRecording) (*RecordingImportResult, error) {
	type found struct {
		key  string
		info blob.ObjectInfo
	}
	var parts []found
	var total int64
	for _, k := range v.keys {
		info, err := r.checkSource(ctx, v.bucket, k)
		if errors.Is(err, ErrRecordingNotFound) {
			slog.WarnContext(ctx, "media-service: live recording part missing; joining the rest",
				"stream_id", v.ref, "key", k)
			continue
		}
		if err != nil {
			return nil, err
		}
		parts = append(parts, found{key: k, info: info})
		total += info.Size
	}
	switch len(parts) {
	case 0:
		return nil, ErrRecordingNotFound
	case 1:
		mediaID := uuid.New()
		dstKey := fmt.Sprintf("user/%s/%s/original", v.owner, mediaID)
		copied, err := r.copySource(ctx, v.bucket, parts[0].key, parts[0].info, dstKey)
		if err != nil {
			return nil, err
		}
		return r.register(ctx, v, mediaID, dstKey, parts[0].info.Size, copied.ETag, []string{dstKey})
	}
	if total > r.cfg.MaxBytes {
		return nil, fmt.Errorf("%w: parts total %d bytes (max %d)", ErrRecordingTooLarge, total, r.cfg.MaxBytes)
	}
	mediaID := uuid.New()
	prefix := fmt.Sprintf("user/%s/%s/", v.owner, mediaID)
	var owned []string
	for i, p := range parts {
		dst := processing.RecordingPartKey(prefix, i+1)
		if _, err := r.copySource(ctx, v.bucket, p.key, p.info, dst); err != nil {
			r.dropCopies(ctx, owned)
			return nil, err
		}
		owned = append(owned, dst)
	}
	return r.register(ctx, v, mediaID, prefix+"original", total, "", owned)
}

// dropCopies drops each of this import's own copies.
func (r *RecordingImporter) dropCopies(ctx context.Context, keys []string) {
	for _, k := range keys {
		r.dropCopy(ctx, k)
	}
}

// existingResult answers a repeated import: the same owner gets the asset,
// anyone else a conflict (never another user's media id).
func existingResult(m *postgres.ImportedMedia, owner uuid.UUID) (*RecordingImportResult, error) {
	if m.UploaderID != owner {
		return nil, ErrRecordingOwnerConflict
	}
	return &RecordingImportResult{MediaID: m.ID, ProcessingStatus: m.ProcessingStatus}, nil
}

// dropCopy removes this import's own copy from the MEDIA bucket. Best
// effort: a leftover object names no row and is only storage.
func (r *RecordingImporter) dropCopy(ctx context.Context, dstKey string) {
	if err := r.blob.DeleteObject(ctx, dstKey); err != nil {
		slog.WarnContext(ctx, "media-service: recording import left an unreferenced copy", "key", dstKey, "error", err.Error())
	}
}
