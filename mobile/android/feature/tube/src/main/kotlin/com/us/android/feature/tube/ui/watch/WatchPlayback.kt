package com.us.android.feature.tube.ui.watch

import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.MediaSource
import com.us.android.core.media.HlsRefresh
import com.us.android.core.media.Playback
import com.us.android.core.media.PlaybackKind
import com.us.android.core.media.isExpiredSignature
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/**
 * The long-video player's life apart from what the video is (2026-11-07):
 * what it is prepared with, getting it playing again after an error, and
 * letting go of its decoder while the screen is behind something. The
 * watch screen's ViewModel owns one and keeps the business (analytics, the
 * countdown, the lock) to itself.
 *
 * ## AN ERROR IS NOT THE END (audit H1)
 *
 * Segment and MP4 addresses are signed and expire; a video paused long
 * enough, or longer than its signatures, used to die on the next 403 with
 * the last frame frozen and Play doing nothing. Now:
 *
 *  - HLS heals itself: [HlsRefresh] (the reel pool's) fetches fresh
 *    playlists where the video was, on a 403 or once the playlist's expiry
 *    has passed;
 *  - a signed MP4 (`playback_kind: original`, a video still transcoding)
 *    heals by reading its address again ([freshOriginal]) on a 403, at most
 *    [MAX_ORIGINAL_REFRESHES] times before it reaches READY;
 *  - anything still failing is [failed], which the screen draws with Retry,
 *    and [retry] — the Retry, and Play on a failed player — prepares again
 *    where the video was, with a fresh budget.
 *
 * ## BEHIND SOMETHING, IT HOLDS NOTHING (audit M2)
 *
 * Watch screens stack (Watch, a channel, another Watch), each with its own
 * player, and phones run out of hardware decoders. [hold] stops a screen's
 * player when the screen goes behind something — keeping its playlist and
 * position, which a stop does not reset — and [resume] prepares it again,
 * paused, where it was. A video held on its last frame stays held (its end
 * screen is what shows) until [replay].
 */
@OptIn(UnstableApi::class)
internal class WatchPlayback(
    val player: ExoPlayer,
    private val sourceFor: (Playback) -> MediaSource,
    private val scope: CoroutineScope,
    /**
     * A signed MP4's address read again from the server, for the stale one
     * given; null when there is none to be had (the read failed, the video
     * is locked or gone, or [Playback] is not one the caller can refresh).
     */
    private val freshOriginal: suspend (stale: Playback) -> Playback?,
) {
    private val hlsRefresh = HlsRefresh(player, sourceFor)

    /** What the player is prepared with; null when it holds nothing. */
    var playback: Playback? = null
        private set

    private val _failed = MutableStateFlow(false)

    /** The player stopped on an error nothing healed: the screen offers Retry. */
    val failed: StateFlow<Boolean> = _failed.asStateFlow()

    /** Stopped while the screen is behind something; [resume] or [replay] prepares it again. */
    var held: Boolean = false
        private set

    /** Held on the video's last frame: it stays held until [replay]. */
    var heldAtEnd: Boolean = false
        private set

    private var originalRefreshes = 0
    private var reprepare: Job? = null

    private val listener = object : Player.Listener {
        override fun onPlayerError(error: PlaybackException) {
            val current = playback ?: return
            if (current.kind != PlaybackKind.Progressive || !error.isExpiredSignature()) return
            if (originalRefreshes >= MAX_ORIGINAL_REFRESHES) return
            originalRefreshes++
            reload(current, player.currentPosition, player.playWhenReady, readAgain = true)
        }

        override fun onPlayerErrorChanged(error: PlaybackException?) = noteFailure()

        override fun onPlaybackStateChanged(playbackState: Int) {
            if (playbackState == Player.STATE_READY) originalRefreshes = 0
            noteFailure()
        }
    }

    init {
        player.addListener(listener)
    }

    /** A new video (or a new source for it): prepared from [positionMs] (the default position when unset). */
    fun prepare(playback: Playback, positionMs: Long = C.TIME_UNSET, play: Boolean = true) {
        originalRefreshes = 0
        load(playback, positionMs, play)
    }

    /** Nothing to play: the player lets go of what it had. */
    fun clear() {
        reprepare?.cancel()
        playback = null
        held = false
        heldAtEnd = false
        player.stop()
        player.clearMediaItems()
        noteFailure()
    }

    /** Retry, or Play on a failed player: prepared again where it was, with a fresh budget. */
    fun retry() {
        val current = playback ?: return
        originalRefreshes = 0
        reload(current, player.currentPosition, play = true, readAgain = true)
    }

    /** The screen went behind something: the player lets go of its decoder, keeping where it was. */
    fun hold() {
        if (playback == null || held) return
        heldAtEnd = player.playbackState == Player.STATE_ENDED
        held = true
        player.playWhenReady = false
        player.stop()
    }

    /** Back in front: prepared again where it was, paused. A video held on its last frame waits for [replay]. */
    fun resume() {
        val current = playback ?: return
        if (!held || heldAtEnd) return
        held = false
        hlsRefresh.prepared(current)
        player.prepare()
    }

    /** Replay from the top, whether the player is on its last frame or was held there. */
    fun replay() {
        val current = playback ?: return
        if (held) {
            held = false
            heldAtEnd = false
            hlsRefresh.prepared(current)
            player.seekTo(0L)
            player.prepare()
        } else {
            player.seekTo(0L)
        }
        player.playWhenReady = true
    }

    fun release() {
        reprepare?.cancel()
        hlsRefresh.remove()
        player.removeListener(listener)
        player.release()
    }

    private fun load(target: Playback, positionMs: Long, play: Boolean) {
        reprepare?.cancel()
        playback = target
        held = false
        heldAtEnd = false
        hlsRefresh.prepared(target)
        val source = sourceFor(target)
        if (positionMs == C.TIME_UNSET) player.setMediaSource(source) else player.setMediaSource(source, positionMs)
        player.prepare()
        player.playWhenReady = play
    }

    /**
     * The same video prepared again at [positionMs]. A signed MP4 is read
     * again first when [readAgain]; if another video was opened while that
     * read was out, the answer is dropped.
     */
    private fun reload(current: Playback, positionMs: Long, play: Boolean, readAgain: Boolean) {
        reprepare?.cancel()
        reprepare = scope.launch {
            val fresh = if (readAgain && current.kind == PlaybackKind.Progressive) freshOriginal(current) else null
            if (playback !== current) return@launch
            load(fresh ?: current, positionMs, play)
        }
    }

    private fun noteFailure() {
        _failed.value = playback != null && isFailed(player.playbackState, player.playerError != null)
    }

    companion object {
        /** Fresh MP4 addresses in a row without reaching READY before the error is left to show. */
        const val MAX_ORIGINAL_REFRESHES = 1
    }
}

/** A player on an error: idle with the error still held. Buffering and ready are never failed. */
internal fun isFailed(playbackState: Int, hasError: Boolean): Boolean =
    playbackState == Player.STATE_IDLE && hasError

/** What the centre button (and the window's play/pause) does. */
internal enum class TransportTap { RETRY, REPLAY, TOGGLE }

/** A failed player is prepared again; an ended one replays; anything else plays or pauses. */
internal fun transportTap(failed: Boolean, ended: Boolean): TransportTap = when {
    failed -> TransportTap.RETRY
    ended -> TransportTap.REPLAY
    else -> TransportTap.TOGGLE
}
