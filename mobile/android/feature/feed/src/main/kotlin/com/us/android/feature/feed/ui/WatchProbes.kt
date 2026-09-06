package com.us.android.feature.feed.ui

import androidx.media3.common.Player
import com.us.android.core.analytics.WatchProbe

/**
 * Reads a player for [com.us.android.core.analytics.VideoWatchTracker].
 *
 * ## WHY A LAMBDA RATHER THAN HANDING OVER THE PLAYER
 *
 * `:core:analytics` must not depend on media3. Half of what it does — like,
 * share, save, follow-from-content, report — has nothing to do with a player,
 * and a media3 dependency would also make every watch-accounting rule
 * untestable without a real `ExoPlayer`. So the tracker asks a function for a
 * reading and never learns what is behind it; the tests hand it a list.
 *
 * The tracker calls this on the main dispatcher, which is required: ExoPlayer
 * state may only be read on the thread the player was built on. It is one read
 * per second, against the 250ms poll the reels progress bar already runs.
 */
internal fun watchProbe(player: Player): suspend () -> WatchProbe = {
    WatchProbe(
        playheadMs = player.currentPosition.coerceAtLeast(0L),
        isPlaying = player.isPlaying,
        isBuffering = player.playbackState == Player.STATE_BUFFERING,
        // READY means the decoder has content to show. The shared spinner
        // observer in `core.media.ui.VideoLoading` watches the same
        // transition, but it keeps only a boolean for drawing — there were no
        // timings there to reuse, so time-to-first-frame is measured from the
        // moment the surface asked for playback instead.
        renderedFirstFrame = player.playbackState == Player.STATE_READY,
        speed = player.playbackParameters.speed,
        durationMs = player.duration.takeIf { it > 0L } ?: 0L,
    )
}
