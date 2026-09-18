package com.us.android.feature.mopedu.captain.upload

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

/** What an upload came to: a CONFIRMED media id, or a reason in the captain's words. */
sealed interface UploadOutcome {
    data class Ready(val mediaId: String) : UploadOutcome

    data class Failed(val message: String) : UploadOutcome
}

/**
 * A KYC photo (selfie, DL, RC) to a media id. A port so the ViewModels can be
 * tested without :core:media; [MediaCaptainDocumentUploader] is the real one.
 */
interface CaptainDocumentUploader {
    suspend fun uploadImage(uri: String, onProgress: (Float) -> Unit): UploadOutcome
}

/**
 * The photo through :core:media's [MediaUploader]: `init` → presigned PUT →
 * `confirm`. Only a confirmed, non-rejected asset answers [UploadOutcome.Ready];
 * a document record is never submitted on anything less.
 *
 * NO upload lease (`upload_purpose` empty): rider-service references the
 * document by media id without telling media-service, so a lease would let
 * media GC delete a selfie the server is still comparing.
 *
 * DUPLICATED from :feature:rider's upload/RiderDocumentUploader.kt (itself a
 * copy of :feature:kitchen's). Three copies now — lift into :core:media.
 */
class MediaCaptainDocumentUploader @Inject constructor(
    private val uploader: MediaUploader,
    private val resolver: MediaSourceResolver,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : CaptainDocumentUploader {

    override suspend fun uploadImage(uri: String, onProgress: (Float) -> Unit): UploadOutcome = withContext(io) {
        val picked = resolver.resolve(uri) ?: return@withContext UploadOutcome.Failed("That photo couldn't be read. Try again.")
        if (!picked.mimeType.startsWith("image/")) {
            return@withContext UploadOutcome.Failed("Choose a photo (JPG or PNG).")
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
                    UploadOutcome.Failed("That photo couldn't be used. Take a clear, well-lit photo.")
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
