@file:OptIn(UnstableApi::class)

package com.us.android.core.media.sound

import androidx.annotation.OptIn
import androidx.media3.common.C
import androidx.media3.common.PlaybackException
import androidx.media3.common.PlaybackParameters
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.MediaSource
import com.us.android.core.media.PlayerFactory
import java.lang.reflect.InvocationHandler
import java.lang.reflect.Method
import java.lang.reflect.Proxy

/**
 * A scripted player: the dozen members the sound players read and write, as
 * plain fields, and a record of everything that was asked of it.
 *
 * `ExoPlayer` is an interface of several hundred members, so the fake is a
 * proxy over it rather than an implementation: a member these tests do not
 * script FAILS the test that reached it, which is the point — a player that
 * quietly answered everything would hide a call the wiring was never meant
 * to make.
 */
internal class FakeExoPlayer(val name: String = "player") {

    var volume = 0f
    var playWhenReady = false
    var playbackState = Player.STATE_IDLE
    var positionMs = 0L
    var durationMs = C.TIME_UNSET
    var speed = 1f
    var repeatMode = Player.REPEAT_MODE_ONE
    var playing = false
    var released = false

    /** Every write, in order: `volume=0.2`, `seekTo=6500`, `prepare`… */
    val calls = mutableListOf<String>()
    val listeners = mutableListOf<Player.Listener>()
    val sources = mutableListOf<MediaSource>()

    val player: ExoPlayer = Proxy.newProxyInstance(
        ExoPlayer::class.java.classLoader,
        arrayOf(ExoPlayer::class.java),
        Handler(),
    ) as ExoPlayer

    fun calls(prefix: String): List<String> = calls.filter { it.startsWith(prefix) }

    // ── The script: what a real player would report ─────────────────────

    /** The media is loaded: the state a prepare ends in. */
    fun becomeReady(durationMs: Long = this.durationMs) {
        this.durationMs = durationMs
        state(Player.STATE_READY)
    }

    fun state(state: Int) {
        playbackState = state
        listeners.toList().forEach { it.onPlaybackStateChanged(state) }
    }

    /** The player is, or is no longer, really advancing. */
    fun playing(isPlaying: Boolean) {
        playing = isPlaying
        listeners.toList().forEach { it.onIsPlayingChanged(isPlaying) }
    }

    /** A seek or a loop put the playhead at [positionMs]. */
    fun jumpTo(positionMs: Long, reason: Int = Player.DISCONTINUITY_REASON_SEEK) {
        val old = position(this.positionMs)
        this.positionMs = positionMs
        val new = position(positionMs)
        listeners.toList().forEach { it.onPositionDiscontinuity(old, new, reason) }
    }

    fun rate(speed: Float) {
        this.speed = speed
        listeners.toList().forEach { it.onPlaybackParametersChanged(PlaybackParameters(speed)) }
    }

    fun fail() {
        val error = PlaybackException("scripted", null, PlaybackException.ERROR_CODE_IO_BAD_HTTP_STATUS)
        playbackState = Player.STATE_IDLE
        listeners.toList().forEach { it.onPlayerError(error) }
    }

    private fun position(ms: Long) = Player.PositionInfo(null, 0, null, null, 0, ms, ms, C.INDEX_UNSET, C.INDEX_UNSET)

    // ── The proxy ───────────────────────────────────────────────────────

    private inner class Handler : InvocationHandler {
        @Suppress("CyclomaticComplexMethod") // One line per scripted member: the list IS the fake.
        override fun invoke(proxy: Any, method: Method, args: Array<out Any?>?): Any? = when (method.name) {
            "getVolume" -> volume
            "setVolume" -> write("volume=${args!![0]}") { volume = args[0] as Float }
            "getPlayWhenReady" -> playWhenReady
            "setPlayWhenReady" -> write("playWhenReady=${args!![0]}") { playWhenReady = args[0] as Boolean }
            "getPlaybackState" -> playbackState
            "getCurrentPosition" -> positionMs
            "getDuration" -> durationMs
            "isPlaying" -> playing
            "getPlaybackParameters" -> PlaybackParameters(speed)
            "setPlaybackSpeed" -> write("speed=${args!![0]}") { speed = args[0] as Float }
            "getRepeatMode" -> repeatMode
            "setRepeatMode" -> write("repeatMode=${args!![0]}") { repeatMode = args[0] as Int }
            "addListener" -> write("addListener") { listeners += args!![0] as Player.Listener }
            "removeListener" -> write("removeListener") { listeners -= args!![0] as Player.Listener }
            "setMediaSource" -> write("setMediaSource") { sources += args!![0] as MediaSource }
            "prepare" -> write("prepare") { playbackState = Player.STATE_BUFFERING }
            "stop" -> write("stop") { playbackState = Player.STATE_IDLE }
            "clearMediaItems" -> write("clearMediaItems") {}
            "release" -> write("release") { released = true }
            "seekTo" -> write("seekTo=${args!!.last()}") {
                positionMs = args.last() as Long
                // A seek on a prepared player buffers until it has landed.
                if (playbackState == Player.STATE_READY) playbackState = Player.STATE_BUFFERING
            }
            "hashCode" -> System.identityHashCode(proxy)
            "equals" -> proxy === args!![0]
            "toString" -> "FakeExoPlayer($name)"
            else -> error("FakeExoPlayer($name) does not script ${method.name}")
        }

        private fun write(call: String, change: () -> Unit): Any? {
            calls += call
            change()
            return null
        }
    }
}

/** Hands out scripted players, in order, and remembers every one it made. */
internal class FakePlayerFactory : PlayerFactory {
    val made = mutableListOf<FakeExoPlayer>()

    override fun create(): ExoPlayer = FakeExoPlayer("sound-${made.size}").also { made += it }.player
}

/** A media source that is only ever handed to a fake player. */
internal fun fakeMediaSource(): MediaSource = Proxy.newProxyInstance(
    MediaSource::class.java.classLoader,
    arrayOf(MediaSource::class.java),
) { proxy, method, args ->
    when (method.name) {
        "hashCode" -> System.identityHashCode(proxy)
        "equals" -> proxy === args!![0]
        "toString" -> "FakeMediaSource"
        else -> error("FakeMediaSource does not script ${method.name}")
    }
} as MediaSource
