package com.us.android.core.call.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path

/**
 * call-service's REST surface (`/v1/calls`, handler.go RegisterRoutes),
 * limited to what the 1:1 calling P0 uses. Field names mirror
 * `internal/service/responses.go` exactly — this client invents nothing.
 */
interface CallApi {

    @POST("v1/calls")
    suspend fun createCall(@Body body: CreateCallRequest): ApiEnvelope<CallDto>

    @GET("v1/calls/{callId}")
    suspend fun call(@Path("callId") callId: String): ApiEnvelope<CallDto>

    /**
     * The callee's discovery surface: pending invitations WITH invite ids
     * (accept/decline require them) — also how a push-tap cold start finds
     * the ringing call.
     */
    @GET("v1/calls/invites/pending")
    suspend fun pendingInvites(): ApiEnvelope<List<PendingInviteDto>>

    /** Marks the viewer joined and returns ICE servers for the engine. */
    @POST("v1/calls/{callId}/join")
    suspend fun join(@Path("callId") callId: String): ApiEnvelope<JoinDto>

    @POST("v1/calls/{callId}/invites/{inviteId}/accept")
    suspend fun acceptInvite(
        @Path("callId") callId: String,
        @Path("inviteId") inviteId: String,
    ): ApiEnvelope<StatusPayload>

    @POST("v1/calls/{callId}/invites/{inviteId}/decline")
    suspend fun declineInvite(
        @Path("callId") callId: String,
        @Path("inviteId") inviteId: String,
    ): ApiEnvelope<StatusPayload>

    @POST("v1/calls/{callId}/leave")
    suspend fun leave(@Path("callId") callId: String): ApiEnvelope<StatusPayload>

    @POST("v1/calls/{callId}/end")
    suspend fun end(@Path("callId") callId: String): ApiEnvelope<StatusPayload>

    @GET("v1/calls/history")
    suspend fun history(): ApiEnvelope<List<CallHistoryItemDto>>
}

@Serializable
data class CreateCallRequest(
    /** `direct_audio` or `direct_video` for the P0 surface. */
    @SerialName("call_type") val callType: String,
    /** Where the call started: `chat` from a thread, `profile` from one. */
    @SerialName("source_type") val sourceType: String,
    @SerialName("source_id") val sourceId: String? = null,
    @SerialName("target_user_ids") val targetUserIds: List<String>,
    @SerialName("audio_only") val audioOnly: Boolean,
    @SerialName("idempotency_key") val idempotencyKey: String = "",
)

@Serializable
data class CallDto(
    val id: String = "",
    @SerialName("call_type") val callType: String = "",
    @SerialName("initiator_user_id") val initiatorUserId: String = "",
    val state: String = "",
    @SerialName("audio_only") val audioOnly: Boolean = false,
    val participants: List<CallParticipantDto> = emptyList(),
    @SerialName("started_at") val startedAt: String? = null,
    @SerialName("ended_at") val endedAt: String? = null,
    @SerialName("ended_reason") val endedReason: String? = null,
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class CallParticipantDto(
    @SerialName("user_id") val userId: String = "",
    val role: String = "",
    @SerialName("invite_state") val inviteState: String = "",
    @SerialName("join_state") val joinState: String = "",
)

@Serializable
data class PendingInviteDto(
    @SerialName("invite_id") val inviteId: String = "",
    @SerialName("call_id") val callId: String = "",
    @SerialName("inviter_user_id") val inviterUserId: String = "",
    @SerialName("call_type") val callType: String = "",
    @SerialName("audio_only") val audioOnly: Boolean = false,
    @SerialName("call_state") val callState: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class JoinDto(
    @SerialName("call_id") val callId: String = "",
    @SerialName("ice_servers") val iceServers: List<IceServerDto> = emptyList(),
    @SerialName("reconnect_grace_seconds") val reconnectGraceSeconds: Int = 0,
)

@Serializable
data class IceServerDto(
    val urls: List<String> = emptyList(),
    val username: String? = null,
    val credential: String? = null,
)

@Serializable
data class CallHistoryItemDto(
    val id: String = "",
    @SerialName("call_type") val callType: String = "",
    @SerialName("initiator_user_id") val initiatorUserId: String = "",
    val state: String = "",
    @SerialName("audio_only") val audioOnly: Boolean = false,
    @SerialName("ended_reason") val endedReason: String? = null,
    @SerialName("duration_seconds") val durationSeconds: Int = 0,
    @SerialName("is_missed") val isMissed: Boolean = false,
    @SerialName("is_incoming") val isIncoming: Boolean = false,
    val participants: List<CallParticipantDto> = emptyList(),
    @SerialName("created_at") val createdAt: String = "",
)

@Serializable
data class StatusPayload(val status: String = "", val message: String = "")
