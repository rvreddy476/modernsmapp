package com.us.android.core.chat.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.pagedApiCall
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Chat reads and writes, wrapped in [AppResult].
 *
 * Thin on purpose: ordering, retry and draft handling belong to the
 * controllers above it, and eligibility belongs to the server. This layer
 * exists so screens never touch Retrofit or the envelope directly.
 */
@Singleton
class ChatRepository @Inject constructor(
    private val api: ChatApi,
    private val errorMapper: ErrorMapper,
) {

    suspend fun conversations(): AppResult<List<Conversation>> =
        apiCall(errorMapper) { api.conversations() }
            .mapList { it.toDomain() }

    suspend fun conversation(conversationId: String): AppResult<Conversation> =
        apiCall(errorMapper) { api.conversation(conversationId) }
            .mapValue { it.toDomain() }

    /**
     * Opens the 1:1 conversation with [otherUserId] under a caller-supplied key.
     *
     * A denial here is a POLICY answer, not a client bug: graph-service decides
     * who may message whom, and the failure surfaces so the UI can say so
     * rather than retrying into a wall.
     *
     * The key is a PARAMETER, for the same reason [send]'s is. This method used
     * to mint one per call, which made every retry a fresh intent: a request
     * that reached the server and lost its response would create a second
     * conversation on retry, and the user would end up with two threads to the
     * same person and their history split between them. Opening a chat is one
     * intent, so it gets one key — [StartDirectController] owns it.
     */
    suspend fun createDirect(
        otherUserId: String,
        idempotencyKey: String,
    ): AppResult<Conversation> =
        apiCall(errorMapper) {
            api.createDirect(idempotencyKey, CreateDirectRequest(otherUserId))
        }.mapValue { it.toDomain() }

    suspend fun createGroup(title: String, memberIds: List<String>): AppResult<Conversation> =
        apiCall(errorMapper) {
            api.createGroup(newIdempotencyKey(), CreateGroupRequest(title, memberIds))
        }.mapValue { it.toDomain() }

    suspend fun messages(
        conversationId: String,
        cursor: String? = null,
        limit: Int = MESSAGE_PAGE_SIZE,
    ): AppResult<Paged<Message>> =
        pagedApiCall(errorMapper) { api.messages(conversationId, limit, cursor) }
            .mapValue { page -> Paged(page.items.map { it.toDomain() }, page.nextCursor) }

    /**
     * Sends one message under a caller-supplied key.
     *
     * The key is a parameter rather than generated here because a RETRY must
     * present the same one: the server replays the original message for a
     * repeated key, which is what stops a lost response becoming two messages.
     * Minting it inside would make every retry a new intent.
     */
    suspend fun send(
        conversationId: String,
        text: String,
        idempotencyKey: String,
    ): AppResult<Message> =
        apiCall(errorMapper) {
            api.send(conversationId, idempotencyKey, SendMessageRequest(text = text))
        }.mapValue { it.toDomain() }

    suspend fun markRead(conversationId: String, messageId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.markRead(conversationId, MarkReadRequest(messageId)) }
            .mapValue { }

    suspend fun setTyping(conversationId: String, typing: Boolean): AppResult<Unit> =
        apiCall(errorMapper) { api.setTyping(conversationId, TypingRequest(typing)) }
            .mapValue { }

    suspend fun presence(conversationId: String): AppResult<Presence> =
        apiCall(errorMapper) { api.presence(conversationId) }
            .mapValue { Presence(it.activeCount, it.isBigGroup) }

    companion object {
        const val MESSAGE_PAGE_SIZE = 30

        fun newIdempotencyKey(): String = UUID.randomUUID().toString()
    }
}

private inline fun <T, R> AppResult<T>.mapValue(block: (T) -> R): AppResult<R> = when (this) {
    is AppResult.Success -> AppResult.Success(block(data))
    is AppResult.Failure -> this
}

private inline fun <T, R> AppResult<List<T>>.mapList(block: (T) -> R): AppResult<List<R>> =
    mapValue { list -> list.map(block) }

/** One inbox row / thread header. */
data class Conversation(
    val id: String,
    val type: String,
    val title: String?,
    val isRequest: Boolean,
    val members: List<ConversationMember>,
    val updatedAt: String,
) {
    /**
     * What to show as the thread's name.
     *
     * A direct conversation has no title, so it is named after the OTHER
     * participant. Falls back to a generic label rather than showing a raw id,
     * which is never a name a person recognises.
     */
    fun displayTitle(viewerId: String): String {
        title?.takeIf { it.isNotBlank() }?.let { return it }
        val other = members.firstOrNull { it.userId != viewerId }
        return other?.displayName?.takeIf { it.isNotBlank() } ?: "Conversation"
    }
}

data class ConversationMember(
    val userId: String,
    val role: String,
    val displayName: String,
)

data class Message(
    val id: String,
    val conversationId: String,
    val senderId: String,
    val senderDisplayName: String?,
    val text: String,
    val createdAt: String,
    /** True while the message is shown optimistically, before the server ack. */
    val pending: Boolean = false,
    /** Set when the send failed and the row is offered for retry. */
    val failed: Boolean = false,
)

data class Presence(val activeCount: Int, val isBigGroup: Boolean)

private fun ConversationDto.toDomain() = Conversation(
    id = id,
    type = type,
    title = title,
    isRequest = isRequest,
    members = members.map { ConversationMember(it.userId, it.role, it.displayName) },
    updatedAt = updatedAt,
)

private fun MessageDto.toDomain() = Message(
    id = msgId,
    conversationId = conversationId,
    senderId = senderId,
    senderDisplayName = senderDisplayName,
    text = text,
    createdAt = createdAt.ifBlank { ts },
)
