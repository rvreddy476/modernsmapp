// media_batch.go is the signed-out batch URL route (BatchMediaURLs) and the
// rules for handing out an asset's ORIGINAL object (reels audit 2026-10-30,
// findings 4 and 6): a video's original goes only to its uploader, and only
// while the stored object still has the ETag recorded at confirm.

package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// BatchMediaURLs returns authorized delivery URLs for multiple media items.
//
// M4-P0-5 — a batch is where an authorization bypass is most valuable to an
// attacker: one request, fifty ids, and before this every one came back with a
// stable URL regardless of who asked.
//
// A media item this viewer may not have is OMITTED from the result rather than
// failing the whole batch. That is deliberate and it is not error-swallowing:
// the batch is a rendering convenience over a mixed set (a feed page contains
// other people's media), so one denied item must not blank the page. The
// omission is indistinguishable from "no such media", which keeps the batch
// from being used to enumerate what exists.
//
// An UNRESOLVED item fails the whole call, because that is an outage rather
// than an answer, and silently dropping it would render a feed with holes and
// no indication anything went wrong.
func (s *Service) BatchMediaURLs(ctx context.Context, viewerID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]*MediaURLResponse, error) {
	if len(ids) > 50 {
		return nil, fmt.Errorf("batch limit is 50 media items")
	}
	if s.gate == nil {
		return nil, fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}

	medias, err := s.pgStore.GetMediaBatch(ctx, ids)
	if err != nil {
		return nil, err
	}

	foundMap := make(map[uuid.UUID]bool, len(medias))
	for _, m := range medias {
		foundMap[m.ID] = true
	}
	for _, id := range ids {
		if !foundMap[id] {
			slog.WarnContext(ctx, "media batch: requested asset absent from database",
				"viewer_id", viewerID,
				"media_id", id)
		}
	}

	// Anonymous assets never get signed URLs; they are answered separately
	// below with this service's own serve paths.
	anonymous := map[uuid.UUID]*postgres.MediaAsset{}
	denyOriginal := map[uuid.UUID]bool{}
	assetKeys := make(map[string]map[string]string, len(medias))
	for i := range medias {
		if medias[i].AccessScope == postgres.AccessScopeAnonymous {
			anonymous[medias[i].ID] = &medias[i]
		}
	}
	for _, m := range medias {
		if anonymous[m.ID] != nil {
			continue
		}
		if ViewerReadDenies(&m, viewerID) {
			// Lane D6 and private documents: omitted exactly like a denial
			// below.
			continue
		}
		keys := map[string]string{}
		if batchIncludesOriginal(&m, viewerID) {
			keys["original"] = m.StorageKey
		}
		for _, v := range m.Variants {
			keys[v.Name] = v.ObjectKey
		}
		if m.HLSMasterKey != "" {
			keys["hls"] = m.HLSMasterKey
		}
		if len(keys) == 0 {
			// Authorized over the original's key, signed nothing: an asset
			// with no servable key is still an audience decision.
			keys["original"] = m.StorageKey
			if !batchIncludesOriginal(&m, viewerID) {
				denyOriginal[m.ID] = true
			}
		}

		assetKeys[m.ID.String()] = keys
	}
	urlsByMedia, err := s.gate.URLsForAssets(ctx, viewerID.String(), assetKeys)
	if err != nil {
		return nil, err
	}

	result := make(map[uuid.UUID]*MediaURLResponse, len(medias))
	for id, m := range anonymous {
		if err := authorizeCaptionRead(ctx, s.gate, m, viewerID); err != nil {
			if errors.Is(err, delivery.ErrDeliveryDenied) {
				continue // resolved denial — omit without revealing existence
			}
			return nil, err
		}
		result[id] = anonymousURLResponse(m)
	}
	for _, m := range medias {
		if anonymous[m.ID] != nil {
			continue
		}
		urls, ok := urlsByMedia[m.ID.String()]
		if !ok {
			slog.InfoContext(ctx, "media batch: asset denied for viewer",
				"viewer_id", viewerID,
				"media_id", m.ID,
				"status", m.ProcessingStatus)
			continue // resolved denial — omit without revealing existence
		}
		if denyOriginal[m.ID] {
			delete(urls, "original")
		}

		if m.ProcessingStatus != "ready" {
			// Permitted content, but asset processing is not yet ready for URL delivery.
			slog.InfoContext(ctx, "media batch: asset permitted but not ready",
				"viewer_id", viewerID,
				"media_id", m.ID,
				"status", m.ProcessingStatus)
			// Instant publish: a confirmed video plays as the original
			// progressive MP4 until the ladder lands (feed hydration reads
			// this DTO, so the reels surface gets it too).
			playbackURL, playbackKind := choosePlayback(m.FileType, "", urls)
			res := &MediaURLResponse{
				MediaID:      m.ID,
				FileType:     m.FileType,
				Status:       m.ProcessingStatus,
				Width:        m.Width,
				Height:       m.Height,
				Blurhash:     m.Blurhash,
				DurationMs:   m.DurationMsValue(),
				Variants:     nil,
				HLSURL:       "",
				PlaybackURL:  playbackURL,
				PlaybackKind: playbackKind,
			}
			if playbackURL != "" {
				expires := time.Now().UTC().Add(delivery.GuaranteedProtectedTTL)
				res.ExpiresAt = &expires
			}
			result[m.ID] = res
			continue
		}

		hlsURL := ""
		if m.HLSMasterKey != "" {
			hlsURL = hlsPlaylistURL(m.ID, "master.m3u8")
		}
		delete(urls, "hls")
		expires := time.Now().UTC().Add(delivery.GuaranteedProtectedTTL)
		playbackURL, playbackKind := choosePlayback(m.FileType, hlsURL, urls)
		result[m.ID] = &MediaURLResponse{
			MediaID:      m.ID,
			FileType:     m.FileType,
			Status:       m.ProcessingStatus,
			Width:        m.Width,
			Height:       m.Height,
			Blurhash:     m.Blurhash,
			DurationMs:   m.DurationMsValue(),
			Variants:     urls,
			HLSURL:       hlsURL,
			ExpiresAt:    &expires,
			PlaybackURL:  playbackURL,
			PlaybackKind: playbackKind,
		}
	}

	return result, nil
}

// ─── Original delivery (reels audit 2026-10-30, findings 4 and 6) ───

// batchIncludesOriginal decides whether POST /v1/media/batch, which needs
// no sign-in, signs an asset's original. Never a video's (finding 4): the
// viewer plays the HLS ladder or a rendition, and the original is the
// uploader's file. The one exception is the uploader's OWN video while it
// is still processing, the instant-publish preview of what they just
// uploaded. Every other kind keeps its original (an image original is
// already re-encoded without EXIF).
func batchIncludesOriginal(m *postgres.MediaAsset, viewerID uuid.UUID) bool {
	if m.FileType != "video" {
		return true
	}
	return viewerID != uuid.Nil && viewerID == m.UploaderID && m.ProcessingStatus != "ready"
}

// originalServable decides whether viewerID may be handed media's original
// object. Anything but a video: yes (decided by the gate as before). A
// video: only to its uploader, and only while the stored object is still
// the one that was confirmed (finding 6).
//
// WHY VIEWERS NO LONGER GET A VIDEO'S ORIGINAL (finding 4). A phone
// MP4/MOV carries the capture location, the device and the time, and the
// original was signed for every authorised viewer, the signed-out batch
// route included. Two fixes were possible: rewrite the original without
// metadata in the worker, or stop handing it out. The second is both
// safer and cheaper. Safer, because viewers then only ever receive bytes
// the worker produced from the scanned original (renditions and the HLS
// ladder, written with -map_metadata -1), so a replace-after-scan of the
// original (finding 6) cannot reach them either; an in-place rewrite would
// still serve whatever sits at a key the upload URL can overwrite.
// Cheaper, because the renditions already exist: no extra ffmpeg pass, no
// second copy in storage. The uploader still gets their own file.
func (s *Service) originalServable(ctx context.Context, media *postgres.MediaAsset, viewerID uuid.UUID) bool {
	if media.FileType != "video" {
		return true
	}
	if viewerID == uuid.Nil || viewerID != media.UploaderID {
		return false
	}
	if media.OriginalETag == "" {
		// Confirmed before upload identity was recorded (migration 022):
		// nothing to compare, and it is the uploader's own file.
		return true
	}
	return s.originalUnchanged(ctx, media)
}

// originalUnchanged reports whether the stored original still has the ETag
// recorded for it (media.OriginalETag, non-empty). The simple-upload PUT URL stays valid for
// presignedPutExpiry after init and confirm does not revoke it, so an
// uploader could replace a scanned, approved original with anything. A
// positive answer obtained after that window closed can never change
// again (nothing but the worker writes the key, and the worker records a
// new ETag), so it is remembered; a mismatch or a failed read refuses.
func (s *Service) originalUnchanged(ctx context.Context, media *postgres.MediaAsset) bool {
	cacheKey := media.ID.String() + "|" + media.OriginalETag
	if _, ok := verifiedOriginals.Load(cacheKey); ok {
		return true
	}
	checkedAt := time.Now()
	info, err := s.objectReader().StatObject(ctx, media.StorageKey)
	if err != nil {
		slog.WarnContext(ctx, "media: original not verifiable; not serving it", "media_id", media.ID, "error", err)
		return false
	}
	if info.ETag != media.OriginalETag {
		slog.WarnContext(ctx, "media: original changed after it was confirmed; refusing it",
			"media_id", media.ID)
		return false
	}
	if checkedAt.After(media.CreatedAt.Add(presignedPutExpiry + time.Minute)) {
		verifiedOriginals.Store(cacheKey, struct{}{})
	}
	return true
}

// verifiedOriginals remembers (media id, etag) pairs verified after the
// upload URL expired. Bounded by the number of distinct video originals a
// process serves; an entry is a few dozen bytes.
var verifiedOriginals sync.Map
