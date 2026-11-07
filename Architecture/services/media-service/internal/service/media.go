// media.go holds the Service itself: the size limits and URL expiry every
// path shares, the Service struct with its constructors and With*/Set*
// wiring, the delivery-gate seams every read goes through (deliveryURL,
// deliveryURLForVariant), and the small owner/status reads that have no
// better home (GetMedia, GetMediaStatus, CheckProfileMediaAuthority, alt
// text). Uploads live in upload.go, playback URLs in playback.go, batch and
// original delivery in media_batch.go, deletion and reclaim in deletion.go.

package service

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/atpost/media-service/internal/captions"
	"github.com/atpost/media-service/internal/config"
	"github.com/atpost/media-service/internal/delivery"
	mediaEvents "github.com/atpost/media-service/internal/events"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/blob"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

// Size limits per subtype/file_type
const (
	MaxImageSize  int64 = 20 * 1024 * 1024       // 20 MB
	// MaxVideoSize no longer caps user uploads (see ValidateUpload); it is
	// the default bound on a live-recording import (DefaultLiveRecordingMaxBytes).
	MaxVideoSize  int64 = 2 * 1024 * 1024 * 1024 // 2 GB
	MaxAvatarSize int64 = 10 * 1024 * 1024       // 10 MB
	MaxCoverSize  int64 = 10 * 1024 * 1024       // 10 MB
	MaxGIFSize    int64 = 15 * 1024 * 1024       // 15 MB

	// defaultURLExpiry bounds every presigned GET this service hands to a
	// caller outside the delivery gate's signer (today: the extracted-audio
	// URL, RecordReads.AudioTrackURLForViewer in record_read.go, which is
	// still authorized THROUGH the gate before it is signed).
	// It is the gate's own cap (P-9, 2026-09-29): a presigned URL cannot be
	// revoked before it expires, so the TTL is the revocation window, and a
	// window longer than the gate's would make the ungated route the
	// durable capability the gate exists to remove.
	defaultURLExpiry = delivery.MaxProtectedTTL
)

type Service struct {
	pgStore   *postgres.MediaAssetStore
	blobStore *blob.Store
	producer  *mediaEvents.Producer // optional, nil = skip Kafka
	cfg       *config.Config
	scanner   processing.Scanner
	rdb       *redis.Client // optional, nil = skip rate limiting
	captions  captions.Backend
	// audioSafety evaluates voice recordings. Defaults to an evaluator
	// that always reports "unavailable", so a missing configuration fails
	// CLOSED to manual review rather than approving (fixes-v2 / P0-2).
	audioSafety processing.AudioSafetyEvaluator
	// gate authorizes and signs every byte-delivery URL (M4-P0-5).
	//
	// Nil is NOT permissive: URLFor on a nil gate returns an unresolved error,
	// so an unwired deployment denies protected media instead of falling back
	// to the stable public URL this item exists to remove.
	gate *delivery.Gate
	// audioTracks is the alternate-audio-track feature (audio_tracks.go).
	audioTracks *AudioTracks
	// objects is the object store the anonymous stream reads through
	// (anonymous_scope.go); nil means blobStore.
	objects objectReader
	// uploadLimit is the per-user upload limiter every upload init calls;
	// nil means CheckUploadRateLimit (ratelimit.go). A seam for tests.
	uploadLimit func(ctx context.Context, userID uuid.UUID) error
	// documentUploadLimit is the per-user DOCUMENT upload limiter (its own
	// budget, document.go); nil means CheckDocumentUploadRateLimit.
	documentUploadLimit func(ctx context.Context, userID uuid.UUID) error
}

// WithDeliveryGate wires byte-delivery authorization. Called from main.go.
func (s *Service) WithDeliveryGate(g *delivery.Gate) *Service {
	s.gate = g
	return s
}

// deliveryURL resolves one object key for one viewer.
//
// Every media read path goes through here. Previously each one called
// blobStore.ObjectURL directly, which returned a stable unauthenticated CDN URL
// for any key — so authorization existed only on the JSON and never on the
// bytes.
func (s *Service) deliveryURL(ctx context.Context, viewerID, mediaID uuid.UUID, objectKey string) (string, error) {
	if s.gate == nil {
		return "", fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	return s.gate.URLFor(ctx, viewerID.String(), mediaID.String(), objectKey)
}

// deliveryURLForVariant is deliveryURL for a read that NAMED a variant.
//
// The name is passed on because the open-graph poster rule (2026-09-18) is
// scoped to stills: an anonymous caller may be admitted to a public post's
// thumbnail, never to its video renditions, its HLS graph or its original.
// Only `…/serve/{variant}` can say which was asked for, so only it uses this;
// every other read path keeps deliveryURL and its unchanged decision.
func (s *Service) deliveryURLForVariant(ctx context.Context, viewerID, mediaID uuid.UUID, variant, objectKey string) (string, error) {
	if s.gate == nil {
		return "", fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	return s.gate.URLForVariant(ctx, viewerID.String(), mediaID.String(), variant, objectKey)
}

func New(pg *postgres.MediaAssetStore, blobStore *blob.Store) *Service {
	cfg := config.Load()
	s := &Service{
		pgStore:     pg,
		blobStore:   blobStore,
		cfg:         cfg,
		scanner:     &processing.StubScanner{},
		captions:    captions.SelectBackend(),
		audioSafety: selectAudioSafety(cfg),
	}
	s.audioTracks = NewAudioTracks(pg, blobStore, s.AuthorizeMediaRead, nil)
	s.logScannerPolicy()
	return s
}

// selectAudioSafety picks the voice safety evaluator. With no blocklist
// configured it returns UnavailableEvaluator, which never returns a safe
// verdict — the deliberate opposite of the permissive image StubScanner.
func selectAudioSafety(cfg *config.Config) processing.AudioSafetyEvaluator {
	if cfg != nil && cfg.VoiceSafetyBlocklist != "" {
		if e := processing.NewKeywordTranscriptEvaluator(cfg.VoiceSafetyBlocklist); e != nil {
			slog.Info("voice safety: transcript evaluator configured", "evaluator", e.Name())
			return e
		}
	}
	slog.Warn("voice safety: no evaluator configured — every voice post will be held for manual review")
	return processing.UnavailableEvaluator{}
}

// WithAudioSafety overrides the evaluator (tests / alternate providers).
func (s *Service) WithAudioSafety(e processing.AudioSafetyEvaluator) *Service {
	s.audioSafety = e
	return s
}

// WithCaptionsBackend overrides the auto-selected captions backend.
// Used in tests + by main.go when a non-default backend (e.g. a
// self-hosted whisper.cpp endpoint) is configured.
func (s *Service) WithCaptionsBackend(b captions.Backend) *Service {
	s.captions = b
	return s
}

// NewWithConfig creates a Service with an explicit config and scanner.
func NewWithConfig(pg *postgres.MediaAssetStore, blobStore *blob.Store, cfg *config.Config, scanner processing.Scanner) *Service {
	if scanner == nil {
		scanner = &processing.StubScanner{}
	}
	s := &Service{
		pgStore:     pg,
		blobStore:   blobStore,
		cfg:         cfg,
		scanner:     scanner,
		captions:    captions.SelectBackend(),
		audioSafety: selectAudioSafety(cfg),
	}
	s.audioTracks = NewAudioTracks(pg, blobStore, s.AuthorizeMediaRead, nil)
	s.logScannerPolicy()
	return s
}

// logScannerPolicy emits a loud startup log describing the active
// content-safety scanning policy. Audit H8: leaving the stub wired
// while ScannerEnabled=true is the same as leaving the gate off —
// except it looks compliant. Make the choice visible at boot.
func (s *Service) logScannerPolicy() {
	if s.cfg == nil {
		return
	}
	_, isStub := s.scanner.(*processing.StubScanner)
	switch {
	case !s.cfg.ScannerEnabled:
		slog.Info("media scanner: disabled (no content safety gate)")
	case isStub && s.cfg.ScannerAllowStub:
		slog.Warn("media scanner: STUB SCANNER ACTIVE — every image will pass. " +
			"Set MEDIA_SCANNER_ALLOW_STUB=false in production and wire a real scanner.")
	case isStub:
		slog.Error("media scanner: enabled but only StubScanner is wired and MEDIA_SCANNER_ALLOW_STUB=false. " +
			"All image uploads will be REJECTED until a real scanner is configured.")
	default:
		slog.Info("media scanner: enabled with real backend (fail-closed on scanner errors)")
	}
}

// SetProducer sets the Kafka producer for async video transcoding events.
func (s *Service) SetProducer(p *mediaEvents.Producer) {
	s.producer = p
}

// SetRedis sets the Redis client used for upload rate limiting.
func (s *Service) SetRedis(rdb *redis.Client) {
	s.rdb = rdb
}

func (s *Service) GetMedia(ctx context.Context, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	return s.pgStore.GetMediaWithVariants(ctx, mediaID)
}

// ─── Status ────────────────────────────────────────────────────────

// MediaStatusResponse is the response for the status endpoint.
// MediaStatusResponse is what a CLIENT polls while it waits to attach an asset.
//
// ModerationStatus is part of the answer, not an extra.
//
// An asset is attachable only at EXACT `processing_status=ready` AND
// `moderation_status=passed` — post-service enforces both. This response
// carried only the processing half, so a client polling here could never learn
// the safety verdict: it either waited forever for a field that is never sent,
// or attached on `ready` alone and had the create refused with an error the
// person could not act on. Found by the C-LB-8 live run; every unit test passed
// because their fakes supplied the field this endpoint does not.
type MediaStatusResponse struct {
	MediaID          uuid.UUID                 `json:"media_id"`
	ProcessingStatus string                    `json:"processing_status"`
	ModerationStatus string                    `json:"moderation_status"`
	FileType         string                    `json:"file_type"`
	Width            *int                      `json:"width,omitempty"`
	Height           *int                      `json:"height,omitempty"`
	DurationSeconds  *int                      `json:"duration_seconds,omitempty"`
	DurationMs       int                       `json:"duration_ms,omitempty"`
	TranscodingJobs  []postgres.TranscodingJob `json:"transcoding_jobs,omitempty"`
}

// ProfileMediaAuthority is the server-to-server answer used before an
// identity profile is allowed to reference an asset.  A client-side upload
// check is not an authority: a caller can bypass the Android application and
// submit any UUID to profile-service.  Keeping this decision in media-service
// means ownership, processing and moderation are evaluated where the asset
// record lives.
type ProfileMediaAuthority struct {
	MediaID    uuid.UUID `json:"media_id"`
	OwnerID    uuid.UUID `json:"owner_id"`
	Subtype    string    `json:"media_subtype"`
	Attachable bool      `json:"attachable"`
}

// CheckProfileMediaAuthority returns an attachable verdict for an avatar or
// cover.  Unknown subtypes fail closed and no storage coordinates are exposed.
func (s *Service) CheckProfileMediaAuthority(ctx context.Context, mediaID, ownerID uuid.UUID, subtype string) (*ProfileMediaAuthority, error) {
	if subtype != "avatar" && subtype != "cover" {
		return nil, fmt.Errorf("unsupported profile media subtype %q", subtype)
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	return &ProfileMediaAuthority{
		MediaID: media.ID,
		OwnerID: media.UploaderID,
		Subtype: media.MediaSubtype,
		Attachable: media.UploaderID == ownerID &&
			media.FileType == "image" &&
			media.MediaSubtype == subtype &&
			media.ProcessingStatus == "ready" &&
			media.ModerationStatus == "passed",
	}, nil
}

// GetMediaStatus returns the processing status and transcoding job details.
//
// No audience decision here: the viewer-facing route is
// RecordReads.StatusForViewer / StatusForService (record_read.go), which
// authorize first and then shape the same DTO through mediaStatusResponse.
func (s *Service) GetMediaStatus(ctx context.Context, mediaID uuid.UUID) (*MediaStatusResponse, error) {
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	return mediaStatusResponse(ctx, media, s.pgStore.GetTranscodingJobs), nil
}

// ─── Alt Text ──────────────────────────────────────────────────────

// UpdateAltText updates the alt_text field of a media asset owned by userID.
// Returns an error if the asset does not exist or is not owned by userID.
func (s *Service) UpdateAltText(ctx context.Context, mediaID uuid.UUID, userID uuid.UUID, altText string) error {
	return s.pgStore.UpdateAltText(ctx, mediaID, userID, altText)
}

// UpdateAltTextWithDecorative sets the description and decorative marker
// together, owner-only (Codex P1-7).
func (s *Service) UpdateAltTextWithDecorative(ctx context.Context, mediaID, userID uuid.UUID, altText string, decorative bool) error {
	return s.pgStore.UpdateAltTextWithDecorative(ctx, mediaID, userID, altText, decorative)
}
