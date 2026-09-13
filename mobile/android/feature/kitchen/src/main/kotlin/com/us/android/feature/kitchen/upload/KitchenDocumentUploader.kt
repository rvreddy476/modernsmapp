package com.us.android.feature.kitchen.upload

import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_IMAGE
import com.us.android.core.media.upload.MEDIA_MODERATION_REJECTED
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PROCESSING_FAILED
import com.us.android.core.media.upload.PROCESSING_REJECTED
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.SUBTYPE_GENERAL
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.withContext
import javax.inject.Inject

sealed interface UploadOutcome {
    data class Ready(val mediaId: String) : UploadOutcome

    data class Failed(val message: String) : UploadOutcome
}

/**
 * A KYC photo (the FSSAI licence) through the platform's one upload path,
 * :core:media's [MediaUploader]: `init` → presigned PUT → `confirm`.
 *
 * NO upload lease: `upload_purpose` is sent empty. A leased upload (the
 * composer's) is reclaimed by media-service GC when nothing references it — and
 * food-service references a KYC document by media id without telling
 * media-service, so a lease would let GC delete a licence under review.
 *
 * The presigned PUT blocks its thread, so the whole sequence runs on IO.
 */
class KitchenDocumentUploader @Inject constructor(
    private val uploader: MediaUploader,
    private val resolver: MediaSourceResolver,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) {

    suspend fun uploadImage(uri: String, onProgress: (Float) -> Unit): UploadOutcome = withContext(io) {
        val picked = resolver.resolve(uri) ?: return@withContext UploadOutcome.Failed("That file couldn't be read. Choose another photo.")
        if (!picked.mimeType.startsWith("image/")) {
            return@withContext UploadOutcome.Failed("Choose a photo (JPG or PNG) of the licence.")
        }
        if (picked.sizeBytes > MAX_BYTES) {
            return@withContext UploadOutcome.Failed("That photo is larger than 10 MB. Choose a smaller one.")
        }
        val reserved = when (
            val result = uploader.reserve(
                mimeType = picked.mimeType,
                sizeBytes = picked.sizeBytes,
                mediaSubtype = SUBTYPE_GENERAL,
                uploadPurpose = NO_LEASE,
                fileType = FILE_TYPE_IMAGE,
            )
        ) {
            is AppResult.Success -> result.data
            is AppResult.Failure -> return@withContext UploadOutcome.Failed(GENERIC_FAILURE)
        }
        val put = uploader.upload(reserved.uploadUrl, picked.mimeType, picked.sizeBytes, picked.source) { sent, total ->
            if (total > 0) onProgress(sent.toFloat() / total)
        }
        when (put) {
            PresignedPutResult.Success -> Unit
            PresignedPutResult.UrlExpired -> return@withContext UploadOutcome.Failed("The upload link expired. Try again.")
            is PresignedPutResult.Failed -> return@withContext UploadOutcome.Failed(GENERIC_FAILURE)
        }
        when (val confirmed = uploader.confirm(reserved.mediaId)) {
            is AppResult.Failure -> UploadOutcome.Failed(GENERIC_FAILURE)
            is AppResult.Success -> {
                val asset = confirmed.data
                val refused = asset.processingStatus == PROCESSING_REJECTED ||
                    asset.processingStatus == PROCESSING_FAILED ||
                    asset.moderationStatus == MEDIA_MODERATION_REJECTED
                if (refused) {
                    UploadOutcome.Failed("That photo couldn't be used. Upload a clear photo of the whole licence.")
                } else {
                    UploadOutcome.Ready(reserved.mediaId)
                }
            }
        }
    }

    private companion object {
        const val NO_LEASE = ""
        const val MAX_BYTES = 10L * 1024L * 1024L
        const val GENERIC_FAILURE = "The photo didn't upload. Check your connection and try again."
    }
}
