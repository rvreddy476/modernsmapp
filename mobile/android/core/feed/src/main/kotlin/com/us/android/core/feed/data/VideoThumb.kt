package com.us.android.core.feed.data

import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia

/**
 * What a video card draws before the video: a still to load, the wash to
 * show while it loads, and the length for the badge. Shared by Tube's cards,
 * the Reels poster and the profile grid, so the four cannot disagree about
 * which still a post shows (cover fix, 2026-09-05).
 *
 * @property url the cover the author chose when the row carries it — as
 *   [FeedItem.coverMedia] (resolved by the hydrator) or as a media entry —
 *   at up to 720 tall; else the transcode's own still (`thumb_150`). Null
 *   when the row has neither — the wash alone, then.
 * @property blurhash the placeholder of whichever still was chosen.
 * @property durationMs the video's length, or 0 when the row does not say.
 * @property isPortrait whether the VIDEO is taller than wide, so a grid
 *   can give it a taller tile; false when the row does not say.
 */
data class VideoThumb(val url: String?, val blurhash: String, val durationMs: Long, val isPortrait: Boolean = false)

/**
 * The card's still, from the row's media: the chosen cover first, the video's
 * own thumbnail otherwise. The cover is an IMAGE the reel form uploaded
 * beside the video, so it has the image ladder to pick a real size from;
 * a video's variants are renditions, and its still is the one small
 * thumbnail the transcode made.
 */
fun MediaUrlResolver.videoThumb(item: FeedItem): VideoThumb {
    val video = item.media.firstOrNull { it.kind == VIDEO_KIND }
    val cover = item.coverStill()
    val still: FeedMedia? = cover ?: video
    val url = when {
        cover != null -> bestVariant(cover.variants, COVER_MAX_HEIGHT) ?: thumbnail(cover.variants)
        video != null -> thumbnail(video.variants)
        else -> null
    }
    return VideoThumb(
        url = url,
        blurhash = still?.blurhash.orEmpty(),
        durationMs = video?.durationMs ?: 0L,
        isPortrait = video?.isVertical ?: false,
    )
}

/**
 * The chosen cover's media, wherever the row keeps it: the hydrator's
 * [FeedItem.coverMedia] first, then a media entry with the cover's id (a
 * server that embeds it). Never the video itself, even when the cover id
 * points at it. Null when there is no cover, or it has no delivery yet.
 */
fun FeedItem.coverStill(): FeedMedia? {
    val id = controls.coverMediaId?.takeIf { it.isNotBlank() } ?: return null
    val candidate = coverMedia?.takeIf { it.mediaId == id }
        ?: media.firstOrNull { it.mediaId == id && it.kind != VIDEO_KIND }
    return candidate?.takeIf { it.variants.isNotEmpty() }
}

private const val VIDEO_KIND = "video"

/** A phone-width card; anything taller is bytes the card cannot show. */
private const val COVER_MAX_HEIGHT = 720
