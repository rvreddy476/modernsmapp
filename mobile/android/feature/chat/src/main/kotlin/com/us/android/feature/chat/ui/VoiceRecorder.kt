package com.us.android.feature.chat.ui

import android.Manifest
import android.content.Context
import android.content.pm.PackageManager
import android.media.MediaRecorder
import android.os.Build
import androidx.activity.compose.rememberLauncherForActivityResult
import androidx.activity.result.contract.ActivityResultContracts
import androidx.compose.foundation.background
import androidx.compose.foundation.gestures.awaitEachGesture
import androidx.compose.foundation.gestures.awaitFirstDown
import androidx.compose.foundation.gestures.waitForUpOrCancellation
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.runtime.Composable
import androidx.compose.runtime.DisposableEffect
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableLongStateOf
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.input.pointer.pointerInput
import androidx.compose.ui.platform.LocalContext
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.unit.dp
import androidx.core.content.ContextCompat
import com.us.android.core.chat.data.MAX_VOICE_MILLIS
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import kotlinx.coroutines.delay
import kotlinx.coroutines.isActive
import java.io.File

/**
 * One voice recording (CONTRACT §2.1): AAC in an MP4 container written to
 * the app's cache, with the recorder's peak amplitude sampled every
 * [SAMPLE_MILLIS] so the waveform can be drawn before the upload.
 *
 * Not thread-safe: start/stop/sample run on the main thread from the
 * composer's gesture.
 */
internal class VoiceRecorder(private val context: Context) {
    private var recorder: MediaRecorder? = null
    private var file: File? = null
    private var startedAt = 0L
    private val samples = mutableListOf<Int>()

    val recording: Boolean get() = recorder != null

    fun start(): Boolean {
        if (recorder != null) return true
        val out = File(context.cacheDir, "voice-${System.currentTimeMillis()}.m4a")
        val next = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) MediaRecorder(context) else legacyRecorder()
        return runCatching {
            next.setAudioSource(MediaRecorder.AudioSource.MIC)
            next.setOutputFormat(MediaRecorder.OutputFormat.MPEG_4)
            next.setAudioEncoder(MediaRecorder.AudioEncoder.AAC)
            next.setAudioEncodingBitRate(BIT_RATE)
            next.setAudioSamplingRate(SAMPLE_RATE)
            next.setMaxDuration(MAX_VOICE_MILLIS.toInt())
            next.setOutputFile(out.absolutePath)
            next.prepare()
            next.start()
            recorder = next
            file = out
            startedAt = System.currentTimeMillis()
            samples.clear()
            true
        }.getOrElse {
            runCatching { next.release() }
            out.delete()
            false
        }
    }

    @Suppress("DEPRECATION") // The context-free constructor is the only one before API 31.
    private fun legacyRecorder() = MediaRecorder()

    /** One amplitude sample (0..32767) since the last call. */
    fun sample() {
        val current = recorder ?: return
        samples += runCatching { current.maxAmplitude }.getOrDefault(0)
    }

    fun elapsedMillis(): Long = if (recorder == null) 0 else System.currentTimeMillis() - startedAt

    /** Stops and hands back the file, its length and the samples; null when nothing usable was recorded. */
    fun stop(): VoiceTake? {
        val current = recorder ?: return null
        val duration = elapsedMillis()
        val stopped = runCatching { current.stop() }.isSuccess
        runCatching { current.release() }
        recorder = null
        val out = file
        file = null
        if (!stopped || out == null || !out.exists()) {
            out?.delete()
            return null
        }
        return VoiceTake(out, duration.coerceAtMost(MAX_VOICE_MILLIS), samples.toList())
    }

    fun cancel() {
        stop()?.file?.delete()
    }

    companion object {
        const val SAMPLE_MILLIS = 100L
        private const val BIT_RATE = 64_000
        private const val SAMPLE_RATE = 44_100
    }
}

internal data class VoiceTake(val file: File, val durationMs: Long, val amplitudes: List<Int>)

/**
 * Hold-to-record (CONTRACT §9): press and hold the mic to record, release to
 * send. A release before the minimum length is discarded by the ViewModel.
 * Asks for the microphone the first time; without it nothing records.
 */
@Composable
internal fun HoldToRecordButton(
    enabled: Boolean,
    onRecordingChange: (recording: Boolean, elapsedMillis: Long) -> Unit,
    onRecorded: (VoiceTake) -> Unit,
) {
    val context = LocalContext.current
    val recorder = remember { VoiceRecorder(context.applicationContext) }
    var recording by remember { mutableStateOf(false) }
    var elapsed by remember { mutableLongStateOf(0L) }
    val permission = rememberLauncherForActivityResult(ActivityResultContracts.RequestPermission()) { }
    DisposableEffect(Unit) { onDispose { recorder.cancel() } }
    LaunchedEffect(recording) {
        while (recording && isActive) {
            delay(VoiceRecorder.SAMPLE_MILLIS)
            recorder.sample()
            elapsed = recorder.elapsedMillis()
            onRecordingChange(true, elapsed)
            if (elapsed >= MAX_VOICE_MILLIS) {
                recording = false
                recorder.stop()?.let(onRecorded)
                onRecordingChange(false, 0)
            }
        }
    }
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(BUTTON)
            .clip(CircleShape)
            .background(if (recording) UsTheme.extended.statusDanger else UsTheme.extended.chatAccent)
            .semantics { contentDescription = if (recording) "Recording. Release to send." else "Hold to record a voice message" }
            .testTag("thread-voice")
            .pointerInput(enabled) {
                if (!enabled) return@pointerInput
                awaitEachGesture {
                    awaitFirstDown()
                    val granted = ContextCompat.checkSelfPermission(context, Manifest.permission.RECORD_AUDIO) ==
                        PackageManager.PERMISSION_GRANTED
                    if (!granted) {
                        permission.launch(Manifest.permission.RECORD_AUDIO)
                        waitForUpOrCancellation()
                        return@awaitEachGesture
                    }
                    recording = recorder.start()
                    if (recording) onRecordingChange(true, 0)
                    val up = waitForUpOrCancellation()
                    if (recording) {
                        recording = false
                        val take = recorder.stop()
                        onRecordingChange(false, 0)
                        if (up != null && take != null) onRecorded(take) else take?.file?.delete()
                    }
                }
            },
    ) {
        Icon(
            imageVector = if (recording) UsIcons.Square else UsIcons.Mic,
            contentDescription = null,
            tint = if (recording) UsTheme.extended.onStatus else UsTheme.extended.onChatAccent,
            modifier = Modifier.size(GLYPH),
        )
    }
}

private val BUTTON = 40.dp
private val GLYPH = 18.dp
