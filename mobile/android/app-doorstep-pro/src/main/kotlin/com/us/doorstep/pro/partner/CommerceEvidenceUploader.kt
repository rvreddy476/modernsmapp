package com.us.doorstep.pro.partner

import android.content.Context
import android.media.MediaMetadataRetriever
import android.net.Uri
import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.*
import dagger.hilt.android.qualifiers.ApplicationContext
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import javax.inject.Inject

internal class EvidenceUploadException(message: String) : Exception(message)

/** Uses the existing secure upload transport. A confirmed upload is not evidence
 * until server-side processing and moderation have both succeeded. */
class CommerceEvidenceUploader @Inject constructor(
    private val media: MediaUploader,
    private val resolver: MediaSourceResolver,
    @ApplicationContext private val context: Context,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) {
    suspend fun upload(uri: String, video: Boolean, rememberUpload: (String) -> Unit): String = withContext(io) {
        val picked = resolver.resolve(uri) ?: throw EvidenceUploadException("That file couldn't be read. Please choose it again.")
        commerceEvidenceError(picked.mimeType, picked.sizeBytes, video)?.let { throw EvidenceUploadException(it) }
        if (video) {
            val reader = MediaMetadataRetriever()
            val duration = try {
                reader.setDataSource(context, Uri.parse(uri))
                reader.extractMetadata(MediaMetadataRetriever.METADATA_KEY_DURATION)?.toLongOrNull()
            } catch (_: Exception) { null } finally { reader.release() }
            if (duration == null || duration !in 1..60_000) throw EvidenceUploadException("Choose an introduction of one minute or less.")
        }
        val reservation = when (val result = media.reserve(picked.mimeType, picked.sizeBytes,
            uploadPurpose = "", fileType = if (video) FILE_TYPE_VIDEO else FILE_TYPE_IMAGE)) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> throw EvidenceUploadException("Couldn't start the upload. Check your connection and try again.")
        }
        val put = media.upload(reservation.uploadUrl, picked.mimeType, picked.sizeBytes, picked.source) { _, _ -> }
        if (put != PresignedPutResult.Success) {
            media.discard(reservation.mediaId)
            throw EvidenceUploadException("The upload didn't finish. Please choose the file again.")
        }
        // Retain this ID before confirming: an interrupted response must not
        // cause a second upload. Confirm and document attachment are idempotent.
        withContext(Dispatchers.Main.immediate) { rememberUpload(reservation.mediaId) }
        awaitReady(reservation.mediaId)
        reservation.mediaId
    }

    suspend fun awaitReady(id: String) {
        if (media.confirm(id) is AppResult.Failure) throw EvidenceUploadException("Your upload is saved. Tap Check upload to try again.")
        repeat(30) {
            when (val result = media.status(id)) {
                is AppResult.Failure -> throw EvidenceUploadException("Couldn't check your upload. Tap Check upload when you're connected.")
                is AppResult.Success -> {
                    val value = result.data
                    when (commerceEvidenceReadiness(value.processingStatus, value.moderationStatus)) {
                        "ready" -> return
                        "rejected" -> throw EvidenceUploadException("This file couldn't be verified. Please replace it with a clear photo or video.")
                    }
                }
            }
            delay(2_000)
        }
        throw EvidenceUploadException("Your file is still being checked. Tap Check upload shortly; you don't need to upload it again.")
    }
}

internal fun commerceEvidenceError(mime: String, bytes: Long, video: Boolean): String? = when {
    bytes <= 0 -> "This file is empty. Please choose another."
    video && mime !in setOf("video/mp4", "video/webm", "video/quicktime") -> "Choose an MP4, WebM or MOV introduction."
    !video && mime !in setOf("image/jpeg", "image/png", "image/webp") -> "Choose a JPG, PNG or WebP photo."
    bytes > (if (video) 50L else 10L) * 1024 * 1024 -> if (video) "Choose a video smaller than 50 MB." else "Choose a photo smaller than 10 MB."
    else -> null
}

internal fun commerceEvidenceReadiness(processing: String, moderation: String): String = when {
    processing in setOf(PROCESSING_FAILED, PROCESSING_REJECTED) || moderation == MEDIA_MODERATION_REJECTED -> "rejected"
    processing == PROCESSING_READY && moderation == MEDIA_MODERATION_PASSED -> "ready"
    else -> "processing"
}
