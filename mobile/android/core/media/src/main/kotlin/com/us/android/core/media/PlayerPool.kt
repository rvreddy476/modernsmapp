package com.us.android.core.media

import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.DefaultLoadControl
import androidx.media3.exoplayer.ExoPlayer
import javax.inject.Inject
import javax.inject.Singleton

/**
 * A fixed set of recycled [ExoPlayer] instances, keyed by page index.
 *
 * This is the single most important piece of the reels surface, and it exists
 * because of a specific failure recorded in the migration plan: the Flutter
 * reference creates and tears down a `VideoPlayerController` per page. That
 * allocation churn during fast scroll is the visible black-frame stutter the
 * native client is meant to eliminate.
 *
 * [MAX_PLAYERS] instances is not arbitrary. It is the current page plus one
 * ahead and one behind, and one spare for the page being swiped toward. More
 * players means more concurrent decoder sessions, and mid-range devices have a
 * hard limit on those — exceeding it throws rather than degrading, so the
 * ceiling is a correctness constraint, not a tuning preference.
 *
 * Not thread-safe by design: every method must be called from the main thread,
 * because ExoPlayer requires it. Guarding with a lock would hide a misuse that
 * ExoPlayer itself will report loudly.
 */
@Singleton
@OptIn(UnstableApi::class)
class PlayerPool @Inject constructor(
    private val playerFactory: PlayerFactory,
    private val sources: MediaSources,
) {

    private val players = mutableMapOf<Int, ExoPlayer>()

    /**
     * What each pooled player is currently prepared with.
     *
     * A page index is not a stable identity: the Reels tabs (For You,
     * Following) share one pager, and a reel the viewer just posted is
     * inserted above page 0 the moment it exists, shifting every index by
     * one. A player handed back by index alone would keep playing the video
     * it had — the wrong one — so [acquire] re-prepares when the playback
     * for that index has changed.
     */
    private val playbacks = mutableMapOf<Int, Playback>()

    /**
     * The player for [pageIndex], creating or recycling one as needed.
     *
     * When the pool is full the least-recently-needed player is reclaimed
     * rather than a new one allocated. Reclaiming stops playback and clears
     * the media item, so the returned instance is always in a known state.
     */
    fun acquire(pageIndex: Int, playback: Playback, contentId: String? = null): ExoPlayer {
        players[pageIndex]?.let { existing ->
            if (playbacks[pageIndex] == playback) {
                if (contentId != null) contents[pageIndex] = contentId
                return existing
            }
            // The SAME video with another source — its audio track switched
            // (2026-10-21) — keeps its place and its play state, as the web's
            // player does; a different video at this index starts clean.
            val keep = keepsPlace(contents[pageIndex], contentId)
            val position = existing.currentPosition
            val playing = existing.playWhenReady
            existing.stop()
            existing.clearMediaItems()
            return prepare(existing, pageIndex, playback, contentId).also { player ->
                if (keep) {
                    player.seekTo(position)
                    player.playWhenReady = playing
                }
            }
        }

        val player = if (players.size >= MAX_PLAYERS) {
            recycleFurthestFrom(pageIndex)
        } else {
            playerFactory.create()
        }
        return prepare(player, pageIndex, playback, contentId)
    }

    private fun prepare(player: ExoPlayer, pageIndex: Int, playback: Playback, contentId: String?): ExoPlayer {
        player.setMediaSource(sources.create(playback))
        player.prepare()
        players[pageIndex] = player
        playbacks[pageIndex] = playback
        if (contentId != null) contents[pageIndex] = contentId else contents.remove(pageIndex)
        return player
    }

    /**
     * Which video (by the caller's id — a post id) each player holds, when
     * the caller said. What tells an audio-track swap on the same reel, which
     * keeps its place, from an index shift onto another reel, which must not.
     */
    private val contents = mutableMapOf<Int, String>()

    /**
     * Warms the item at [pageIndex] without playing it.
     *
     * Prepare-but-do-not-play is what makes a swipe show a first frame
     * immediately instead of a spinner. It is deliberately limited to the
     * immediate neighbours: preloading further ahead spends the user's data on
     * videos most of them will never reach.
     */
    fun preload(pageIndex: Int, playback: Playback, contentId: String? = null) {
        if (playbacks[pageIndex] == playback) return
        acquire(pageIndex, playback, contentId).playWhenReady = false
    }

    /** Plays [pageIndex] and pauses every other player. */
    fun playOnly(pageIndex: Int) {
        players.forEach { (index, player) ->
            if (index == pageIndex) {
                player.playWhenReady = true
            } else {
                player.playWhenReady = false
            }
        }
    }

    /** Pauses everything — call when the surface leaves the foreground. */
    fun pauseAll() = players.values.forEach { it.playWhenReady = false }

    /**
     * Releases every player.
     *
     * Must be called when the surface is destroyed. A leaked ExoPlayer holds a
     * decoder session and an audio focus request, so leaking one does not just
     * waste memory — it can stop the next video from playing at all.
     */
    fun release() {
        players.values.forEach { it.release() }
        players.clear()
        playbacks.clear()
        contents.clear()
    }

    private fun recycleFurthestFrom(pageIndex: Int): ExoPlayer {
        val victim = players.keys.maxByOrNull { kotlin.math.abs(it - pageIndex) }
            ?: error("pool reported full but held no players")
        val player = players.remove(victim)!!
        playbacks.remove(victim)
        contents.remove(victim)
        player.stop()
        player.clearMediaItems()
        return player
    }

    companion object {
        /** Current page, one either side, and one for the in-flight swipe. */
        const val MAX_PLAYERS = 4
    }
}

/**
 * Creates configured players.
 *
 * Separated from the pool so the pool is testable without an Android context,
 * and so every player in the app gets identical buffering behaviour — a
 * per-call-site `ExoPlayer.Builder` is how two surfaces end up with different
 * startup latency for no stated reason.
 */
interface PlayerFactory {
    fun create(): ExoPlayer
}

/**
 * Buffering tuned for short vertical video.
 *
 * The defaults target long-form playback: buffer a lot, start when comfortable.
 * A reel is a few seconds long and the user judges it on how fast the first
 * frame appears, so the buffer floor is small and time is prioritised over
 * size. On a slow connection that means starting at a lower rung and letting
 * ABR climb, rather than stalling on a black frame while a high rung loads.
 */
@OptIn(UnstableApi::class)
internal fun reelsLoadControl(): DefaultLoadControl = DefaultLoadControl.Builder()
    .setBufferDurationsMs(
        MIN_BUFFER_MS,
        MAX_BUFFER_MS,
        BUFFER_FOR_PLAYBACK_MS,
        BUFFER_FOR_PLAYBACK_AFTER_REBUFFER_MS,
    )
    .setPrioritizeTimeOverSizeThresholds(true)
    .build()

private const val MIN_BUFFER_MS = 2_000
private const val MAX_BUFFER_MS = 10_000

/** Start after a quarter second of media, not the default two and a half. */
private const val BUFFER_FOR_PLAYBACK_MS = 250
private const val BUFFER_FOR_PLAYBACK_AFTER_REBUFFER_MS = 1_000

/** Disk cache ceiling for media segments. */
internal const val MEDIA_CACHE_BYTES = 256L * 1024 * 1024

/** Kept for callers that need to express "no seek position yet". */
internal const val UNSET_POSITION = C.TIME_UNSET

/** Convenience for surfaces that only care whether something is playing. */
val Player.isActuallyPlaying: Boolean
    get() = playWhenReady && playbackState == Player.STATE_READY

/**
 * Whether a re-prepare at one page keeps the playhead: only when the caller
 * named the video both times and it is the same video — a source swap such
 * as an audio track (2026-10-21). Anything unnamed starts from the top, as
 * every re-prepare did before.
 */
internal fun keepsPlace(before: String?, after: String?): Boolean = before != null && before == after
