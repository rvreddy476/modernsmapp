package com.us.android.feature.tube.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path

/**
 * Watch progress — post-service's `/v1/videos/:videoId/progress` (Tube,
 * 2026-09-05). The save has been there since the video-series work; the
 * read is the contract agreed for Tube and may still be landing — a 404 on
 * it is "never watched", never an error the viewer sees.
 */
interface WatchProgressApi {

    @POST("v1/videos/{postId}/progress")
    suspend fun save(
        @Path("postId") postId: String,
        @Body body: WatchProgressRequest,
    ): ApiEnvelope<WatchProgressDto>

    @GET("v1/videos/{postId}/progress")
    suspend fun get(@Path("postId") postId: String): ApiEnvelope<WatchProgressDto>
}

/**
 * Every field always on the wire: `duration_ms` is `binding:"required"`
 * server-side, and a `position_ms` of 0 must say 0 rather than vanish —
 * the app's Json leaves `encodeDefaults` off, so no Kotlin defaults here.
 */
@Serializable
data class WatchProgressRequest(
    @SerialName("position_ms") val positionMs: Long,
    @SerialName("duration_ms") val durationMs: Long,
    val completed: Boolean,
    /**
     * When the report was taken, Unix milliseconds (lv-53, was LV-C-3).
     * post-service applies a report only if it is not older than the stored
     * one, so a late heartbeat or a retry cannot move the resume point back
     * or clear `completed`; a time ahead of the server's is clamped to now.
     */
    @SerialName("sent_at_ms") val sentAtMs: Long,
)

/** The stored row. `updated_at` is the agreed name; `last_watched_at` is what the store has carried. */
@Serializable
data class WatchProgressDto(
    @SerialName("position_ms") val positionMs: Long = 0L,
    @SerialName("duration_ms") val durationMs: Long = 0L,
    val completed: Boolean = false,
    @SerialName("updated_at") val updatedAt: String = "",
    @SerialName("last_watched_at") val lastWatchedAt: String = "",
)
