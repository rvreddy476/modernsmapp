package com.us.android.feature.post.createhub

import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.error.AppError
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
import com.us.android.core.media.upload.RESUMABLE_THRESHOLD_BYTES
import com.us.android.core.media.upload.ResumableOutcome
import com.us.android.core.media.upload.ResumableSession
import com.us.android.core.media.upload.ResumableUploader
import com.us.android.core.media.upload.UploadSource
import com.us.android.core.media.upload.asksForResumableUpload
import com.us.android.core.media.upload.videoTooLarge
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
 * Since instant reels (2026-09-04) a flick is created from the CONFIRMED id
 * and the server transcodes behind the post, so [uploadVideo] stops at
 * "confirmed" and the pipeline creates at once. [awaitVideoReady] remains
 * for the one server that still answers `MEDIA_NOT_READY`: a phone video can
 * take the dev machine a quarter of an hour (2 minutes of footage took 17),
 * a WorkManager run is cut off at ten, so it polls under a deadline the
 * caller sets and answers [Readiness.Pending] when that passes. The worker
 * persists the confirmed id, hands off to a continuation, and never
 * re-uploads a video the server already has.
 *
 * The cover is a JPEG through the same `image` path the composer's photo
 * takes, with the photo-sized window folded in. The presigned PUT is a
 * BLOCKING OkHttp `execute()`, so it runs on the injected IO dispatcher;
 * everything else is suspend-friendly and stays on the caller's.
 */
@Singleton
class ReelMediaUploads @Inject constructor(
    private val uploader: MediaUploader,
    private val resumable: ResumableUploader,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) {

    sealed interface Outcome {
        /**
         * For the cover: ready AND passed, attachable now. For the video:
         * confirmed on the server — readiness is [awaitVideoReady]'s answer.
         */
        data class Ready(val mediaId: String) : Outcome

        /**
         * [transient]: the network or the server's moment, not the file or
         * the request — the worker tries again by itself once the phone is
         * back online (2026-11-07). Implies [retryable].
         */
        data class Failed(val message: String, val retryable: Boolean, val transient: Boolean = false) : Outcome
    }

    sealed interface Readiness {
        data object Ready : Readiness

        /** The deadline passed and the server is still working. Not a failure. */
        data object Pending : Readiness
        data class Failed(val message: String, val retryable: Boolean) : Readiness
    }

    /**
     * The video's bytes up, and confirmed. Ready here means CONFIRMED.
     *
     * Two paths (2026-10-21, no size limit for long videos):
     *  - under [RESUMABLE_THRESHOLD_BYTES]: reserve, ONE presigned PUT with
     *    progress, confirm — as before;
     *  - at or over it, or when the simple reserve answers 413
     *    `USE_RESUMABLE_UPLOAD`, or when a previous run left a [resume]
     *    session: part by part through [ResumableUploader], whose `complete`
     *    is the confirm.
     *
     * [onSession] receives the resumable session to persist (null once it
     * must be forgotten), so a stopped worker resumes the same upload.
     *
     * The single PUT is checkpointed too (2026-11-07): [onReserved] receives
     * the reserved id before a byte goes up (null once it must be
     * forgotten), and a run handed that id back as [reserved] CONFIRMS it
     * first. Bytes that landed before a failed confirm, or before the
     * process died, are never sent twice and the first asset is not
     * orphaned; the server's confirm checks the object is really there.
     */
    @Suppress("LongParameterList") // The two checkpoints' ids and their callbacks; no bundle reads clearer.
    suspend fun uploadVideo(
        video: PickedMedia,
        resume: ResumableSession? = null,
        onSession: suspend (ResumableSession?) -> Unit = {},
        reserved: String? = null,
        onReserved: suspend (String?) -> Unit = {},
        onProgress: (Float) -> Unit,
    ): Outcome {
        if (resume != null || video.sizeBytes >= RESUMABLE_THRESHOLD_BYTES) {
            return uploadResumable(video, resume, onSession, onProgress)
        }
        if (reserved != null) confirmReserved(reserved, onReserved)?.let { return it }
        val init = when (val result = uploader.reserve(video.mimeType, video.sizeBytes, fileType = FILE_TYPE_VIDEO)) {
            is AppResult.Failure -> return reserveRefused(result.error, video, onSession, onProgress)
            is AppResult.Success -> result.data
        }
        onReserved(init.mediaId)
        val put = withContext(io) {
            uploader.upload(
                uploadUrl = init.uploadUrl,
                mimeType = video.mimeType,
                sizeBytes = video.sizeBytes,
                source = video.source,
                onProgress = { sent, total -> if (total > 0) onProgress(sent.toFloat() / total) },
            )
        }
        putFailure(put, onReserved)?.let { return it }
        return confirmOrKeep(init.mediaId, onReserved)
    }

    /**
     * A previous run's reserved id: [Outcome.Ready] when its bytes are on the
     * server, the network's failure when that cannot be told yet (the id is
     * kept), and null — the id forgotten — when the server refused it (the
     * object is missing or the wrong size: the PUT never landed whole), so
     * the upload starts clean.
     */
    private suspend fun confirmReserved(mediaId: String, onReserved: suspend (String?) -> Unit): Outcome? =
        when (val confirmed = uploader.confirm(mediaId)) {
            is AppResult.Success -> Outcome.Ready(mediaId)
            is AppResult.Failure -> if (confirmed.error.isTransient()) {
                CONFIRM_FAILED
            } else {
                onReserved(null)
                null
            }
        }

    /**
     * Null when the PUT landed. An expired signature forgets the id (it can
     * never take these bytes); a refusal does too. A dropped connection
     * KEEPS it: the bytes may have landed before the answer was lost, and
     * the next run's confirm tells.
     */
    private suspend fun putFailure(put: PresignedPutResult, onReserved: suspend (String?) -> Unit): Outcome? =
        when (put) {
            PresignedPutResult.Success -> null
            PresignedPutResult.UrlExpired -> {
                onReserved(null)
                PUT_FAILED
            }
            is PresignedPutResult.Failed -> {
                val transient = put.isTransient()
                if (!transient) onReserved(null)
                PUT_FAILED.copy(transient = transient)
            }
        }

    /** The fresh upload's confirm; a network failure keeps the id for the next run. */
    private suspend fun confirmOrKeep(mediaId: String, onReserved: suspend (String?) -> Unit): Outcome =
        when (val confirmed = uploader.confirm(mediaId)) {
            is AppResult.Success -> Outcome.Ready(mediaId)
            is AppResult.Failure -> if (confirmed.error.isTransient()) {
                CONFIRM_FAILED
            } else {
                onReserved(null)
                CONFIRM_FAILED.copy(transient = false)
            }
        }

    /**
     * The simple `init` said no. Over the platform's cap (413
     * `VIDEO_TOO_LARGE`, 2026-11-07) is final and says the cap — never the
     * resumable fallback, which refuses it too. `USE_RESUMABLE_UPLOAD` is
     * the fallback. Anything else is the start failing.
     */
    private suspend fun reserveRefused(
        error: AppError,
        video: PickedMedia,
        onSession: suspend (ResumableSession?) -> Unit,
        onProgress: (Float) -> Unit,
    ): Outcome {
        error.videoTooLarge()?.let { return Outcome.Failed(it.message, retryable = false) }
        if (error.asksForResumableUpload()) return uploadResumable(video, resume = null, onSession, onProgress)
        return Outcome.Failed(START_FAILED, retryable = true, transient = error.isTransient())
    }

    private suspend fun uploadResumable(
        video: PickedMedia,
        resume: ResumableSession?,
        onSession: suspend (ResumableSession?) -> Unit,
        onProgress: (Float) -> Unit,
    ): Outcome {
        val outcome = resumable.upload(
            mimeType = video.mimeType,
            totalBytes = video.sizeBytes,
            source = video.source,
            resume = resume,
            onSession = { onSession(it) },
            onProgress = { sent, total -> if (total > 0) onProgress(sent.toFloat() / total) },
        )
        return when (outcome) {
            is ResumableOutcome.Completed -> Outcome.Ready(outcome.mediaId)
            is ResumableOutcome.Failed -> {
                if (outcome.discardSession) onSession(null)
                Outcome.Failed(outcome.message, outcome.retryable)
            }
        }
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

        const val START_FAILED = "Couldn't start the upload. Check your connection."
        private val PUT_FAILED =
            Outcome.Failed("The upload didn't finish. Try again.", retryable = true, transient = true)
        private val CONFIRM_FAILED =
            Outcome.Failed("The server didn't confirm the upload. Try again.", retryable = true, transient = true)

        private const val HTTP_REQUEST_TIMEOUT = 408
        private const val HTTP_TOO_MANY_REQUESTS = 429
        private const val HTTP_SERVER_ERROR = 500

        /** The network, or the server's moment: a later try of the same request may land. */
        internal fun AppError.isTransient(): Boolean = when (this) {
            is AppError.NoNetwork, is AppError.Timeout, is AppError.Server, is AppError.RateLimited -> true
            else -> false
        }

        /** No response (code 0: the connection dropped), a timeout, throttling, or the store's 5xx. */
        internal fun PresignedPutResult.Failed.isTransient(): Boolean =
            code == 0 || code == HTTP_REQUEST_TIMEOUT || code == HTTP_TOO_MANY_REQUESTS || code >= HTTP_SERVER_ERROR

        /** Long videos take this long on the dev machine; the worker chains runs to cover it. */
        const val VIDEO_READINESS_WINDOW_MILLIS = 30L * 60L * 1_000L
        const val VIDEO_POLL_MILLIS = 5_000L

        /** Photo-sized window, the composer's: 30 × 1 s. */
        private const val COVER_READINESS_POLLS = 30
        private const val COVER_POLL_MILLIS = 1_000L
    }
}
