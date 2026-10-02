package com.us.android.feature.dating.clips

import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow

/*
 * Playing a card's clip (mechanic M15).
 *
 * Nothing plays by itself: a clip loads on the first tap of Play, never
 * before. A voice answer then plays with sound — the viewer pressed Play on a
 * voice. A video starts MUTED and its sound comes on only with the viewer's
 * own tap, so a clip never plays sound nobody asked for. One clip plays at a
 * time on a screen; it pauses when the screen goes to the background, and its
 * player is released the moment the card leaves.
 */

/** What a clip's control shows. */
enum class ClipPlayerPhase {
    /** Nothing loaded yet: Play loads it. */
    IDLE,

    /** Asked to play, not playable yet. */
    LOADING,
    PLAYING,
    PAUSED,

    /** It could not be loaded or played; Play tries again. */
    FAILED,
}

data class ClipPlayerUi(
    val phase: ClipPlayerPhase = ClipPlayerPhase.IDLE,
    /** Video only: true until the viewer turns the sound on. A voice clip is never muted. */
    val muted: Boolean = false,
    /** The player is released: the card has gone. */
    val released: Boolean = false,
)

/** The one thing the state machine drives: a media player. A port, so it tests on the JVM. */
interface ClipEngine {
    /** Opens [url] and prepares it; [ClipPlayback.onReady] or [ClipPlayback.onError] follows. */
    fun load(url: String)

    fun setPlaying(play: Boolean)

    /** 0 is silent, 1 is full. */
    fun setVolume(volume: Float)

    /** Back to the start. */
    fun rewind()

    fun release()
}

/**
 * One card clip's player, as a state machine over [ClipEngine]. The engine is
 * made on the first Play, and every engine callback after [release] is ignored.
 */
class ClipPlayback(
    val kind: ClipKind,
    private val url: String,
    private val engineFactory: (ClipPlayback) -> ClipEngine,
) {
    private enum class Load { NONE, LOADING, READY, FAILED }

    private var engine: ClipEngine? = null
    private var load = Load.NONE
    private var wantsPlay = false
    private var muted = kind == ClipKind.VIDEO
    private var released = false

    private val _state = MutableStateFlow(ClipPlayerUi(muted = muted))
    val state: StateFlow<ClipPlayerUi> = _state.asStateFlow()

    /** The engine, once Play made one; null before and after. */
    val currentEngine: ClipEngine? get() = engine

    /** Play ↔ pause. From idle or failed it loads afresh and plays. */
    fun toggle() {
        if (released) return
        when (load) {
            Load.NONE, Load.FAILED -> {
                wantsPlay = true
                startLoading()
            }
            Load.LOADING, Load.READY -> wantsPlay = !wantsPlay
        }
        obey()
    }

    /** Video only: sound on ↔ off. A voice clip has nothing to mute. */
    fun toggleMute() {
        if (released || kind != ClipKind.VIDEO) return
        muted = !muted
        obey()
    }

    /** Another clip started, the screen went to the background, or the viewer moved on. */
    fun pause() {
        if (released || !wantsPlay) return
        wantsPlay = false
        obey()
    }

    /** The card left: the player goes, for good. */
    fun release() {
        if (released) return
        released = true
        wantsPlay = false
        engine?.let {
            it.setPlaying(false)
            it.release()
        }
        engine = null
        load = Load.NONE
        _state.value = ClipPlayerUi(phase = ClipPlayerPhase.IDLE, muted = muted, released = true)
    }

    // ── engine callbacks ─────────────────────────────────────────────────────

    fun onReady() {
        if (released || load != Load.LOADING) return
        load = Load.READY
        obey()
    }

    /** Played through: back to the start, paused, waiting for the next tap. */
    fun onEnded() {
        if (released) return
        wantsPlay = false
        engine?.rewind()
        obey()
    }

    fun onError() {
        if (released) return
        load = Load.FAILED
        wantsPlay = false
        obey()
    }

    private fun startLoading() {
        val player = engine ?: engineFactory(this).also { engine = it }
        load = Load.LOADING
        player.load(url)
    }

    private fun obey() {
        if (released) return
        engine?.let {
            it.setVolume(if (muted) 0f else 1f)
            it.setPlaying(wantsPlay && (load == Load.LOADING || load == Load.READY))
        }
        _state.value = ClipPlayerUi(phase = phase(), muted = muted)
    }

    /** What the control says a tap does. A clip still loading that was paused reads paused. */
    private fun phase(): ClipPlayerPhase = when (load) {
        Load.NONE -> ClipPlayerPhase.IDLE
        Load.FAILED -> ClipPlayerPhase.FAILED
        Load.LOADING -> if (wantsPlay) ClipPlayerPhase.LOADING else ClipPlayerPhase.PAUSED
        Load.READY -> if (wantsPlay) ClipPlayerPhase.PLAYING else ClipPlayerPhase.PAUSED
    }
}

/**
 * Which clip on a screen may play: starting one pauses the one before. Held by
 * the screen; pure.
 */
class ClipFocus {
    private val playing = mutableMapOf<String, ClipPlayback>()

    /** [playback] (keyed by [key]) is about to play: every other clip pauses. */
    fun starting(key: String, playback: ClipPlayback) {
        playing.filterKeys { it != key }.values.forEach { it.pause() }
        playing.clear()
        playing[key] = playback
    }

    /** [key]'s card left. */
    fun left(key: String) {
        playing.remove(key)
    }
}
