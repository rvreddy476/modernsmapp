package com.us.android.core.call.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Calls over [AppResult]. Thin on purpose: the state machine belongs to
 * [com.us.android.core.call.CallSessionManager], eligibility belongs to the
 * SERVER (CallPolicy + the graph permission matrix). A refused create is a
 * POLICY answer surfaced generically — this layer never pre-judges who may
 * be called.
 */
@Singleton
class CallRepository @Inject constructor(
    private val api: CallApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun createDirectCall(
        targetUserId: String,
        video: Boolean,
        sourceConversationId: String?,
    ): AppResult<CallInfo> = apiCall(errorMapper) {
        api.createCall(
            CreateCallRequest(
                callType = if (video) "direct_video" else "direct_audio",
                sourceType = if (sourceConversationId != null) "chat" else "profile",
                sourceId = sourceConversationId,
                targetUserIds = listOf(targetUserId),
                audioOnly = !video,
                idempotencyKey = newIdempotencyKey(),
            ),
        )
    }.mapValue { it.toDomain() }

    suspend fun call(callId: String): AppResult<CallInfo> =
        apiCall(errorMapper) { api.call(callId) }.mapValue { it.toDomain() }

    suspend fun pendingInvites(): AppResult<List<PendingInvite>> =
        listApiCall(errorMapper) { api.pendingInvites() }.mapValue { rows ->
            rows.map {
                PendingInvite(
                    inviteId = it.inviteId,
                    callId = it.callId,
                    inviterUserId = it.inviterUserId,
                    video = it.callType.endsWith("video") && !it.audioOnly,
                    callState = it.callState,
                )
            }
        }

    suspend fun join(callId: String): AppResult<JoinGrant> =
        apiCall(errorMapper) { api.join(callId) }.mapValue { dto ->
            JoinGrant(
                iceServers = dto.iceServers.map {
                    IceServerConfig(it.urls, it.username, it.credential)
                },
            )
        }

    suspend fun acceptInvite(callId: String, inviteId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.acceptInvite(callId, inviteId) }.mapValue { }

    suspend fun declineInvite(callId: String, inviteId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.declineInvite(callId, inviteId) }.mapValue { }

    suspend fun leave(callId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.leave(callId) }.mapValue { }

    suspend fun end(callId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.end(callId) }.mapValue { }

    suspend fun history(): AppResult<List<CallHistoryItem>> =
        listApiCall(errorMapper) { api.history() }.mapValue { rows ->
            rows.map {
                CallHistoryItem(
                    id = it.id,
                    video = it.callType.endsWith("video"),
                    incoming = it.isIncoming,
                    missed = it.isMissed,
                    endedReason = it.endedReason.orEmpty(),
                    durationSeconds = it.durationSeconds,
                    peerUserId = it.participants
                        .firstOrNull { p -> p.userId != it.initiatorUserId }
                        ?.userId
                        .takeIf { _ -> !it.isIncoming }
                        ?: it.initiatorUserId,
                    createdAt = it.createdAt,
                )
            }
        }

    companion object {
        fun newIdempotencyKey(): String = UUID.randomUUID().toString()
    }
}

/** One live or historical call, as the state machine needs it. */
data class CallInfo(
    val id: String,
    val video: Boolean,
    val initiatorUserId: String,
    val state: String,
    val participantUserIds: List<String>,
)

data class PendingInvite(
    val inviteId: String,
    val callId: String,
    val inviterUserId: String,
    val video: Boolean,
    val callState: String,
)

data class JoinGrant(val iceServers: List<IceServerConfig>)

data class IceServerConfig(
    val urls: List<String>,
    val username: String? = null,
    val credential: String? = null,
)

data class CallHistoryItem(
    val id: String,
    val video: Boolean,
    val incoming: Boolean,
    val missed: Boolean,
    val endedReason: String,
    val durationSeconds: Int,
    val peerUserId: String,
    val createdAt: String,
)

internal fun CallDto.toDomain() = CallInfo(
    id = id,
    video = callType.endsWith("video") && !audioOnly,
    initiatorUserId = initiatorUserId,
    state = state,
    participantUserIds = participants.map { it.userId },
)

private inline fun <T, R> AppResult<T>.mapValue(block: (T) -> R): AppResult<R> = when (this) {
    is AppResult.Success -> AppResult.Success(block(data))
    is AppResult.Failure -> this
}
