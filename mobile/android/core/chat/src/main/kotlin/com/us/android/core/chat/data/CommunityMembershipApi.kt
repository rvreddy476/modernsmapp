package com.us.android.core.chat.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.JsonElement
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path

/**
 * Who is in a community and how: join and leave, the viewer's mute, and
 * the owner's admin roster. Split from [CommunityApi] so each interface
 * mirrors one shape — the channel itself, its membership, its updates.
 */
interface CommunityMembershipApi {

    @POST("v1/broadcast-channels/{id}/subscribe")
    suspend fun subscribe(@Path("id") id: String)

    @DELETE("v1/broadcast-channels/{id}/subscribe")
    suspend fun unsubscribe(@Path("id") id: String)

    @PUT("v1/broadcast-channels/{id}/subscribe/mute")
    suspend fun mute(@Path("id") id: String, @Body body: MuteCommunityRequest)

    @DELETE("v1/broadcast-channels/{id}/subscribe/mute")
    suspend fun unmute(@Path("id") id: String)

    /** Rows are read raw: the admin row shape is the one part of this contract without a capture. */
    @GET("v1/broadcast-channels/{id}/admins")
    suspend fun admins(@Path("id") id: String): ApiEnvelope<JsonElement>

    @POST("v1/broadcast-channels/{id}/admins")
    suspend fun addAdmin(@Path("id") id: String, @Body body: CommunityAdminRequest)

    @DELETE("v1/broadcast-channels/{id}/admins/{userId}")
    suspend fun removeAdmin(@Path("id") id: String, @Path("userId") userId: String)
}
