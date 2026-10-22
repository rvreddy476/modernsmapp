package com.us.android.core.chat.data

import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.apiCall
import com.us.android.core.network.listApiCall
import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import kotlinx.serialization.json.JsonElement
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.POST
import retrofit2.http.Path
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Pinned messages (web parity, 2026-10-22, `messageService.ts` "Pin
 * Message API"): message-service's three routes, on their own interface so
 * the [ChatApi] and [ChatParityApi] fakes keep their shape.
 *
 *     POST   /v1/chat/conversations/:id/pins          {message_id} -> 201 the pin row
 *     DELETE /v1/chat/conversations/:id/pins/:pinId                -> 200
 *     GET    /v1/chat/conversations/:id/pins                       -> pin rows, newest first
 *
 * Any member may pin and unpin (`PinMessageSvc` checks membership only); at
 * most [MAX_PINS_PER_CONVERSATION] per chat, refused past that with `400
 * INVALID_REQUEST`. Unpin takes the PIN's id, not the message's.
 */
interface ChatPinsApi {

    @GET("v1/chat/conversations/{id}/pins")
    suspend fun pins(@Path("id") conversationId: String): ApiEnvelope<List<PinDto>>

    @POST("v1/chat/conversations/{id}/pins")
    suspend fun pin(@Path("id") conversationId: String, @Body body: PinMessageRequest): ApiEnvelope<PinDto>

    /** `{"data":{"status":"message unpinned"}}`; nothing in it is read. */
    @DELETE("v1/chat/conversations/{id}/pins/{pinId}")
    suspend fun unpin(@Path("id") conversationId: String, @Path("pinId") pinId: String): ApiEnvelope<JsonElement>
}

@Serializable
data class PinMessageRequest(@SerialName("message_id") val messageId: String)

/** `postgres.ConversationPin`. */
@Serializable
data class PinDto(
    val id: String = "",
    @SerialName("conversation_id") val conversationId: String = "",
    @SerialName("message_id") val messageId: String = "",
    @SerialName("pinned_by") val pinnedBy: String = "",
    @SerialName("pinned_at") val pinnedAt: String = "",
)

/** One pin: its own id (what unpin takes), the message it points at, who pinned it and when. */
data class PinnedMessage(
    val id: String,
    val messageId: String,
    val pinnedBy: String,
    val pinnedAt: String,
)

/** What the thread reads and writes for pins; a seam so the thread's ViewModel is tested against a fake. */
interface ChatPinsSource {
    suspend fun pins(conversationId: String): AppResult<List<PinnedMessage>>
    suspend fun pin(conversationId: String, messageId: String): AppResult<PinnedMessage>
    suspend fun unpin(conversationId: String, pinId: String): AppResult<Unit>
}

@Singleton
class ChatPinsRepository @Inject constructor(
    private val api: ChatPinsApi,
    private val errorMapper: ErrorMapper,
) : ChatPinsSource {

    override suspend fun pins(conversationId: String): AppResult<List<PinnedMessage>> =
        when (val result = listApiCall(errorMapper) { api.pins(conversationId) }) {
            is AppResult.Success -> AppResult.Success(orderPins(result.data.mapNotNull { it.toDomain() }))
            is AppResult.Failure -> result
        }

    override suspend fun pin(conversationId: String, messageId: String): AppResult<PinnedMessage> =
        when (val result = apiCall(errorMapper) { api.pin(conversationId, PinMessageRequest(messageId)) }) {
            is AppResult.Success -> result.data.toDomain()?.let { AppResult.Success(it) }
                // A 201 without the pin's id cannot be unpinned later: treat it as the row it should be.
                ?: AppResult.Success(PinnedMessage(id = "", messageId = messageId, pinnedBy = "", pinnedAt = ""))
            is AppResult.Failure -> result
        }

    override suspend fun unpin(conversationId: String, pinId: String): AppResult<Unit> =
        when (val result = apiCall(errorMapper) { api.unpin(conversationId, pinId) }) {
            is AppResult.Success -> AppResult.Success(Unit)
            is AppResult.Failure -> result
        }
}

/** message-service's cap (`PinMessage`: "maximum 3 pinned messages per conversation"). */
const val MAX_PINS_PER_CONVERSATION = 3

private fun PinDto.toDomain(): PinnedMessage? =
    if (id.isBlank() || messageId.isBlank()) null else PinnedMessage(id, messageId, pinnedBy, pinnedAt)

/**
 * One pin per message, in the order given: the server lists newest first
 * (`ORDER BY pinned_at DESC`), and a new pin is put in front, so the order
 * is never re-derived from timestamps two clocks wrote.
 */
fun orderPins(pins: List<PinnedMessage>): List<PinnedMessage> = pins.distinctBy { it.messageId }

/**
 * Whether [message] may be pinned or unpinned from its long-press (the web's
 * `messageActions`): anything a member can see, except a message still
 * sending, one that was unsent, and a call line.
 */
fun Message.canPin(): Boolean = !pending && !deleted && id.isNotBlank() && type != CALL_MESSAGE

/** The pin bar's next pin after a tap: one at a time, wrapping, like Telegram (the web's `nextPinIndex`). */
fun nextPinIndex(current: Int, count: Int): Int = if (count <= 0) 0 else (current + 1) % count

/**
 * A `pin_update` frame applied to the pins on screen. A pin frame with its
 * pin id joins at the front (newest first) unless already there; an unpin
 * removes by pin id, or by message id when the frame named only that. Null
 * when the frame cannot be applied in place (a pin without its id): the
 * thread reads the list again.
 */
fun applyPinUpdate(pins: List<PinnedMessage>, event: ChatSocketEvent.PinUpdate): List<PinnedMessage>? =
    if (event.pinned) {
        val pinId = event.pinId
        val messageId = event.messageId
        if (pinId.isNullOrBlank() || messageId.isNullOrBlank()) {
            null
        } else if (pins.any { it.id == pinId }) {
            pins
        } else {
            orderPins(listOf(PinnedMessage(pinId, messageId, event.by.orEmpty(), event.at.orEmpty())) + pins)
        }
    } else {
        pins.filterNot { pin ->
            (!event.pinId.isNullOrBlank() && pin.id == event.pinId) ||
                (event.pinId.isNullOrBlank() && !event.messageId.isNullOrBlank() && pin.messageId == event.messageId)
        }
    }
