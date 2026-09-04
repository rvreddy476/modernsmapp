package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// Post purge → asset delete (founder decision, 2026-09-04: post deletion
// is soft first, hard later). post-service's purge worker calls
//
//	DELETE /v1/media/internal/{mediaId}
//	{"referrer":"post","referrer_id":"<postId>"}
//
// for each asset the purged post was the LAST post to reference. This side
// re-checks its own reference tables inside the row-deleting transaction
// (postgres.DeleteAssetForReferrer) and then removes EVERY object under the
// asset's key prefix — original, variants, thumbnails, frames, hls/* — not
// only the keys the row tables happened to record.

// ErrAssetStillReferenced: something other than the named referrer still
// holds the asset. The caller treats it as "nothing more to do".
var ErrAssetStillReferenced = errors.New("asset is still referenced")

// ErrAssetNotFound: the asset row is already gone.
var ErrAssetNotFound = errors.New("asset not found")

// Referrer names who is giving up the asset.
type Referrer struct {
	Kind string    // "post"
	ID   uuid.UUID // the post id
}

// assetPurgeStore is the Postgres side of the purge. *postgres.MediaAssetStore
// implements it; tests substitute a fake.
type assetPurgeStore interface {
	DeleteAssetForReferrer(ctx context.Context, mediaID uuid.UUID, referrerKind string, referrerID uuid.UUID) (*postgres.AssetPurgeRecord, error)
	ClearBlobReclaim(ctx context.Context, objectKey string) error
	RecordBlobReclaimFailure(ctx context.Context, objectKey, reason string) error
}

// prefixObjectStore is the object-store side. *blob.Store implements it.
type prefixObjectStore interface {
	ListObjectKeys(ctx context.Context, prefix string) ([]string, error)
	DeleteObject(ctx context.Context, objectKey string) error
}

// AssetPurger deletes one asset's rows and objects.
type AssetPurger struct {
	store assetPurgeStore
	blobs prefixObjectStore
	log   *slog.Logger
}

// NewAssetPurger builds a purger. log may be nil.
func NewAssetPurger(store assetPurgeStore, blobs prefixObjectStore, log *slog.Logger) *AssetPurger {
	if log == nil {
		log = slog.Default()
	}
	return &AssetPurger{store: store, blobs: blobs, log: log}
}

// PurgeResult is what a purge removed (logs, tests).
type PurgeResult struct {
	Prefix         string
	ObjectsDeleted int
	ObjectsFailed  int
}

// Purge deletes the asset for the referrer. Rows go first (transactionally,
// reference-checked); objects second. An object that fails to delete stays
// in media_blob_reclaim for the sweeper — the rows are already gone, so the
// asset can never be served again either way.
func (p *AssetPurger) Purge(ctx context.Context, mediaID uuid.UUID, ref Referrer) (*PurgeResult, error) {
	if ref.Kind != "post" || ref.ID == uuid.Nil {
		return nil, fmt.Errorf("unsupported referrer %q", ref.Kind)
	}
	rec, err := p.store.DeleteAssetForReferrer(ctx, mediaID, ref.Kind, ref.ID)
	switch {
	case errors.Is(err, postgres.ErrMediaNotFound):
		return nil, ErrAssetNotFound
	case errors.Is(err, postgres.ErrMediaStillReferenced):
		return nil, fmt.Errorf("%w: %v", ErrAssetStillReferenced, err)
	case err != nil:
		return nil, err
	}

	// Everything under the prefix, plus whatever the rows recorded (keys
	// outside the prefix, if any deployment ever wrote them there).
	keys := map[string]struct{}{}
	listed, listErr := p.blobs.ListObjectKeys(ctx, rec.Prefix)
	if listErr != nil {
		p.log.Warn("asset purge: prefix listing failed; deleting recorded keys only",
			"media_id", mediaID, "prefix", rec.Prefix, "err", listErr)
	}
	for _, k := range listed {
		keys[k] = struct{}{}
	}
	for _, k := range rec.ObjectKeys {
		if k != "" {
			keys[k] = struct{}{}
		}
	}

	res := &PurgeResult{Prefix: rec.Prefix}
	for k := range keys {
		if err := p.blobs.DeleteObject(ctx, k); err != nil {
			res.ObjectsFailed++
			p.log.Warn("asset purge: object delete failed (sweeper will retry)", "key", k, "err", err)
			_ = p.store.RecordBlobReclaimFailure(ctx, k, err.Error())
			continue
		}
		res.ObjectsDeleted++
		_ = p.store.ClearBlobReclaim(ctx, k)
	}
	p.log.Info("asset purged", "event", "media_asset_purged", "media_id", mediaID,
		"referrer", ref.Kind, "referrer_id", ref.ID, "uploader_id", rec.UploaderID,
		"prefix", rec.Prefix, "objects_deleted", res.ObjectsDeleted, "objects_failed", res.ObjectsFailed)
	if res.ObjectsFailed > 0 && listErr != nil {
		// Both halves degraded: keep the caller's queue row so it retries.
		return res, fmt.Errorf("asset purge: %d object(s) not deleted and listing failed", res.ObjectsFailed)
	}
	return res, nil
}

// PurgeAssetForReferrer is the Service entry point for the internal route.
func (s *Service) PurgeAssetForReferrer(ctx context.Context, mediaID uuid.UUID, ref Referrer) (*PurgeResult, error) {
	return NewAssetPurger(s.pgStore, s.blobStore, nil).Purge(ctx, mediaID, ref)
}

// ParseReferrer validates the request body of the internal delete.
func ParseReferrer(kind, id string) (Referrer, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind != "post" {
		return Referrer{}, fmt.Errorf("referrer must be \"post\"")
	}
	rid, err := uuid.Parse(strings.TrimSpace(id))
	if err != nil || rid == uuid.Nil {
		return Referrer{}, fmt.Errorf("referrer_id must be a uuid")
	}
	return Referrer{Kind: kind, ID: rid}, nil
}
