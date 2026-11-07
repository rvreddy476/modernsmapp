@file:OptIn(UnstableApi::class)

package com.us.android.feature.tube.ui.watch

import androidx.annotation.OptIn
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.exoplayer.ExoPlaybackException
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.source.MediaSource
import java.lang.reflect.InvocationHandler
import java.lang.reflect.Method
import java.lang.reflect.Proxy

/**
 * A scripted long-video player: the members [WatchPlayback] and the HLS
 * refresher read and write, as plain fields, and a record of every write.
 * A proxy over `ExoPlayer`, so a member nobody scripted fails the test that
 * reached it rather than answering quietly.
 */
internal class FakeWatchPlayer {

    var playWhenReady = false
    var playbackState = Player.STATE_IDLE
    var positionMs = 0L
    var playerError: ExoPlaybackException? = null
    var released = false

    /** Every write, in order: `setMediaSource@42000`, `prepare`, `playWhenReady=true`… */
    val calls = mutableListOf<String>()
    val listeners = mutableListOf<Player.Listener>()

    val player: ExoPlayer = Proxy.newProxyInstance(
        ExoPlayer::class.java.classLoader,
        arrayOf(ExoPlayer::class.java),
        Handler(),
    ) as ExoPlayer

    fun state(state: Int) {
        playbackState = state
        listeners.toList().forEach { it.onPlaybackStateChanged(state) }
    }

    /**
     * The error a real player reports: idle with the error held, then the
     * events. As on a real player, a listener that prepares again while the
     * events go out leaves the player in the state it asked for; the events
     * still in the queue do not undo it.
     */
    fun fail(error: ExoPlaybackException) {
        playerError = error
        playbackState = Player.STATE_IDLE
        listeners.toList().forEach { it.onPlayerErrorChanged(error) }
        listeners.toList().forEach { it.onPlayerError(error) }
        listeners.toList().forEach { it.onPlaybackStateChanged(Player.STATE_IDLE) }
    }

    private fun prepare() {
        if (playerError != null) {
            playerError = null
            listeners.toList().forEach { it.onPlayerErrorChanged(null) }
        }
        state(Player.STATE_BUFFERING)
    }

    private inner class Handler : InvocationHandler {
        @Suppress("CyclomaticComplexMethod") // One line per scripted member: the list IS the fake.
        override fun invoke(proxy: Any, method: Method, args: Array<out Any?>?): Any? = when (method.name) {
            "getPlayWhenReady" -> playWhenReady
            "setPlayWhenReady" -> write("playWhenReady=${args!![0]}") { playWhenReady = args[0] as Boolean }
            "getPlaybackState" -> playbackState
            "getPlayerError" -> playerError
            "getCurrentPosition" -> positionMs
            "addListener" -> write("addListener") { listeners += args!![0] as Player.Listener }
            "removeListener" -> write("removeListener") { listeners -= args!![0] as Player.Listener }
            // The HLS refresher reads load headers; nothing here loads.
            "addAnalyticsListener", "removeAnalyticsListener" -> null
            "setMediaSource" -> write(if (args!!.size > 1) "setMediaSource@${args[1]}" else "setMediaSource") {
                (args.getOrNull(1) as? Long)?.let { positionMs = it }
            }
            "prepare" -> write("prepare") { prepare() }
            "stop" -> write("stop") { state(Player.STATE_IDLE) }
            "clearMediaItems" -> write("clearMediaItems") {}
            "seekTo" -> write("seekTo=${args!!.last()}") { positionMs = args.last() as Long }
            "release" -> write("release") { released = true }
            "hashCode" -> System.identityHashCode(proxy)
            "equals" -> proxy === args!![0]
            "toString" -> "FakeWatchPlayer"
            else -> error("FakeWatchPlayer does not script ${method.name}")
        }

        private fun write(call: String, change: () -> Unit): Any? {
            calls += call
            change()
            return null
        }
    }
}

/** A media source that is only ever handed to a fake player. */
internal fun fakeSource(): MediaSource = Proxy.newProxyInstance(
    MediaSource::class.java.classLoader,
    arrayOf(MediaSource::class.java),
) { proxy, method, args ->
    when (method.name) {
        "hashCode" -> System.identityHashCode(proxy)
        "equals" -> proxy === args!![0]
        "toString" -> "FakeSource"
        else -> error("FakeSource does not script ${method.name}")
    }
} as MediaSource
