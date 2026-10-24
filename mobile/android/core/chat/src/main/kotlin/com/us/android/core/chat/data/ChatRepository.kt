package com.us.android.core.chat.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.Paged
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import com.us.android.core.network.pagedApiCall
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * What a post's share sheet needs from chat (2026-10-17): the inbox to pick
 * a conversation from, and a post card to send into it. An interface so the
 * sheet's ViewModel is tested against a fake rather than the whole route
 * surface.
 */
interface PostShareChats {
    suspend fun conversations(): AppResult<List<Conversation>>
    suspend fun sendPost(conversationId: String, postId: String, idempotencyKey: String): AppResult<Message>

    /**
     * Chat parity (2026-10-18): the card with the sheet's optional note as
     * its caption — a `post` message's `text` IS its caption, so the note
     * rides the card rather than becoming a second message. Defaults to the
     * plain card so a seam without captions still sends.
     */
    suspend fun sendPostWithNote(
        conversationId: String,
        postId: String,
        idempotencyKey: String,
        note: String,
    ): AppResult<Message> = sendPost(conversationId, postId, idempotencyKey)
}

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
) : PostShareChats {

    /**
     * The FULL inbox, paged to the server cap and followed to exhaustion
     * (bounded). The previous single unparameterised call silently truncated
     * the inbox to the server's default page of 20.
     */
    override suspend fun conversations(): AppResult<List<Conversation>> {
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
        replyTo: ReplyRef? = null,
    ): AppResult<Message> =
        apiCall(errorMapper) {
            api.send(
                conversationId,
                idempotencyKey,
                SendMessageRequest(
                    text = text,
                    replyToId = replyTo?.messageId,
                    replyToPreview = replyTo?.preview,
                    replyToSenderId = replyTo?.senderId,
                ),
            )
        }.mapValue { it.toDomain() }

    suspend fun markRead(conversationId: String, messageId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.markRead(conversationId, MarkReadRequest(messageId)) }
            .mapValue { }

    suspend fun setTyping(conversationId: String, typing: Boolean): AppResult<Unit> =
        // A stop also carries CONTRACT §4's `is_typing: false`; a start
        // stays byte-identical to what every server already accepts.
        apiCall(errorMapper) {
            api.setTyping(conversationId, TypingRequest(typing, isTyping = if (typing) null else false))
        }
            .mapValue { }

    suspend fun presence(conversationId: String): AppResult<Presence> =
        apiCall(errorMapper) { api.presence(conversationId) }
            .mapValue { Presence(it.activeCount, it.isBigGroup) }

    /** Bulk presence for the Online Now rail: the set of ONLINE ids. */
    suspend fun bulkPresence(userIds: List<String>): AppResult<Set<String>> =
        apiCall(errorMapper) { api.bulkPresence(BulkPresenceRequest(userIds)) }
            .mapValue { map -> map.filterValues { it }.keys }

    // ── Production chat pass: requests, groups, invitations, media ─────

    /** Sends a MEDIA message: the asset must already be uploaded and ready. */
    suspend fun sendMedia(
        conversationId: String,
        mediaId: String,
        idempotencyKey: String,
        caption: String = "",
        replyTo: ReplyRef? = null,
    ): AppResult<Message> =
        apiCall(errorMapper) {
            api.send(
                conversationId,
                idempotencyKey,
                SendMessageRequest(
                    type = MEDIA_MESSAGE,
                    text = caption,
                    mediaId = mediaId,
                    replyToId = replyTo?.messageId,
                    replyToPreview = replyTo?.preview,
                    replyToSenderId = replyTo?.senderId,
                ),
            )
        }.mapValue { it.toDomain() }

    /**
     * Shares a post into a conversation as a card (2026-10-17):
     * `{"type":"post","text":"","post_id":"<id>"}`. The server refuses one as
     * the first message of a message request and checks the viewer may see
     * the post; both come back as a failure the share sheet words.
     */
    override suspend fun sendPost(
        conversationId: String,
        postId: String,
        idempotencyKey: String,
    ): AppResult<Message> =
        apiCall(errorMapper) {
            api.send(
                conversationId,
                idempotencyKey,
                SendMessageRequest(type = POST_MESSAGE, text = "", postId = postId),
            )
        }.mapValue { it.toDomain() }

    override suspend fun sendPostWithNote(
        conversationId: String,
        postId: String,
        idempotencyKey: String,
        note: String,
    ): AppResult<Message> =
        apiCall(errorMapper) {
            api.send(
                conversationId,
                idempotencyKey,
                SendMessageRequest(type = POST_MESSAGE, text = note.trim().take(MAX_MESSAGE_LENGTH), postId = postId),
            )
        }.mapValue { it.toDomain() }

    suspend fun requests(): AppResult<List<Conversation>> =
        listApiCall(errorMapper) { api.requests() }.mapList { it.toDomain() }

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
        description: String? = null,
    ): AppResult<Unit> =
        apiCall(errorMapper) {
            api.updateGroupInfo(conversationId, UpdateGroupInfoRequest(title, avatarMediaId, description))
        }.mapValue { }

    suspend fun invitations(): AppResult<List<GroupInvitation>> =
        listApiCall(errorMapper) { api.invitations() }
            .mapList { GroupInvitation(it.id, it.conversationId, it.inviterId, it.createdAt) }

    suspend fun acceptInvitation(invitationId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.acceptInvitation(invitationId) }.mapValue { }

    suspend fun declineInvitation(invitationId: String): AppResult<Unit> =
        apiCall(errorMapper) { api.declineInvitation(invitationId) }.mapValue { }

    /** The viewer's own connections — the member-picker candidate ids. */
    suspend fun connections(viewerId: String): AppResult<List<String>> =
        listApiCall(errorMapper) { api.connections(viewerId) }

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

    /**
     * Deletes one message; the server enforces who may. [scope] is
     * CONTRACT §2.3's `everyone` (unsend) or `me` (hide for me); null sends
     * the old body, which the server treats as `everyone`.
     */
    suspend fun deleteMessage(
        conversationId: String,
        message: Message,
        scope: String? = null,
    ): AppResult<Unit> =
        apiCall(errorMapper) {
            api.deleteMessage(
                conversationId,
                message.id,
                DeleteMessageRequest(bucket = message.bucket, ts = message.ts, scope = scope),
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

/** A conversation's name when neither a title nor any member's name is known. */
const val UNNAMED_CONVERSATION = "Conversation"

/** How the server handled one group add. */
data class AddOutcome(val userId: String, val outcome: String, val reason: String)

/** One pending group invitation, from the invitee's point of view. */
data class GroupInvitation(
    val id: String,
    val conversationId: String,
    val inviterId: String,
    val createdAt: String,
)

/**
 * The viewer's own way into a group (founder, 2026-10-22): a newcomer sees
 * only what was sent after [joinedAt], and the thread says so at the top.
 */
data class MyJoin(
    /** RFC 3339, as the server sent it. */
    val joinedAt: String,
    val method: JoinMethod,
    /** Who added them / whose invitation they accepted; null when unknown or a link. */
    val addedByUserId: String?,
)

/** The three ways in that earn the note. */
enum class JoinMethod { InviteLink, Added, Invitation }

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
    /** Groups pass: the description and the signed, short-lived avatar URL. */
    val description: String = "",
    val avatarUrl: String? = null,
    /** The product a conversation belongs to ("dating" for a Pulse match's chat); null for every other. */
    val sourceApp: String? = null,
    /** That product's own id for it (the Pulse match); null otherwise. */
    val matchId: String? = null,
    // Chat parity (2026-10-18, CONTRACT §1). Defaults read as before.
    /** Server-maintained; 0 with [hasUnread] true means "unread, count unknown". */
    val unreadCount: Int = 0,
    val muteCalls: Boolean = false,
    /** `default|chime|pop|bell|silent`; null when the server never said. */
    val alertTone: String? = null,
    /** Direct chats only. */
    val streak: ChatStreak? = null,
    /** Direct chats only: the other person, privacy-gated presence included. */
    val peer: ChatPeer? = null,
    val lastMessage: LastMessageInfo? = null,
    /** Fix pass 2: the viewer marked it unread on the server; opening it clears that. */
    val markedUnread: Boolean = false,
    /** Fix pass 2: a request's sender (requests list and request detail only). */
    val sender: RequestSender? = null,
    /** Who opened the conversation (`created_by`); blank when the server did not say. */
    val createdBy: String = "",
    /** A requests-list row only: `incoming` or `outgoing`; null everywhere else. */
    val requestDirection: String? = null,
    /** How the viewer joined this group, when their history starts at that join; null otherwise. */
    val myJoin: MyJoin? = null,
    /** The viewer-scoped delivered/read watermarks for their own messages (ticks). */
    val receipts: ReceiptWatermarks = ReceiptWatermarks(),
    /** Hidden chats: the viewer hid it (`locked` on the wire). Never cached, never in a normal list. */
    val locked: Boolean = false,
) {
    /**
     * A Pulse match's chat. It belongs to the dating feature (founder,
     * 2026-10-18): never listed in Messages, never counted in its unread
     * dots, never offered by the share picker. The one predicate every one
     * of those surfaces asks, so they cannot drift apart.
     */
    val isDatingChat: Boolean
        get() = sourceApp?.trim().equals(DATING_SOURCE_APP, ignoreCase = true)

    /**
     * What to show as the thread's name.
     *
     * The title when there is one. Otherwise — a direct chat, or a group
     * nobody named — the OTHER members' names, "Ana, Ben", as many as the
     * server named (2026-10-17: it used to take only the first other member,
     * so a group whose first member had no display name read "Conversation"
     * even when the rest were named). "Conversation" only when no name is
     * known at all, rather than a raw id, which is never a name a person
     * recognises.
     */
    fun displayTitle(viewerId: String): String {
        title?.takeIf { it.isNotBlank() }?.let { return it }
        val names = members
            .filter { it.userId != viewerId }
            .map { it.displayName.trim() }
            .filter { it.isNotEmpty() }
            .distinct()
        return if (names.isEmpty()) UNNAMED_CONVERSATION else names.joinToString(", ")
    }
}

/** `source_app` on a Pulse match's conversation. */
const val DATING_SOURCE_APP = "dating"

/** The conversations Messages may show: everything but Pulse match chats. */
fun List<Conversation>.withoutDatingChats(): List<Conversation> = filterNot { it.isDatingChat }

data class ConversationMember(
    val userId: String,
    val role: String,
    val displayName: String,
    val avatarMediaId: String? = null,
)

/**
 * The quote a reply carries: which message, its display snapshot, its sender.
 * Built by the thread from the message the user long-pressed.
 */
data class ReplyRef(
    val messageId: String,
    val preview: String,
    val senderId: String,
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
    /**
     * Quoted reply: the id of the message this one answers plus the display
     * snapshot stored at send time. A quote renders from these alone — the
     * original may be outside the loaded page.
     */
    val replyToId: String? = null,
    val replyToPreview: String? = null,
    val replyToSenderId: String? = null,
    /** Set on a shared post's card: the post it opens. The thread draws it from the post itself. */
    val postId: String? = null,
    /** True while the message is shown optimistically, before the server ack. */
    val pending: Boolean = false,
    /** Set when the send failed and the row is offered for retry. */
    val failed: Boolean = false,
    /**
     * The Scylla address half of the id: reactions and deletion address a
     * message by (bucket, ts, msg_id). Blank on socket rows (the frame does
     * not carry it); HTTP rows carry it and the Room cache keeps it. Those
     * operations are offered only on rows that carry the address.
     */
    val bucket: String = "",
    val ts: String = "",
    /** Reaction summaries as the LIST response reported them. */
    val reactions: List<ReactionSummary> = emptyList(),
    // Chat parity (2026-10-18, CONTRACT §2.2). Defaults read as before.
    /** The wire `type`: text, media, post, voice, sticker, gif, profile, sound, live, call. */
    val type: String = TEXT_MESSAGE,
    val forwarded: Boolean = false,
    val voice: VoiceInfo? = null,
    val sticker: StickerInfo? = null,
    val gif: GifInfo? = null,
    val profileUserId: String? = null,
    val soundId: String? = null,
    val streamId: String? = null,
    /** `type = "location"` (23 Oct 2026): the shared place. */
    val location: ChatLocation? = null,
    /** `message`, `note`, or null. */
    val replyKind: String? = null,
    /** The attachment's kind, size and sensitivity, when the server said. */
    val media: MediaInfo? = null,
    /** Unsent: drawn as a tombstone, never as its old content. */
    val deleted: Boolean = false,
    /** The author edited the text; the bubble says so. */
    val edited: Boolean = false,
    /** History's `status` on the viewer's own message: `sent|delivered|read` (see [tickState]); null otherwise. */
    val receipt: String? = null,
) {
    /** True when this row carries enough address to react/delete. */
    val addressable: Boolean get() = bucket.isNotBlank() && ts.isNotBlank()
}

data class ReactionSummary(val emoji: String, val userIds: List<String>)

data class ReactionToggle(val added: Boolean, val emoji: String, val messageId: String)

/** The viewer's own per-conversation switches. */
data class ConversationSettings(val isMuted: Boolean, val isPinned: Boolean)

data class Presence(val activeCount: Int, val isBigGroup: Boolean)

internal fun MessageDto.toDomain() = Message(
    id = msgId,
    conversationId = conversationId,
    senderId = senderId,
    senderDisplayName = senderDisplayName,
    text = text,
    mediaId = mediaId,
    replyToId = replyToId,
    replyToPreview = replyToPreview,
    replyToSenderId = replyToSenderId,
    postId = postId?.takeIf { it.isNotBlank() },
    createdAt = createdAt.ifBlank { ts },
    bucket = bucket,
    ts = ts,
    reactions = reactions.map { ReactionSummary(it.emoji, it.userIds) },
    type = type.ifBlank { TEXT_MESSAGE },
    forwarded = forwarded,
    voice = voice?.let { VoiceInfo(it.durationMs, it.waveform) },
    sticker = sticker?.takeIf { it.url.isNotBlank() }?.let { StickerInfo(it.id, it.url, it.width, it.height) },
    gif = gif?.takeIf { it.url.isNotBlank() }?.toInfo(),
    profileUserId = profileUserId?.takeIf { it.isNotBlank() },
    soundId = soundId?.takeIf { it.isNotBlank() },
    streamId = streamId?.takeIf { it.isNotBlank() },
    location = location?.takeIf { it.valid },
    replyKind = replyKind?.takeIf { it.isNotBlank() },
    media = mediaInfoOf(media),
    deleted = deleted,
    edited = !editedAt.isNullOrBlank(),
    receipt = status?.takeIf { it.isNotBlank() },
)
