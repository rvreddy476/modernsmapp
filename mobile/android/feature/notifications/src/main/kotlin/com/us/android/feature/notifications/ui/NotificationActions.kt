package com.us.android.feature.notifications.ui

import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.common.result.AppResult
import com.us.android.core.common.result.map
import com.us.android.core.model.NotificationAddress
import com.us.android.core.model.SessionState
import com.us.android.core.notifications.data.NotificationDeletes
import com.us.android.core.profile.data.ConnectionsSource
import com.us.android.core.profile.data.ProfileRepository
import kotlinx.coroutines.async
import kotlinx.coroutines.awaitAll
import kotlinx.coroutines.coroutineScope
import javax.inject.Inject

/**
 * What a notification row can DO — the port the inbox acts through.
 *
 * An interface rather than the two repositories directly so the ViewModel
 * tests can fake six calls instead of standing up a `ChatApi` with forty
 * members. The server stays authoritative: every answer here is the server's.
 */
interface NotificationActions {
    /** Conversations still waiting on this user's decision. */
    suspend fun pendingRequestIds(): AppResult<Set<String>>

    /**
     * Which of [actorIds] the viewer already follows. Best-effort: a lookup
     * that fails is simply absent, and the row offers Follow — the server
     * treats a repeated follow as a no-op, so the worst case is a redundant
     * tap, never a wrong edge.
     */
    suspend fun alreadyFollowing(actorIds: Set<String>): Set<String>

    suspend fun follow(userId: String): AppResult<Unit>
    suspend fun acceptRequest(conversationId: String): AppResult<Unit>
    suspend fun declineRequest(conversationId: String): AppResult<Unit>
    suspend fun blockRequest(conversationId: String): AppResult<Unit>

    /** Approves an incoming follow request — a private account's owner only. */
    suspend fun acceptFollowRequest(requesterId: String): AppResult<Unit>
    suspend fun declineFollowRequest(requesterId: String): AppResult<Unit>

    /** Who has a connection request to the viewer still waiting (graph `GET /v1/graph/connection-requests`). */
    suspend fun pendingConnectionSenders(): AppResult<Set<String>>

    /** Answers a connection request; [senderId] is the row's actor — the person who asked. */
    suspend fun acceptConnectionRequest(senderId: String): AppResult<Unit>
    suspend fun declineConnectionRequest(senderId: String): AppResult<Unit>

    /** Deletes one notification by its address (2026-10-24, `DELETE /v1/notifications/:bucket/:ts`). */
    suspend fun delete(address: NotificationAddress): AppResult<Unit>
}

class DefaultNotificationActions @Inject constructor(
    private val chat: ChatRepository,
    private val profiles: ProfileRepository,
    private val session: SessionStateProvider,
    private val connections: ConnectionsSource,
    private val deletes: NotificationDeletes,
) : NotificationActions {

    /**
     * Everything `GET /v1/chat/requests` returns IS a pending request: the
     * server filters on `is_request = TRUE AND request_declined_at IS NULL`
     * but does not echo the flag back (it scans the conversation columns
     * only), so filtering on `isRequest` here would drop every row.
     */
    override suspend fun pendingRequestIds(): AppResult<Set<String>> =
        when (val result = chat.requests()) {
            is AppResult.Success -> AppResult.Success(result.data.mapTo(mutableSetOf()) { it.id })
            is AppResult.Failure -> result
        }

    override suspend fun alreadyFollowing(actorIds: Set<String>): Set<String> {
        val viewer = (session.sessionState.value as? SessionState.Authenticated)?.userId
            ?: return emptySet()
        return coroutineScope {
            actorIds.take(MAX_RELATIONSHIP_LOOKUPS)
                .map { id -> async { id to profiles.relationship(viewer, id) } }
                .awaitAll()
                .filter { (_, rel) -> rel is AppResult.Success && rel.data.isFollowing }
                .mapTo(mutableSetOf()) { it.first }
        }
    }

    // The row cannot tell "followed" from "requested" apart — and does not
    // need to; a follow-back from the inbox settles on whichever the server
    // decided, and the row just needs to know the write succeeded.
    override suspend fun follow(userId: String): AppResult<Unit> = profiles.follow(userId).map { }

    override suspend fun acceptRequest(conversationId: String): AppResult<Unit> =
        chat.acceptRequest(conversationId)

    override suspend fun declineRequest(conversationId: String): AppResult<Unit> =
        chat.declineRequest(conversationId)

    override suspend fun blockRequest(conversationId: String): AppResult<Unit> =
        chat.blockRequest(conversationId)

    override suspend fun acceptFollowRequest(requesterId: String): AppResult<Unit> =
        profiles.acceptFollowRequest(requesterId)

    override suspend fun declineFollowRequest(requesterId: String): AppResult<Unit> =
        profiles.declineFollowRequest(requesterId)

    override suspend fun pendingConnectionSenders(): AppResult<Set<String>> = connections.pendingSenderIds()

    override suspend fun acceptConnectionRequest(senderId: String): AppResult<Unit> = connections.accept(senderId)

    override suspend fun declineConnectionRequest(senderId: String): AppResult<Unit> = connections.decline(senderId)

    override suspend fun delete(address: NotificationAddress): AppResult<Unit> = deletes.delete(address)

    private companion object {
        /** One page of follow notifications, not the whole graph. */
        const val MAX_RELATIONSHIP_LOOKUPS = 20
    }
}
