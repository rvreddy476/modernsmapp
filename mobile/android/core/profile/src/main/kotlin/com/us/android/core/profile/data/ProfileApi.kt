package com.us.android.core.profile.data

import com.us.android.core.network.ApiEnvelope
import com.us.android.core.profile.data.dto.GraphStatusDto
import com.us.android.core.profile.data.dto.GraphUserIdRequest
import com.us.android.core.profile.data.dto.OwnProfileDto
import com.us.android.core.profile.data.dto.ProfileMediaUpdateDto
import com.us.android.core.profile.data.dto.ProfileStatsDto
import com.us.android.core.profile.data.dto.PublicProfileDto
import com.us.android.core.profile.data.dto.RelationshipDto
import com.us.android.core.profile.data.dto.UpdateMediaIdRequest
import com.us.android.core.profile.data.dto.UpdateProfileRequest
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.HTTP
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

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
 * Every path here was exercised live through the API gateway at
 * `http://localhost:8080` — the reads and graph mutations on 2026-08-16, and
 * `PUT /v1/profiles/me` on the 2026-08-17 repair recapture; see
 * prompt/android-api-contracts.md §5.
 */
@Suppress("TooManyFunctions") // Mirrors the profile + graph route surface one-to-one.
interface ProfileApi {

    /** Public projection. Succeeds anonymously; 404 `NOT_FOUND` when absent. */
    @GET("v1/profiles/{userId}")
    suspend fun getProfile(@Path("userId") userId: String): ApiEnvelope<PublicProfileDto>

    /** Owner projection. Requires the access JWT. */
    @GET("v1/profiles/me")
    suspend fun getOwnProfile(): ApiEnvelope<OwnProfileDto>

    /**
     * Full replacement of the owner's editable fields. Returns the saved
     * owner projection.
     *
     * NO CSRF HEADER. The 2026-08-17 repair settled the open question that
     * previously kept this endpoint unimplemented: a validated bearer token is
     * a non-ambient credential, so bearer-only writes intentionally bypass CSRF
     * and native clients neither persist nor rotate the CSRF cookie. The
     * server's decision is based on which credential passed validation, not on
     * header presence — a cookie-authenticated write with
     * `X-Client-Platform: android` and no CSRF pair still returned
     * `CSRF_FAILED`, and an invalid bearer with the same header still returned
     * `401`. The required headers are exactly `Authorization` and
     * `Content-Type`, both supplied by `:core:network`.
     *
     * REPLACEMENT, NOT PATCH. `{}` returns `200` and clears `display_name`,
     * `category`, `profession` and `location`. That is why the body type is
     * [UpdateProfileRequest], whose properties have no Kotlin defaults, and
     * why the repository accepts only a complete `EditableProfile` snapshot.
     */
    @PUT("v1/profiles/me")
    suspend fun updateProfile(@Body body: UpdateProfileRequest): ApiEnvelope<OwnProfileDto>

    @PUT("v1/profiles/me/avatar")
    suspend fun updateAvatar(@Body body: UpdateMediaIdRequest): ApiEnvelope<ProfileMediaUpdateDto>

    @PUT("v1/profiles/me/cover")
    suspend fun updateCover(@Body body: UpdateMediaIdRequest): ApiEnvelope<ProfileMediaUpdateDto>

    /** Counts, including the two the profile payload does not carry. */
    @GET("v1/profiles/{userId}/stats")
    suspend fun getStats(@Path("userId") userId: String): ApiEnvelope<ProfileStatsDto>

    /**
     * The viewer's edges toward one other account — the truth the profile
     * header renders. `user_id` is the VIEWER (this endpoint reads query
     * params, not the gateway identity header), `other_id` the profile.
     */
    @GET("v1/graph/relationship")
    suspend fun relationship(
        @Query("user_id") userId: String,
        @Query("other_id") otherId: String,
    ): ApiEnvelope<RelationshipDto>

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
}
