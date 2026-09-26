package service

import (
	"fmt"

	"github.com/google/uuid"

	"github.com/atpost/media-service/internal/store/postgres"
)

/*
	The URL shape an anonymous asset is handed out under: this service's own
	serve routes, gateway-relative, streamed on request. A signed object URL
	would carry user/<uploader>/… in its path — the one thing an anonymous
	post's picture must not say. These paths have no expiry: authorization
	happens on every request to them.
*/

func anonymousServePath(mediaID uuid.UUID, variant string) string {
	if variant == "" || variant == "original" {
		return fmt.Sprintf("/v1/media/%s/serve", mediaID)
	}
	return fmt.Sprintf("/v1/media/%s/serve/%s", mediaID, variant)
}

func anonymousURLResponse(media *postgres.MediaAsset) *MediaURLResponse {
	urls := map[string]string{"original": anonymousServePath(media.ID, "original")}
	for _, v := range media.Variants {
		urls[v.Name] = anonymousServePath(media.ID, v.Name)
	}
	hlsURL := ""
	if media.HLSMasterKey != "" {
		hlsURL = hlsPlaylistURL(media.ID, "master.m3u8")
	}
	playbackURL, playbackKind := choosePlayback(media.FileType, hlsURL, urls)
	res := &MediaURLResponse{
		MediaID:      media.ID,
		FileType:     media.FileType,
		Status:       media.ProcessingStatus,
		Width:        media.Width,
		Height:       media.Height,
		Blurhash:     media.Blurhash,
		DurationMs:   media.DurationMsValue(),
		HLSURL:       hlsURL,
		PlaybackURL:  playbackURL,
		PlaybackKind: playbackKind,
	}
	if media.ProcessingStatus == "ready" {
		res.Variants = urls
	}
	return res
}
