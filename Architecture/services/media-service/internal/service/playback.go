// playback.go answers "what URL does this viewer open": GetMediaURL with its
// playback choice (HLS ladder, original, or best rendition), the rewritten
// and signed HLS playlists (GetHLSPlaylist), and single-variant delivery
// including the avatar alias (GetMediaVariantURL). Every URL is authorized
// and signed by the delivery gate; nothing here returns a stable object URL.

package service

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/atpost/media-service/internal/delivery"
	"github.com/atpost/media-service/internal/store/postgres"
	"github.com/google/uuid"
)

// MediaURLResponse is the response for serving media URLs.
type MediaURLResponse struct {
	MediaID  uuid.UUID `json:"media_id"`
	FileType string    `json:"kind"`
	Status   string    `json:"status"`
	Width    *int      `json:"width,omitempty"`
	Height   *int      `json:"height,omitempty"`
	Blurhash *string   `json:"blurhash,omitempty"`
	// DurationMs is the ffprobe duration in milliseconds for video and
	// audio; omitted (0) while unknown and for images (Tube, 2026-09-05).
	DurationMs int               `json:"duration_ms,omitempty"`
	Variants   map[string]string `json:"variants,omitempty"`
	HLSURL     string            `json:"hls_url,omitempty"`
	// ExpiresAt tells native clients when the bounded delivery capability
	// must be refreshed. Public variants may be stable, but using the same
	// refresh boundary keeps one DTO valid when visibility later becomes
	// protected. hls_url itself is a route on this service and does not
	// expire; the segment URLs inside its child playlists carry their own
	// expiry in the playlist's X-HLS-Expires-At header (HLSPlaylist).
	ExpiresAt *time.Time `json:"expires_at,omitempty"`

	// PlaybackURL / PlaybackKind (instant publish, 2026-09-04): the ONE URL
	// a video player should open and what it is.
	//
	//   - "hls": the authorized master playlist (gateway-relative, same
	//     value as hls_url) — once the transcode has produced a ladder.
	//   - "original": the signed URL of the object the phone uploaded, a
	//     progressive MP4 — while the transcode is still running (or if it
	//     produced no HLS). A reel is playable the moment its upload
	//     finishes; the ladder is a quality upgrade that lands later.
	//
	// Absent for images and audio. The poster/thumbnail may still be
	// missing while the kind is "original"; the worker generates it.
	PlaybackURL  string `json:"playback_url,omitempty"`
	PlaybackKind string `json:"playback_kind,omitempty"`
}

// Playback kinds. See MediaURLResponse.PlaybackKind.
const (
	PlaybackKindHLS      = "hls"
	PlaybackKindOriginal = "original"
)

// choosePlayback picks the playback URL for a video asset. Pure, so it is
// the one place the fallback rule lives and it is unit-tested without a
// blob store. `urls` is the signed map from the delivery gate; it always
// carries "original" once the asset is authorized, whatever its processing
// state, because the original object exists from the moment the upload is
// confirmed.
func choosePlayback(fileType, hlsURL string, urls map[string]string) (url, kind string) {
	if fileType != "video" {
		return "", ""
	}
	if hlsURL != "" {
		return hlsURL, PlaybackKindHLS
	}
	if orig := urls["original"]; orig != "" {
		return orig, PlaybackKindOriginal
	}
	// A viewer who is not handed the original (finding 4) still plays a
	// ready video without a ladder from its best progressive rendition.
	// The kind stays "original": to every client it means "a progressive
	// file, not a playlist", which a rendition is.
	for _, name := range playbackRenditionLadder {
		if u := urls[name]; u != "" {
			return u, PlaybackKindOriginal
		}
	}
	return "", ""
}

// playbackRenditionLadder is the order a viewer who may not have a video's
// original is given a rendition in its place (choosePlayback, and
// /serve/original).
var playbackRenditionLadder = []string{"720p", "480p", "360p", "1080p", "4k"}

// bestRenditionKey is the object key of the first rendition of the ladder
// the asset has, or "".
func bestRenditionKey(variants []postgres.MediaVariant) string {
	byName := make(map[string]string, len(variants))
	for _, v := range variants {
		byName[v.Name] = v.ObjectKey
	}
	for _, name := range playbackRenditionLadder {
		if k := byName[name]; k != "" {
			return k
		}
	}
	return ""
}

// GetMediaURL returns authorized delivery URLs for a media item and all its
// variants.
//
// M4-P0-5 — this takes a viewer now. It previously took only a media id and
// handed back stable CDN URLs for every key, so the caller's identity never
// entered the decision at all.
//
// Signing errors are RETURNED rather than skipped. The old loop used
// `if err == nil { variants[name] = url }`, which meant a misconfiguration
// produced a 200 with an empty variant map — indistinguishable from media that
// had not finished processing, and impossible to alert on.
func (s *Service) GetMediaURL(ctx context.Context, viewerID, mediaID uuid.UUID) (*MediaURLResponse, error) {
	media, err := s.pgStore.GetMediaWithVariants(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if ViewerReadDenies(media, viewerID) {
		return nil, delivery.ErrDeliveryDenied
	}
	if s.gate == nil {
		return nil, fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}

	// An anonymous asset is never a signed object URL (the key names the
	// uploader): every URL is this service's own serve route, which streams.
	if media.AccessScope == postgres.AccessScopeAnonymous {
		if err := authorizeCaptionRead(ctx, s.gate, media, viewerID); err != nil {
			return nil, err
		}
		return anonymousURLResponse(media), nil
	}

	keys := map[string]string{}
	if s.originalServable(ctx, media, viewerID) {
		keys["original"] = media.StorageKey
	}
	for _, v := range media.Variants {
		keys[v.Name] = v.ObjectKey
	}
	if media.HLSMasterKey != "" {
		keys["hls"] = media.HLSMasterKey
	}
	if len(keys) == 0 {
		// Nothing servable to this viewer yet (a processing video that is
		// not theirs) is still an audience decision, made over the
		// original's key: URLsForAsset answers an empty key set without
		// asking anyone.
		if err := s.gate.AuthorizeAsset(ctx, viewerID.String(), mediaID.String(),
			map[string]string{"original": media.StorageKey}); err != nil {
			return nil, err
		}
	}

	// One authorization for the asset, then every key signed.
	urls, err := s.gate.URLsForAsset(ctx, viewerID.String(), mediaID.String(), keys)
	if err != nil {
		return nil, err
	}

	if media.ProcessingStatus != "ready" {
		slog.InfoContext(ctx, "media single: asset permitted but not ready",
			"media_id", media.ID,
			"viewer_id", viewerID,
			"status", media.ProcessingStatus)
		// Instant publish: no variants yet, but a confirmed video is
		// playable as the original progressive MP4 right now. The URL is
		// signed, so it carries the same refresh boundary as a ready asset.
		playbackURL, playbackKind := choosePlayback(media.FileType, "", urls)
		res := &MediaURLResponse{
			MediaID:      media.ID,
			FileType:     media.FileType,
			Status:       media.ProcessingStatus,
			Width:        media.Width,
			Height:       media.Height,
			Blurhash:     media.Blurhash,
			DurationMs:   media.DurationMsValue(),
			Variants:     nil,
			HLSURL:       "",
			PlaybackURL:  playbackURL,
			PlaybackKind: playbackKind,
		}
		if playbackURL != "" {
			expires := time.Now().UTC().Add(delivery.GuaranteedProtectedTTL)
			res.ExpiresAt = &expires
		}
		return res, nil
	}

	hlsURL := ""
	if media.HLSMasterKey != "" {
		// A signature on the storage master does not propagate to its relative
		// child playlists and segments. Return the authorized API playlist path;
		// that path rewrites children and signs the byte-bearing segments.
		hlsURL = hlsPlaylistURL(media.ID, "master.m3u8")
	}
	delete(urls, "hls")
	expires := time.Now().UTC().Add(delivery.GuaranteedProtectedTTL)
	playbackURL, playbackKind := choosePlayback(media.FileType, hlsURL, urls)

	return &MediaURLResponse{
		MediaID:      media.ID,
		FileType:     media.FileType,
		Status:       media.ProcessingStatus,
		Width:        media.Width,
		Height:       media.Height,
		Blurhash:     media.Blurhash,
		DurationMs:   media.DurationMsValue(),
		Variants:     urls,
		HLSURL:       hlsURL,
		ExpiresAt:    &expires,
		PlaybackURL:  playbackURL,
		PlaybackKind: playbackKind,
	}, nil
}

func hlsPlaylistURL(mediaID uuid.UUID, playlist string) string {
	return fmt.Sprintf("/v1/media/%s/hls/%s", mediaID, playlist)
}

// HLSPlaylist is one rewritten playlist.
type HLSPlaylist struct {
	Body []byte
	// ExpiresAt is when the first signed segment URL in Body stops working
	// (delivery.Gate.HLSURLsForAsset): a player still playing then must
	// re-fetch this playlist, because a VOD playlist is never reloaded on
	// its own. Zero when nothing in Body expires (a master playlist, whose
	// children are routes on this service; an anonymous asset, whose
	// segments are streamed through it). The route sends it as the
	// X-HLS-Expires-At header.
	ExpiresAt time.Time
}

// GetHLSPlaylist authorizes an asset and rewrites its playlist into a graph a
// native player can follow. Only generated basename playlists are accepted;
// callers cannot turn this endpoint into an arbitrary bucket reader.
func (s *Service) GetHLSPlaylist(ctx context.Context, viewerID, mediaID uuid.UUID, playlist string) (*HLSPlaylist, error) {
	if s.gate == nil {
		return nil, fmt.Errorf("%w: delivery gate not configured", delivery.ErrDeliveryUnresolved)
	}
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return nil, err
	}
	if ViewerReadDenies(media, viewerID) {
		return nil, delivery.ErrDeliveryDenied
	}
	if media.HLSMasterKey == "" {
		return nil, fmt.Errorf("HLS is not available for media %s", mediaID)
	}
	playlist = strings.TrimSpace(playlist)
	if playlist == "" || playlist == "hls" {
		playlist = "master.m3u8"
	}
	if strings.ContainsAny(playlist, `/\\`) || playlist == "." || playlist == ".." || !strings.HasSuffix(playlist, ".m3u8") {
		return nil, fmt.Errorf("invalid HLS playlist %q", playlist)
	}

	baseKey := strings.TrimSuffix(media.HLSMasterKey, "master.m3u8")
	playlistKey := baseKey + playlist
	raw, err := s.blobStore.DownloadObject(ctx, playlistKey)
	if err != nil {
		return nil, fmt.Errorf("load HLS playlist: %w", err)
	}
	lines := strings.Split(strings.ReplaceAll(string(raw), "\r\n", "\n"), "\n")
	keys := map[string]string{"playlist": playlistKey}
	if playlist != "master.m3u8" {
		for _, line := range lines {
			name := strings.TrimSpace(line)
			if name == "" || strings.HasPrefix(name, "#") {
				continue
			}
			if strings.ContainsAny(name, `/\\`) || name == "." || name == ".." {
				return nil, fmt.Errorf("invalid HLS segment reference %q", name)
			}
			keys[name] = baseKey + name
		}
	}

	// An anonymous asset's segments must not become signed object URLs — the
	// object key names the uploader. Authorize over the same key set (the
	// same choke point, the same decision) and send segments back through
	// this service instead. See anonymous_scope.go.
	if media.AccessScope == postgres.AccessScopeAnonymous {
		if err := s.gate.AuthorizeAsset(ctx, viewerID.String(), mediaID.String(), keys); err != nil {
			return nil, err
		}
		body, err := rewriteHLS(lines, playlist, mediaID, nil, true)
		if err != nil {
			return nil, err
		}
		return &HLSPlaylist{Body: body}, nil
	}

	// URLsForAsset is the authorization choke point. Including the playlist
	// key makes even an empty playlist require a resolved permission decision;
	// child segment URLs are then signed after that same single decision.
	//
	// Segments are signed to outlive a realistic play session of this
	// video (delivery.HLSSegmentTTL, finding 9): a VOD playlist is never
	// reloaded, so a 5-minute segment signature broke every reel played
	// or paused past five minutes. Past even that, the client re-fetches
	// at ExpiresAt.
	urls, expiresAt, err := s.gate.HLSURLsForAsset(ctx, viewerID.String(), mediaID.String(), keys,
		delivery.HLSSegmentTTL(media.DurationMsValue()))
	if err != nil {
		return nil, err
	}
	body, err := rewriteHLS(lines, playlist, mediaID, urls, false)
	if err != nil {
		return nil, err
	}
	return &HLSPlaylist{Body: body, ExpiresAt: expiresAt}, nil
}

// GetMediaVariantURL returns an authorized delivery URL for one variant.
// AvatarVariant is the rendition alias a client asks for when it wants "an
// image the size of a face on a row", without knowing which renditions this
// particular asset actually has.
//
// # WHY AN ALIAS RATHER THAN A VARIANT NAME IN THE URL
//
// The image pipeline SKIPS a variant whose target is larger than the original
// (internal/processing/image.go), so the ladder an asset owns depends on the
// bytes the user uploaded. A profile payload that hard-coded
// `…/serve/thumb_150` would therefore 404 for any avatar smaller than 150px —
// and for any asset whose processing never completed, of which this database
// already holds one. The caller that renders an avatar does not know, and
// should not have to fetch the asset's variant list to find out.
//
// The alias resolves that server-side against what exists.
const AvatarVariant = "avatar"

// avatarRenditionLadder is tried in order, smallest adequate first.
//
// Falling back to the ORIGINAL last is deliberate: it is the only rendition
// guaranteed to exist, and a slow full-size avatar is a better failure than a
// missing one. It is also why the alias exists at all rather than a fallback
// inside GetMediaVariantURL — a caller that names `thumb_150` explicitly must
// keep getting `thumb_150` or an error, never a silent 1080px substitute.
var avatarRenditionLadder = []string{"thumb_150", "small_480", "medium_1080"}

// pickAvatarRendition chooses the key to deliver. Split out from the fetch so
// the ladder — including the fall through to the original — is a table test
// rather than something only a live database can demonstrate.
func pickAvatarRendition(variants []postgres.MediaVariant, originalKey string) string {
	byName := make(map[string]string, len(variants))
	for _, v := range variants {
		byName[v.Name] = v.ObjectKey
	}
	for _, name := range avatarRenditionLadder {
		if key, ok := byName[name]; ok {
			return key
		}
	}
	return originalKey
}

// resolveAvatarRendition returns the object key to deliver for AvatarVariant.
func (s *Service) resolveAvatarRendition(ctx context.Context, mediaID uuid.UUID) (string, error) {
	variants, err := s.pgStore.GetVariants(ctx, mediaID)
	if err != nil {
		return "", err
	}
	// GetMedia is needed for the fallback key, and it is also the only check
	// that the asset exists at all: an id with no renditions and no row must
	// be a not-found, not a delivery URL for an empty key.
	media, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return "", err
	}
	return pickAvatarRendition(variants, media.StorageKey), nil
}

func (s *Service) GetMediaVariantURL(ctx context.Context, viewerID, mediaID uuid.UUID, variant string) (string, error) {
	// Lane D6: every variant of a dating-scoped asset, the blurred one
	// included, is refused here to anyone but its uploader. Other viewers
	// reach dating photos only through a URL dating-service obtained after
	// its own audience decision.
	asset, err := s.pgStore.GetMedia(ctx, mediaID)
	if err != nil {
		return "", err
	}
	// A private document is refused here to everyone (document.go).
	if ViewerReadDenies(asset, viewerID) {
		return "", delivery.ErrDeliveryDenied
	}
	// An anonymous asset is never a signed object URL (the key names the
	// uploader): the URL is this service's own serve route, which streams.
	if asset.AccessScope == postgres.AccessScopeAnonymous {
		if err := authorizeCaptionRead(ctx, s.gate, asset, viewerID); err != nil {
			return "", err
		}
		return anonymousServePath(mediaID, variant), nil
	}
	if variant == AvatarVariant {
		key, err := s.resolveAvatarRendition(ctx, mediaID)
		if err != nil {
			return "", err
		}
		return s.deliveryURL(ctx, viewerID, mediaID, key)
	}
	if variant == "original" {
		if !s.originalServable(ctx, asset, viewerID) {
			// A video's uploaded file is its uploader's alone (findings 4 and
			// 6): anyone else asking for "original" gets the best rendition,
			// which carries no capture metadata and was cut from the bytes
			// that were scanned. No rendition yet: nothing to serve.
			variants, err := s.pgStore.GetVariants(ctx, mediaID)
			if err != nil {
				return "", err
			}
			key := bestRenditionKey(variants)
			if key == "" {
				return "", delivery.ErrDeliveryDenied
			}
			return s.deliveryURL(ctx, viewerID, mediaID, key)
		}
		return s.deliveryURL(ctx, viewerID, mediaID, asset.StorageKey)
	}

	variants, err := s.pgStore.GetVariants(ctx, mediaID)
	if err != nil {
		return "", err
	}
	for _, v := range variants {
		if v.Name == variant {
			// The only read that names a derived variant, and therefore the
			// only one the anonymous open-graph poster rule can apply to.
			// `original` above and the avatar branch keep the unchanged
			// decision: a poster is a still the pipeline derived, never the
			// file the author uploaded.
			return s.deliveryURLForVariant(ctx, viewerID, mediaID, v.Name, v.ObjectKey)
		}
	}
	return "", fmt.Errorf("variant %q not found", variant)
}
