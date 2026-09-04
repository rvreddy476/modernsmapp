package com.us.android.feature.post.createhub

import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_IMAGE
import com.us.android.core.media.upload.FILE_TYPE_VIDEO
import com.us.android.core.media.upload.MEDIA_MODERATION_PASSED
import com.us.android.core.media.upload.MEDIA_MODERATION_REJECTED
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PROCESSING_FAILED
import com.us.android.core.media.upload.PROCESSING_READY
import com.us.android.core.media.upload.PROCESSING_REJECTED
import com.us.android.core.media.upload.PickedMedia
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.UploadSource
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import java.io.ByteArrayInputStream
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The reel's two uploads — the video and its cover — over the ONE
 * [MediaUploader], each with the composer's exact readiness rule.
 *
 * reserve → presigned PUT → confirm → poll for EXACTLY `ready` + `passed`.
 * Nothing less is attachable: `ready` with moderation still pending is an id
 * the server will refuse on create, and a create that fails after a
 * two-minute transcode is the worst place to learn that.
 *
 * The video reserves `file_type: video` with a window sized for a transcode;
 * the cover is a JPEG through the same `image` path the composer's photo
 * takes, with the photo-sized window. The presigned PUT is a BLOCKING OkHttp
 * `execute()`, so it runs on the injected IO dispatcher; everything else is
 * suspend-friendly and stays on the caller's.
 */
@Singleton
class ReelMediaUploads @Inject constructor(
    private val uploader: MediaUploader,
    private val encoder: ReelCoverEncoder,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) {

    sealed interface Outcome {
        data class Ready(val mediaId: String) : Outcome
        data class Failed(val message: String, val retryable: Boolean) : Outcome
    }

    /** Progress and the switch to "processing" are reported so the UI can say so. */
    suspend fun uploadVideo(
        video: PickedMedia,
        onProgress: (Float) -> Unit,
        onProcessing: () -> Unit,
    ): Outcome {
        val init = when (val reserved = uploader.reserve(video.mimeType, video.sizeBytes, fileType = FILE_TYPE_VIDEO)) {
            is AppResult.Failure -> return Outcome.Failed("Couldn't start the upload. Check your connection.", true)
            is AppResult.Success -> reserved.data
        }
        val put = withContext(io) {
            uploader.upload(
                uploadUrl = init.uploadUrl,
                mimeType = video.mimeType,
                sizeBytes = video.sizeBytes,
                source = video.source,
                onProgress = { sent, total -> if (total > 0) onProgress(sent.toFloat() / total) },
            )
        }
        if (put !is PresignedPutResult.Success) {
            return Outcome.Failed("The upload didn't finish. Try again.", retryable = true)
        }
        if (uploader.confirm(init.mediaId) is AppResult.Failure) {
            return Outcome.Failed("The server didn't confirm the upload. Try again.", retryable = true)
        }
        onProcessing()
        return awaitReady(init.mediaId, VIDEO_READINESS_POLLS, VIDEO_POLL_MILLIS, "This video")
    }

    /**
     * The chosen frame as a JPEG, through the image path. A frame that
     * cannot be encoded is a terminal failure of THIS cover, not of the
     * post — the caller decides whether to post without one (it does not).
     */
    suspend fun uploadCover(frame: CoverFrame): Outcome {
        val bytes = encoder.encode(frame)
            ?: return Outcome.Failed("That cover frame couldn't be prepared. Pick another.", retryable = true)
        val size = bytes.size.toLong()
        val init = when (val reserved = uploader.reserve(COVER_MIME, size, fileType = FILE_TYPE_IMAGE)) {
            is AppResult.Failure -> return Outcome.Failed("Couldn't upload the cover. Try again.", true)
            is AppResult.Success -> reserved.data
        }
        val put = withContext(io) {
            uploader.upload(
                uploadUrl = init.uploadUrl,
                mimeType = COVER_MIME,
                sizeBytes = size,
                source = UploadSource { ByteArrayInputStream(bytes) },
                onProgress = { _, _ -> },
            )
        }
        if (put !is PresignedPutResult.Success) {
            return Outcome.Failed("The cover upload didn't finish. Try again.", retryable = true)
        }
        if (uploader.confirm(init.mediaId) is AppResult.Failure) {
            return Outcome.Failed("The server didn't confirm the cover. Try again.", retryable = true)
        }
        return awaitReady(init.mediaId, COVER_READINESS_POLLS, COVER_POLL_MILLIS, "This cover")
    }

    private suspend fun awaitReady(mediaId: String, polls: Int, pollMillis: Long, what: String): Outcome {
        repeat(polls) { attempt ->
            when (val status = uploader.status(mediaId)) {
                is AppResult.Failure -> Unit // transient; keep polling
                is AppResult.Success -> {
                    val processing = status.data.processingStatus
                    val moderation = status.data.moderationStatus
                    if (processing == PROCESSING_REJECTED || processing == PROCESSING_FAILED ||
                        moderation == MEDIA_MODERATION_REJECTED
                    ) {
                        return Outcome.Failed("$what was rejected ($processing/$moderation).", retryable = false)
                    }
                    if (processing == PROCESSING_READY && moderation == MEDIA_MODERATION_PASSED) {
                        return Outcome.Ready(mediaId)
                    }
                }
            }
            if (attempt < polls - 1) delay(pollMillis)
        }
        return Outcome.Failed("Processing is taking too long. Try again in a minute.", retryable = true)
    }

    private companion object {
        const val COVER_MIME = "image/jpeg"

        /** Transcode-sized window: 120 × 2 s = four minutes. */
        const val VIDEO_READINESS_POLLS = 120
        const val VIDEO_POLL_MILLIS = 2_000L

        /** Photo-sized window, the composer's: 30 × 1 s. */
        const val COVER_READINESS_POLLS = 30
        const val COVER_POLL_MILLIS = 1_000L
    }
}
