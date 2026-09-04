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

/** The speeds the sheet offers, in its order — YouTube's six. [SPEED_NORMAL] is the player's rest. */
val PLAYBACK_SPEEDS: List<Float> =
    listOf(SPEED_HALF, SPEED_THREE_QUARTERS, SPEED_NORMAL, SPEED_QUICK, SPEED_QUICKER, SPEED_DOUBLE)

const val SPEED_HALF = 0.5f
const val SPEED_THREE_QUARTERS = 0.75f
const val SPEED_NORMAL = 1f
const val SPEED_QUICK = 1.25f
const val SPEED_QUICKER = 1.5f
const val SPEED_DOUBLE = 2f

/** "1x", "1.25x", "0.5x" — no trailing zeros, the way YouTube prints them. */
fun speedLabel(speed: Float): String {
    val text = speed.toString().trimEnd('0').trimEnd('.')
    return "${text}x"
}
