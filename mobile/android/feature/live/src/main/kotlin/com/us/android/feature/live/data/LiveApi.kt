package com.us.android.feature.live.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * live-service-v2 endpoints, through the gateway at `/v1/livestream`.
 *
 * The lifecycle is the server's, not ours: create reserves a room, start
 * makes it live and returns the LiveKit publisher credentials, end closes
 * it. Watching is a viewer token plus the same server URL. Every field is
 * defaulted so a server-side addition cannot break decoding.
 */
interface LiveApi {

    @POST("v1/livestream/streams")
    suspend fun createStream(@Body body: CreateStreamRequest): ApiEnvelope<LiveStreamDto>

    @POST("v1/livestream/streams/{id}/start")
    suspend fun startStream(@Path("id") id: String): ApiEnvelope<StartStreamDto>

    @POST("v1/livestream/streams/{id}/end")
    suspend fun endStream(@Path("id") id: String): ApiEnvelope<EndStreamDto>

    @GET("v1/livestream/streams")
    suspend fun listLiveNow(@Query("limit") limit: Int = 20): ApiEnvelope<List<LiveStreamDto>>

    @GET("v1/livestream/streams/{id}")
    suspend fun getStream(@Path("id") id: String): ApiEnvelope<LiveStreamDto>

    @GET("v1/livestream/streams/{id}/viewer-token")
    suspend fun viewerToken(@Path("id") id: String): ApiEnvelope<ViewerTokenDto>

    @POST("v1/livestream/streams/{id}/chat")
    suspend fun sendChat(@Path("id") id: String, @Body body: SendChatRequest): ApiEnvelope<LiveChatMessageDto>

    @GET("v1/livestream/streams/{id}/chat")
    suspend fun listChat(@Path("id") id: String, @Query("limit") limit: Int = 50): ApiEnvelope<List<LiveChatMessageDto>>
}

@Serializable
data class CreateStreamRequest(
    val title: String,
    val visibility: String = "public",
)

@Serializable
data class LiveStreamDto(
    val id: String = "",
    @SerialName("creator_user_id") val creatorUserId: String = "",
    @SerialName("livekit_room") val livekitRoom: String = "",
    val title: String = "",
    val description: String = "",
    val status: String = "",
    val visibility: String = "",
    @SerialName("viewer_peak") val viewerPeak: Int = 0,
    @SerialName("started_at") val startedAt: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class StartStreamDto(
    val stream: LiveStreamDto = LiveStreamDto(),
    @SerialName("publisher_token") val publisherToken: String = "",
    val room: String = "",
    @SerialName("server_url") val serverUrl: String = "",
)

@Serializable
data class EndStreamDto(val status: String = "")

@Serializable
data class ViewerTokenDto(
    val token: String = "",
    val room: String = "",
    @SerialName("server_url") val serverUrl: String = "",
)

@Serializable
data class SendChatRequest(val text: String)

@Serializable
data class LiveChatMessageDto(
    val id: String = "",
    @SerialName("user_id") val userId: String = "",
    val text: String = "",
    @SerialName("is_pinned") val isPinned: Boolean = false,
    @SerialName("created_at") val createdAt: String = "",
)
