package service

import "testing"

// Instant publish: GET /v1/media/{id}/url and POST /v1/media/batch return a
// playable playback_url for a video whose HLS ladder does not exist yet —
// the signed original progressive MP4 — and switch to the HLS master once
// the transcode lands.

func TestChoosePlayback_FallsBackToOriginalUntilHLSExists(t *testing.T) {
	signed := map[string]string{"original": "https://minio.test/user/u/m/original?sig=1"}

	url, kind := choosePlayback("video", "", signed)
	if kind != PlaybackKindOriginal || url != signed["original"] {
		t.Fatalf("processing video must play the original: got (%q, %q)", url, kind)
	}

	url, kind = choosePlayback("video", "/v1/media/m/hls/master.m3u8", signed)
	if kind != PlaybackKindHLS || url != "/v1/media/m/hls/master.m3u8" {
		t.Fatalf("ready video must play HLS: got (%q, %q)", url, kind)
	}
}

func TestChoosePlayback_NothingForImagesOrUnsignedAssets(t *testing.T) {
	if url, kind := choosePlayback("image", "", map[string]string{"original": "x"}); url != "" || kind != "" {
		t.Fatalf("an image has no playback URL: got (%q, %q)", url, kind)
	}
	if url, kind := choosePlayback("video", "", nil); url != "" || kind != "" {
		t.Fatalf("no signed original means no playback URL, not an empty-string URL: got (%q, %q)", url, kind)
	}
}
