package com.us.android.feature.dating.photos

import com.us.android.core.common.di.Dispatcher
import com.us.android.core.common.di.UsDispatcher
import com.us.android.core.common.result.AppResult
import com.us.android.core.media.upload.FILE_TYPE_IMAGE
import com.us.android.core.media.upload.MEDIA_MODERATION_REJECTED
import com.us.android.core.media.upload.MediaSourceResolver
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PROCESSING_FAILED
import com.us.android.core.media.upload.PROCESSING_READY
import com.us.android.core.media.upload.PROCESSING_REJECTED
import com.us.android.core.media.upload.PresignedPutResult
import com.us.android.core.media.upload.SUBTYPE_GENERAL
import com.us.android.core.network.ApiConfig
import com.us.android.feature.dating.network.DatingPersonDto
import kotlinx.coroutines.CoroutineDispatcher
import kotlinx.coroutines.delay
import kotlinx.coroutines.withContext
import javax.inject.Inject
import javax.inject.Singleton

/** The two variants dating-service serves for a photo. */
enum class PhotoVariant(val wire: String) {
    FULL("full"),
    BLURRED("blurred"),
}

/**
 * Which photo a viewer is shown — the rule, in one place (D6).
 *
 * Someone you have not matched with is shown the BLURRED variant, whatever the
 * server's card says it would allow: the founder's rule is blurred until
 * matched. A match (and the owner) sees the full photo. The image routes need
 * the bearer token and answer 307 to a signed URL that lives 120 s, so the app
 * only ever keeps the gateway path, never the signed URL.
 */
object PhotoRules {

    private val PHOTO_PATH = Regex("^/v1/dating/photos/([^/?#]+)/(full|blurred)$")

    /** The photo id in a server path (`/v1/dating/photos/{id}/full|blurred`); null for anything else. */
    fun photoIdOf(path: String?): String? = path?.trim()?.let { PHOTO_PATH.matchEntire(it)?.groupValues?.get(1) }

    fun variantFor(matched: Boolean): PhotoVariant = if (matched) PhotoVariant.FULL else PhotoVariant.BLURRED

    fun pathFor(photoId: String, variant: PhotoVariant): String = "/v1/dating/photos/$photoId/${variant.wire}"

    /** The path a viewer loads for [serverPath]: blurred unless [matched]. Null when the path is not a dating photo. */
    fun viewerPath(serverPath: String?, matched: Boolean): String? =
        photoIdOf(serverPath)?.let { pathFor(it, variantFor(matched)) }

    /**
     * The variant a person card's `photo_state` allows.
     *
     * The server has already applied the D6 rule, so the app obeys it and never
     * upgrades a card to the full image. It FAILS CLOSED: only the exact word
     * `full` gives the full variant, so a blank, unknown or future state is
     * blurred rather than exposed.
     */
    fun variantForState(photoState: String?): PhotoVariant =
        if (photoState?.trim() == STATE_FULL) PhotoVariant.FULL else PhotoVariant.BLURRED

    /** The path a viewer loads for a person card. Null when there is no usable photo path. */
    fun statePath(serverPath: String?, photoState: String?): String? =
        photoIdOf(serverPath)?.let { pathFor(it, variantForState(photoState)) }

    const val STATE_FULL = "full"
    const val STATE_BLURRED = "blurred"
}

/** Resolves the gateway-relative photo paths against the API base URL. */
@Singleton
class DatingPhotoUrls @Inject constructor(private val config: ApiConfig) {

    /** Someone else's photo: blurred unless [matched]. */
    fun forViewer(serverPath: String?, matched: Boolean): String? =
        PhotoRules.viewerPath(serverPath, matched)?.let(::absolute)

    /** A person card's photo, in the variant its `photo_state` allows. */
    fun forPerson(person: DatingPersonDto?): String? =
        PhotoRules.statePath(person?.primaryPhotoUrl, person?.photoState)?.let(::absolute)

    /** The person's own photo, always the full variant. */
    fun own(photoId: String): String = absolute(PhotoRules.pathFor(photoId, PhotoVariant.FULL))

    private fun absolute(path: String): String = config.baseUrl.trimEnd('/') + path
}

sealed interface UploadOutcome {
    data class Ready(val mediaId: String) : UploadOutcome

    data class Failed(val message: String) : UploadOutcome
}

/** A picked photo → a media id dating-service can attach. A port so the photo step tests on the JVM. */
interface PhotoUploader {
    suspend fun upload(uri: String, onProgress: (Float) -> Unit): UploadOutcome
}

/**
 * A dating photo through `:core:media`'s [MediaUploader]: reserve → presigned
 * PUT → confirm, then a short wait for media-service to finish processing
 * (dating refuses a media id that is not ready with 409 PHOTO_MEDIA_NOT_READY).
 *
 * NO upload lease: dating-service references the media id itself, and a
 * composer lease would let media GC reclaim a photo on a live profile.
 */
class MediaPhotoUploader @Inject constructor(
    private val uploader: MediaUploader,
    private val resolver: MediaSourceResolver,
    @Dispatcher(UsDispatcher.IO) private val io: CoroutineDispatcher,
) : PhotoUploader {

    override suspend fun upload(uri: String, onProgress: (Float) -> Unit): UploadOutcome = withContext(io) {
        val picked = resolver.resolve(uri) ?: return@withContext UploadOutcome.Failed("That photo couldn't be read. Try another.")
        if (!picked.mimeType.startsWith("image/")) return@withContext UploadOutcome.Failed("Choose a photo (JPG or PNG).")
        if (picked.sizeBytes > MAX_BYTES) return@withContext UploadOutcome.Failed("That photo is larger than 10 MB. Choose a smaller one.")

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
        val confirmed = (uploader.confirm(reserved.mediaId) as? AppResult.Success)?.data
            ?: return@withContext UploadOutcome.Failed(GENERIC_FAILURE)
        if (confirmed.refused()) return@withContext UploadOutcome.Failed(REFUSED)
        awaitProcessed(uploader, reserved.mediaId, POLLS)?.let { return@withContext UploadOutcome.Failed(REFUSED) }
        UploadOutcome.Ready(reserved.mediaId)
    }

    private companion object {
        const val NO_LEASE = ""
        const val MAX_BYTES = 10L * 1024L * 1024L
        const val POLLS = 10
        const val GENERIC_FAILURE = "The photo didn't upload. Check your connection and try again."
        const val REFUSED = "That photo can't be used. Choose a clear photo of you."
    }
}

private fun com.us.android.core.media.upload.MediaAssetDto.refused(): Boolean =
    processingStatus == PROCESSING_REJECTED || processingStatus == PROCESSING_FAILED ||
        moderationStatus == MEDIA_MODERATION_REJECTED

/**
 * Polls media status about once a second until the asset is ready. Returns a
 * non-null refusal reason only when media-service refused it; a timeout returns
 * null and leaves the verdict to dating-service.
 */
internal suspend fun awaitProcessed(uploader: MediaUploader, mediaId: String, polls: Int): String? {
    repeat(polls) {
        val status = (uploader.status(mediaId) as? AppResult.Success)?.data
        when {
            status == null -> Unit
            status.processingStatus == PROCESSING_READY -> return null
            status.processingStatus == PROCESSING_REJECTED || status.processingStatus == PROCESSING_FAILED ||
                status.moderationStatus == MEDIA_MODERATION_REJECTED -> return status.processingStatus.ifBlank { "rejected" }
        }
        delay(POLL_MILLIS)
    }
    return null
}

private const val POLL_MILLIS = 1_000L
