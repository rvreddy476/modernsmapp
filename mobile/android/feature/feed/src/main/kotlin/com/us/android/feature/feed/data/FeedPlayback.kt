package com.us.android.feature.feed.data

import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.media.Playback
import com.us.android.core.media.PlaybackKind
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia

/**
 * What to play for a feed row's video, or null when there is nothing to play.
 *
 * Three answers, in order of authority:
 *
 *  1. The server's own choice (`playback_url` + `playback_kind`), sent since
 *     instant reels: the HLS master once a ladder exists, the original file
 *     while the transcoder is still on it. Gateway-relative paths are resolved
 *     and authorized like `hls_url`; signed absolute URLs pass through.
 *  2. A ready asset's `hls_url` — every row from before the contract change.
 *  3. A still-processing asset's `original` variant, when the ladder lists
 *     one: the same fallback the server makes, made here for a feed-service
 *     that has not yet learned to.
 *
 * Null is a real outcome, not a defect: a processing asset with no original
 * on offer has no rendition, and the pager shows its poster rather than
 * handing the player a URL it will fail on. A ready asset that is missing
 * its `hls_url` stays null too — that is a broken row, and guessing a path
 * for it produces a player error where a poster belongs.
 */
internal fun MediaUrlResolver.playbackFor(item: FeedItem): Playback? =
    item.media.firstOrNull { it.kind == VIDEO_KIND }?.let { playbackFor(it) }

internal fun MediaUrlResolver.playbackFor(media: FeedMedia): Playback? {
    val chosen = media.playbackUrl?.takeIf { it.isNotBlank() }
    if (chosen != null) {
        val url = hlsUrl(chosen) ?: return null
        val kind = if (media.playbackKind == PLAYBACK_ORIGINAL) PlaybackKind.Progressive else PlaybackKind.Hls
        return Playback(url, kind)
    }
    if (media.isReady) return hlsUrl(media.hlsUrl)?.let { Playback.hls(it) }
    if (media.isProcessing) return media.variants[ORIGINAL_VARIANT]?.let { Playback.original(it) }
    return null
}

/**
 * Whether the row carries a video at all — playable or not. A tap on a
 * video's media goes to Reels even while it is still processing: Reels is
 * where a video is watched, and it already knows how to say "still
 * processing" for one that cannot play yet.
 */
internal fun FeedItem.hasVideo(): Boolean = media.any { it.kind == VIDEO_KIND }

/** `kind` on a feed media entry; `image` is the other value in use. */
private const val VIDEO_KIND = "video"
private const val PLAYBACK_ORIGINAL = "original"
private const val ORIGINAL_VARIANT = "original"
