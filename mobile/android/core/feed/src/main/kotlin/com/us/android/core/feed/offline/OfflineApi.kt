package com.us.android.core.feed.offline

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Offline copies (2026-10-02): post-service's four routes, path for path
 * (`internal/http/offline_copies.go`).
 *
 * founder, 2026-10-02: "Keep a copy is not direct download. It should be
 * like to see offline in the app only." A viewer never receives a file: the
 * server GRANTS this device a copy, the app stores it privately, and the
 * server is asked again whether the copy may still be kept.
 *
 * Every call names this install with `device_id`: a copy is granted per
 * device, and checking or removing one device's copies must not touch
 * another's.
 */
interface OfflineApi {

    /**
     * Grants this device a copy and answers what to store: the rendition's
     * `serve` path, the caption tracks, the reel's added sound. Idempotent
     * per device; a repeat refreshes the expiry. 403 `OFFLINE_NOT_ALLOWED`,
     * 409 `NOT_READY`, 409 `OFFLINE_LIMIT`, 404 for a post the viewer may
     * not watch.
     */
    @POST("v1/posts/{postId}/offline")
    suspend fun grant(
        @Path("postId") postId: String,
        @Body body: OfflineDeviceRequest,
    ): ApiEnvelope<OfflineGrantDto>

    /** Which of this device's copies may still be kept. At most 100 ids; never extends an expiry. */
    @POST("v1/posts/offline/check")
    suspend fun check(@Body body: OfflineCheckRequest): ApiEnvelope<List<OfflineCheckDto>>

    /** The copies the server holds for this device: the grant's card without the media path. */
    @GET("v1/posts/offline")
    suspend fun list(@Query("device_id") deviceId: String): ApiEnvelope<List<OfflineGrantDto>>

    /**
     * Gives the copy up. Idempotent. The device travels in the query: a
     * DELETE with a body is dropped by some proxies, and the route takes
     * either. The answer's body is not read; a 2xx is the signal.
     */
    @DELETE("v1/posts/{postId}/offline")
    suspend fun remove(
        @Path("postId") postId: String,
        @Query("device_id") deviceId: String,
    )

    companion object {
        /** The server's bound on one check. */
        const val MAX_CHECK_IDS = 100
    }
}

@Serializable
data class OfflineDeviceRequest(@SerialName("device_id") val deviceId: String)

@Serializable
data class OfflineCheckRequest(
    @SerialName("device_id") val deviceId: String,
    @SerialName("post_ids") val postIds: List<String>,
)

/**
 * The grant's card, and each row of the list (which carries no
 * `media.path`). Every field defaults: the app reads what is there
 * ([toGrant]) and treats absent, null, `""` and `0` alike as "not said".
 */
@Serializable
data class OfflineGrantDto(
    @SerialName("post_id") val postId: String = "",
    /** `long_video`, or `flick` for a reel. */
    @SerialName("content_type") val contentType: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
    @SerialName("recheck_after_seconds") val recheckAfterSeconds: Long = 0L,
    val title: String = "",
    @SerialName("channel_name") val channelName: String = "",
    @SerialName("duration_ms") val durationMs: Long = 0L,
    @SerialName("poster_path") val posterPath: String = "",
    val media: OfflineMediaDto? = null,
    val captions: List<OfflineCaptionDto>? = null,
    val sound: OfflineSoundDto? = null,
)

@Serializable
data class OfflineMediaDto(
    @SerialName("media_id") val mediaId: String = "",
    val variant: String = "",
    /** Gateway-relative `serve` path. Absent on a list row. */
    val path: String = "",
    val mime: String = "",
    /** The exact length when present; 0 when the server did not say. */
    @SerialName("size_bytes") val sizeBytes: Long = 0L,
)

@Serializable
data class OfflineCaptionDto(
    val lang: String = "",
    val label: String = "",
    /** Serves WebVTT. */
    val path: String = "",
)

/** A reel's added sound. The two volumes are nullable: an absent level is full, a present 0 is muted. */
@Serializable
data class OfflineSoundDto(
    val path: String = "",
    val mime: String = "",
    @SerialName("size_bytes") val sizeBytes: Long = 0L,
    @SerialName("start_ms") val startMs: Long = 0L,
    @SerialName("original_volume") val originalVolume: Double? = null,
    @SerialName("overlay_volume") val overlayVolume: Double? = null,
)

/** One answer of a check: `valid` with the expiry, or not valid with why. */
@Serializable
data class OfflineCheckDto(
    @SerialName("post_id") val postId: String = "",
    val valid: Boolean = false,
    @SerialName("expires_at") val expiresAt: String = "",
    /** `deleted`, `private`, `not_allowed`, `expired`, `blocked`, `revoked` or `unknown`. */
    val reason: String = "",
    @SerialName("content_type") val contentType: String = "",
)
