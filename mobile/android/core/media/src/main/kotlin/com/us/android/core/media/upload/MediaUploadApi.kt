package com.us.android.core.media.upload

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path

/**
 * The three-step media upload, and nothing about what the media is FOR.
 *
 * `init` → presigned `PUT` → `confirm` is the platform's only standard upload
 * path, and it is identical whether the asset ends up on a post, a story, a
 * chat message or a profile. So it lives here once, in a product-neutral
 * module, rather than being copied into whichever feature needs it next. This
 * module deliberately does not know the word "post".
 *
 * The `PUT` is NOT declared here. It goes to a presigned object-store URL, not
 * to our API, and it must travel on the bare OkHttp client with no
 * `Authorization` header at all — see [PresignedUploader].
 */
interface MediaUploadApi {

    /**
     * Reserves a media row and returns the presigned URL to push bytes to.
     *
     * The row exists at `pending_upload` from this moment, which is what makes
     * an abandoned upload reclaimable by server GC rather than invisible.
     */
    @POST("v1/media/init")
    suspend fun init(@Body body: MediaInitRequest): ApiEnvelope<MediaInitDto>

    /**
     * Tells the server the bytes have landed.
     *
     * Owner-bound and idempotent server-side, so a client that is unsure
     * whether its confirm arrived may simply repeat it. Until this succeeds the
     * asset is not attachable to anything.
     */
    @POST("v1/media/confirm")
    suspend fun confirm(@Body body: MediaConfirmRequest): ApiEnvelope<MediaAssetDto>

    /**
     * Sets the final accessibility decision on an uploaded asset.
     *
     * Owner-authenticated server-side. See [MediaAltTextRequest] for why the
     * composer cannot supply this at `init` time.
     */
    @PATCH("v1/media/{mediaId}/alt-text")
    suspend fun updateAltText(
        @Path("mediaId") mediaId: String,
        @Body body: MediaAltTextRequest,
    ): ApiEnvelope<MediaStatusDto>

    /** Processing state, for polling an asset to `ready`. */
    @GET("v1/media/{mediaId}/status")
    suspend fun status(@Path("mediaId") mediaId: String): ApiEnvelope<MediaStatusDto>

    /**
     * Owner-authenticated delete, used when a creator discards their draft.
     *
     * Best-effort by design: correctness does not depend on it, because an app
     * that is uninstalled or killed will never make this call. Server GC is the
     * authority.
     */
    @DELETE("v1/media/{mediaId}")
    suspend fun delete(@Path("mediaId") mediaId: String): ApiEnvelope<MediaStatusDto>
}

/**
 * Transcribed from `media-service/internal/http/handler.go:127`.
 *
 * `@EncodeDefault` on [fileType] and [decorative] is LOAD-BEARING.
 * kotlinx.serialization omits a property equal to its default and the shared
 * `Json` leaves `encodeDefaults` off, so without these annotations:
 *
 *  - `file_type` — which the server binds `required,oneof=image video` —
 *    disappears and every init is a 400;
 *  - `decorative:false` disappears, and an image the creator described is
 *    indistinguishable on the wire from one they marked decorative.
 *
 * This exact defect shipped twice in Slice B (`SendMessageRequest.type`,
 * `ResendVerificationRequestDto.type`). `MediaUploadWireTest` pins the bytes.
 */
@Serializable
data class MediaInitRequest(
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("file_type") val fileType: String = FILE_TYPE_IMAGE,
    // Explicit: the subtype selects the server-side size limit (an avatar has a
    // different cap from a general image), so leaving it to a server default is
    // leaving a validation rule to something the client cannot see.
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("media_subtype") val mediaSubtype: String = SUBTYPE_GENERAL,
    @SerialName("mime_type") val mimeType: String,
    @SerialName("file_size_bytes") val fileSizeBytes: Long,
    /**
     * The creator's description. Empty is only legitimate alongside
     * `decorative = true`; the product rule that enforces that lives in the
     * feature, not here.
     */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("alt_text") val altText: String = "",
    /**
     * Explicitly false rather than omitted: "this image carries no
     * information" is a claim, and its absence must not be guessed.
     */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val decorative: Boolean = false,
    /**
     * The lease naming the surface that created this upload.
     *
     * Explicitly encoded: an omitted lease leaves the column NULL server-side,
     * and a NULL lease means the asset is NEVER a candidate for confirmed
     * reclamation. For the composer that is a storage leak — the abandoned
     * photo is kept forever — so this must actually reach the wire.
     */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("upload_purpose") val uploadPurpose: String = "",
)

/** Response of `init`. `upload_url` is presigned and short-lived. */
@Serializable
data class MediaInitDto(
    @SerialName("media_id") val mediaId: String = "",
    @SerialName("upload_url") val uploadUrl: String = "",
    @SerialName("object_key") val objectKey: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class MediaConfirmRequest(
    @SerialName("media_id") val mediaId: String,
)

/**
 * The asset row after confirmation.
 *
 * Every field defaults because the response is partial by design while
 * processing runs, and a missing key must not fail the whole decode.
 */
@Serializable
data class MediaAssetDto(
    val id: String = "",
    @SerialName("uploader_id") val uploaderId: String = "",
    @SerialName("file_type") val fileType: String = "",
    @SerialName("processing_status") val processingStatus: String = "",
    @SerialName("moderation_status") val moderationStatus: String = "",
)

@Serializable
data class MediaStatusDto(
    @SerialName("media_id") val mediaId: String = "",
    val status: String = "",
    @SerialName("processing_status") val processingStatus: String = "",
    @SerialName("moderation_status") val moderationStatus: String = "",
)

const val FILE_TYPE_IMAGE = "image"
const val FILE_TYPE_VIDEO = "video"
const val SUBTYPE_GENERAL = "general"
const val SUBTYPE_AVATAR = "avatar"
const val SUBTYPE_COVER = "cover"

/** Terminal-good processing state. Nothing may be attached before this. */
const val PROCESSING_READY = "ready"

/** Terminal-bad. A rejected asset can never become publishable. */
const val PROCESSING_REJECTED = "rejected"

/** Terminal-bad processing outcome: the asset can never become publishable. */
const val PROCESSING_FAILED = "failed"

/** The only moderation verdict that permits publication. */
const val MEDIA_MODERATION_PASSED = "passed"

/** Terminal-bad moderation verdict. */
const val MEDIA_MODERATION_REJECTED = "rejected"

/**
 * The composer's upload lease — Slice C, C-P0-4.
 *
 * Sent on `init` so the server can later reclaim this asset if the user
 * abandons it. Without the lease an abandoned composer image is stored forever,
 * because confirmed-media reclamation is scoped to leased uploads only — a
 * global sweep over "old and apparently unreferenced" would delete avatars and
 * channel banners whose owning rows carry no foreign key.
 */
const val UPLOAD_PURPOSE_COMPOSER = "composer"

/**
 * Updates the accessibility decision after the fact.
 *
 * REQUIRED by the composer's ordering, not a convenience. The user picks a
 * photo, the upload starts immediately so Post is not gated later, and only
 * then do they type a description or mark it decorative. Whatever `init`
 * carried is therefore a placeholder, and this is what makes the value the
 * server keeps match the value the composer required and displayed.
 */
@Serializable
data class MediaAltTextRequest(
    @SerialName("alt_text") val altText: String,
    /**
     * Explicitly encoded. `decorative:false` omitted is not "false" — it is
     * "unspecified", and a described image would be indistinguishable on the
     * wire from one marked as carrying no information.
     */
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val decorative: Boolean = false,
)
