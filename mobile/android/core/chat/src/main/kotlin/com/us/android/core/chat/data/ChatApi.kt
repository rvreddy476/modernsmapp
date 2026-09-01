package com.us.android.core.chat.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import retrofit2.http.Body
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * The chat endpoints, transcribed from live captures on 2026-08-21.
 *
 * Every shape here came from a real request through the gateway with a real
 * JWT — see `prompt/slice-b-chat-contracts.md`. Nothing is inferred from
 * handler source, because a handler tells you what the server COULD send, not
 * what it does.
 *
 * These are conversation routes. The product guide's
 * `POST /v1/chat/messages/{id}` with a `recipient_id` does not exist.
 */
@Suppress("TooManyFunctions") // Mirrors the message-service route surface one-to-one.
interface ChatApi {

    /**
     * The inbox, one page. Sorted by the caller; `updated_at` advances on
     * each message. The server default page is 20 and the cap is 100
     * (`handler.go:464-468`); [cursor] is `meta.next_cursor`, opaque.
     */
    @GET("v1/chat/conversations")
    suspend fun conversations(
        @Query("limit") limit: Int = CONVERSATION_PAGE_LIMIT,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<ConversationDto>>

    @GET("v1/chat/conversations/{id}")
    suspend fun conversation(@Path("id") conversationId: String): ApiEnvelope<ConversationDto>

    /**
     * Opens (or returns) the 1:1 conversation with another user.
     *
     * Denied with `403 MESSAGING_NOT_ALLOWED` when graph-service says the
     * viewer may not message that person. The client does not pre-judge that:
     * eligibility is the graph's decision and is re-checked server-side on
     * every attempt.
     */
    @POST("v1/chat/conversations/direct")
    suspend fun createDirect(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: CreateDirectRequest,
    ): ApiEnvelope<ConversationDto>

    @POST("v1/chat/conversations/group")
    suspend fun createGroup(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: CreateGroupRequest,
    ): ApiEnvelope<ConversationDto>

    /**
     * One page of history, newest first.
     *
     * [cursor] is `meta.next_cursor` from the previous page: an opaque base64
     * document. It is passed back untouched and never parsed.
     */
    @GET("v1/chat/conversations/{id}/messages")
    suspend fun messages(
        @Path("id") conversationId: String,
        @Query("limit") limit: Int,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<MessageDto>>

    /**
     * Sends a message.
     *
     * The idempotency key is REQUIRED — the server answers
     * `400 MISSING_IDEMPOTENCY_KEY` without one. That is stricter than post
     * comments, where the header is optional, and it is why the parameter is
     * non-null here rather than a nullable convenience.
     */
    @POST("v1/chat/conversations/{id}/messages")
    suspend fun send(
        @Path("id") conversationId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: SendMessageRequest,
    ): ApiEnvelope<MessageDto>

    @POST("v1/chat/conversations/{id}/read")
    suspend fun markRead(
        @Path("id") conversationId: String,
        @Body body: MarkReadRequest,
    ): ApiEnvelope<StatusDto>

    @POST("v1/chat/conversations/{id}/typing")
    suspend fun setTyping(
        @Path("id") conversationId: String,
        @Body body: TypingRequest,
    ): ApiEnvelope<StatusDto>

    @GET("v1/chat/conversations/{id}/presence")
    suspend fun presence(@Path("id") conversationId: String): ApiEnvelope<PresenceDto>

    /**
     * Bulk presence for the inbox's Online Now rail: user id → online. The
     * server answers only for people the requester may see (privacy gate is
     * server-side) and caps the list at 50.
     */
    @POST("v1/chat/presence")
    suspend fun bulkPresence(@Body body: BulkPresenceRequest): ApiEnvelope<Map<String, Boolean>>

    // ── Production chat pass: requests, groups, invitations ─────────────

    /** The recipient's pending message requests. */
    @GET("v1/chat/requests")
    suspend fun requests(): ApiEnvelope<List<ConversationDto>>

    @POST("v1/chat/conversations/{id}/requests/accept")
    suspend fun acceptRequest(@Path("id") conversationId: String): ApiEnvelope<StatusDto>

    @POST("v1/chat/conversations/{id}/requests/decline")
    suspend fun declineRequest(@Path("id") conversationId: String): ApiEnvelope<StatusDto>

    @POST("v1/chat/conversations/{id}/requests/block")
    suspend fun blockRequest(@Path("id") conversationId: String): ApiEnvelope<StatusDto>

    @POST("v1/chat/conversations/{id}/requests/report")
    suspend fun reportRequest(@Path("id") conversationId: String): ApiEnvelope<StatusDto>

    /** Self-removal. The owner is refused until ownership is transferred. */
    @POST("v1/chat/conversations/{id}/leave")
    suspend fun leave(@Path("id") conversationId: String): ApiEnvelope<StatusDto>

    @POST("v1/chat/conversations/{id}/transfer-owner")
    suspend fun transferOwner(
        @Path("id") conversationId: String,
        @Body body: TransferOwnerRequest,
    ): ApiEnvelope<StatusDto>

    @retrofit2.http.PUT("v1/chat/conversations/{id}/members/{userId}/role")
    suspend fun setMemberRole(
        @Path("id") conversationId: String,
        @Path("userId") userId: String,
        @Body body: SetRoleRequest,
    ): ApiEnvelope<StatusDto>

    /** Adds a member; the server answers added / invited / skipped honestly. */
    @POST("v1/chat/conversations/{id}/members")
    suspend fun addMember(
        @Path("id") conversationId: String,
        @Body body: AddMemberRequest,
    ): ApiEnvelope<AddOutcomeDto>

    @retrofit2.http.DELETE("v1/chat/conversations/{id}/members/{userId}")
    suspend fun removeMember(
        @Path("id") conversationId: String,
        @Path("userId") userId: String,
    ): ApiEnvelope<StatusDto>

    @retrofit2.http.PUT("v1/chat/conversations/{id}")
    suspend fun updateGroupInfo(
        @Path("id") conversationId: String,
        @Body body: UpdateGroupInfoRequest,
    ): ApiEnvelope<StatusDto>

    /** The viewer's pending group invitations. */
    @GET("v1/chat/invitations")
    suspend fun invitations(): ApiEnvelope<List<GroupInvitationDto>>

    @POST("v1/chat/invitations/{invitationId}/accept")
    suspend fun acceptInvitation(@Path("invitationId") invitationId: String): ApiEnvelope<StatusDto>

    @POST("v1/chat/invitations/{invitationId}/decline")
    suspend fun declineInvitation(@Path("invitationId") invitationId: String): ApiEnvelope<StatusDto>

    /**
     * The viewer's accepted connections (graph authority) — the group
     * member-picker's candidate list. IDs only; names resolve via profiles.
     * The graph never hands the client anyone else's adjacency (directive
     * §3.1): this route requires userId == the authenticated caller.
     */
    @GET("v1/graph/connections/{userId}")
    suspend fun connections(
        @Path("userId") userId: String,
        @Query("limit") limit: Int = 50,
    ): ApiEnvelope<List<String>>

    // ── Android chat completion pass ────────────────────────────────────

    /**
     * Toggles the viewer's [ToggleReactionRequest.emoji] on one message.
     * Scylla addresses a message by (bucket, ts, msg_id), so all three ride
     * the body — `handler.go:737-741` binds each `required`.
     */
    @retrofit2.http.PUT("v1/chat/conversations/{id}/messages/{messageId}/reactions")
    suspend fun toggleReaction(
        @Path("id") conversationId: String,
        @Path("messageId") messageId: String,
        @Body body: ToggleReactionRequest,
    ): ApiEnvelope<ToggleReactionDto>

    /**
     * Deletes one message (sender's own; group owner/admin may moderate).
     * DELETE with a REQUIRED body — Retrofit needs the explicit @HTTP form,
     * a plain @DELETE cannot carry one.
     */
    @retrofit2.http.HTTP(
        method = "DELETE",
        path = "v1/chat/conversations/{id}/messages/{messageId}",
        hasBody = true,
    )
    suspend fun deleteMessage(
        @Path("id") conversationId: String,
        @Path("messageId") messageId: String,
        @Body body: DeleteMessageRequest,
    ): ApiEnvelope<StatusDto>

    /** The VIEWER's per-conversation settings (mute/pin live here). */
    @GET("v1/chat/conversations/{id}/settings")
    suspend fun conversationSettings(
        @Path("id") conversationId: String,
    ): ApiEnvelope<ConversationSettingsDto>

    @retrofit2.http.PUT("v1/chat/conversations/{id}/settings")
    suspend fun updateConversationSettings(
        @Path("id") conversationId: String,
        @Body body: ConversationSettingsDto,
    ): ApiEnvelope<ConversationSettingsDto>

    /**
     * An owner-issued conversation-room entitlement (roomauth §5.3): the
     * membership authority signs a short-lived token the socket presents in
     * a `conversation.subscribe` frame. 403 for non-members — including a
     * member severed a moment ago.
     */
    @POST("v1/chat/conversations/{id}/subscription")
    suspend fun subscriptionEntitlement(
        @Path("id") conversationId: String,
    ): ApiEnvelope<SubscriptionEntitlementDto>
}

@Serializable
data class TransferOwnerRequest(@SerialName("new_owner_id") val newOwnerId: String)

@Serializable
data class SetRoleRequest(val role: String)

@Serializable
data class AddMemberRequest(@SerialName("user_id") val userId: String)

/** How the server handled one add: `added`, `invited` or `skipped`. */
@Serializable
data class AddOutcomeDto(
    @SerialName("user_id") val userId: String = "",
    val outcome: String = "",
    val reason: String = "",
)

@Serializable
data class UpdateGroupInfoRequest(
    val title: String? = null,
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
)

@Serializable
data class GroupInvitationDto(
    val id: String = "",
    @SerialName("conversation_id") val conversationId: String = "",
    @SerialName("inviter_id") val inviterId: String = "",
    @SerialName("invitee_id") val inviteeId: String = "",
    val status: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

/** Field is `other_user_id`. `user_id` is rejected as a missing field. */
@Serializable
data class CreateDirectRequest(@SerialName("other_user_id") val otherUserId: String)

/** Field is `title`, not `name`. */
@Serializable
data class CreateGroupRequest(
    val title: String,
    @SerialName("member_ids") val memberIds: List<String>,
)

/**
 * A message to send.
 *
 * `@EncodeDefault` on [type] is LOAD-BEARING, not decoration.
 *
 * kotlinx.serialization omits any property whose value equals its default
 * unless told otherwise, and the app-wide `Json` leaves `encodeDefaults` off.
 * So `SendMessageRequest(text = "hi")` serialised to `{"text":"hi"}` — no
 * `type` at all — and message-service binds that field `required,oneof=text
 * media`, answering 400 for every message the app ever tried to send.
 *
 * Nothing caught it: `ChatContractTest` pins the shapes the server SENDS, the
 * contracts were captured with curl (which spells the field out), and the
 * controller tests use a fake API that never serialises anything. It surfaced
 * on the first real device send, as "Message not sent." with a 32-byte body.
 *
 * The default stays because `text` is the only kind this client sends today;
 * the annotation is what puts it on the wire.
 */
@Serializable
data class SendMessageRequest(
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val type: String = TEXT_MESSAGE,
    val text: String,
    /**
     * Set for `type = "media"` messages: the id of an ALREADY uploaded,
     * confirmed, ready+passed media asset. Null (and omitted from the wire)
     * for text, so every existing text send stays byte-identical.
     */
    @SerialName("media_id") val mediaId: String? = null,
)

/** Field is `message_id`, not `last_read_message_id`. */
@Serializable
data class MarkReadRequest(@SerialName("message_id") val messageId: String)

/**
 * Scylla message address + emoji. `ts` is the message's own `ts` string,
 * passed back verbatim — the server parses it as RFC3339; reformatting it
 * client-side would address a different (nonexistent) row.
 */
@Serializable
data class ToggleReactionRequest(
    val emoji: String,
    val bucket: String,
    val ts: String,
)

@Serializable
data class ToggleReactionDto(
    val added: Boolean = false,
    val emoji: String = "",
    @SerialName("message_id") val messageId: String = "",
)

@Serializable
data class DeleteMessageRequest(
    val bucket: String,
    val ts: String,
)

/**
 * The viewer's per-conversation settings. Only the fields this client edits
 * are modelled; the PUT carries the full read-modify-write of these.
 * @EncodeDefault: an omitted `false` is not "false" to the server, it is
 * "unspecified" — the SendMessageRequest default-omission trap again.
 */
@Serializable
data class ConversationSettingsDto(
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("is_muted") val isMuted: Boolean = false,
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    @SerialName("is_pinned") val isPinned: Boolean = false,
)

@Serializable
data class SubscriptionEntitlementDto(
    val token: String = "",
    @SerialName("conversation_id") val conversationId: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class ReactionSummaryDto(
    val emoji: String = "",
    @SerialName("user_ids") val userIds: List<String> = emptyList(),
)

@Serializable
data class TypingRequest(val typing: Boolean)

@Serializable
data class BulkPresenceRequest(@SerialName("user_ids") val userIds: List<String>)

@Serializable
data class StatusDto(val status: String = "")

/** A count and a flag, not a roster — the server does not enumerate members. */
@Serializable
data class PresenceDto(
    @SerialName("active_count") val activeCount: Int = 0,
    @SerialName("is_big_group") val isBigGroup: Boolean = false,
)

@Serializable
data class ConversationDto(
    val id: String = "",
    /** `direct` or `group`. */
    val type: String = "",
    val title: String? = null,
    @SerialName("created_by") val createdBy: String = "",
    /** True while this is a pending message request rather than a thread. */
    @SerialName("is_request") val isRequest: Boolean = false,
    val members: List<ConversationMemberDto> = emptyList(),
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("updated_at") val updatedAt: String = "",
    // Production chat pass: inbox metadata. All defaulted — older captures
    // and the create responses may omit them.
    @SerialName("avatar_media_id") val avatarMediaId: String? = null,
    @SerialName("last_message_at") val lastMessageAt: String? = null,
    @SerialName("last_message_preview") val lastMessagePreview: String = "",
    @SerialName("last_message_sender") val lastMessageSender: String? = null,
    @SerialName("has_unread") val hasUnread: Boolean = false,
    /** The VIEWER's settings flags, joined server-side (one page query). */
    @SerialName("is_pinned") val isPinned: Boolean = false,
    @SerialName("is_muted") val isMuted: Boolean = false,
)

/**
 * Carries `display_name` on the wire.
 *
 * This is the difference from comments, where the payload has only an author
 * id and a name would cost one profile request per row. Chat needs no
 * hydration and must not add any.
 */
@Serializable
data class ConversationMemberDto(
    @SerialName("user_id") val userId: String = "",
    val role: String = "",
    @SerialName("joined_at") val joinedAt: String = "",
    @SerialName("display_name") val displayName: String = "",
)

@Serializable
data class MessageDto(
    /** The id field is `msg_id`. There is no `id`. */
    @SerialName("msg_id") val msgId: String = "",
    @SerialName("conversation_id") val conversationId: String = "",
    @SerialName("sender_id") val senderId: String = "",
    /**
     * Present on the LIST response and absent from the send response, so a
     * just-sent message renders its name from the local member list rather
     * than from its own payload.
     */
    @SerialName("sender_display_name") val senderDisplayName: String? = null,
    val type: String = TEXT_MESSAGE,
    val text: String = "",
    /** Present on media messages: the attachment's media id. */
    @SerialName("media_id") val mediaId: String? = null,
    /** Reaction summaries, present on the LIST response when any exist. */
    val reactions: List<ReactionSummaryDto> = emptyList(),
    /**
     * Storage partition (`YYYYMM`) that leaks into the DTO. Carried because
     * reactions and deletion ADDRESS a message by (bucket, ts, msg_id);
     * never interpreted.
     */
    val bucket: String = "",
    val ts: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

const val TEXT_MESSAGE = "text"
const val MEDIA_MESSAGE = "media"

/** The server's page cap for `GET /v1/chat/conversations`. */
const val CONVERSATION_PAGE_LIMIT = 100
