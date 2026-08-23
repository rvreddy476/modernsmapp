package com.us.android.core.media.upload

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import javax.inject.Inject
import javax.inject.Singleton

/** How far an upload has got. Resumable across process death by design. */
sealed interface UploadStage {
    data object Idle : UploadStage

    /** `init` has returned; [mediaId] exists at `pending_upload`. */
    data class Reserved(val mediaId: String, val uploadUrl: String) : UploadStage

    data class Uploading(val mediaId: String, val uploaded: Long, val total: Long) : UploadStage

    /** Bytes landed and `confirm` succeeded. Not necessarily publishable yet. */
    data class Confirmed(val mediaId: String, val processingStatus: String) : UploadStage

    /** Terminal-good: the asset may be attached. */
    data class Ready(val mediaId: String) : UploadStage

    /**
     * Terminal-bad: moderation or processing refused this asset.
     *
     * Distinct from [Failed] because retrying cannot change it. Offering a
     * Retry button here would be a control that can never succeed.
     */
    data class Rejected(val mediaId: String, val reason: String) : UploadStage

    data class Failed(val message: String, val retryable: Boolean) : UploadStage
}

/**
 * One upload implementation for every future consumer.
 *
 * Product-neutral on purpose: it takes bytes, a MIME type and an accessibility
 * decision, and returns a media id. It does not know whether the result becomes
 * a post, a story, a chat attachment or an avatar. The architecture ruling for
 * Slice C is explicit that there is ONE upload implementation, not one copy per
 * feature — the second copy is where the two drift and only one of them gets
 * the next security fix.
 *
 * ## RESUMPTION IS THE CALLER'S, DELIBERATELY
 *
 * This class does not persist anything. It exposes each step separately so the
 * caller — which owns a durable draft — can restart from whichever step it had
 * reached: a killed PUT restarts from a fresh [reserve], while a successful
 * [confirm] means the media id is already good and must be reused rather than
 * re-uploaded. Hiding that behind one `upload()` call would make recovery
 * impossible to express.
 */
@Singleton
class MediaUploader @Inject constructor(
    private val api: MediaUploadApi,
    private val presigned: PresignedUploader,
    private val errorMapper: ErrorMapper,
) {

    /**
     * Step 1: reserve the row and get a presigned URL.
     *
     * Carries the composer lease so the server can reclaim this asset if the
     * user abandons it. Alt text is deliberately NOT sent: the accessibility
     * decision is made after the picker returns, so anything supplied here
     * would be a placeholder the server then keeps. See [updateAccessibility].
     */
    suspend fun reserve(
        mimeType: String,
        sizeBytes: Long,
        mediaSubtype: String = SUBTYPE_GENERAL,
        uploadPurpose: String = UPLOAD_PURPOSE_COMPOSER,
    ): AppResult<MediaInitDto> = apiCall(errorMapper) {
        api.init(
            MediaInitRequest(
                fileType = FILE_TYPE_IMAGE,
                mediaSubtype = mediaSubtype,
                mimeType = mimeType,
                fileSizeBytes = sizeBytes,
                uploadPurpose = uploadPurpose,
            ),
        )
    }

    /**
     * Writes the FINAL accessibility decision before the asset is attached.
     *
     * Returns whether it succeeded. The caller treats failure as a blocking
     * error rather than ignoring it: the composer required a description and
     * showed it to the user, so publishing with the earlier empty placeholder
     * would silently discard a decision the product insisted on.
     */
    suspend fun updateAccessibility(
        mediaId: String,
        altText: String,
        decorative: Boolean,
    ): Boolean = apiCall(errorMapper) {
        api.updateAltText(mediaId, MediaAltTextRequest(altText = altText, decorative = decorative))
    } is AppResult.Success

    /** Step 2: push the bytes. No auth headers — see [PresignedUploader]. */
    suspend fun upload(
        uploadUrl: String,
        mimeType: String,
        sizeBytes: Long,
        source: UploadSource,
        onProgress: (Long, Long) -> Unit,
    ): PresignedPutResult = presigned.put(uploadUrl, mimeType, sizeBytes, source, onProgress)

    /** Step 3: tell the server the bytes are there. Idempotent server-side. */
    suspend fun confirm(mediaId: String): AppResult<MediaAssetDto> =
        apiCall(errorMapper) { api.confirm(MediaConfirmRequest(mediaId)) }

    /** Poll for readiness. */
    suspend fun status(mediaId: String): AppResult<MediaStatusDto> =
        apiCall(errorMapper) { api.status(mediaId) }

    /**
     * Best-effort discard.
     *
     * Failure is not reported: the caller has already thrown the draft away and
     * cannot act on it, and server GC reclaims the asset regardless. Surfacing
     * an error here would ask the user to care about cleanup they did not
     * initiate.
     */
    suspend fun discard(mediaId: String) {
        runCatching { api.delete(mediaId) }
    }
}
