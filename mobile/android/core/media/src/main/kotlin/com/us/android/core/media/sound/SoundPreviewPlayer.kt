package com.us.android.core.media.sound

import android.os.SystemClock
import androidx.annotation.OptIn
import androidx.media3.common.PlaybackException
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.MediaSource
import com.us.android.core.media.MediaSources
import com.us.android.core.media.Playback
import com.us.android.core.media.PlayerFactory
import com.us.android.core.network.ApiConfig
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import javax.inject.Inject

/** What a preview button shows. */
enum class SoundPreview {
    /** Nothing asked for yet. */
    IDLE,

    /** Asked to play, not playable yet. */
    LOADING,
    PLAYING,
    PAUSED,

    /** It could not be loaded. */
    FAILED,
}

/**
 * What the preview button shows, from where the sound stands and whether the
 * listener asked for it to run. A sound still loading that was paused
 * meanwhile reads paused, not loading: the button must say what a tap does.
 */
fun soundPreview(load: SoundLoad, wantsPlay: Boolean): SoundPreview = when (load) {
    SoundLoad.NONE -> SoundPreview.IDLE
    SoundLoad.FAILED -> SoundPreview.FAILED
    SoundLoad.LOADING -> if (wantsPlay) SoundPreview.LOADING else SoundPreview.PAUSED
    SoundLoad.READY -> if (wantsPlay) SoundPreview.PLAYING else SoundPreview.PAUSED
}

/**
 * Plays a sound ALONE: the preview button of the sound page and of the reel
 * form's Sound section (original sounds, 2026-09-30). [ReelSoundPlayer]'s
 * sibling — the same factory, the same data-source chain, the same `/serve`
 * address and the same reload rule — without a video to follow.
 *
 * It plays once and stops at the end, from the start. It ignores the Reels
 * mute switch: the listener pressed Play on a sound, which is not a reel
 * starting by itself. Held by the screen's ViewModel, paused when the
 * surface stops and released with it.
 */
@OptIn(UnstableApi::class)
class SoundPreviewPlayer internal constructor(
    private val playerFactory: PlayerFactory,
    private val sourceFor: (url: String) -> MediaSource,
    private val serveUrl: (soundId: String) -> String?,
    private val clock: () -> Long,
) {

    @Inject
    constructor(playerFactory: PlayerFactory, sources: MediaSources, config: ApiConfig) : this(
        playerFactory = playerFactory,
        sourceFor = { url -> sources.create(Playback.original(url)) },
        serveUrl = { id -> soundServeUrl(config.baseUrl, id) },
        clock = SystemClock::elapsedRealtime,
    )

    private var player: ExoPlayer? = null
    private var soundId: String? = null
    private var load = SoundLoad.NONE
    private var wantsPlay = false
    private var level = 1.0
    private var loadedAt: Long? = null

    private val _state = MutableStateFlow(SoundPreview.IDLE)

    /** What the button shows. */
    val state: StateFlow<SoundPreview> = _state.asStateFlow()

    private val listener = object : Player.Listener {
        override fun onPlaybackStateChanged(playbackState: Int) {
            if (soundId == null) return
            when (playbackState) {
                Player.STATE_READY -> if (load == SoundLoad.LOADING) {
                    load = SoundLoad.READY
                    loadedAt = clock()
                }
                // Played through: back to the start, waiting for the next tap.
                Player.STATE_ENDED -> {
                    wantsPlay = false
                    player?.seekTo(0L)
                }
                else -> Unit
            }
            obey()
        }

        override fun onPlayerError(error: PlaybackException) {
            if (soundId == null) return
            when (onSoundError(load, loadedAt?.let { clock() - it })) {
                SoundErrorAction.RELOAD -> startLoading()
                SoundErrorAction.FAIL -> {
                    load = SoundLoad.FAILED
                    wantsPlay = false
                }
            }
            obey()
        }
    }

    /** Play ↔ pause for [soundId]; a different sound, or one that failed, is loaded afresh and played. */
    fun toggle(soundId: String) {
        val same = this.soundId == soundId && (load == SoundLoad.LOADING || load == SoundLoad.READY)
        if (same) {
            wantsPlay = !wantsPlay
        } else {
            this.soundId = soundId
            wantsPlay = true
            startLoading()
        }
        obey()
    }

    /** The "Sound" slider: how loud the preview is, 0..1. */
    fun setVolume(level: Double) {
        this.level = clamp01(level)
        obey()
    }

    /** The surface left the foreground, or the listener moved on. */
    fun pause() {
        if (!wantsPlay) return
        wantsPlay = false
        obey()
    }

    /** Forgets the sound: the button is back to idle. */
    fun stop() {
        soundId = null
        load = SoundLoad.NONE
        wantsPlay = false
        loadedAt = null
        player?.let { sound ->
            sound.playWhenReady = false
            sound.stop()
            sound.clearMediaItems()
        }
        obey()
    }

    fun release() {
        stop()
        player?.let { sound ->
            sound.removeListener(listener)
            sound.release()
        }
        player = null
    }

    private fun startLoading() {
        val url = soundId?.let(serveUrl)
        if (url == null) {
            load = SoundLoad.FAILED
            wantsPlay = false
            return
        }
        val sound = player ?: playerFactory.create().also {
            // A preview plays once; the factory's players loop, as a reel does.
            it.repeatMode = Player.REPEAT_MODE_OFF
            it.addListener(listener)
            player = it
        }
        load = SoundLoad.LOADING
        loadedAt = null
        sound.setMediaSource(sourceFor(url))
        sound.prepare()
    }

    private fun obey() {
        player?.let { sound ->
            val volume = appliedVolume(level, muted = false)
            if (sound.volume != volume) sound.volume = volume
            val play = wantsPlay && (load == SoundLoad.LOADING || load == SoundLoad.READY)
            if (sound.playWhenReady != play) sound.playWhenReady = play
        }
        _state.value = soundPreview(load, wantsPlay)
    }
}
