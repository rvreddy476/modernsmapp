package com.us.android.feature.tube.ui.watch

import androidx.media3.common.C
import androidx.media3.common.Player
import androidx.media3.common.Tracks
import com.us.android.core.ui.UsReelQuality

/**
 * The heights of every playable video track the player knows about — read
 * once an HLS master's ladder is known, which is a beat after prepare, on
 * `onTracksChanged`. The quality sheet lists them through
 * `reelQualityOptions`, tallest first.
 */
fun Tracks.videoHeights(): List<Int> = groups
    .filter { it.type == C.TRACK_TYPE_VIDEO }
    .flatMap { group ->
        (0 until group.length)
            .filter(group::isTrackSupported)
            .map { group.getTrackFormat(it).height }
    }
    .filter { it > 0 }

/**
 * A quality choice, applied to the player. Auto lifts every size constraint
 * and lets ABR choose; a height pins BOTH bounds to it so the selector takes
 * that rung and no other. A change on a PREPARED player re-prepares it where
 * it is — stop, prepare, seek back, same play state — rather than letting
 * the selector swap renditions inside the running decoder, which the
 * emulator's codec mishandles (a torn frame, seen on Reels 2026-09-04). An
 * unchanged choice is left alone.
 */
fun Player.applyQuality(quality: UsReelQuality) {
    val before = trackSelectionParameters
    val builder = before.buildUpon()
    when (quality) {
        UsReelQuality.Auto -> builder.clearVideoSizeConstraints().setMinVideoSize(0, 0)
        is UsReelQuality.Height -> {
            builder.setMaxVideoSize(Int.MAX_VALUE, quality.height)
            builder.setMinVideoSize(0, quality.height)
        }
    }
    val after = builder.build()
    if (after == before) return
    trackSelectionParameters = after
    if (playbackState == Player.STATE_IDLE) return
    val position = currentPosition
    val wasPlaying = playWhenReady
    stop()
    prepare()
    seekTo(position)
    playWhenReady = wasPlaying
}

// The speeds, `speedLabel` and the caption switch moved out of this file on
// 2026-10-15 (T-32), so Reels offers the very same ones: the speeds to
// `:core:ui` (UsPlaybackChoices.kt), `applyCaption` and `captionChoice` to
// `:core:media` (PlayerCaptions.kt).
