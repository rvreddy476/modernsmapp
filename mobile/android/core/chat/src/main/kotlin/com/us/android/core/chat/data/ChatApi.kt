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
interface ChatApi {

    /** The inbox. Sorted by the caller; `updated_at` advances on each message. */
    @GET("v1/chat/conversations")
    suspend fun conversations(): ApiEnvelope<List<ConversationDto>>

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
}

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
)

/** Field is `message_id`, not `last_read_message_id`. */
@Serializable
data class MarkReadRequest(@SerialName("message_id") val messageId: String)

@Serializable
data class TypingRequest(val typing: Boolean)

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
    /**
     * Storage partition (`YYYYMM`) that leaks into the DTO. Carried so a
     * round-trip is lossless; never interpreted.
     */
    val bucket: String = "",
    val ts: String = "",
    @SerialName("created_at") val createdAt: String = "",
)

const val TEXT_MESSAGE = "text"
