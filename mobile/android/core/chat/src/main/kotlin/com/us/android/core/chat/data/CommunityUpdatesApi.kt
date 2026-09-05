package com.us.android.core.chat.data

import com.us.android.core.network.ApiEnvelope
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.HTTP
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * A community's updates — the half of `v1/broadcast-channels` that lives
 * under `/{id}/updates`: the admin's post, the newest-first page, one
 * reaction per viewer, the view ping, and the report. Split from
 * [CommunityApi] so each interface mirrors one shape: the channel, or what
 * is posted into it.
 */
interface CommunityUpdatesApi {

    /** Owner/admin only; 403 for everyone else. */
    @POST("v1/broadcast-channels/{id}/updates")
    suspend fun postUpdate(@Path("id") id: String, @Body body: PostUpdateRequest): ApiEnvelope<CommunityUpdateDto>

    /** Newest first. */
    @GET("v1/broadcast-channels/{id}/updates")
    suspend fun updates(
        @Path("id") id: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String?,
    ): ApiEnvelope<List<CommunityUpdateDto>>

    /** One reaction per viewer per update; PUT replaces. */
    @PUT("v1/broadcast-channels/{id}/updates/{updateId}/reaction")
    suspend fun react(
        @Path("id") id: String,
        @Path("updateId") updateId: String,
        @Body body: ReactRequest,
    )

    @HTTP(method = "DELETE", path = "v1/broadcast-channels/{id}/updates/{updateId}/reaction", hasBody = true)
    suspend fun unreact(
        @Path("id") id: String,
        @Path("updateId") updateId: String,
        @Body body: ReactRequest,
    )

    @POST("v1/broadcast-channels/{id}/updates/{updateId}/view")
    suspend fun view(@Path("id") id: String, @Path("updateId") updateId: String)

    @POST("v1/broadcast-channels/{id}/updates/{updateId}/report")
    suspend fun reportUpdate(
        @Path("id") id: String,
        @Path("updateId") updateId: String,
        @Body body: ReportRequest,
    )
}
