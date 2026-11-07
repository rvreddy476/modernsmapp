package com.us.android.core.media

import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.datasource.HttpDataSource
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.analytics.AnalyticsListener
import androidx.media3.exoplayer.source.LoadEventInfo
import androidx.media3.exoplayer.source.MediaLoadData
import androidx.media3.exoplayer.source.MediaSource
import java.time.OffsetDateTime

/**
 * Keeps one player's HLS playable after the segment signatures inside its
 * playlists expire (2026-11-07). Shared: the reel pool installs it on every
 * player, and the long-video player is meant to install the same.
 *
 * ## WHY A PLAYER NEEDS THIS
 *
 * A VOD media playlist is fetched once, at prepare, and never again: its
 * segment URLs are signed, and media-service signs them for twice the
 * video's length plus a quarter of an hour (at most 12 h). A video paused
 * past that — overnight, say — or a reel looping on a stand finds its next
 * segment answered 403, and the player stops for good. The master playlist
 * (`hls_url`) does not expire and the playlists are never cached
 * ([PlaylistAwareDataSourceFactory]), so preparing the SAME [Playback] again
 * fetches fresh playlists with fresh signatures.
 *
 * ## WHEN IT RE-PREPARES
 *
 *  - On a 403 anywhere in a playback error's causes: a signature died.
 *  - Before it would: once the earliest `X-HLS-Expires-At` a playlist was
 *    served with has passed, at the next play (a long pause ending) or the
 *    next loop of a reel — both moments where a re-prepare costs nothing
 *    the viewer would see.
 *
 * Each re-prepare keeps the playhead and the play state. At most
 * [MAX_REFRESHES] in a row: a server that keeps answering 403 (a revoked
 * video, a blocked viewer) must leave the error showing, not loop. Reaching
 * READY resets the count. Progressive and offline playback are left alone:
 * re-preparing a signed MP4 URL fetches the same dead signature.
 */
@OptIn(UnstableApi::class)
class HlsRefresh(
    private val player: ExoPlayer,
    private val sourceFor: (Playback) -> MediaSource,
    private val now: () -> Long = System::currentTimeMillis,
) {
    private var playback: Playback? = null
    private val clock = SignatureClock()

    private val listener = object : Player.Listener {
        override fun onPlayerError(error: PlaybackException) {
            if (error.isExpiredSignature() || clock.expired(now())) refresh()
        }

        override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) {
            if (playWhenReady && clock.expired(now())) refresh()
        }

        override fun onPositionDiscontinuity(
            oldPosition: Player.PositionInfo,
            newPosition: Player.PositionInfo,
            reason: Int,
        ) {
            if (reason == Player.DISCONTINUITY_REASON_AUTO_TRANSITION && clock.expired(now())) refresh()
        }

        override fun onPlaybackStateChanged(playbackState: Int) {
            if (playbackState == Player.STATE_READY) clock.playing()
        }
    }

    private val loads = object : AnalyticsListener {
        override fun onLoadCompleted(
            eventTime: AnalyticsListener.EventTime,
            loadEventInfo: LoadEventInfo,
            mediaLoadData: MediaLoadData,
        ) {
            if (mediaLoadData.dataType != C.DATA_TYPE_MANIFEST) return
            hlsExpiresAtMillis(loadEventInfo.responseHeaders)?.let(clock::served)
        }
    }

    init {
        player.addListener(listener)
        player.addAnalyticsListener(loads)
    }

    /** The player was just prepared with [playback]: a new video, a new clock, a fresh budget. */
    fun prepared(playback: Playback) {
        this.playback = playback
        clock.reset()
    }

    /** Stop listening; call before the player is released. */
    fun remove() {
        player.removeListener(listener)
        player.removeAnalyticsListener(loads)
        playback = null
    }

    private fun refresh() {
        val target = playback?.takeIf { it.kind == PlaybackKind.Hls } ?: return
        if (!clock.spendRefresh()) return
        val position = player.currentPosition
        val play = player.playWhenReady
        player.setMediaSource(sourceFor(target), position)
        player.prepare()
        player.playWhenReady = play
    }

    companion object {
        /** Re-prepares in a row without reaching READY before the error is left to show. */
        const val MAX_REFRESHES = 2

        /** The response header media-service puts on a child playlist. */
        const val EXPIRES_AT_HEADER = "X-HLS-Expires-At"
    }
}

/**
 * The bookkeeping, apart from the player so it is a table test: the
 * earliest expiry a playlist was served with, and the refresh budget.
 */
internal class SignatureClock {
    private var expiresAtMillis: Long? = null
    private var refreshes = 0

    fun reset() {
        expiresAtMillis = null
        refreshes = 0
    }

    /** A playlist arrived signed until [atMillis]: the video's signatures die with the earliest. */
    fun served(atMillis: Long) {
        expiresAtMillis = expiresAtMillis?.coerceAtMost(atMillis) ?: atMillis
    }

    fun expired(nowMillis: Long): Boolean = expiresAtMillis?.let { nowMillis >= it } == true

    /** READY: the last refresh (if any) worked. */
    fun playing() {
        refreshes = 0
    }

    /** Whether a refresh may happen now; it starts a new clock (the fresh playlists report theirs). */
    fun spendRefresh(): Boolean {
        if (refreshes >= HlsRefresh.MAX_REFRESHES) return false
        refreshes++
        expiresAtMillis = null
        return true
    }
}

/** `X-HLS-Expires-At` (RFC 3339, UTC) from response headers, any case; null when absent or unreadable. */
internal fun hlsExpiresAtMillis(headers: Map<String, List<String>>): Long? {
    val value = headers.entries
        .firstOrNull { (name, _) -> name.equals(HlsRefresh.EXPIRES_AT_HEADER, ignoreCase = true) }
        ?.value?.firstOrNull()?.trim()
        ?: return null
    return runCatching { OffsetDateTime.parse(value).toInstant().toEpochMilli() }.getOrNull()
}

/**
 * A 403 anywhere in the causes: an object store refusing an expired signature.
 * Public for the long-video player (2026-11-07), which also heals a signed
 * MP4 by reading its address again; [HlsRefresh] heals HLS by itself.
 */
fun PlaybackException.isExpiredSignature(): Boolean =
    generateSequence<Throwable>(this) { it.cause }.take(MAX_CAUSE_DEPTH).any { cause ->
        (cause as? HttpDataSource.InvalidResponseCodeException)?.responseCode == HTTP_FORBIDDEN
    }

private const val HTTP_FORBIDDEN = 403
private const val MAX_CAUSE_DEPTH = 8
