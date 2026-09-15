package com.us.android.feature.dating.selfie

import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_VIDEO
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.SUBTYPE_GENERAL
import com.us.android.core.media.upload.UploadSource
import com.us.android.feature.dating.photos.UploadOutcome
import com.us.android.feature.dating.photos.awaitProcessed
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import java.io.File
import javax.inject.Inject

/** A recorded selfie clip → a media id for `POST /verification/selfie`. A port so the flow tests on the JVM. */
interface SelfieVideoUploader {
    suspend fun upload(file: File, onProgress: (Float) -> Unit): UploadOutcome
}

/**
 * The selfie video through `:core:media`: reserve (`file_type: video`) →
 * presigned PUT → confirm → a short wait for processing. The local clip is
 * deleted afterwards whatever happened: a biometric video has no business
 * lingering in the cache.
 */
class MediaSelfieVideoUploader @Inject constructor(
    private val uploader: MediaUploader,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : SelfieVideoUploader {

    override suspend fun upload(file: File, onProgress: (Float) -> Unit): UploadOutcome = withContext(io) {
        try {
            val size = file.length()
            if (!file.exists() || size <= 0L) return@withContext UploadOutcome.Failed("The recording didn't save. Try again.")
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
                is AppResult.Failure -> return@withContext UploadOutcome.Failed(GENERIC_FAILURE)
            }
            val put = uploader.upload(reserved.uploadUrl, MIME_MP4, size, UploadSource { file.inputStream() }) { sent, total ->
                if (total > 0) onProgress(sent.toFloat() / total)
            }
            if (put != PresignedPutResult.Success) return@withContext UploadOutcome.Failed(GENERIC_FAILURE)
            if (uploader.confirm(reserved.mediaId) !is AppResult.Success) return@withContext UploadOutcome.Failed(GENERIC_FAILURE)
            if (awaitProcessed(uploader, reserved.mediaId, POLLS) != null) {
                return@withContext UploadOutcome.Failed("That recording couldn't be used. Try again in good light.")
            }
            UploadOutcome.Ready(reserved.mediaId)
        } finally {
            file.delete()
        }
    }

    private companion object {
        const val MIME_MP4 = "video/mp4"
        const val NO_LEASE = ""
        const val POLLS = 30
        const val GENERIC_FAILURE = "The video didn't upload. Check your connection and try again."
    }
}
