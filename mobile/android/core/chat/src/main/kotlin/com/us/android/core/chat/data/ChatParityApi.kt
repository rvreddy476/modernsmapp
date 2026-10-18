package com.us.android.core.chat.data

import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.EncodeDefault
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.POST
import retrofit2.http.PUT
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * The chat-parity routes (2026-10-18, CONTRACT.md §1-§6), on their OWN
 * interface rather than appended to [ChatApi].
 *
 * Several tests implement [ChatApi] member by member; a new abstract member
 * there would break every one of them for routes they never call. A second
 * interface keeps those fakes as they are, and every route here is new, so
 * nothing that already works changes shape.
 *
 * Every response DTO below is tolerant: each field defaults, so a server
 * that has not shipped a field yet (or a route that answers 404) degrades
 * to "the feature is hidden", never to a parse failure.
 */
@Suppress("TooManyFunctions") // One route per contract section.
interface ChatParityApi {

    /** §1: the inbox by lane — `primary` (default), `requests` or `archived`. */
    @GET("v1/chat/conversations")
    suspend fun conversationsInLane(
        @Query("lane") lane: String,
        @Query("limit") limit: Int = CONVERSATION_PAGE_LIMIT,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<ConversationDto>>

    /** §7: requests, `filter=all|filtered`. */
    @GET("v1/chat/requests")
    suspend fun requestsFiltered(@Query("filter") filter: String): ApiEnvelope<List<ConversationDto>>

    /** §1.2: hide the conversation for me and clear my history up to now. 204. */
    @DELETE("v1/chat/conversations/{id}/for-me")
    suspend fun deleteForMe(@Path("id") conversationId: String)

    /** §1.3: clear my history up to now without hiding. 204. */
    @POST("v1/chat/conversations/{id}/clear")
    suspend fun clear(@Path("id") conversationId: String)

    /** §1.4: shared post cards or media, newest first. */
    @GET("v1/chat/conversations/{id}/shared")
    suspend fun shared(
        @Path("id") conversationId: String,
        @Query("kind") kind: String,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<List<SharedItemDto>>

    /** §2.4: forward one message to at most five conversations. */
    @POST("v1/chat/conversations/{id}/messages/{messageId}/forward")
    suspend fun forward(
        @Path("id") conversationId: String,
        @Path("messageId") messageId: String,
        @Body body: ForwardRequest,
    ): ApiEnvelope<ForwardResponseDto>

    /**
     * Edit own text (CONTRACT §9). The route already exists on
     * message-service (`handler.go EditMessage`: `{text, bucket, ts}`); the
     * contract's §2 does not list it because nothing about it changes.
     */
    @retrofit2.http.PATCH("v1/chat/conversations/{id}/messages/{messageId}")
    suspend fun edit(
        @Path("id") conversationId: String,
        @Path("messageId") messageId: String,
        @Body body: EditMessageRequest,
    ): ApiEnvelope<MessageDto>

    /** §2.5: who reacted, grouped by emoji. */
    @GET("v1/chat/conversations/{id}/messages/{messageId}/reactions")
    suspend fun reactions(
        @Path("id") conversationId: String,
        @Path("messageId") messageId: String,
    ): ApiEnvelope<List<ReactionGroupDto>>

    /** §2.6: report one message. 202 with or without a body. */
    @POST("v1/chat/conversations/{id}/messages/{messageId}/report")
    suspend fun report(
        @Path("id") conversationId: String,
        @Path("messageId") messageId: String,
        @Body body: ReportMessageRequest,
    )

    /** §2.7: the sticker catalogue; empty is valid (the tab hides). */
    @GET("v1/chat/stickers")
    suspend fun stickers(): ApiEnvelope<StickerCatalogDto>

    /** §2.9: who has seen a group message. */
    @GET("v1/chat/conversations/{id}/read-summary")
    suspend fun readSummary(
        @Path("id") conversationId: String,
        @Query("message_id") messageId: String,
    ): ApiEnvelope<ReadSummaryDto>

    /** §5: notes. */
    @GET("v1/chat/notes")
    suspend fun notes(): ApiEnvelope<NotesDto>

    @PUT("v1/chat/notes/me")
    suspend fun putNote(@Body body: PutNoteRequest): ApiEnvelope<NoteDto>

    @DELETE("v1/chat/notes/me")
    suspend fun deleteNote()

    /** §6: people, chats and messages for one query (q ≥ 2 characters). */
    @GET("v1/chat/search")
    suspend fun search(
        @Query("q") query: String,
        @Query("cursor") cursor: String? = null,
    ): ApiEnvelope<ChatSearchDto>

    /**
     * §4: bulk presence, read RAW. The values were plain booleans and gain
     * `last_active_at`; a raw read accepts either shape per user.
     */
    @POST("v1/chat/presence")
    suspend fun presenceDetails(@Body body: BulkPresenceRequest): ApiEnvelope<JsonElement>

    /** §1.1 for the chat settings page: the viewer's full per-conversation switches. */
    @PUT("v1/chat/conversations/{id}/settings")
    suspend fun updateSettings(
        @Path("id") conversationId: String,
        @Body body: ConversationSettingsDto,
    ): ApiEnvelope<ConversationSettingsDto>

    @GET("v1/chat/conversations/{id}/settings")
    suspend fun settings(@Path("id") conversationId: String): ApiEnvelope<ConversationSettingsDto>

    /** The send route again, so a forward-free typed send never needs [ChatApi] changes. */
    @POST("v1/chat/conversations/{id}/messages")
    suspend fun send(
        @Path("id") conversationId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: SendMessageRequest,
    ): ApiEnvelope<MessageDto>
}

// ── Row fields (§1) ──────────────────────────────────────────────────────

@Serializable
data class StreakDto(
    val days: Int = 0,
    @SerialName("at_risk") val atRisk: Boolean = false,
    @SerialName("expires_at") val expiresAt: String? = null,
)

@Serializable
data class PeerDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("display_name") val displayName: String = "",
    val username: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
    @SerialName("is_online") val isOnline: Boolean? = null,
    @SerialName("last_active_at") val lastActiveAt: String? = null,
)

@Serializable
data class LastMessageDto(
    val type: String = "",
    val preview: String = "",
    @SerialName("sender_id") val senderId: String = "",
    val at: String = "",
    /** `sent`, `read`, or null when the caller did not send it. */
    @SerialName("own_status") val ownStatus: String? = null,
)

// ── Message fields (§2.2) ────────────────────────────────────────────────

@Serializable
data class VoiceDto(
    @SerialName("duration_ms") val durationMs: Long = 0,
    val waveform: List<Int> = emptyList(),
)

@Serializable
data class StickerDto(
    val id: String = "",
    val url: String = "",
    val width: Int = 0,
    val height: Int = 0,
)

@Serializable
data class GifDto(
    val provider: String = "",
    val id: String = "",
    val url: String = "",
    @SerialName("preview_url") val previewUrl: String? = null,
    val width: Int = 0,
    val height: Int = 0,
    /** Founder, 18 Oct: the still frame Giphy returned with the pick. */
    @SerialName("still_url") val stillUrl: String? = null,
)

// ── Shared, forward, reactions, report (§1.4, §2.4-§2.6) ────────────────

@Serializable
data class SharedItemDto(
    @SerialName("message_id") val messageId: String = "",
    @SerialName("post_id") val postId: String? = null,
    @SerialName("media_id") val mediaId: String? = null,
    val kind: String? = null,
    @SerialName("sender_id") val senderId: String = "",
    val at: String = "",
)

@Serializable
data class ForwardRequest(
    @SerialName("conversation_ids") val conversationIds: List<String>,
    @SerialName("idempotency_key") val idempotencyKey: String,
)

@Serializable
data class ForwardResponseDto(val results: List<ForwardResultDto> = emptyList())

@Serializable
data class ForwardResultDto(
    @SerialName("conversation_id") val conversationId: String = "",
    val outcome: String = "",
    @SerialName("message_id") val messageId: String? = null,
)

@Serializable
data class EditMessageRequest(val text: String, val bucket: String, val ts: String)

@Serializable
data class ReactionGroupDto(
    val emoji: String = "",
    val count: Int = 0,
    val users: List<ReactionUserDto> = emptyList(),
)

@Serializable
data class ReactionUserDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
)

@Serializable
data class ReportMessageRequest(
    val reason: String,
    val note: String? = null,
)

// ── Stickers and GIFs (§2.7, §2.8) ───────────────────────────────────────

@Serializable
data class StickerCatalogDto(val packs: List<StickerPackDto> = emptyList())

@Serializable
data class StickerPackDto(
    val id: String = "",
    val name: String = "",
    val stickers: List<StickerDto> = emptyList(),
)

// ── Read summary (§2.9) ──────────────────────────────────────────────────

@Serializable
data class ReadSummaryDto(
    @SerialName("seen_by") val seenBy: List<SeenByDto> = emptyList(),
    val count: Int = 0,
)

@Serializable
data class SeenByDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
    val at: String = "",
)

// ── Notes (§5) ───────────────────────────────────────────────────────────

@Serializable
data class PutNoteRequest(
    val text: String,
    @EncodeDefault(EncodeDefault.Mode.ALWAYS)
    val audience: String = NOTE_AUDIENCE_EVERYONE,
)

@Serializable
data class NoteDto(
    val id: String = "",
    @SerialName("user_id") val userId: String = "",
    val text: String = "",
    val audience: String = NOTE_AUDIENCE_EVERYONE,
    @SerialName("created_at") val createdAt: String = "",
    @SerialName("expires_at") val expiresAt: String = "",
)

@Serializable
data class NotesDto(
    val mine: NoteDto? = null,
    val others: List<OtherNoteDto> = emptyList(),
)

@Serializable
data class OtherNoteDto(
    val note: NoteDto = NoteDto(),
    val user: NoteUserDto = NoteUserDto(),
)

@Serializable
data class NoteUserDto(
    @SerialName("user_id") val userId: String = "",
    @SerialName("display_name") val displayName: String = "",
    @SerialName("avatar_url") val avatarUrl: String? = null,
    @SerialName("is_online") val isOnline: Boolean? = null,
)

// ── Search (§6) ──────────────────────────────────────────────────────────

@Serializable
data class ChatSearchDto(
    val people: List<PersonHitDto> = emptyList(),
    val conversations: List<ConversationDto> = emptyList(),
    val messages: List<MessageHitDto> = emptyList(),
)

@Serializable
data class MessageHitDto(
    @SerialName("conversation_id") val conversationId: String = "",
    @SerialName("message_id") val messageId: String = "",
    val snippet: String = "",
    @SerialName("sender_id") val senderId: String = "",
    val at: String = "",
)

const val NOTE_AUDIENCE_EVERYONE = "everyone"
const val NOTE_AUDIENCE_FRIENDS = "friends"
const val NOTE_AUDIENCE_ONLY_ME = "only_me"

/** A Note's text cap, in graphemes (CONTRACT §5). */
const val NOTE_MAX_LENGTH = 60

const val LANE_PRIMARY = "primary"
const val LANE_ARCHIVED = "archived"
const val REQUESTS_ALL = "all"
const val REQUESTS_FILTERED = "filtered"
const val SHARED_POSTS = "posts"
const val SHARED_MEDIA = "media"
const val DELETE_SCOPE_EVERYONE = "everyone"
const val DELETE_SCOPE_ME = "me"
const val LABEL_ARCHIVED = "archived"
const val LABEL_NONE = "none"

/** Forward targets per request (CONTRACT §2.4). Also the share sheet's cap. */
const val MAX_FORWARD_TARGETS = 5
