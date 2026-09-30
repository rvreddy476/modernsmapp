package com.us.android.core.media.sound

import android.os.SystemClock
import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.PlaybackException
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.MediaSource
import com.us.android.core.media.MediaSources
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.network.ApiConfig
import javax.inject.Inject
import kotlin.math.roundToLong

/**
 * Plays a reel's added sound beside its video (original sounds, 2026-09-30).
 *
 * The video and the sound are two files and nothing is mixed on the server,
 * so this owns a SECOND player: audio only, built by the injected
 * [PlayerFactory] like every other player in the app, and outside
 * `PlayerPool` — it opens no video decoder, so it does not count against the
 * pool's four. It serves ONLY the settled page: [attach] when the pager
 * settles on a reel that plays a sound, [detach] when it settles on one that
 * does not.
 *
 * ## IT DECIDES NOTHING
 *
 * Every position, every level and whether the sound runs come from
 * [planSoundSync]; this class reads the two players into a [SoundSyncInput]
 * and copies the answer back. It is driven by the VIDEO player's events —
 * play and pause, a seek or a loop, the rate, the state, an error — each a
 * [SyncReason.JUMP], and by [tick], which the reel page calls from its
 * existing 250 ms progress poll, a [SyncReason.TICK].
 *
 * While a sound is attached the video player's volume is set ONLY here, from
 * the plan: the creator's level for the reel's own audio applies while there
 * is a sound beside it, and the viewer's full level when there is none or it
 * failed. [detach] gives the video back at the plain level.
 *
 * ## MUTE IS ONE SWITCH
 *
 * [setMuted] moves both players together (founder, 2026-09-30: the choice
 * "drives both players"). A Media3 player has no mute beside its volume, so
 * muted is volume 0 on each — [appliedVolume]. It starts muted, and stays so
 * until the screen has read the viewer's stored choice.
 *
 * ## FAILURE IS SILENT
 *
 * A sound that cannot be loaded — a 404 because its source went private, a
 * network error, ten seconds without an answer — is no sound: nothing is
 * shown, and the reel plays alone at the viewer's full level. One that fails
 * AFTER it had loaded has most likely outlived its five-minute link, and is
 * asked for once more ([onSoundError]).
 *
 * ## LIFETIME
 *
 * Not thread-safe by design, like the pool: every method is called on the
 * main thread, because the players require it. The player is made on the
 * first [attach] and let go by [release], after which a later [attach] makes
 * a new one — the screen releases it when it leaves composition and pauses it
 * on stop, exactly as it does the pool.
 */
@OptIn(UnstableApi::class)
class ReelSoundPlayer internal constructor(
    private val playerFactory: PlayerFactory,
    private val sourceFor: (url: String) -> MediaSource,
    private val serveUrl: (soundId: String) -> String?,
    /** Monotonic milliseconds, for the load timeout and the reload rule. */
    private val clock: () -> Long,
) {

    @Inject
    constructor(playerFactory: PlayerFactory, sources: MediaSources, config: ApiConfig) : this(
        playerFactory = playerFactory,
        // Progressive, through the ONE authenticated, cached chain: `/serve`
        // is a whole file behind a redirect, not a playlist.
        sourceFor = { url -> sources.create(Playback.original(url)) },
        serveUrl = { id -> soundServeUrl(config.baseUrl, id) },
        clock = SystemClock::elapsedRealtime,
    )

    private var player: ExoPlayer? = null
    private var video: Player? = null
    private var track: SoundTrack? = null
    private var mix = SoundMix()
    private var muted = true

    private var loadStartedAt = 0L
    private var loadedAt: Long? = null
    private var seekIssued = false

    /** Where the attached sound stands; [SoundLoad.NONE] while nothing is attached. */
    var load: SoundLoad = SoundLoad.NONE
        private set

    private val videoListener = object : Player.Listener {
        override fun onIsPlayingChanged(isPlaying: Boolean) = sync(SyncReason.JUMP)

        override fun onPlayWhenReadyChanged(playWhenReady: Boolean, reason: Int) = sync(SyncReason.JUMP)

        override fun onPlaybackStateChanged(playbackState: Int) = sync(SyncReason.JUMP)

        // A seek, and the loop: REPEAT_MODE_ONE reports the wrap as a discontinuity.
        override fun onPositionDiscontinuity(
            oldPosition: Player.PositionInfo,
            newPosition: Player.PositionInfo,
            reason: Int,
        ) = sync(SyncReason.JUMP)

        override fun onPlaybackParametersChanged(playbackParameters: PlaybackParameters) = sync(SyncReason.JUMP)

        // A video that failed is not advancing: the sound stops with it.
        override fun onPlayerError(error: PlaybackException) = sync(SyncReason.JUMP)
    }

    private val soundListener = object : Player.Listener {
        override fun onPlaybackStateChanged(playbackState: Int) {
            // Only the LOAD is an event here. The player is also READY again
            // after every seek this class asked for, and answering that with
            // another jump would chase its own seeks.
            if (track == null || playbackState != Player.STATE_READY || load != SoundLoad.LOADING) return
            load = SoundLoad.READY
            loadedAt = clock()
            sync(SyncReason.JUMP)
        }

        override fun onPlayerError(error: PlaybackException) {
            if (track == null) return
            when (onSoundError(load, loadedAt?.let { clock() - it })) {
                SoundErrorAction.RELOAD -> startLoading()
                SoundErrorAction.FAIL -> fail()
            }
            sync(SyncReason.JUMP)
        }
    }

    /**
     * Plays [track] beside [video] at the creator's [mix]. Called when the
     * pager settles, BEFORE the video is told to play, so the first frame
     * already has the mix. Attaching the same sound to the same player again
     * only takes the new mix; anything else lets the old sound go first.
     */
    fun attach(video: Player, track: SoundTrack, mix: SoundMix) {
        if (this.video === video && this.track == track) {
            this.mix = mix
            sync(SyncReason.JUMP)
            return
        }
        detach()
        this.video = video
        this.track = track
        this.mix = mix
        video.addListener(videoListener)
        startLoading()
        sync(SyncReason.JUMP)
    }

    /**
     * Lets the sound go and gives the video back at the plain level: the
     * viewer's, with the mute switch. Safe to call with nothing attached.
     */
    fun detach() {
        val released = video
        released?.removeListener(videoListener)
        player?.let { sound ->
            sound.playWhenReady = false
            sound.stop()
            sound.clearMediaItems()
        }
        video = null
        track = null
        mix = SoundMix()
        load = SoundLoad.NONE
        loadedAt = null
        seekIssued = false
        released?.let { it.volume = appliedVolume(plan(it, SyncReason.JUMP).videoVolume, muted) }
    }

    /** The viewer's mute switch: both players, together. */
    fun setMuted(muted: Boolean) {
        if (this.muted == muted) return
        this.muted = muted
        sync(SyncReason.JUMP)
    }

    /** The running clock: called from the reel page's 250 ms progress poll, for drift. */
    fun tick() = sync(SyncReason.TICK)

    /** The surface left the foreground. The next video event starts the sound again. */
    fun pause() {
        player?.playWhenReady = false
    }

    /** Lets the player go. Call BEFORE the pool is released: [detach] still touches the video. */
    fun release() {
        detach()
        player?.let { sound ->
            sound.removeListener(soundListener)
            sound.release()
        }
        player = null
    }

    // ── Loading ─────────────────────────────────────────────────────────

    private fun startLoading() {
        val url = track?.let { serveUrl(it.id) }
        if (url == null) {
            load = SoundLoad.FAILED
            return
        }
        val sound = player ?: playerFactory.create().also {
            it.addListener(soundListener)
            player = it
        }
        load = SoundLoad.LOADING
        loadStartedAt = clock()
        loadedAt = null
        seekIssued = false
        sound.playWhenReady = false
        sound.setMediaSource(sourceFor(url))
        sound.prepare()
    }

    private fun fail() {
        load = SoundLoad.FAILED
        player?.let { sound ->
            sound.playWhenReady = false
            sound.stop()
        }
    }

    // ── Sync ────────────────────────────────────────────────────────────

    private fun sync(reason: SyncReason) {
        val video = video ?: return
        if (loadTimedOut(load, clock() - loadStartedAt)) fail()
        obey(video, plan(video, reason))
    }

    /** The two players, read into the planner's input, and its answer. */
    private fun plan(video: Player, reason: SyncReason): SoundSyncPlan {
        val sound = player
        val buffering = sound?.playbackState == Player.STATE_BUFFERING
        if (!buffering) seekIssued = false
        return planSoundSync(
            SoundSyncInput(
                videoTime = video.currentPosition.toSeconds(),
                videoPlaying = video.isPlaying,
                rate = video.playbackParameters.speed.toDouble(),
                // The device's own volume is the viewer's level on Android;
                // the app has no second one, so it is always full here.
                viewerVolume = VIEWER_VOLUME,
                muted = muted,
                originalVolume = mix.originalVolume,
                overlayVolume = mix.overlayVolume,
                startOffsetS = (track?.startMs ?: 0L).toSeconds(),
                soundDurationS = soundDurationS(sound?.knownDurationS(), track?.durationMs),
                load = load,
                soundTime = (sound?.currentPosition ?: 0L).toSeconds(),
                soundSeeking = soundSeeking(seekIssued, buffering),
                reason = reason,
            ),
        )
    }

    /** Copies the plan onto the players, touching only what differs. */
    private fun obey(video: Player, plan: SoundSyncPlan) {
        val videoVolume = appliedVolume(plan.videoVolume, muted)
        if (video.volume != videoVolume) video.volume = videoVolume

        val sound = player ?: return
        val command = plan.sound
        if (command == null) {
            if (sound.playWhenReady) sound.playWhenReady = false
            return
        }
        val volume = appliedVolume(command.volume, command.muted)
        if (sound.volume != volume) sound.volume = volume
        val rate = command.rate.toFloat()
        if (sound.playbackParameters.speed != rate) sound.setPlaybackSpeed(rate)
        command.seekTo?.let { target ->
            seekIssued = true
            sound.seekTo((target * MILLIS_PER_SECOND).roundToLong())
        }
        if (sound.playWhenReady != command.play) sound.playWhenReady = command.play
    }

    private fun Player.knownDurationS(): Double? = duration.takeIf { it != C.TIME_UNSET && it > 0L }?.toSeconds()

    private fun Long.toSeconds(): Double = this / MILLIS_PER_SECOND

    private companion object {
        const val MILLIS_PER_SECOND = 1_000.0
        const val VIEWER_VOLUME = 1.0
    }
}
