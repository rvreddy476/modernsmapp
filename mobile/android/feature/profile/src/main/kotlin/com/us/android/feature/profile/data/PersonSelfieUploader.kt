package com.us.android.feature.profile.data

import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_VIDEO
import com.us.android.core.media.upload.MEDIA_MODERATION_REJECTED
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PROCESSING_FAILED
import com.us.android.core.media.upload.PROCESSING_READY
import com.us.android.core.media.upload.PROCESSING_REJECTED
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.SUBTYPE_GENERAL
import com.us.android.core.media.upload.UploadSource
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import java.io.File
import javax.inject.Inject

/** How a selfie clip's upload ended. */
sealed interface SelfieUpload {
    data class Ready(val mediaId: String) : SelfieUpload
    data class Failed(val message: String) : SelfieUpload
}

/** A recorded selfie clip → a media id for `POST …/verification/person/selfie`; a port for JVM tests. */
fun interface PersonSelfieUploader {
    suspend fun upload(file: File): SelfieUpload
}

/**
 * The selfie clip through `:core:media`, the same three steps the dating selfie uses:
 * reserve (`file_type: video`, `media_subtype: general`) → presigned PUT → confirm, then a
 * short wait while media-service processes it (the server checks a READY video only).
 * The local clip is deleted afterwards whatever happened: a face video has no business
 * lingering in the cache.
 */
class MediaPersonSelfieUploader @Inject constructor(
    private val uploader: MediaUploader,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : PersonSelfieUploader {

    override suspend fun upload(file: File): SelfieUpload = withContext(io) {
        try {
            val size = file.length()
            if (!file.isFile || size <= 0L) return@withContext SelfieUpload.Failed(NOT_SAVED)
            val reserved = when (
                val result = uploader.reserve(
                    mimeType = MIME_MP4,
                    sizeBytes = size,
                    mediaSubtype = SUBTYPE_GENERAL,
                    uploadPurpose = NO_LEASE,
                    fileType = FILE_TYPE_VIDEO,
                )
            ) {
                is AppResult.Success -> result.data
                is AppResult.Failure -> return@withContext SelfieUpload.Failed(NOT_UPLOADED)
            }
            val source = UploadSource { file.inputStream() }
            val put = uploader.upload(reserved.uploadUrl, MIME_MP4, size, source) { _, _ -> }
            if (put != PresignedPutResult.Success) return@withContext SelfieUpload.Failed(NOT_UPLOADED)
            if (uploader.confirm(reserved.mediaId) !is AppResult.Success) {
                return@withContext SelfieUpload.Failed(NOT_UPLOADED)
            }
            if (!awaitReady(reserved.mediaId)) return@withContext SelfieUpload.Failed(NOT_USABLE)
            SelfieUpload.Ready(reserved.mediaId)
        } finally {
            file.delete()
        }
    }

    /** False only when media-service refused the clip; a slow answer is left to the server's own check. */
    private suspend fun awaitReady(mediaId: String): Boolean {
        repeat(POLLS) {
            val status = (uploader.status(mediaId) as? AppResult.Success)?.data
            when {
                status == null -> Unit
                status.processingStatus == PROCESSING_READY -> return true
                status.processingStatus == PROCESSING_REJECTED ||
                    status.processingStatus == PROCESSING_FAILED ||
                    status.moderationStatus == MEDIA_MODERATION_REJECTED -> return false
            }
            delay(POLL_MILLIS)
        }
        return true
    }

    private companion object {
        const val MIME_MP4 = "video/mp4"
        const val NO_LEASE = ""
        const val POLLS = 30
        const val POLL_MILLIS = 1_000L
        const val NOT_SAVED = "The recording didn't save. Try again."
        const val NOT_UPLOADED = "The video didn't upload. Check your connection and try again."
        const val NOT_USABLE = "That recording couldn't be used. Try again in good light."
    }
}
