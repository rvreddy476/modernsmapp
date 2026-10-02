package com.us.android.feature.live.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * live-service-v2 endpoints, through the gateway at `/v1/livestream`.
 *
 * The lifecycle is the server's, not ours: create reserves a room, start
 * returns the LiveKit publisher credentials and puts the stream in
 * `starting`, and only the host's first published track makes it `live`
 * (live-fix contract, 2026-10-01). Watching is a viewer token plus the same
 * server URL. Every response field is defaulted so a server-side addition
 * cannot break decoding.
 *
 * Moderation routes (remove, ban, moderators, report) answer with whatever
 * body the server chooses, or none: they return [Unit] so Retrofit discards
 * the body and a 204 and a 200 both succeed (see `noContentApiCall`).
 */
@Suppress("TooManyFunctions") // one function per live-service-v2 route
interface LiveApi {

    /**
     * Whether the signed-in user may go live, and what is still missing
     * (live-eligibility contract, 2026-10-02). Asked before the go-live form
     * is shown; the server still decides on create and start.
     */
    @GET("v1/livestream/eligibility")
    suspend fun eligibility(): ApiEnvelope<LiveEligibilityDto>

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

    /** Host, stream moderators or admin: hides the message for everyone (`chat.removed`). */
    @DELETE("v1/livestream/streams/{id}/chat/{messageId}")
    suspend fun removeChatMessage(@Path("id") id: String, @Path("messageId") messageId: String)

    /** Host or moderators: the user can no longer chat in, or join, this stream. */
    @POST("v1/livestream/streams/{id}/bans")
    suspend fun banUser(@Path("id") id: String, @Body body: BanUserRequest)

    @DELETE("v1/livestream/streams/{id}/bans/{userId}")
    suspend fun unbanUser(@Path("id") id: String, @Path("userId") userId: String)

    /** Host only. The WHOLE set, at most [MAX_STREAM_MODERATORS]; an empty list clears it. */
    @PUT("v1/livestream/streams/{id}/moderators")
    suspend fun setModerators(@Path("id") id: String, @Body body: SetModeratorsRequest)

    /** Any viewer. One report per reporter per target; the server rate-limits. */
    @POST("v1/livestream/streams/{id}/reports")
    suspend fun report(@Path("id") id: String, @Body body: LiveReportRequest)
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
    /** scheduled | starting | live | reconnecting | ended | failed. Read through [liveStatusOf]. */
    val status: String = "",
    val visibility: String = "",
    /** Current viewers, the host EXCLUDED by the server. Never derived on the client. */
    @SerialName("viewer_count") val viewerCount: Int = 0,
    /** The true maximum of [viewerCount] over the stream. */
    @SerialName("viewer_peak") val viewerPeak: Int = 0,
    /** Why an ended or failed stream stopped. Read through [endedReasonOf]. */
    @SerialName("ended_reason") val endedReason: String = "",
    @SerialName("status_changed_at") val statusChangedAt: String = "",
    /**
     * Present (possibly empty) ONLY when the reader is the host or one of the
     * stream's moderators; absent for everyone else. Its presence is how a
     * viewer learns they may moderate this chat.
     */
    @SerialName("moderator_user_ids") val moderatorUserIds: List<String>? = null,
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
    /**
     * Who wrote it (live-eligibility contract B, 2026-10-02). Absent on a row
     * from a server that does not hydrate authors yet; read through
     * [chatAuthorName], [chatRoleOf] and [isFoundingCreator], never directly.
     */
    val author: LiveChatAuthorDto? = null,
)

/**
 * A chat row's author. Only `user_id` and `role` are guaranteed: the name,
 * handle and avatar come from a directory lookup that may fail without
 * failing the message, and Go sends `""` and `null` for "nothing".
 */
@Serializable
data class LiveChatAuthorDto(
    @SerialName("user_id") val userId: String = "",
    val name: String = "",
    val handle: String = "",
    @SerialName("avatar_url") val avatarUrl: String = "",
    val badges: List<String> = emptyList(),
    /** host | moderator | viewer. Read through [chatRoleOf]. */
    val role: String = "",
)

/** `reason` is omitted when the host gave none. */
@Serializable
data class BanUserRequest(
    @SerialName("user_id") val userId: String,
    val reason: String? = null,
)

@Serializable
data class SetModeratorsRequest(
    @SerialName("user_ids") val userIds: List<String>,
)

/**
 * `reason` is a [LiveReportReason.wire]. `message_id` is present only when a
 * chat message is reported (absent = the stream itself); `note` only when
 * the viewer wrote one.
 */
@Serializable
data class LiveReportRequest(
    val reason: String,
    @SerialName("message_id") val messageId: String? = null,
    val note: String? = null,
)

/**
 * `GET /v1/livestream/eligibility`. [requirements] is computed in both
 * modes; [pilotOnly] is set for a user outside the pilot list while live is
 * in its closed pilot; [viewerCap] is present (non-zero) only while the
 * new-streamer cap applies. Read through [liveGateOf].
 */
@Serializable
data class LiveEligibilityDto(
    /** pilot | open. */
    val mode: String = "",
    val eligible: Boolean = false,
    val requirements: List<LiveRequirementDto> = emptyList(),
    @SerialName("pilot_only") val pilotOnly: Boolean = false,
    @SerialName("viewer_cap") val viewerCap: Int = 0,
)

/**
 * One requirement. [met] is true, false, or null when the server could not
 * check it right now. `account_age` carries [current] / [needed] / [unit];
 * `activity` carries [posts] and [followers]. Every other field is absent.
 */
@Serializable
data class LiveRequirementDto(
    val key: String = "",
    val met: Boolean? = null,
    val current: Int = 0,
    val needed: Int = 0,
    val unit: String = "",
    val posts: LiveProgressDto? = null,
    val followers: LiveProgressDto? = null,
)

@Serializable
data class LiveProgressDto(
    val current: Int = 0,
    val needed: Int = 0,
)
