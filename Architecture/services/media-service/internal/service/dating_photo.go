package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/processing"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// Dating plan lane D6 — dating photo safety, media side.
//
// dating-service owns who may see a dating photo (matches, sparks, the
// owner's blur setting). media-service owns the bytes. The split:
//
//   - OwnerStatus: is this media the requester's, what state is it in, and
//     what did the scanner find (every label, not only the verdict)?
//   - Prepare: re-encode the original with its EXIF orientation applied and
//     every metadata segment dropped, rebuild the renditions from the oriented
//     pixels, render a strongly blurred downscaled variant, and scope the
//     asset to dating. From then on the public read routes refuse it to
//     anyone but the uploader (DatingScopeDenies).
//   - DeliveryURL: a short-lived signed URL for the full or blurred image,
//     issued to dating-service after ITS audience decision. The signer is the
//     delivery gate's (CloudFront canned policy in production, MinIO presign
//     locally); the TTL never exceeds delivery.MaxProtectedTTL.
//   - Delete: the owner-checked asset purge (rows, renditions, objects).
//
// The blurred variant lives at user/<owner>/dating-blurred/<random>.jpg, not
// under the asset's own prefix, so its URL names neither the media id nor the
// original's key. The purge still removes it: variant keys are recorded before
// the rows go.

var (
	// ErrDatingPhotoNotFound covers a missing asset and one the requester
	// does not own. One error so existence is never revealed.
	ErrDatingPhotoNotFound = errors.New("dating photo: media not found")
	// ErrDatingPhotoNotReady: not an image, not processed, or not
	// moderation-passed.
	ErrDatingPhotoNotReady = errors.New("dating photo: media is not a ready, moderation-passed image")
	// ErrDatingPhotoUnsupported: the stored bytes cannot be decoded.
	ErrDatingPhotoUnsupported = errors.New("dating photo: image cannot be prepared")
	// ErrDatingPhotoNotPrepared: delivery was asked for before Prepare ran.
	ErrDatingPhotoNotPrepared = errors.New("dating photo: media has not been prepared")
	// ErrDatingPhotoInvalid: nil ids or an unknown variant.
	ErrDatingPhotoInvalid = errors.New("dating photo: invalid request")
)

// Delivery variants dating-service may ask for.
const (
	DatingVariantFull    = "full"
	DatingVariantBlurred = "blurred"
)

// DefaultDatingPhotoURLTTL is the signed URL lifetime when unset. dating-service
// redirects to the URL with a 60s private cache, so it must outlive that.
const DefaultDatingPhotoURLTTL = 2 * time.Minute

// EnvDatingPhotoURLTTLSeconds overrides the signed URL lifetime (30-300).
const EnvDatingPhotoURLTTLSeconds = "MEDIA_DATING_PHOTO_URL_TTL_SECONDS"

// ResolveDatingPhotoURLTTL reads EnvDatingPhotoURLTTLSeconds. A malformed or
// out-of-range value is an error, on which main refuses to start.
func ResolveDatingPhotoURLTTL(getenv func(string) string) (time.Duration, error) {
	raw := strings.TrimSpace(getenv(EnvDatingPhotoURLTTLSeconds))
	if raw == "" {
		return DefaultDatingPhotoURLTTL, nil
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < 30 || time.Duration(n)*time.Second > delivery.MaxProtectedTTL {
		return 0, fmt.Errorf("%s must be a whole number of seconds from 30 to %d, got %q",
			EnvDatingPhotoURLTTLSeconds, int(delivery.MaxProtectedTTL.Seconds()), raw)
	}
	return time.Duration(n) * time.Second, nil
}

// DatingScopeDenies reports whether a dating-scoped asset must be refused to
// viewer on a public read route: always, unless the viewer uploaded it.
// Everyone else reaches the bytes only through a URL dating-service obtained
// from DeliveryURL after deciding they may.
func DatingScopeDenies(m *postgres.MediaAsset, viewerID uuid.UUID) bool {
	if m == nil || m.AccessScope != postgres.AccessScopeDatingPhoto {
		return false
	}
	return viewerID == uuid.Nil || viewerID != m.UploaderID
}

// DatingPhotoStore is the asset store slice this service needs.
type DatingPhotoStore interface {
	GetMedia(ctx context.Context, id uuid.UUID) (*postgres.MediaAsset, error)
	GetVariants(ctx context.Context, mediaAssetID uuid.UUID) ([]postgres.MediaVariant, error)
	InsertVariants(ctx context.Context, variants []postgres.MediaVariant) error
	MarkDatingPhotoPrepared(ctx context.Context, id uuid.UUID, mime string, sizeBytes int64, width, height int) error
	assetPurgeStore
}

// DatingPhotoBlobs is the object store slice this service needs.
type DatingPhotoBlobs interface {
	DownloadObject(ctx context.Context, objectKey string) ([]byte, error)
	UploadObject(ctx context.Context, objectKey string, data []byte, contentType string) error
	prefixObjectStore
}

// DatingPhotoSigner signs a protected key (delivery.URLSigner's half).
type DatingPhotoSigner interface {
	SignProtected(key string, ttl time.Duration, now time.Time) (string, error)
}

// DatingPhotoStatus is the owner-status / prepare response.
type DatingPhotoStatus struct {
	MediaID          uuid.UUID `json:"media_id"`
	OwnerMatches     bool      `json:"owner_matches"`
	Kind             string    `json:"kind"`
	Status           string    `json:"status"`
	ModerationStatus string    `json:"moderation_status"`
	ContentType      string    `json:"content_type"`
	Width            *int      `json:"width,omitempty"`
	Height           *int      `json:"height,omitempty"`
	// ModerationScanned is false when no real scanner ran (scanner disabled,
	// or the stub): the caller must not treat the empty label list as clean.
	ModerationScanned bool                         `json:"moderation_scanned"`
	ModerationScanner string                       `json:"moderation_scanner,omitempty"`
	ModerationLabels  []processing.ModerationLabel `json:"moderation_labels"`
	Prepared          bool                         `json:"prepared"`
	// FaceCount is set only by Prepare with detect_faces when a provider
	// answered.
	FaceCount *int `json:"face_count,omitempty"`
}

// DatingPhotoURL is the delivery-url response.
type DatingPhotoURL struct {
	URL       string    `json:"url"`
	Variant   string    `json:"variant"`
	ExpiresAt time.Time `json:"expires_at"`
}

// DatingPhotoService implements the internal dating photo routes.
type DatingPhotoService struct {
	store  DatingPhotoStore
	blobs  DatingPhotoBlobs
	signer DatingPhotoSigner
	faces  processing.FaceCounter
	ttl    time.Duration
	now    func() time.Time
	log    *slog.Logger
}

// NewDatingPhotoService wires the service. faces nil skips the face count;
// a ttl outside (0, delivery.MaxProtectedTTL] becomes the default.
func NewDatingPhotoService(store DatingPhotoStore, blobs DatingPhotoBlobs, signer DatingPhotoSigner,
	faces processing.FaceCounter, ttl time.Duration, log *slog.Logger) *DatingPhotoService {
	if ttl <= 0 || ttl > delivery.MaxProtectedTTL {
		ttl = DefaultDatingPhotoURLTTL
	}
	if log == nil {
		log = slog.Default()
	}
	return &DatingPhotoService{store: store, blobs: blobs, signer: signer, faces: faces, ttl: ttl, now: time.Now, log: log}
}

// OwnerStatus returns the asset's state for its owner. No side effects.
func (s *DatingPhotoService) OwnerStatus(ctx context.Context, requester, mediaID uuid.UUID) (*DatingPhotoStatus, error) {
	m, err := s.owned(ctx, requester, mediaID)
	if err != nil {
		return nil, err
	}
	variants, err := s.store.GetVariants(ctx, m.ID)
	if err != nil {
		return nil, fmt.Errorf("dating photo: load renditions: %w", err)
	}
	return datingPhotoStatusOf(m, variants), nil
}

// Prepare strips metadata, rebuilds renditions, renders the blurred variant
// and scopes the asset to dating. Idempotent: an already prepared asset is
// not re-encoded. detectFaces counts faces when a provider is wired.
func (s *DatingPhotoService) Prepare(ctx context.Context, requester, mediaID uuid.UUID, detectFaces bool) (*DatingPhotoStatus, error) {
	m, err := s.eligible(ctx, requester, mediaID)
	if err != nil {
		return nil, err
	}
	variants, err := s.store.GetVariants(ctx, m.ID)
	if err != nil {
		return nil, fmt.Errorf("dating photo: load renditions: %w", err)
	}

	var faceInput []byte
	if !datingPhotoPrepared(m, variants) {
		raw, err := s.blobs.DownloadObject(ctx, m.StorageKey)
		if err != nil {
			return nil, fmt.Errorf("dating photo: read original: %w", err)
		}
		img, err := processing.PrepareDatingImage(raw)
		if err != nil {
			return nil, fmt.Errorf("%w: %v", ErrDatingPhotoUnsupported, err)
		}
		// Local/dev mock only: carry the upload's face test marker onto the
		// re-encoded renditions. Rekognition does not implement the stamper,
		// so production stores exactly what PrepareDatingImage rendered.
		if stamper, ok := s.faces.(processing.DatingImageStamper); ok {
			stamper.StampPreparedDatingImage(raw, img)
		}
		if err := s.storePrepared(ctx, m, variants, img); err != nil {
			return nil, err
		}
		// The provider reads pixels; the uploaded bytes are the same picture
		// (and carry the local mock's test marker). Too large → the stripped
		// rendition.
		faceInput = raw
		if len(faceInput) > processing.MaxFaceImageBytes {
			faceInput = smallestFaceInput(img)
		}
		s.log.Info("media: dating photo prepared", "media_id", m.ID, "uploader_id", m.UploaderID,
			"width", img.Original.Width, "height", img.Original.Height, "renditions", len(img.Variants)+1)
		if m, err = s.store.GetMedia(ctx, m.ID); err != nil {
			return nil, fmt.Errorf("dating photo: reload: %w", err)
		}
		if variants, err = s.store.GetVariants(ctx, m.ID); err != nil {
			return nil, fmt.Errorf("dating photo: reload renditions: %w", err)
		}
	} else if detectFaces && s.faces != nil {
		key := datingVariantKey(variants, FaceCompareVariant)
		if key == "" {
			key = m.StorageKey
		}
		if faceInput, err = s.blobs.DownloadObject(ctx, key); err != nil {
			return nil, fmt.Errorf("dating photo: read for face count: %w", err)
		}
	}

	st := datingPhotoStatusOf(m, variants)
	if detectFaces && s.faces != nil && len(faceInput) > 0 {
		n, err := s.faces.CountFaces(ctx, faceInput)
		if err != nil {
			// No count is not "no face": the caller decides without it.
			s.log.Warn("media: dating photo face count unavailable", "media_id", m.ID, "error", err)
		} else {
			st.FaceCount = &n
		}
	}
	return st, nil
}

func smallestFaceInput(img *processing.DatingImage) []byte {
	if len(img.Original.Bytes) <= processing.MaxFaceImageBytes {
		return img.Original.Bytes
	}
	for i := len(img.Variants) - 1; i >= 0; i-- {
		if len(img.Variants[i].Bytes) <= processing.MaxFaceImageBytes && !strings.HasPrefix(img.Variants[i].Name, "thumb") {
			return img.Variants[i].Bytes
		}
	}
	return nil
}

// storePrepared uploads the renditions and the blurred variant, records
// them, then overwrites the original and marks the asset. The original goes
// last: until then nothing is lost, and a retry re-encodes whatever is there.
func (s *DatingPhotoService) storePrepared(ctx context.Context, m *postgres.MediaAsset, existing []postgres.MediaVariant, img *processing.DatingImage) error {
	if !strings.HasSuffix(m.StorageKey, "/original") {
		return fmt.Errorf("dating photo: unexpected storage key layout for media %s", m.ID)
	}
	rows := make([]postgres.MediaVariant, 0, len(img.Variants)+1)
	for _, v := range img.Variants {
		key := strings.TrimSuffix(m.StorageKey, "original") + v.Name
		if err := s.blobs.UploadObject(ctx, key, v.Bytes, "image/jpeg"); err != nil {
			return fmt.Errorf("dating photo: upload %s: %w", v.Name, err)
		}
		rows = append(rows, datingVariantRow(m.ID, v, key))
	}
	blurKey := datingVariantKey(existing, processing.DatingBlurVariant)
	if blurKey == "" {
		blurKey = fmt.Sprintf("user/%s/dating-blurred/%s.jpg", m.UploaderID, uuid.New())
	}
	if err := s.blobs.UploadObject(ctx, blurKey, img.Blurred.Bytes, "image/jpeg"); err != nil {
		return fmt.Errorf("dating photo: upload blurred: %w", err)
	}
	rows = append(rows, datingVariantRow(m.ID, img.Blurred, blurKey))
	if err := s.store.InsertVariants(ctx, rows); err != nil {
		return fmt.Errorf("dating photo: record renditions: %w", err)
	}
	if err := s.blobs.UploadObject(ctx, m.StorageKey, img.Original.Bytes, "image/jpeg"); err != nil {
		return fmt.Errorf("dating photo: replace original: %w", err)
	}
	if err := s.store.MarkDatingPhotoPrepared(ctx, m.ID, "image/jpeg", int64(len(img.Original.Bytes)),
		img.Original.Width, img.Original.Height); err != nil {
		return fmt.Errorf("dating photo: mark prepared: %w", err)
	}
	return nil
}

// DeliveryURL signs the full or blurred image of a prepared dating photo for
// its owner's audience. The caller (dating-service) has already decided the
// viewer may see this variant.
func (s *DatingPhotoService) DeliveryURL(ctx context.Context, owner, mediaID uuid.UUID, variant string) (*DatingPhotoURL, error) {
	if variant != DatingVariantFull && variant != DatingVariantBlurred {
		return nil, fmt.Errorf("%w: variant must be %q or %q", ErrDatingPhotoInvalid, DatingVariantFull, DatingVariantBlurred)
	}
	m, err := s.eligible(ctx, owner, mediaID)
	if err != nil {
		return nil, err
	}
	variants, err := s.store.GetVariants(ctx, m.ID)
	if err != nil {
		return nil, fmt.Errorf("dating photo: load renditions: %w", err)
	}
	if !datingPhotoPrepared(m, variants) {
		return nil, ErrDatingPhotoNotPrepared
	}
	var key string
	if variant == DatingVariantBlurred {
		key = datingVariantKey(variants, processing.DatingBlurVariant)
	} else if key = datingVariantKey(variants, FaceCompareVariant); key == "" {
		key = m.StorageKey
	}
	if s.signer == nil {
		return nil, fmt.Errorf("%w: no delivery signer", delivery.ErrDeliveryUnresolved)
	}
	now := s.now()
	u, err := s.signer.SignProtected(key, s.ttl, now)
	if err != nil {
		return nil, fmt.Errorf("%w: sign: %v", delivery.ErrDeliveryUnresolved, err)
	}
	return &DatingPhotoURL{URL: u, Variant: variant, ExpiresAt: now.Add(s.ttl).UTC()}, nil
}

// Delete purges the owner's asset: rows, renditions (the blurred variant
// included) and objects. Another live reference (a post, a story) keeps the
// asset: ErrAssetStillReferenced.
func (s *DatingPhotoService) Delete(ctx context.Context, owner, mediaID uuid.UUID) (*PurgeResult, error) {
	if _, err := s.owned(ctx, owner, mediaID); err != nil {
		return nil, err
	}
	return NewAssetPurger(s.store, s.blobs, s.log).Purge(ctx, mediaID, Referrer{Kind: ReferrerDatingPhoto, ID: owner})
}

func (s *DatingPhotoService) owned(ctx context.Context, requester, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	if requester == uuid.Nil || mediaID == uuid.Nil {
		return nil, ErrDatingPhotoInvalid
	}
	m, err := s.store.GetMedia(ctx, mediaID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrDatingPhotoNotFound
		}
		return nil, fmt.Errorf("dating photo: load media: %w", err)
	}
	if m == nil || m.UploaderID != requester {
		return nil, ErrDatingPhotoNotFound
	}
	return m, nil
}

func (s *DatingPhotoService) eligible(ctx context.Context, requester, mediaID uuid.UUID) (*postgres.MediaAsset, error) {
	m, err := s.owned(ctx, requester, mediaID)
	if err != nil {
		return nil, err
	}
	if m.FileType != "image" || m.ProcessingStatus != "ready" || m.ModerationStatus != "passed" {
		return nil, ErrDatingPhotoNotReady
	}
	return m, nil
}

func datingPhotoStatusOf(m *postgres.MediaAsset, variants []postgres.MediaVariant) *DatingPhotoStatus {
	st := &DatingPhotoStatus{
		MediaID:           m.ID,
		OwnerMatches:      true,
		Kind:              m.FileType,
		Status:            m.ProcessingStatus,
		ModerationStatus:  m.ModerationStatus,
		ContentType:       m.MimeType,
		Width:             m.Width,
		Height:            m.Height,
		ModerationScanner: m.ModerationScanner,
		ModerationLabels:  []processing.ModerationLabel{},
		Prepared:          datingPhotoPrepared(m, variants),
	}
	// The stub "scans" by approving everything; its empty list is not a scan.
	if m.ModerationScanner != "" && m.ModerationScanner != "stub" && len(m.ModerationLabels) > 0 {
		var labels []processing.ModerationLabel
		if err := json.Unmarshal(m.ModerationLabels, &labels); err == nil {
			if labels != nil {
				st.ModerationLabels = labels
			}
			st.ModerationScanned = true
		}
	}
	return st
}

func datingPhotoPrepared(m *postgres.MediaAsset, variants []postgres.MediaVariant) bool {
	return m.AccessScope == postgres.AccessScopeDatingPhoto && m.MetadataStrippedAt != nil &&
		datingVariantKey(variants, processing.DatingBlurVariant) != ""
}

func datingVariantKey(variants []postgres.MediaVariant, name string) string {
	for _, v := range variants {
		if v.Name == name && v.ObjectKey != "" {
			return v.ObjectKey
		}
	}
	return ""
}

func datingVariantRow(mediaID uuid.UUID, r processing.RenderedImage, key string) postgres.MediaVariant {
	w, h, size := r.Width, r.Height, int64(len(r.Bytes))
	return postgres.MediaVariant{MediaAssetID: mediaID, Name: r.Name, Width: &w, Height: &h, SizeBytes: &size, Mime: "image/jpeg", ObjectKey: key}
}
