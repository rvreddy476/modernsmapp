package com.us.android.feature.profile.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.profile.data.dto.GraphStatusDto
import com.us.android.feature.profile.data.dto.GraphUserIdRequest
import com.us.android.feature.profile.data.dto.OwnProfileDto
import com.us.android.feature.profile.data.dto.ProfileStatsDto
import com.us.android.feature.profile.data.dto.PublicProfileDto
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.HTTP
import retrofit2.http.POST
import retrofit2.http.Path

/**
 * Profile and relationship endpoints.
 *
 * This is a Retrofit *interface*, not a client. It is created from the single
 * app-wide `Retrofit` instance built in `:core:network`, so it inherits the
 * base URL, the auth interceptor, single-flight token refresh, tracing, retry
 * policy and error mapping. No feature may construct its own OkHttp or
 * Retrofit — that would fork the refresh logic, and two refreshers racing a
 * rotating refresh token log the user out.
 *
 * Every path here was exercised live on 2026-08-16 through the API gateway at
 * `http://localhost:8080`; see prompt/android-api-contracts.md.
 */
interface ProfileApi {

    /** Public projection. Succeeds anonymously; 404 `NOT_FOUND` when absent. */
    @GET("v1/profiles/{userId}")
    suspend fun getProfile(@Path("userId") userId: String): ApiEnvelope<PublicProfileDto>

    /** Owner projection. Requires the access JWT. */
    @GET("v1/profiles/me")
    suspend fun getOwnProfile(): ApiEnvelope<OwnProfileDto>

    /** Counts, including the two the profile payload does not carry. */
    @GET("v1/profiles/{userId}/stats")
    suspend fun getStats(@Path("userId") userId: String): ApiEnvelope<ProfileStatsDto>

    @POST("v1/graph/follow")
    suspend fun follow(@Body body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto>

    /**
     * Removing a follow is a POST to a different path, NOT `DELETE /follow`.
     *
     * The capture probed `DELETE /v1/graph/follow` directly and the router
     * answered a plain-text `404 page not found` — the route is not
     * registered. Do not "tidy" this into a DELETE to match block/unblock
     * below; the asymmetry is real and lives in the server's route table.
     */
    @POST("v1/graph/unfollow")
    suspend fun unfollow(@Body body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto>

    @POST("v1/graph/block")
    suspend fun block(@Body body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto>

    /**
     * Unblock genuinely is `DELETE`, and it carries a body.
     *
     * `@HTTP(hasBody = true)` rather than `@DELETE`: Retrofit's `@DELETE` has
     * no body parameter, and the server requires `{"user_id": ...}` — the same
     * validation error appears without it.
     */
    @HTTP(method = "DELETE", path = "v1/graph/block", hasBody = true)
    suspend fun unblock(@Body body: GraphUserIdRequest): ApiEnvelope<GraphStatusDto>

    // Deliberately absent: GET /v1/graph/blocked-and-muted.
    //
    // It takes an arbitrary `user_id` query parameter, requires no
    // authentication, and returns that account's block and mute list to any
    // caller. It also bypasses the standard envelope, returning bare
    // {"user_ids":[]}. Wiring it would make this client the consumer of a
    // privacy hole. Relationship state is instead tracked from actions this
    // device performed until the backend provides a viewer-scoped route.
    //
    // Also absent: PUT /v1/profiles/me. It requires the login-issued CSRF
    // cookie plus a matching X-CSRF-Token header, and it is a full
    // replacement — an empty body clears display_name, category, profession
    // and location. Editing needs the complete editable snapshot and a
    // decision on whether native clients carry CSRF cookies at all, which is
    // an open question in the capture's blocker list.
}
