package com.us.android.feature.post.createhub

import android.content.Context
import android.media.MediaPlayer
import android.net.Uri
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_AUDIO
import com.us.android.core.media.upload.MAX_VOICE_DURATION_SECONDS
import com.us.android.core.media.upload.MAX_VOICE_UPLOAD_BYTES
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.media.upload.isSupportedAudioUpload
import com.us.android.feature.post.data.ComposerRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import java.io.File
import java.io.FileInputStream
import java.util.UUID
import javax.inject.Inject

/**
 * The Create sheet's Audio surface: record a voice note or pick a track,
 * preview it, caption it, post it as a `voice` post.
 *
 * ## THE PIPELINE IS THE REEL'S
 *
 * Reserve (`file_type: audio`) → presigned PUT → confirm → poll → create,
 * through the ONE uploader and the ONE create call site. What differs is the
 * readiness rule: media-service marks a voice asset `ready` as soon as its
 * duration passes (`media.go:397-404`) and then holds `moderation_status` at
 * `pending` until a transcript scan lands (`voice.go:31-43`). post-service
 * accepts the post in that state and keeps it out of public surfaces itself
 * (`post.go:1031`), so this client waits for `ready` and treats only
 * `rejected` as the end of the road — waiting for `passed` would wait forever.
 *
 * ## WHAT THE SERVER ACTUALLY ACCEPTS TODAY
 *
 * The service layer takes audio; the HTTP binding in front of `init` still
 * says `oneof=image video` (`handler.go:129`). Until that changes the reserve
 * step is refused with a 400, and this ViewModel shows the server's own words
 * for it rather than a success it did not get.
 *
 * ## LIMITS
 *
 * Three minutes and 25 MB — media-service's `MaxVoiceDurationSec` and
 * `MaxVoiceSizeBytes` (`validation.go:19-23`). The recorder stops itself at
 * the cap; a longer file would only be rejected at confirm, after the bytes
 * had already been paid for.
 */
@HiltViewModel
class VoicePublishViewModel @Inject constructor(
    @ApplicationContext private val appContext: Context,
    private val uploader: MediaUploader,
    private val repository: ComposerRepository,
) : ViewModel() {

    /** Where the audio came from. Recorded files are ours to delete; picked ones are not. */
    sealed interface VoiceSource {
        val mimeType: String

        data class Recorded(val path: String, val durationMillis: Long) : VoiceSource {
            override val mimeType: String get() = RECORDED_MIME
        }

        data class Picked(val uri: String, override val mimeType: String, val displayName: String) : VoiceSource
    }

    sealed interface Phase {
        data object Editing : Phase
        data class Uploading(val fraction: Float) : Phase
        data object Processing : Phase
        data object Posting : Phase
        data class Published(val postId: String) : Phase
        data class Failure(val message: String, val retryable: Boolean) : Phase
    }

    data class VoiceUiState(
        val source: VoiceSource? = null,
        val recording: Boolean = false,
        /** Elapsed recording time, ticking while [recording]. */
        val elapsedMillis: Long = 0L,
        /** 0..1 from the recorder's peak amplitude, ~10 Hz. */
        val level: Float = 0f,
        val playing: Boolean = false,
        val caption: String = "",
        val phase: Phase = Phase.Editing,
        /** The user declined the microphone; the surface says how to fix it. */
        val micDenied: Boolean = false,
    ) {
        val canPost: Boolean
            get() = source != null && !recording &&
                (phase is Phase.Editing || phase is Phase.Failure)

        val isBusy: Boolean
            get() = phase is Phase.Uploading || phase is Phase.Processing || phase is Phase.Posting
    }

    private val _state = MutableStateFlow(VoiceUiState())
    val state: StateFlow<VoiceUiState> = _state.asStateFlow()

    private val recorder = VoiceRecorder(appContext)
    private var player: MediaPlayer? = null
    private var meterJob: Job? = null
    private var recordingFile: File? = null

    private var creationKey: String = UUID.randomUUID().toString()

    /** A confirmed+ready media id survives a create retry — no re-upload. */
    private var readyMediaId: String? = null

    // ── Recording ───────────────────────────────────────────────────────

    fun onStartRecording() {
        if (_state.value.recording || _state.value.isBusy) return
        stopPlayback()
        val dir = File(appContext.cacheDir, RECORDING_DIR).apply { mkdirs() }
        val file = File(dir, "voice_${System.currentTimeMillis()}.m4a")
        val started = runCatching {
            recorder.start(file, MAX_DURATION_MILLIS) { onMaxDurationReached() }
        }
        if (started.isFailure) {
            fail("The microphone couldn't be started. Try again.", retryable = true)
            return
        }
        recordingFile = file
        val startedAt = System.currentTimeMillis()
        _state.update {
            it.copy(recording = true, elapsedMillis = 0L, level = 0f, phase = Phase.Editing, micDenied = false)
        }
        meterJob = viewModelScope.launch {
            while (isActive) {
                delay(METER_PERIOD_MILLIS)
                val peak = recorder.peakAmplitude().toFloat() / MAX_AMPLITUDE
                _state.update {
                    it.copy(
                        elapsedMillis = System.currentTimeMillis() - startedAt,
                        level = peak.coerceIn(0f, 1f),
                    )
                }
            }
        }
    }

    fun onStopRecording() {
        if (!_state.value.recording) return
        meterJob?.cancel()
        meterJob = null
        val elapsed = _state.value.elapsedMillis
        val finalised = recorder.stop()
        val file = recordingFile
        recordingFile = null
        val empty = file == null || file.length() <= 0L
        val tooShort = elapsed < MIN_DURATION_MILLIS
        if (!finalised || empty || tooShort) {
            file?.delete()
            _state.update { it.copy(recording = false, level = 0f) }
            fail("That was too short to keep. Hold Record a little longer.", retryable = false)
            return
        }
        // A new recording is a new post: fresh key, nothing reusable.
        creationKey = UUID.randomUUID().toString()
        readyMediaId = null
        _state.update {
            it.copy(
                recording = false,
                level = 0f,
                source = VoiceSource.Recorded(file.absolutePath, elapsed),
                phase = Phase.Editing,
            )
        }
    }

    /** The platform hit the cap on its own thread; finalise on ours. */
    private fun onMaxDurationReached() {
        viewModelScope.launch { onStopRecording() }
    }

    fun onMicDenied() = _state.update { it.copy(micDenied = true) }

    // ── Picking ─────────────────────────────────────────────────────────

    fun onAudioPicked(uri: Uri) {
        stopPlayback()
        val resolver = appContext.contentResolver
        val mime = resolver.getType(uri) ?: DEFAULT_PICKED_MIME
        if (!isSupportedAudioUpload(mime)) {
            fail("That audio format isn't supported ($mime).", retryable = false)
            return
        }
        val size = querySize(uri)
        if (size <= 0L) {
            fail("That file couldn't be read.", retryable = false)
            return
        }
        if (size > MAX_VOICE_UPLOAD_BYTES) {
            fail("Audio up to ${MAX_VOICE_UPLOAD_BYTES / BYTES_PER_MIB} MB can be posted.", retryable = false)
            return
        }
        creationKey = UUID.randomUUID().toString()
        readyMediaId = null
        _state.update {
            it.copy(
                source = VoiceSource.Picked(uri.toString(), mime, uri.lastPathSegment ?: "Audio file"),
                phase = Phase.Editing,
            )
        }
    }

    fun onClearAudio() {
        if (_state.value.isBusy) return
        stopPlayback()
        (_state.value.source as? VoiceSource.Recorded)?.let { File(it.path).delete() }
        readyMediaId = null
        _state.update { it.copy(source = null, phase = Phase.Editing, elapsedMillis = 0L) }
    }

    fun onCaptionChanged(value: String) = _state.update { it.copy(caption = value) }

    // ── Preview ─────────────────────────────────────────────────────────

    fun onTogglePlayback() {
        if (_state.value.playing) {
            stopPlayback()
            return
        }
        val source = _state.value.source ?: return
        val fresh = MediaPlayer()
        val prepared = runCatching {
            when (source) {
                is VoiceSource.Recorded -> fresh.setDataSource(source.path)
                is VoiceSource.Picked -> fresh.setDataSource(appContext, Uri.parse(source.uri))
            }
            fresh.setOnPreparedListener { it.start() }
            fresh.setOnCompletionListener { stopPlayback() }
            fresh.setOnErrorListener { _, _, _ ->
                stopPlayback()
                true
            }
            fresh.prepareAsync()
        }
        if (prepared.isFailure) {
            fresh.release()
            fail("That audio couldn't be played.", retryable = false)
            return
        }
        player = fresh
        _state.update { it.copy(playing = true) }
    }

    private fun stopPlayback() {
        player?.let { current ->
            runCatching { if (current.isPlaying) current.stop() }
            runCatching { current.release() }
        }
        player = null
        _state.update { it.copy(playing = false) }
    }

    // ── Publish ─────────────────────────────────────────────────────────

    fun onPost() {
        val current = _state.value
        val source = current.source ?: return
        if (!current.canPost) return
        stopPlayback()
        // IO, not Main: the presigned PUT is a blocking OkHttp execute().
        viewModelScope.launch(Dispatchers.IO) {
            val mediaId = readyMediaId ?: uploadAudio(source) ?: return@launch
            readyMediaId = mediaId
            createVoicePost(mediaId, current.caption)
        }
    }

    private suspend fun uploadAudio(source: VoiceSource): String? {
        val size = when (source) {
            is VoiceSource.Recorded -> File(source.path).length()
            is VoiceSource.Picked -> querySize(Uri.parse(source.uri))
        }
        if (size <= 0L) {
            fail("That audio can't be read.", retryable = false)
            return null
        }
        _state.update { it.copy(phase = Phase.Uploading(0f)) }

        val init = when (
            val reserved = uploader.reserve(source.mimeType, size, fileType = FILE_TYPE_AUDIO)
        ) {
            is AppResult.Failure -> {
                fail(reserveFailureMessage(reserved.error), retryable = reserved.error !is AppError.InvalidRequest)
                return null
            }
            is AppResult.Success -> reserved.data
        }

        val put = uploader.upload(
            uploadUrl = init.uploadUrl,
            mimeType = source.mimeType,
            sizeBytes = size,
            source = UploadSource {
                when (source) {
                    is VoiceSource.Recorded -> FileInputStream(File(source.path))
                    is VoiceSource.Picked ->
                        appContext.contentResolver.openInputStream(Uri.parse(source.uri))
                            ?: error("content stream unavailable")
                }
            },
            onProgress = { sent, total ->
                if (total > 0) {
                    _state.update { it.copy(phase = Phase.Uploading(sent.toFloat() / total)) }
                }
            },
        )
        if (put !is PresignedPutResult.Success) {
            fail("The upload didn't finish. Try again.", retryable = true)
            return null
        }

        when (val confirmed = uploader.confirm(init.mediaId)) {
            is AppResult.Failure -> {
                fail(confirmFailureMessage(confirmed.error), retryable = confirmed.error !is AppError.InvalidRequest)
                return null
            }
            is AppResult.Success -> Unit
        }

        _state.update { it.copy(phase = Phase.Processing) }
        return awaitAudioReady(init.mediaId)
    }

    /**
     * `ready` is the bar. `moderation_status` stays `pending` until the
     * transcript scan; post-service gates visibility on it (`post.go:1031`),
     * so waiting on it here would only stall a post the server will accept.
     */
    private suspend fun awaitAudioReady(mediaId: String): String? {
        repeat(READINESS_POLLS) { attempt ->
            when (val status = uploader.status(mediaId)) {
                is AppResult.Failure -> Unit // transient; keep polling
                is AppResult.Success -> {
                    val processing = status.data.processingStatus
                    val moderation = status.data.moderationStatus
                    if (processing == "rejected" || processing == "failed" || moderation == "rejected") {
                        fail("This audio was rejected ($processing/$moderation).", retryable = false)
                        return null
                    }
                    if (processing == "ready") return mediaId
                }
            }
            if (attempt < READINESS_POLLS - 1) delay(READINESS_POLL_MILLIS)
        }
        fail("Processing is taking too long. Try again in a minute.", retryable = true)
        return null
    }

    private suspend fun createVoicePost(mediaId: String, caption: String) {
        _state.update { it.copy(phase = Phase.Posting) }
        when (val result = repository.createPost(creationKey, VoicePostRequests.build(caption, mediaId))) {
            is AppResult.Success -> {
                creationKey = UUID.randomUUID().toString()
                readyMediaId = null
                (_state.value.source as? VoiceSource.Recorded)?.let { File(it.path).delete() }
                _state.update { it.copy(phase = Phase.Published(result.data)) }
            }
            is AppResult.Failure ->
                fail(repository.message(result.error), retryable = !repository.isTerminal(result.error))
        }
    }

    /**
     * The server's OWN words for a refused reservation. Today that is the
     * `oneof=image video` binding in `handler.go:129`; saying "check your
     * connection" for a 400 would send the user chasing the wrong thing.
     */
    private fun reserveFailureMessage(error: AppError): String = when (error) {
        is AppError.InvalidRequest -> "The server refused this audio upload: ${error.message}"
        is AppError.NoNetwork -> "You're offline."
        is AppError.RateLimited -> "You've uploaded a lot recently. Try again shortly."
        else -> "Couldn't start the upload. Check your connection."
    }

    private fun confirmFailureMessage(error: AppError): String = when (error) {
        // Confirm is where the server measures duration (voice.go:44+).
        is AppError.InvalidRequest -> "The server refused this recording: ${error.message}"
        else -> "The server didn't confirm the upload. Try again."
    }

    private fun querySize(uri: Uri): Long = runCatching {
        appContext.contentResolver.openAssetFileDescriptor(uri, "r")?.use { it.length } ?: -1L
    }.getOrDefault(-1L)

    private fun fail(message: String, retryable: Boolean) =
        _state.update { it.copy(phase = Phase.Failure(message, retryable)) }

    override fun onCleared() {
        meterJob?.cancel()
        recorder.stop()
        recordingFile?.delete()
        stopPlayback()
    }

    private companion object {
        const val RECORDED_MIME = "audio/mp4"
        const val DEFAULT_PICKED_MIME = "audio/mpeg"
        const val RECORDING_DIR = "create_voice"
        const val MAX_DURATION_MILLIS = MAX_VOICE_DURATION_SECONDS * 1_000
        const val MIN_DURATION_MILLIS = 1_000L
        const val METER_PERIOD_MILLIS = 100L
        const val MAX_AMPLITUDE = 32_767f
        const val BYTES_PER_MIB = 1024L * 1024L

        /** Duration probe + waveform are synchronous at confirm; 30 × 1 s covers a slow box. */
        const val READINESS_POLLS = 30
        const val READINESS_POLL_MILLIS = 1_000L
    }
}
