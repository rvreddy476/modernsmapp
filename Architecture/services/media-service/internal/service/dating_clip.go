package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Pulse dating clips — a ≤30 s voice or video answer to a dating profile
// prompt, media side.
//
// The clip is uploaded through the ordinary routes (POST /v1/media/init →
// presigned PUT → POST /v1/media/confirm), so it gets the ordinary pipeline:
//
//   - audio: confirm measures the duration (ffprobe) and, with
//     MEDIA_VOICE_SAFETY_REQUIRED (the default), holds moderation at
//     'pending' while the caption job runs; evaluateVoiceSafety then writes
//     'approved', 'rejected' or 'failed'. With no approval-capable evaluator
//     configured the verdict is 'failed', which this file reports as
//     "review": a human approves it in dating-service's admin queue. Nothing
//     here weakens that default.
//   - video: confirm queues the transcode; the worker writes the MP4
//     renditions, HLS, duration and the frame scan's verdict ('passed',
//     'rejected', 'manual_review'). The video's AUDIO TRACK is not
//     moderated by that scan.
//
// dating-service owns who may hear or see a clip. This service owns the
// bytes:
//
//   - OwnerStatus: is it the requester's, what kind, how far along, what
//     did moderation say (one vocabulary, NormaliseClipModeration), and is
//     it usable as a clip right now?
//   - Prepare: scope the asset to dating_clip. From then on every public
//     read route refuses it to anyone but the uploader (DatingScopeDenies).
//     Idempotent; does not require moderation to have finished.
//   - DeliveryURL: a short-lived signed URL (and a poster for video), issued
//     to dating-service after ITS audience decision, only for a prepared,
//     ready, moderation-passed clip.
//   - Delete: the owner-checked asset purge.

var (
	// ErrDatingClipNotFound covers a missing asset and one the requester
	// does not own (and, on delivery, every refusal). One error so
	// existence and state are never revealed.
	ErrDatingClipNotFound = errors.New("dating clip: media not found")
	// ErrDatingClipUnsupported: not audio/video, an audio container players
	// cannot open, processing failed, or the asset already belongs to
	// another uploader-only scope.
	ErrDatingClipUnsupported = errors.New("dating clip: media cannot be used as a clip")
	// ErrDatingClipNotReady: still processing; the caller retries.
	ErrDatingClipNotReady = errors.New("dating clip: media is still processing")
	// ErrDatingClipTooLong: the measured duration is over the limit.
	ErrDatingClipTooLong = errors.New("dating clip: media is too long")
	// ErrDatingClipInvalid: nil ids.
	ErrDatingClipInvalid = errors.New("dating clip: invalid request")
)

// Configuration.
const (
	// EnvDatingClipsEnabled turns the internal dating clip routes on
	// (true|false, default false). Off: the routes are not registered.
	EnvDatingClipsEnabled = "MEDIA_DATING_CLIPS_ENABLED"
	// EnvDatingClipURLTTLSeconds is the signed URL lifetime (30-300).
	EnvDatingClipURLTTLSeconds = "MEDIA_DATING_CLIP_URL_TTL_SECONDS"
	// EnvDatingClipMaxMs is the longest clip accepted, in milliseconds
	// (1000-180000; the voice pipeline's own cap is 180 s).
	EnvDatingClipMaxMs = "MEDIA_DATING_CLIP_MAX_MS"

	DefaultDatingClipURLTTL = 120 * time.Second
	DefaultDatingClipMaxMs  = 30_000
	maxDatingClipMaxMs      = MaxVoiceDurationSec * 1000
)

// Normalised states on the owner-status wire.
const (
	ClipKindAudio = "audio"
	ClipKindVideo = "video"

	ClipProcessing      = "processing"
	ClipProcessingReady = "ready"
	ClipProcessingFail  = "failed"

	ClipModerationPending  = "pending"
	ClipModerationPassed   = "passed"
	ClipModerationReview   = "review"
	ClipModerationRejected = "rejected"
)

// DatingClipSettings is the resolved configuration.
type DatingClipSettings struct {
	Enabled bool
	URLTTL  time.Duration
	MaxMs   int
}

// ResolveDatingClipSettings reads the three env vars. A malformed value is
// an error (main refuses to start) only while the feature is enabled; off,
// nothing else is read.
func ResolveDatingClipSettings(getenv func(string) string) (DatingClipSettings, error) {
	out := DatingClipSettings{URLTTL: DefaultDatingClipURLTTL, MaxMs: DefaultDatingClipMaxMs}
	if raw := strings.TrimSpace(getenv(EnvDatingClipsEnabled)); raw != "" {
		enabled, err := strconv.ParseBool(raw)
		if err != nil {
			return out, fmt.Errorf("%s must be true or false, got %q", EnvDatingClipsEnabled, raw)
		}
		out.Enabled = enabled
	}
	if !out.Enabled {
		return out, nil
	}
	if raw := strings.TrimSpace(getenv(EnvDatingClipURLTTLSeconds)); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 30 || time.Duration(n)*time.Second > delivery.MaxProtectedTTL {
			return out, fmt.Errorf("%s must be a whole number of seconds from 30 to %d, got %q",
				EnvDatingClipURLTTLSeconds, int(delivery.MaxProtectedTTL.Seconds()), raw)
		}
		out.URLTTL = time.Duration(n) * time.Second
	}
	if raw := strings.TrimSpace(getenv(EnvDatingClipMaxMs)); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1000 || n > maxDatingClipMaxMs {
			return out, fmt.Errorf("%s must be a whole number of milliseconds from 1000 to %d, got %q",
				EnvDatingClipMaxMs, maxDatingClipMaxMs, raw)
		}
		out.MaxMs = n
	}
	return out, nil
}

// NormaliseClipModeration maps this service's two moderation vocabularies
// onto one: images/video write passed|rejected|manual_review, the voice
// pipeline approved|rejected|failed. Anything else (pending, unset) is
// pending.
func NormaliseClipModeration(status string) string {
	switch strings.TrimSpace(status) {
	case "passed", VoiceSafetyApproved:
		return ClipModerationPassed
	case "manual_review", VoiceSafetyFailed:
		return ClipModerationReview
	case VoiceSafetyRejected:
		return ClipModerationRejected
	default:
		return ClipModerationPending
	}
}

// normaliseClipProcessing: ready, failed (failed/rejected), else processing.
func normaliseClipProcessing(status string) string {
	switch status {
	case "ready":
		return ClipProcessingReady
	case "failed", "rejected":
		return ClipProcessingFail
	default:
		return ClipProcessing
	}
}

// playableClipAudio is every audio type confirm admits that browsers and
// Android's MediaPlayer can both open as-is. The voice pipeline does not
// transcode, so AMR (admitted for low-end recorders, not playable in a
// browser) cannot be a clip.
var playableClipAudio = map[string]bool{
	"audio/mp4": true, "audio/m4a": true, "audio/aac": true, "audio/mpeg": true,
	"audio/ogg": true, "audio/opus": true, "audio/wav": true, "audio/x-wav": true,
	"audio/webm": true, "audio/flac": true,
}

// clipKindSupported: audio in a playable container, or video.
func clipKindSupported(m *postgres.MediaAsset) bool {
	switch m.FileType {
	case ClipKindVideo:
		return true
	case ClipKindAudio:
		return playableClipAudio[strings.ToLower(strings.TrimSpace(m.MimeType))]
	}
	return false
}

// clipScopeCompatible: unscoped, or already a dating clip.
func clipScopeCompatible(m *postgres.MediaAsset) bool {
	return m.AccessScope == "" || m.AccessScope == postgres.AccessScopeDatingClip
}

// DatingClipStatus is the owner-status response.
type DatingClipStatus struct {
	OwnerUserID uuid.UUID `json:"owner_user_id"`
	Kind        string    `json:"kind"`
	Processing  string    `json:"processing"`
	Moderation  string    `json:"moderation"`
	DurationMs  int       `json:"duration_ms"`
	Usable      bool      `json:"usable"`
}

// DatingClipPrepared is the prepare response.
type DatingClipPrepared struct {
	Kind       string `json:"kind"`
	DurationMs int    `json:"duration_ms"`
}

// DatingClipURL is the delivery-url response.
type DatingClipURL struct {
	Kind      string    `json:"kind"`
	URL       string    `json:"url"`
	PosterURL string    `json:"poster_url,omitempty"`
	ExpiresAt time.Time `json:"expires_at"`
}

// DatingClipStore is the asset store slice this service needs.
type DatingClipStore interface {
	GetMedia(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
	GetVariants(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.MediaVariant, error)
	MarkDatingClip(ctx context.Context, id, uploaderID uuid.UUID) error
	assetPurgeStore
}

// DatingClipService implements the internal dating clip routes.
type DatingClipService struct {
	store  DatingClipStore
	blobs  prefixObjectStore
	signer DatingPhotoSigner
	ttl    time.Duration
	maxMs  int
	now    func() time.Time
	log    *slog.Logger
}

// NewDatingClipService wires the service. A ttl outside
// (0, delivery.MaxProtectedTTL] and a maxMs outside (0, 180000] become the
// defaults.
func NewDatingClipService(store DatingClipStore, blobs prefixObjectStore, signer DatingPhotoSigner,
	ttl time.Duration, maxMs int, log *slog.Logger) *DatingClipService {
	if ttl <= 0 || ttl > delivery.MaxProtectedTTL {
		ttl = DefaultDatingClipURLTTL
	}
	if maxMs <= 0 || maxMs > maxDatingClipMaxMs {
		maxMs = DefaultDatingClipMaxMs
	}
	if log == nil {
		log = slog.Default()
	}
	return &DatingClipService{store: store, blobs: blobs, signer: signer, ttl: ttl, maxMs: maxMs, now: time.Now, log: log}
}

// MaxMs is the configured length limit.
func (s *DatingClipService) MaxMs() int { return s.maxMs }

// OwnerStatus returns the asset's clip state for its owner. No side effects.
func (s *DatingClipService) OwnerStatus(ctx context.Context, requester, mediaID uuid.UUID) (*DatingClipStatus, error) {
	m, err := s.owned(ctx, requester, mediaID)
	if err != nil {
		return nil, err
	}
	st := &DatingClipStatus{
		OwnerUserID: m.UploaderID,
		Kind:        m.FileType,
		Processing:  normaliseClipProcessing(m.ProcessingStatus),
		Moderation:  NormaliseClipModeration(m.ModerationStatus),
		DurationMs:  m.DurationMsValue(),
	}
	st.Usable = m.UploaderID == requester && clipKindSupported(m) && clipScopeCompatible(m) &&
		st.Processing == ClipProcessingReady && st.Moderation == ClipModerationPassed &&
		st.DurationMs > 0 && st.DurationMs <= s.maxMs
	return st, nil
}

// Prepare scopes the owner's audio/video asset to dating_clip. It may run
// before moderation finishes; it refuses an asset still processing (the
// duration is not known yet), one that failed, and one over the limit —
// without scoping it. Idempotent.
func (s *DatingClipService) Prepare(ctx context.Context, requester, mediaID uuid.UUID) (*DatingClipPrepared, error) {
	m, err := s.owned(ctx, requester, mediaID)
	if err != nil {
		return nil, err
	}
	if !clipKindSupported(m) || !clipScopeCompatible(m) {
		return nil, ErrDatingClipUnsupported
	}
	switch normaliseClipProcessing(m.ProcessingStatus) {
	case ClipProcessingFail:
		return nil, fmt.Errorf("%w: processing %s", ErrDatingClipUnsupported, m.ProcessingStatus)
	case ClipProcessing:
		return nil, ErrDatingClipNotReady
	}
	d := m.DurationMsValue()
	if d <= 0 {
		// A ready audio or video always has a measured duration; one
		// without cannot be held to the limit.
		return nil, fmt.Errorf("%w: no measured duration", ErrDatingClipUnsupported)
	}
	if d > s.maxMs {
		return nil, ErrDatingClipTooLong
	}
	if m.AccessScope != postgres.AccessScopeDatingClip {
		if err := s.store.MarkDatingClip(ctx, m.ID, requester); err != nil {
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				return nil, ErrDatingClipNotFound
			case errors.Is(err, postgres.ErrScopeConflict):
				return nil, ErrDatingClipUnsupported
			}
			return nil, fmt.Errorf("dating clip: mark scope: %w", err)
		}
		s.log.Info("media: dating clip prepared", "media_id", m.ID, "uploader_id", m.UploaderID,
			"kind", m.FileType, "duration_ms", d)
	}
	return &DatingClipPrepared{Kind: m.FileType, DurationMs: d}, nil
}

// clipVideoRenditions is the delivery preference for a video clip: the
// best progressive MP4 at or under 720p. The original is the fallback only
// when the pipeline produced none (a source under 360p).
var clipVideoRenditions = []string{"720p", "480p", "360p"}

// clipPosterVariant is the still the transcode extracts for every video.
const clipPosterVariant = "thumb_150"

// DeliveryURL signs the clip for its owner's audience. The caller
// (dating-service) has already decided the viewer may hear/see it. Every
// refusal is ErrDatingClipNotFound: the state is reported by OwnerStatus,
// never here.
func (s *DatingClipService) DeliveryURL(ctx context.Context, owner, mediaID uuid.UUID) (*DatingClipURL, error) {
	m, err := s.owned(ctx, owner, mediaID)
	if err != nil {
		return nil, err
	}
	if m.AccessScope != postgres.AccessScopeDatingClip || !clipKindSupported(m) ||
		m.ProcessingStatus != "ready" || NormaliseClipModeration(m.ModerationStatus) != ClipModerationPassed {
		return nil, ErrDatingClipNotFound
	}
	if d := m.DurationMsValue(); d <= 0 || d > s.maxMs {
		return nil, ErrDatingClipNotFound
	}
	key, poster := m.StorageKey, ""
	if m.FileType == ClipKindVideo {
		variants, err := s.store.GetVariants(ctx, m.ID)
		if err != nil {
			return nil, fmt.Errorf("dating clip: load renditions: %w", err)
		}
		if k := firstVariantKey(variants, clipVideoRenditions...); k != "" {
			key = k
		}
		poster = firstVariantKey(variants, clipPosterVariant)
	}
	if key == "" {
		return nil, ErrDatingClipNotFound
	}
	if s.signer == nil {
		return nil, fmt.Errorf("%w: no delivery signer", delivery.ErrDeliveryUnresolved)
	}
	now := s.now()
	out := &DatingClipURL{Kind: m.FileType, ExpiresAt: now.Add(s.ttl).UTC()}
	if out.URL, err = s.signer.SignProtected(key, s.ttl, now); err != nil {
		return nil, fmt.Errorf("%w: sign: %v", delivery.ErrDeliveryUnresolved, err)
	}
	if poster != "" {
		if out.PosterURL, err = s.signer.SignProtected(poster, s.ttl, now); err != nil {
			return nil, fmt.Errorf("%w: sign poster: %v", delivery.ErrDeliveryUnresolved, err)
		}
	}
	return out, nil
}

// Delete purges the owner's asset: rows, renditions and objects. Another
// live reference (a post, a story) keeps the asset: ErrAssetStillReferenced.
func (s *DatingClipService) Delete(ctx context.Context, owner, mediaID uuid.UUID) (*PurgeResult, error) {
	if _, err := s.owned(ctx, owner, mediaID); err != nil {
		return nil, err
	}
	return NewAssetPurger(s.store, s.blobs, s.log).Purge(ctx, mediaID, Referrer{Kind: ReferrerDatingClip, ID: owner})
}

func (s *DatingClipService) owned(ctx context.Context, requester, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	if requester == uuid.Nil || mediaID == uuid.Nil {
		return nil, ErrDatingClipInvalid
	}
	m, err := s.store.GetMedia(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDatingClipNotFound
		}
		return nil, fmt.Errorf("dating clip: load media: %w", err)
	}
	if m == nil || m.UploaderID != requester {
		return nil, ErrDatingClipNotFound
	}
	return m, nil
}

// firstVariantKey returns the object key of the first named variant present.
func firstVariantKey(variants []postgres.MediaVariant, names ...string) string {
	for _, name := range names {
		if k := datingVariantKey(variants, name); k != "" {
			return k
		}
	}
	return ""
}
