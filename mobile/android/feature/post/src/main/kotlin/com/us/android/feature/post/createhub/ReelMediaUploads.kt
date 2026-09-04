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
 * seventeen-minute transcode is the worst place to learn that.
 *
 * ## WHY THE VIDEO'S READINESS IS A SEPARATE CALL
 *
 * A phone video can take the dev machine a quarter of an hour to transcode
 * (2 minutes of footage took 17 on 2026-09-04), and a WorkManager run is cut
 * off at ten. So [uploadVideo] stops at "confirmed" — the bytes are on the
 * server and the id is durable — and [awaitVideoReady] polls under a deadline
 * the caller sets, answering [Readiness.Pending] when the deadline passes
 * with the video still processing. The worker persists the confirmed id,
 * hands off to a continuation, and never re-uploads a video the server
 * already has.
 *
 * The cover is a JPEG through the same `image` path the composer's photo
 * takes, with the photo-sized window folded in. The presigned PUT is a
 * BLOCKING OkHttp `execute()`, so it runs on the injected IO dispatcher;
 * everything else is suspend-friendly and stays on the caller's.
 */
@Singleton
class ReelMediaUploads @Inject constructor(
    private val uploader: MediaUploader,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) {

    sealed interface Outcome {
        /**
         * For the cover: ready AND passed, attachable now. For the video:
         * confirmed on the server — readiness is [awaitVideoReady]'s answer.
         */
        data class Ready(val mediaId: String) : Outcome
        data class Failed(val message: String, val retryable: Boolean) : Outcome
    }

    sealed interface Readiness {
        data object Ready : Readiness

        /** The deadline passed and the server is still working. Not a failure. */
        data object Pending : Readiness
        data class Failed(val message: String, val retryable: Boolean) : Readiness
    }

    /** Reserve, PUT with progress, confirm. Ready here means CONFIRMED. */
    suspend fun uploadVideo(video: PickedMedia, onProgress: (Float) -> Unit): Outcome {
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
        return Outcome.Ready(init.mediaId)
    }

    /**
     * Poll a confirmed video every five seconds until it is EXACTLY
     * ready+passed, rejected, or [untilMillis] (by [now]) has passed.
     */
    suspend fun awaitVideoReady(mediaId: String, untilMillis: Long, now: () -> Long): Readiness {
        while (true) {
            when (val status = uploader.status(mediaId)) {
                is AppResult.Failure -> Unit // transient; keep polling
                is AppResult.Success -> {
                    val processing = status.data.processingStatus
                    val moderation = status.data.moderationStatus
                    if (isRejected(processing, moderation)) {
                        return Readiness.Failed("This video was rejected ($processing/$moderation).", retryable = false)
                    }
                    if (processing == PROCESSING_READY && moderation == MEDIA_MODERATION_PASSED) {
                        return Readiness.Ready
                    }
                }
            }
            if (now() + VIDEO_POLL_MILLIS > untilMillis) return Readiness.Pending
            delay(VIDEO_POLL_MILLIS)
        }
    }

    /**
     * The chosen frame's JPEG bytes, through the image path, polled with the
     * photo-sized window. A cover that fails is a failure of THIS cover, not
     * of the post — the caller decides whether to post without one (it does not).
     */
    suspend fun uploadCover(bytes: ByteArray): Outcome {
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
        return awaitCoverReady(init.mediaId)
    }

    private suspend fun awaitCoverReady(mediaId: String): Outcome {
        repeat(COVER_READINESS_POLLS) { attempt ->
            when (val status = uploader.status(mediaId)) {
                is AppResult.Failure -> Unit
                is AppResult.Success -> {
                    val processing = status.data.processingStatus
                    val moderation = status.data.moderationStatus
                    if (isRejected(processing, moderation)) {
                        return Outcome.Failed("This cover was rejected ($processing/$moderation).", retryable = false)
                    }
                    if (processing == PROCESSING_READY && moderation == MEDIA_MODERATION_PASSED) {
                        return Outcome.Ready(mediaId)
                    }
                }
            }
            if (attempt < COVER_READINESS_POLLS - 1) delay(COVER_POLL_MILLIS)
        }
        return Outcome.Failed("The cover is taking too long to process. Try again in a minute.", retryable = true)
    }

    private fun isRejected(processing: String?, moderation: String?): Boolean =
        processing == PROCESSING_REJECTED || processing == PROCESSING_FAILED ||
            moderation == MEDIA_MODERATION_REJECTED

    companion object {
        private const val COVER_MIME = "image/jpeg"

        /** Long videos take this long on the dev machine; the worker chains runs to cover it. */
        const val VIDEO_READINESS_WINDOW_MILLIS = 30L * 60L * 1_000L
        const val VIDEO_POLL_MILLIS = 5_000L

        /** Photo-sized window, the composer's: 30 × 1 s. */
        private const val COVER_READINESS_POLLS = 30
        private const val COVER_POLL_MILLIS = 1_000L
    }
}
