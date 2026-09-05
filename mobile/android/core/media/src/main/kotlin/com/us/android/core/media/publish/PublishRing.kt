package com.us.android.core.media.publish

/**
 * What the ring over a pending video tile draws (founder, 2026-09-05: the
 * own profile shows the posting video first, with an ember ring in the
 * middle). Derived from the publish state so the tile has one rule:
 *
 *  - bytes leaving the device → a determinate sweep, the uploaded fraction;
 *  - everything else in flight (copying, the server processing, the create)
 *    → an indeterminate spin, because nothing measurable is happening
 *    on this side;
 *  - published → the full ring, for the beat before the real tile lands;
 *  - failed, or idle → no ring; the tile says why, or is gone.
 */
sealed interface PublishRing {
    /** [fraction] is 0..1 of the video's bytes. */
    data class Determinate(val fraction: Float) : PublishRing
    data object Indeterminate : PublishRing
    data object None : PublishRing
}

fun ReelPublishState.ring(): PublishRing = when (this) {
    is ReelPublishState.Uploading -> PublishRing.Determinate(fraction.coerceIn(0f, 1f))
    ReelPublishState.Preparing, ReelPublishState.Processing, ReelPublishState.Posting -> PublishRing.Indeterminate
    is ReelPublishState.Published -> PublishRing.Determinate(1f)
    is ReelPublishState.Failed, ReelPublishState.Idle -> PublishRing.None
}

/** The words under the ring, for a screen reader and the tile's caption. */
fun ReelPublishState.ringLabel(): String = when (this) {
    is ReelPublishState.Uploading -> "Uploading ${(fraction.coerceIn(0f, 1f) * PERCENT).toInt()}%"
    ReelPublishState.Preparing -> "Preparing"
    ReelPublishState.Processing -> "Processing"
    ReelPublishState.Posting -> "Posting"
    is ReelPublishState.Published -> "Posted"
    is ReelPublishState.Failed -> "Couldn't post"
    ReelPublishState.Idle -> ""
}

private const val PERCENT = 100
