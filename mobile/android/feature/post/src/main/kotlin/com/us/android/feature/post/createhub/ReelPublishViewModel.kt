package com.us.android.feature.post.createhub

import android.content.Context
import android.net.Uri
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_VIDEO
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.UploadSource
import com.us.android.feature.post.data.ComposerRepository
import com.us.android.feature.post.data.dto.CONTENT_TYPE_FLICK
import com.us.android.feature.post.data.dto.CreatePostRequest
import com.us.android.feature.post.data.dto.DistributionRequest
import com.us.android.feature.post.data.dto.POST_TYPE_VIDEO
import dagger.hilt.android.lifecycle.HiltViewModel
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.UUID
import javax.inject.Inject

/**
 * The Create hub's Reel surface: pick or record a video, caption it, post it
 * as a `flick`.
 *
 * ## THE PIPELINE
 *
 * The composer's exact media discipline, with video parameters: reserve
 * (`file_type: video`) → presigned PUT streamed from the content URI → confirm
 * → poll for EXACT ready+passed → create (`content_type: flick`, title +
 * caption) through the ONE create call site. Server-side transcode to HLS is
 * what "processing" means here, so the readiness window is minutes, not
 * seconds, and the UI states each stage honestly.
 *
 * ## WHAT THIS DOES NOT DO YET
 *
 * No trimming, effects, or cover choice — the CameraX/Media3 editing stack is
 * a decided future phase. And the upload runs in this ViewModel's scope, so
 * backgrounding the app mid-upload can lose it; moving reels onto a
 * WorkManager worker like the Studio's is tracked in the handover.
 */
@HiltViewModel
class ReelPublishViewModel @Inject constructor(
    @ApplicationContext private val appContext: Context,
    private val uploader: MediaUploader,
    private val repository: ComposerRepository,
) : ViewModel() {

    sealed interface Phase {
        data object Editing : Phase
        data class Uploading(val fraction: Float) : Phase
        data object Processing : Phase
        data object Posting : Phase
        data class Published(val postId: String) : Phase
        data class Failure(val message: String, val retryable: Boolean) : Phase
    }

    data class ReelUiState(
        val videoUri: String? = null,
        val title: String = "",
        val caption: String = "",
        val phase: Phase = Phase.Editing,
    ) {
        val canPost: Boolean
            get() = videoUri != null &&
                title.isNotBlank() &&
                (phase is Phase.Editing || phase is Phase.Failure)
    }

    private val _state = MutableStateFlow(ReelUiState())
    val state: StateFlow<ReelUiState> = _state.asStateFlow()

    private var creationKey: String = UUID.randomUUID().toString()

    /** A confirmed+ready media id survives a create retry — no re-upload. */
    private var readyMediaId: String? = null

    fun onVideoPicked(uri: Uri) = _state.update {
        // A different video is a different post: new key, new upload.
        creationKey = UUID.randomUUID().toString()
        readyMediaId = null
        it.copy(videoUri = uri.toString(), phase = Phase.Editing)
    }

    fun onTitleChanged(value: String) = _state.update { it.copy(title = value) }

    fun onCaptionChanged(value: String) = _state.update { it.copy(caption = value) }

    fun onPost() {
        val current = _state.value
        if (!current.canPost) return
        // IO dispatcher, not Main: the presigned PUT is a BLOCKING OkHttp
        // execute() (the composer's path also runs it off-main). State updates
        // go through the thread-safe StateFlow, so nothing here needs Main.
        viewModelScope.launch(Dispatchers.IO) {
            val mediaId = readyMediaId ?: uploadVideo(Uri.parse(current.videoUri!!)) ?: return@launch
            readyMediaId = mediaId
            createFlick(mediaId, current)
        }
    }

    // ── Upload ──────────────────────────────────────────────────────────

    private suspend fun uploadVideo(uri: Uri): String? {
        val resolver = appContext.contentResolver
        val mime = resolver.getType(uri) ?: DEFAULT_VIDEO_MIME
        val size = runCatching {
            resolver.openAssetFileDescriptor(uri, "r")?.use { it.length } ?: -1L
        }.getOrDefault(-1L)
        if (size <= 0) {
            fail("That video can't be read.", retryable = false)
            return null
        }

        _state.update { it.copy(phase = Phase.Uploading(0f)) }

        val init = when (val reserved = uploader.reserve(mime, size, fileType = FILE_TYPE_VIDEO)) {
            is AppResult.Failure -> {
                fail("Couldn't start the upload. Check your connection.", retryable = true)
                return null
            }
            is AppResult.Success -> reserved.data
        }

        val put = uploader.upload(
            uploadUrl = init.uploadUrl,
            mimeType = mime,
            sizeBytes = size,
            source = UploadSource {
                resolver.openInputStream(uri)
                    ?: error("content stream unavailable")
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

        if (uploader.confirm(init.mediaId) is AppResult.Failure) {
            fail("The server didn't confirm the upload. Try again.", retryable = true)
            return null
        }

        _state.update { it.copy(phase = Phase.Processing) }
        return awaitVideoReady(init.mediaId)
    }

    /**
     * The composer's EXACT readiness discipline — ready+passed, nothing less —
     * with a window sized for transcoding instead of thumbnailing.
     */
    private suspend fun awaitVideoReady(mediaId: String): String? {
        repeat(READINESS_POLLS) { attempt ->
            when (val status = uploader.status(mediaId)) {
                is AppResult.Failure -> Unit // transient; keep polling
                is AppResult.Success -> {
                    val processing = status.data.processingStatus
                    val moderation = status.data.moderationStatus
                    if (processing == "rejected" || processing == "failed" ||
                        moderation == "rejected"
                    ) {
                        fail("This video was rejected ($processing/$moderation).", retryable = false)
                        return null
                    }
                    if (processing == "ready" && moderation == "passed") return mediaId
                }
            }
            if (attempt < READINESS_POLLS - 1) delay(READINESS_POLL_MILLIS)
        }
        fail("Processing is taking too long. Try again in a minute.", retryable = true)
        return null
    }

    // ── Create ──────────────────────────────────────────────────────────

    private suspend fun createFlick(mediaId: String, current: ReelUiState) {
        _state.update { it.copy(phase = Phase.Posting) }
        val request = CreatePostRequest(
            text = current.caption.trim(),
            contentType = CONTENT_TYPE_FLICK,
            postType = POST_TYPE_VIDEO,
            mediaIds = listOf(mediaId),
            language = DEFAULT_LANGUAGE,
            distribution = DistributionRequest(),
            title = current.title.trim(),
        )
        when (val result = repository.createPost(creationKey, request)) {
            is AppResult.Success -> {
                creationKey = UUID.randomUUID().toString()
                readyMediaId = null
                _state.update { it.copy(phase = Phase.Published(result.data)) }
            }
            is AppResult.Failure ->
                fail(repository.message(result.error), retryable = !repository.isTerminal(result.error))
        }
    }

    private fun fail(message: String, retryable: Boolean) =
        _state.update { it.copy(phase = Phase.Failure(message, retryable)) }

    private companion object {
        const val DEFAULT_VIDEO_MIME = "video/mp4"
        const val DEFAULT_LANGUAGE = "en"

        /** Transcode-sized window: 120 × 2 s = four minutes. */
        const val READINESS_POLLS = 120
        const val READINESS_POLL_MILLIS = 2_000L
    }
}
