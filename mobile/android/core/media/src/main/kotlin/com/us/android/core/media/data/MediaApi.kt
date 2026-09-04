package com.us.android.core.media.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.GET
import retrofit2.http.Path

/**
 * Delivery details for a single asset.
 *
 * WHY THE CLIENT ASKS AT ALL
 *
 * The FEED hydrates media server-side, so a feed row needs no extra call. The
 * post-detail payload does not: it carries only `{media_id, kind}` with no
 * dimensions and no URLs. Rather than invent a URL from the id — which the
 * contract capture warns against explicitly — the client resolves it here.
 *
 * This is a detail screen, so it is one call for one post, not an N+1 inside a
 * scrolling list.
 */
interface MediaApi {

    /** Signed variants and, for video, an authorized HLS master path. */
    @GET("v1/media/{mediaId}/url")
    suspend fun getDelivery(@Path("mediaId") mediaId: String): ApiEnvelope<MediaDeliveryDto>
}

/**
 * Transcribed from a verbatim capture on 2026-08-18, not from handler source.
 *
 * Every field carries a default because delivery is partial by design: an
 * asset that is not finished processing arrives with a status and no variants,
 * and a missing key must not fail the whole response.
 */
@Serializable
data class MediaDeliveryDto(
    @SerialName("media_id") val mediaId: String = "",
    val kind: String = "",
    val status: String = "",
    val width: Int = 0,
    val height: Int = 0,
    val blurhash: String = "",
    val variants: Map<String, String> = emptyMap(),
    @SerialName("hls_url") val hlsUrl: String? = null,
    @SerialName("expires_at") val expiresAt: String? = null,
    // Instant reels (2026-09-04): the transcode's own state and the server's
    // choice of what to play while it runs — the original file.
    @SerialName("processing_status") val processingStatus: String = "",
    @SerialName("moderation_status") val moderationStatus: String = "",
    @SerialName("playback_url") val playbackUrl: String? = null,
    @SerialName("playback_kind") val playbackKind: String = "",
    /** The transcode's measured length; 0 for images and for older assets. */
    @SerialName("duration_ms") val durationMs: Long = 0L,
)
