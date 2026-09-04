package com.us.android.feature.tube.ui

import com.us.android.core.media.MediaUrlResolver
import com.us.android.core.model.FeedItem
import com.us.android.core.model.FeedMedia

/**
 * What a video card draws before the video: a still to load, the wash to
 * show while it loads, and the length for the badge.
 *
 * @property url the cover the author chose when the row carries it as a
 *   media entry, at up to 720 tall; else the transcode's own still
 *   (`thumb_150`). Null when the row has neither — the wash alone, then.
 * @property blurhash the placeholder of whichever still was chosen.
 * @property durationMs the video's length, or 0 when the row does not say.
 */
data class VideoThumb(val url: String?, val blurhash: String, val durationMs: Long)

/**
 * The card's still, from the row's media: the chosen cover first, the video's
 * own thumbnail otherwise. The cover is an IMAGE the reel form uploaded
 * beside the video, so it has the image ladder to pick a real size from;
 * a video's variants are renditions, and its still is the one small
 * thumbnail the transcode made.
 */
fun MediaUrlResolver.videoThumb(item: FeedItem): VideoThumb {
    val video = item.media.firstOrNull { it.kind == VIDEO_KIND }
    val cover = item.controls.coverMediaId
        ?.let { id -> item.media.firstOrNull { it.mediaId == id && it.kind != VIDEO_KIND } }
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
    )
}

private const val VIDEO_KIND = "video"

/** A phone-width card; anything taller is bytes the card cannot show. */
private const val COVER_MAX_HEIGHT = 720
