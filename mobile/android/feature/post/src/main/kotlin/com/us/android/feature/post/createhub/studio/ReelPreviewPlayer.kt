package com.us.android.feature.post.createhub.studio

import android.content.Context
import android.view.TextureView
import androidx.media3.common.MediaItem
import androidx.media3.common.Player
import androidx.media3.common.util.UnstableApi
import androidx.media3.effect.RgbMatrix
import androidx.media3.exoplayer.ExoPlayer
import androidx.media3.exoplayer.SeekParameters

/**
 * The studio's preview (2026-09-05): ONE ExoPlayer on the source, looping
 * the trimmed span, at the chosen speed, with the chosen look applied as
 * a video effect — the same `RgbMatrix` the export uses, so the colours on
 * screen are the colours in the file.
 *
 * The FRAME is not an effect here. The player draws the whole source into
 * a TextureView and the screen scales and slides that view inside a 9:16
 * box ([ReelFrame] does the sums), so a pan is a layout change at 60 fps
 * rather than a re-prepare per drag tick. The look IS an effect, and
 * changing it re-prepares: ExoPlayer applies a new effect list to the next
 * input stream, and seven taps a session is nothing.
 *
 * The effect list is set BEFORE the first prepare and never empty — the
 * renderer only builds its effect pipeline when it has effects at that
 * moment — which is why "None" is still an `RgbMatrix` (a no-op one).
 */
@UnstableApi
class ReelPreviewPlayer(context: Context) {

    val player: ExoPlayer = ExoPlayer.Builder(context).build().apply {
        repeatMode = Player.REPEAT_MODE_ONE
        setSeekParameters(SeekParameters.CLOSEST_SYNC)
    }

    private var uri: String? = null
    private var look: ReelLook? = null
    private var view: TextureView? = null

    /** The view the frames land on. Set once from the AndroidView factory. */
    fun attach(textureView: TextureView) {
        if (view === textureView) return
        view = textureView
        player.setVideoTextureView(textureView)
    }

    /** Bring the player to [edit]: re-prepares only for a new source or a new look. */
    fun apply(edit: ReelEdit, playing: Boolean) {
        val needsPrepare = uri != edit.sourceUri || look != edit.look
        if (needsPrepare) {
            val wasAt = if (uri == edit.sourceUri) player.currentPosition else edit.trimStartUs / MICROS_PER_MILLI
            uri = edit.sourceUri
            look = edit.look
            player.stop()
            player.setVideoEffects(listOf(LookMatrix(edit.look)))
            player.setMediaItem(MediaItem.fromUri(edit.sourceUri))
            player.prepare()
            player.seekTo(wasAt)
        }
        player.setPlaybackSpeed(edit.speed.factor)
        player.playWhenReady = playing
    }

    /** The trimmed span as a loop: past its end (or before its start) jumps back to the start. */
    fun keepInside(edit: ReelEdit) {
        val position = player.currentPosition * MICROS_PER_MILLI
        if (position >= edit.trimEndUs || position < edit.trimStartUs - SLACK_US) {
            player.seekTo(edit.trimStartUs / MICROS_PER_MILLI)
        }
    }

    /** A trim handle under a finger: show that instant, paused. */
    fun scrubTo(timeUs: Long) {
        player.playWhenReady = false
        player.seekTo(timeUs / MICROS_PER_MILLI)
    }

    fun release() {
        player.clearVideoTextureView(view)
        player.release()
    }

    private class LookMatrix(private val look: ReelLook) : RgbMatrix {
        private val matrix = look.glMatrix()
        override fun getMatrix(presentationTimeUs: Long, useHdr: Boolean): FloatArray = matrix
    }

    private companion object {
        const val MICROS_PER_MILLI = 1_000L

        /** A seek lands on the nearest sync frame, which may sit a little before the start handle. */
        const val SLACK_US = 1_500_000L
    }
}
