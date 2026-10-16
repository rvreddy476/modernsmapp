package com.us.android.feature.tube.data.hub

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.JsonElement
import retrofit2.http.GET
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * The Creator Hub's reads (T-31, 2026-10-15) — the same four routes the web
 * Creator Hub reads (`hubApi.ts`), and nothing that writes:
 *
 *   GET /v1/analytics/creator/me?period          analytics-service; the stats object is the
 *                                                BODY (no envelope), so it is read raw and
 *                                                unwrapped the way the web does (`data ?? body`)
 *   GET /v1/uploads/videos|flicks?limit&cursor   post-service my_uploads.go
 *   GET /v1/reels/drafts?limit&cursor            post-service drafts.go
 *   GET /v1/comments/inbox?status&content&sort&limit&cursor   post-service comment_creator.go
 *
 * Rows come back as [JsonElement] on purpose: the normalisers in
 * `CreatorHubModels.kt` are ports of the web's, with the same fall-throughs
 * (Go zero values, older field names), and they need the raw shape to make
 * the same choices. A typed DTO would decide some of them silently.
 */
interface CreatorHubApi {

    @GET("v1/analytics/creator/me")
    suspend fun creatorInsights(@Query("period") period: String): JsonElement

    /** [kind] is `videos` or `flicks` ([HubLibraryKind.wire]). */
    @GET("v1/uploads/{kind}")
    suspend fun uploads(
        @Path("kind") kind: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<JsonElement>>

    @GET("v1/reels/drafts")
    suspend fun drafts(
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<JsonElement>>

    @Suppress("LongParameterList") // the route's own five query parameters
    @GET("v1/comments/inbox")
    suspend fun inbox(
        @Query("status") status: String,
        @Query("content") content: String,
        @Query("sort") sort: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<JsonElement>>
}
