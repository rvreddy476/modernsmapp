package com.us.android.core.media.upload

import android.content.Context
import android.net.Uri
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.ensureActive
import kotlinx.coroutines.withContext
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.coroutines.coroutineContext

/**
 * Uploads one picked image for a CHAT message and returns its media id once
 * the asset is READY and moderation-PASSED — the same discipline every other
 * attach path uses (production chat pass §5.6).
 *
 * The chat message itself then references the id; message-service pins the
 * asset through media-service's chat-attachment reservation at send time, so
 * an id that is not owned/ready/approved is refused server-side regardless of
 * what this client claims.
 */
@Singleton
class ChatAttachmentUploader @Inject constructor(
    @ApplicationContext private val context: Context,
    private val uploader: MediaUploader,
) {

    /**
     * Returns the ready media id, or a failure the UI can say out loud.
     *
     * [onProgress] reports uploaded/total bytes so the composer can render a
     * real bar. Cancellation is COOPERATIVE: cancel the calling coroutine and
     * the pipeline stops at the next stage boundary — the server side is
     * safe either way, because an unconfirmed reservation is garbage-collected
     * by media-service's lifecycle and an unreferenced confirmed asset is
     * never pinned to a message.
     */
    suspend fun uploadImage(
        uri: Uri,
        onProgress: (sentBytes: Long, totalBytes: Long) -> Unit = { _, _ -> },
    ): AppResult<String> = upload(uri, mimeOverride = null, allowed = setOf("image/"), onProgress = onProgress)

    /**
     * Chat parity (2026-10-18): a photo OR a video from the gallery, through
     * the same reserve → PUT → confirm → ready pipeline. A video gets a
     * longer readiness wait — it has to be processed before it can be sent.
     */
    suspend fun uploadPhotoOrVideo(
        uri: Uri,
        onProgress: (sentBytes: Long, totalBytes: Long) -> Unit = { _, _ -> },
    ): AppResult<String> = upload(uri, mimeOverride = null, allowed = setOf("image/", "video/"), onProgress = onProgress)

    /** A recorded voice message (a file this app wrote), as audio. */
    suspend fun uploadVoice(file: java.io.File, mime: String = VOICE_MIME): AppResult<String> =
        upload(Uri.fromFile(file), mimeOverride = mime, allowed = setOf("audio/"))

    /** The media type a picked item reports, for staging it as a photo or a video. */
    fun mimeOf(uri: Uri): String? = context.contentResolver.getType(uri)

    @Suppress("CyclomaticComplexMethod", "LongMethod") // Reserve → PUT → confirm → poll pipeline; each branch is one failure mode.
    private suspend fun upload(
        uri: Uri,
        mimeOverride: String?,
        allowed: Set<String>,
        onProgress: (sentBytes: Long, totalBytes: Long) -> Unit = { _, _ -> },
    ): AppResult<String> = withContext(Dispatchers.IO) {
        val resolver = context.contentResolver
        val mime = mimeOverride ?: resolver.getType(uri) ?: DEFAULT_IMAGE_MIME
        // Validate BEFORE any network: the two refusals a user can act on.
        if (allowed.none { mime.startsWith(it) }) {
            return@withContext AppResult.Failure(
                AppError.InvalidRequest(
                    if (allowed == setOf("image/")) "Only photos can be attached here." else "That file can't be sent here.",
                ),
            )
        }
        val polls = if (mime.startsWith("video/")) VIDEO_READINESS_POLLS else READINESS_POLLS
        val size = runCatching {
            resolver.openAssetFileDescriptor(uri, "r")?.use { it.length } ?: -1L
        }.getOrDefault(-1L)
        if (size <= 0) {
            return@withContext AppResult.Failure(
                AppError.InvalidRequest("That file couldn't be read."),
            )
        }
        if (size > MAX_ATTACHMENT_BYTES) {
            return@withContext AppResult.Failure(
                AppError.InvalidRequest("Photos up to ${MAX_ATTACHMENT_BYTES / BYTES_PER_MIB} MB can be sent."),
            )
        }

        val init = when (val reserved = uploader.reserve(mime, size)) {
            is AppResult.Failure -> return@withContext AppResult.Failure(reserved.error)
            is AppResult.Success -> reserved.data
        }

        coroutineContext.ensureActive()
        val put = uploader.upload(
            uploadUrl = init.uploadUrl,
            mimeType = mime,
            sizeBytes = size,
            source = UploadSource {
                resolver.openInputStream(uri) ?: error("content stream unavailable")
            },
            onProgress = onProgress,
        )
        if (put !is PresignedPutResult.Success) {
            return@withContext AppResult.Failure(AppError.Unknown(null, null))
        }

        coroutineContext.ensureActive()
        if (uploader.confirm(init.mediaId) is AppResult.Failure) {
            return@withContext AppResult.Failure(AppError.Unknown(null, null))
        }

        repeat(polls) { attempt ->
            coroutineContext.ensureActive()
            when (val status = uploader.status(init.mediaId)) {
                is AppResult.Failure -> Unit
                is AppResult.Success -> {
                    val processing = status.data.processingStatus
                    val moderation = status.data.moderationStatus
                    if (processing == "rejected" || processing == "failed" || moderation == "rejected") {
                        return@withContext AppResult.Failure(
                            AppError.InvalidRequest("That photo couldn't be accepted."),
                        )
                    }
                    if (processing == "ready" && moderation == "passed") {
                        return@withContext AppResult.Success(init.mediaId)
                    }
                }
            }
            if (attempt < polls - 1) delay(READINESS_POLL_MILLIS)
        }
        AppResult.Failure(AppError.Timeout())
    }

    private companion object {
        const val DEFAULT_IMAGE_MIME = "image/jpeg"
        const val READINESS_POLLS = 30

        /** A video is transcoded before it is ready; two minutes before giving up. */
        const val VIDEO_READINESS_POLLS = 120
        const val VOICE_MIME = "audio/mp4"
        const val READINESS_POLL_MILLIS = 1_000L

        /** The chat attachment cap; media-service enforces its own upstream. */
        const val MAX_ATTACHMENT_BYTES = 25L * 1024 * 1024
        const val BYTES_PER_MIB = 1024L * 1024
    }
}
