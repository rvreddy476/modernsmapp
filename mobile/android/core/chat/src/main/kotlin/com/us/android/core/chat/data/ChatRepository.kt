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
@Suppress("TooManyFunctions") // Thin mapping layer over ChatApi; grows with the route surface.
class ChatRepository @Inject constructor(
    private val api: ChatApi,
    private val errorMapper: ErrorMapper,
) {

    /**
     * The FULL inbox, paged to the server cap and followed to exhaustion
     * (bounded). The previous single unparameterised call silently truncated
     * the inbox to the server's default page of 20.
     */
    suspend fun conversations(): AppResult<List<Conversation>> {
        val all = mutableListOf<Conversation>()
        var cursor: String? = null
        repeat(MAX_CONVERSATION_PAGES) {
            when (val page = pagedApiCall(errorMapper) { api.conversations(cursor = cursor) }) {
                is AppResult.Failure ->
                    // A failed FIRST page is a failed sync; a failed later page
                    // returns what we have — a partial inbox beats none.
                    return if (all.isEmpty()) page else AppResult.Success(all)
                is AppResult.Success -> {
                    all += page.data.items.map { it.toDomain() }
                    cursor = page.data.nextCursor ?: return AppResult.Success(all)
                }
            }
        }
        return AppResult.Success(all)
    }

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

    // ── Production chat pass: requests, groups, invitations, media ─────

    /** Sends a MEDIA message: the asset must already be uploaded and ready. */
    suspend fun sendMedia(
        conversationId: String,
        mediaId: String,
        idempotencyKey: String,
        caption: String = "",
    ): AppResult<Message> =
        apiCall(errorMapper) {
            api.send(
                conversationId,
                idempotencyKey,
                SendMessageRequest(type = MEDIA_MESSAGE, text = caption, mediaId = mediaId),
            )
        }.mapValue { it.toDomain() }

    suspend fun requests(): AppResult<List<Conversation>> =
        apiCall(errorMapper) { api.requests() }.mapList { it.toDomain() }

    suspend fun acceptRequest(conversationId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.acceptRequest(conversationId) }.mapValue { }

    suspend fun declineRequest(conversationId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.declineRequest(conversationId) }.mapValue { }

    suspend fun blockRequest(conversationId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.blockRequest(conversationId) }.mapValue { }

    suspend fun reportRequest(conversationId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.reportRequest(conversationId) }.mapValue { }

    suspend fun leave(conversationId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.leave(conversationId) }.mapValue { }

    suspend fun transferOwner(conversationId: String, newOwnerId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.transferOwner(conversationId, TransferOwnerRequest(newOwnerId)) }
            .mapValue { }

    suspend fun setMemberRole(conversationId: String, userId: String, role: String): AppResult<Unit> =
        apiCall(errorMapper) { api.setMemberRole(conversationId, userId, SetRoleRequest(role)) }
            .mapValue { }

    suspend fun addMember(conversationId: String, userId: String): AppResult<AddOutcome> =
        apiCall(errorMapper) { api.addMember(conversationId, AddMemberRequest(userId)) }
            .mapValue { AddOutcome(it.userId, it.outcome, it.reason) }

    suspend fun removeMember(conversationId: String, userId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.removeMember(conversationId, userId) }.mapValue { }

    suspend fun updateGroupInfo(
        conversationId: String,
        title: String? = null,
        avatarMediaId: String? = null,
    ): AppResult<Unit> =
        apiCall(errorMapper) {
            api.updateGroupInfo(conversationId, UpdateGroupInfoRequest(title, avatarMediaId))
        }.mapValue { }

    suspend fun invitations(): AppResult<List<GroupInvitation>> =
        apiCall(errorMapper) { api.invitations() }
            .mapList { GroupInvitation(it.id, it.conversationId, it.inviterId, it.createdAt) }

    suspend fun acceptInvitation(invitationId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.acceptInvitation(invitationId) }.mapValue { }

    suspend fun declineInvitation(invitationId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.declineInvitation(invitationId) }.mapValue { }

    /** The viewer's own connections — the member-picker candidate ids. */
    suspend fun connections(viewerId: String): AppResult<List<String>> =
        apiCall(errorMapper) { api.connections(viewerId) }.mapValue { it }

    // ── Android chat completion pass ────────────────────────────────────

    /** Toggles the viewer's reaction. The server answers added/removed. */
    suspend fun toggleReaction(
        conversationId: String,
        message: Message,
        emoji: String,
    ): AppResult<ReactionToggle> =
        apiCall(errorMapper) {
            api.toggleReaction(
                conversationId,
                message.id,
                ToggleReactionRequest(emoji = emoji, bucket = message.bucket, ts = message.ts),
            )
        }.mapValue { ReactionToggle(added = it.added, emoji = it.emoji, messageId = it.messageId) }

    /** Deletes one message; the server enforces who may. */
    suspend fun deleteMessage(conversationId: String, message: Message): AppResult<Unit> =
        apiCall(errorMapper) {
            api.deleteMessage(
                conversationId,
                message.id,
                DeleteMessageRequest(bucket = message.bucket, ts = message.ts),
            )
        }.mapValue { }

    suspend fun conversationSettings(conversationId: String): AppResult<ConversationSettings> =
        apiCall(errorMapper) { api.conversationSettings(conversationId) }
            .mapValue { ConversationSettings(isMuted = it.isMuted, isPinned = it.isPinned) }

    suspend fun updateConversationSettings(
        conversationId: String,
        settings: ConversationSettings,
    ): AppResult<ConversationSettings> =
        apiCall(errorMapper) {
            api.updateConversationSettings(
                conversationId,
                ConversationSettingsDto(isMuted = settings.isMuted, isPinned = settings.isPinned),
            )
        }.mapValue { ConversationSettings(isMuted = it.isMuted, isPinned = it.isPinned) }

    /** An owner-issued room entitlement for the session socket to present. */
    suspend fun subscriptionEntitlement(conversationId: String): AppResult<String> =
        apiCall(errorMapper) { api.subscriptionEntitlement(conversationId) }
            .mapValue { it.token }

    /** Creates a group; the server reports per-target add/invite/skip. */
    suspend fun createGroupGoverned(
        title: String,
        memberIds: List<String>,
        idempotencyKey: String,
    ): AppResult<Conversation> =
        apiCall(errorMapper) {
            api.createGroup(idempotencyKey, CreateGroupRequest(title, memberIds))
        }.mapValue { it.toDomain() }

    companion object {
        const val MESSAGE_PAGE_SIZE = 30

        /** 5 pages × the 100 cap: a 500-conversation inbox before truncation. */
        const val MAX_CONVERSATION_PAGES = 5

        fun newIdempotencyKey(): String = UUID.randomUUID().toString()
    }
}

private inline fun <T, R> AppResult<T>.mapValue(block: (T) -> R): AppResult<R> = when (this) {
    is AppResult.Success -> AppResult.Success(block(data))
    is AppResult.Failure -> this
}

private inline fun <T, R> AppResult<List<T>>.mapList(block: (T) -> R): AppResult<List<R>> =
    mapValue { list -> list.map(block) }

/** How the server handled one group add. */
data class AddOutcome(val userId: String, val outcome: String, val reason: String)

/** One pending group invitation, from the invitee's point of view. */
data class GroupInvitation(
    val id: String,
    val conversationId: String,
    val inviterId: String,
    val createdAt: String,
)

/** One inbox row / thread header. */
data class Conversation(
    val id: String,
    val type: String,
    val title: String?,
    val isRequest: Boolean,
    val members: List<ConversationMember>,
    val updatedAt: String,
    // Production chat pass: inbox metadata.
    val avatarMediaId: String? = null,
    val lastMessageAt: String? = null,
    val lastMessagePreview: String = "",
    val lastMessageSender: String? = null,
    val hasUnread: Boolean = false,
    val isPinned: Boolean = false,
    val isMuted: Boolean = false,
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
    /** Set for media messages — resolved to a URL by the media layer. */
    val mediaId: String? = null,
    /** True while the message is shown optimistically, before the server ack. */
    val pending: Boolean = false,
    /** Set when the send failed and the row is offered for retry. */
    val failed: Boolean = false,
    /**
     * The Scylla address half of the id: reactions and deletion address a
     * message by (bucket, ts, msg_id). Blank on cached/socket rows — those
     * operations are offered only on rows that carry the address.
     */
    val bucket: String = "",
    val ts: String = "",
    /** Reaction summaries as the LIST response reported them. */
    val reactions: List<ReactionSummary> = emptyList(),
) {
    /** True when this row carries enough address to react/delete. */
    val addressable: Boolean get() = bucket.isNotBlank() && ts.isNotBlank()
}

data class ReactionSummary(val emoji: String, val userIds: List<String>)

data class ReactionToggle(val added: Boolean, val emoji: String, val messageId: String)

/** The viewer's own per-conversation switches. */
data class ConversationSettings(val isMuted: Boolean, val isPinned: Boolean)

data class Presence(val activeCount: Int, val isBigGroup: Boolean)

private fun ConversationDto.toDomain() = Conversation(
    id = id,
    type = type,
    title = title,
    isRequest = isRequest,
    members = members.map { ConversationMember(it.userId, it.role, it.displayName) },
    updatedAt = updatedAt,
    avatarMediaId = avatarMediaId,
    lastMessageAt = lastMessageAt,
    lastMessagePreview = lastMessagePreview,
    lastMessageSender = lastMessageSender,
    hasUnread = hasUnread,
    isPinned = isPinned,
    isMuted = isMuted,
)

private fun MessageDto.toDomain() = Message(
    id = msgId,
    conversationId = conversationId,
    senderId = senderId,
    senderDisplayName = senderDisplayName,
    text = text,
    mediaId = mediaId,
    createdAt = createdAt.ifBlank { ts },
    bucket = bucket,
    ts = ts,
    reactions = reactions.map { ReactionSummary(it.emoji, it.userIds) },
)
