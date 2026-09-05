package com.us.android.core.feed.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * Channels (Tube, 2026-09-05): the identity a long video is posted under.
 * One per user. The contract as the server agent is building it:
 *
 *  - `POST v1/channels {name, handle, about?}` → 201 the channel;
 *    409 `CHANNEL_EXISTS` / `HANDLE_TAKEN`; 400 `INVALID_NAME` /
 *    `INVALID_HANDLE` / `INVALID_ABOUT`.
 *  - `GET v1/channels/me` → the channel, or 404 `NO_CHANNEL`.
 *  - `PATCH v1/channels/me` → the channel, updated.
 *  - `GET v1/channels/{handle_or_user_id}` → a channel with `video_count`.
 *  - `GET v1/channels/handle-available?handle=` → `{available, suggestion}`.
 *
 * `POST v1/posts` with a long video and no channel answers 403
 * `CHANNEL_REQUIRED`; that one is the publish pipeline's to read.
 */
interface ChannelApi {

    @POST("v1/channels")
    suspend fun create(@Body body: CreateChannelRequest): ApiEnvelope<ChannelDto>

    @GET("v1/channels/me")
    suspend fun me(): ApiEnvelope<ChannelDto>

    @PATCH("v1/channels/me")
    suspend fun update(@Body body: UpdateChannelRequest): ApiEnvelope<ChannelDto>

    /** [key] is a handle (without `@`) or a user id; the server accepts either. */
    @GET("v1/channels/{key}")
    suspend fun get(@Path("key") key: String): ApiEnvelope<ChannelDto>

    @GET("v1/channels/handle-available")
    suspend fun handleAvailable(@Query("handle") handle: String): ApiEnvelope<HandleAvailabilityDto>
}

@Serializable
data class ChannelDto(
    @SerialName("user_id") val userId: String = "",
    val name: String = "",
    val handle: String = "",
    val about: String = "",
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
    @SerialName("avatar_url") val avatarUrl: String? = null,
    @SerialName("video_count") val videoCount: Int = 0,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
)

@Serializable
data class HandleAvailabilityDto(
    val available: Boolean = false,
    /** The server's alternative when [available] is false; blank when it has none. */
    val suggestion: String = "",
)

/** `about` is omitted when blank — the server treats absent and empty alike, and absent is the smaller request. */
@Serializable
data class CreateChannelRequest(
    val name: String,
    val handle: String,
    val about: String? = null,
)

/** Every field optional: only what changed goes on the wire. */
@Serializable
data class UpdateChannelRequest(
    val name: String? = null,
    val handle: String? = null,
    val about: String? = null,
)
