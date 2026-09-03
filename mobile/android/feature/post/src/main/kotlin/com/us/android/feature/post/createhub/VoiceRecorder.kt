package com.us.android.feature.post.createhub

import android.content.Context
import android.media.MediaRecorder
import android.os.Build
import java.io.File
import java.io.IOException

/**
 * A thin owner of one [MediaRecorder]: AAC in an MP4 container (`.m4a`),
 * mono, 64 kbps — a voice-note profile, small on the uplink and inside
 * media-service's `allowedAudioMIME` as `audio/mp4`.
 *
 * Deliberately not a ViewModel and not a singleton: a recorder holds the
 * microphone, so whoever starts it must be the one that stops it, and that is
 * [VoicePublishViewModel]'s lifecycle. Everything here is synchronous; the
 * ViewModel owns the clock and the level polling.
 */
internal class VoiceRecorder(private val context: Context) {

    private var recorder: MediaRecorder? = null

    /**
     * Starts writing to [output]. [onMaxDuration] fires on the recorder's own
     * thread when [maxDurationMillis] elapses — the platform stops the
     * recording itself at that point, so the caller only has to finalise.
     */
    @Throws(IOException::class, IllegalStateException::class)
    fun start(output: File, maxDurationMillis: Int, onMaxDuration: () -> Unit) {
        stop()
        val fresh = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
            MediaRecorder(context)
        } else {
            @Suppress("DEPRECATION")
            MediaRecorder()
        }
        fresh.setAudioSource(MediaRecorder.AudioSource.MIC)
        fresh.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
        fresh.setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
        fresh.setAudioChannels(CHANNELS_MONO)
        fresh.setAudioEncodingBitRate(BIT_RATE)
        fresh.setAudioSamplingRate(SAMPLE_RATE)
        fresh.setMaxDuration(maxDurationMillis)
        fresh.setOutputFile(output.absolutePath)
        fresh.setOnInfoListener { _, what, _ ->
            if (what == MediaRecorder.MEDIA_RECORDER_INFO_MAX_DURATION_REACHED) onMaxDuration()
        }
        fresh.prepare()
        fresh.start()
        recorder = fresh
    }

    /**
     * The peak sample since the last call, 0..32767, or 0 when idle. The
     * platform resets it on every read, which is what makes it a level meter
     * when polled on a fixed cadence.
     */
    fun peakAmplitude(): Int = recorder?.let { runCatching { it.maxAmplitude }.getOrDefault(0) } ?: 0

    /**
     * Stops and releases. Returns false when the platform refused to finalise
     * the file — a recording stopped within its first instant has no valid
     * data, and the output is not a playable file.
     */
    fun stop(): Boolean {
        val current = recorder ?: return true
        recorder = null
        val finalised = runCatching { current.stop() }.isSuccess
        runCatching { current.release() }
        return finalised
    }

    val isRecording: Boolean get() = recorder != null

    private companion object {
        const val CHANNELS_MONO = 1
        const val BIT_RATE = 64_000
        const val SAMPLE_RATE = 44_100
    }
}
