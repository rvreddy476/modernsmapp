package com.us.android.core.chat.data

import kotlinx.coroutines.channels.awaitClose
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.callbackFlow
import kotlinx.serialization.json.Json
import kotlinx.serialization.json.JsonObject
import kotlinx.serialization.json.booleanOrNull
import kotlinx.serialization.json.contentOrNull
import kotlinx.serialization.json.jsonObject
import kotlinx.serialization.json.jsonPrimitive
import kotlinx.serialization.json.longOrNull
import okhttp3.OkHttpClient
import okhttp3.Request
import okhttp3.Response
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import kotlin.random.Random

/**
 * What the socket tells the app.
 *
 * A sealed set rather than raw JSON so a screen cannot accidentally depend on
 * a field the gateway has not promised.
 */
sealed interface ChatSocketEvent {
    data object Connected : ChatSocketEvent

    /** Disconnected. [permanent] means do not reconnect — the token was rejected. */
    data class Disconnected(val permanent: Boolean) : ChatSocketEvent

    /**
     * A message somebody else sent.
     *
     * Never the viewer's own: message-service skips the sender during Redis
     * fan-out (`message.go:897`), so the only copy of a sent message a client
     * sees is the HTTP response. The de-duplication downstream stays anyway —
     * it costs a set lookup, and it is the difference between a defect and a
     * doubled conversation if that ever changes.
     */
    data class MessageReceived(val message: Message) : ChatSocketEvent

    /** Somebody's typing state changed. [isTyping] is false on an explicit stop. */
    data class Typing(
        val conversationId: String,
        val userId: String,
        val isTyping: Boolean,
    ) : ChatSocketEvent

    /**
     * The peer in a DIRECT conversation read up to [messageId].
     *
     * Server-gated disclosure: message-service publishes this frame only when
     * the reader's privacy settings and the graph's `see_read_receipts`
     * decision both permit (`message.go:1596-1650`), and only for direct
     * conversations. The client renders what arrives and infers nothing.
     */
    data class ReadReceipt(
        val conversationId: String,
        val userId: String,
        val messageId: String,
        /** `read_up_to` (or `read_at`): the reader's watermark, when the frame carries one. */
        val readAt: String? = null,
    ) : ChatSocketEvent

    /**
     * Message ticks (2026-10-22): the viewer-scoped aggregate watermarks for
     * their own messages in [conversationId] moved — from `delivery_receipt`
     * ([deliveredUpTo]; never privacy-gated) or a GROUP's aggregate
     * `read_receipt` ([readUpTo]; sent only when disclosed). Either may be
     * null; a watermark only ever moves forward.
     */
    data class ReceiptWatermark(
        val conversationId: String,
        val deliveredUpTo: String? = null,
        val readUpTo: String? = null,
    ) : ChatSocketEvent

    /**
     * The viewer's room subscription for [conversationId] was revoked —
     * they were removed from the conversation. The session manager drops the
     * room; screens showing it fall back to their next refresh's 403.
     */
    data class SubscriptionRevoked(val conversationId: String) : ChatSocketEvent

    /**
     * A call-signaling frame relayed by ws-gateway (`call_*` and
     * `ice_candidate`). These arrive FLAT — the gateway injects `sender_id`
     * into the client's own envelope and relays it verbatim, so there is no
     * nested `payload` object. Carried raw to :core:call, whose parser
     * validates every identifier fail-closed; nothing in :core:chat trusts
     * or interprets the contents.
     */
    data class CallSignal(val type: String, val frame: JsonObject) : ChatSocketEvent

    /**
     * Chat parity (CONTRACT §2.3): a message was unsent for everyone.
     *
     * [expired]: the same frame with `"reason": "expired"` — a disappearing
     * message whose timer ran out. The thread removes that bubble outright;
     * a plain unsend keeps its "unsent" placeholder.
     */
    data class MessageDeleted(
        val conversationId: String,
        val messageId: String,
        val expired: Boolean = false,
    ) : ChatSocketEvent

    /**
     * DM consent (2026-10-20): a request left the viewer's Requests
     * (`message_request_removed {conversation_id, reason}`; reason `expired`
     * after 30 days unanswered). Only the recipient gets it.
     */
    data class MessageRequestRemoved(
        val conversationId: String,
        val reason: String? = null,
    ) : ChatSocketEvent

    /**
     * Disappearing messages: someone in the chat changed its shared timer
     * (`disappearing_timer_updated`). [disappearAfterMs] 0 is off.
     */
    data class DisappearingTimerUpdated(
        val conversationId: String,
        val disappearAfterMs: Long,
        val updatedBy: String? = null,
    ) : ChatSocketEvent

    /**
     * Pinned messages (2026-10-22): someone in the chat pinned or unpinned a
     * message (`pin_update {conversation_id, action: pinned|unpinned,
     * pin_id, message_id, by, at}`, `publishPinUpdate`). The thread applies
     * it in place; a pin without its id makes the thread read the list.
     */
    data class PinUpdate(
        val conversationId: String,
        val pinned: Boolean,
        val pinId: String? = null,
        val messageId: String? = null,
        val by: String? = null,
        val at: String? = null,
    ) : ChatSocketEvent

    /**
     * Chat parity (CONTRACT §2.5): one reaction added or removed. The server
     * sends the reactor's own frame to their other devices too, so applying
     * it must be idempotent.
     */
    data class ReactionChanged(
        val conversationId: String,
        val messageId: String,
        val userId: String,
        val emoji: String,
        val added: Boolean,
    ) : ChatSocketEvent

    /**
     * Chat parity (CONTRACT §4): a DM peer's presence changed. ws-gateway
     * relays this one FLAT (`type` injected into the presence payload), so
     * the parser reads it from the frame itself when there is no `payload`.
     */
    data class PresenceUpdate(
        val userId: String,
        val isOnline: Boolean,
        val lastActiveAt: String?,
    ) : ChatSocketEvent

    /**
     * A frame this client does not model.
     *
     * Surfaced rather than dropped so an unhandled server event is visible in
     * logs instead of looking like a dead socket.
     */
    data class Unknown(val type: String) : ChatSocketEvent

    /**
     * A frame this client DOES model, whose required fields are missing.
     *
     * Distinct from [Unknown] on purpose. "Unknown type=message" would tell
     * whoever is debugging that the client has no message support at all,
     * which is the opposite of true and sends them to the wrong file. This
     * says the frame arrived and was refused, and why.
     *
     * Refused, not repaired: see [parseChatFrame].
     */
    data class Malformed(val type: String, val reason: String) : ChatSocketEvent
}

/**
 * The realtime half of chat.
 *
 * DELIBERATELY SEPARATE FROM THE REST API. History is fetched over HTTP with a
 * cursor; new messages and typing arrive here. Trying to serve both from one
 * abstraction is how a paginated list ends up re-rendering on every heartbeat.
 *
 * ## RECEIVE-ONLY, AND WHY
 *
 * This client sends nothing on the socket. That is not an oversight and not a
 * simplification — the gateway refuses every frame a chat client would want to
 * send. `isScopedRoomFrame` (`ws-gateway/internal/http/server.go:537`) covers
 * `conversation.enter`, `conversation.heartbeat`, `conversation.leave`,
 * `typing.start` and `typing.started`, and `server.go:310` drops all of them
 * with a warning while `EnableScopedRooms` is false. That flag is documented as
 * one that MUST stay false until client-selected channels are checked against
 * their owning service, so public beta exposes only `chat:<authenticated-user>`.
 *
 * Sending them anyway would produce one gateway warning per frame and one
 * discarded heartbeat every fifteen seconds, and would make conversation
 * presence look implemented when the server never records it. So:
 *
 *  - typing is announced over REST (`POST …/typing`), which message-service
 *    publishes to each member's channel — that path is not gated;
 *  - conversation presence is NOT surfaced anywhere in the UI, because the
 *    enter/heartbeat/leave writes that would make `GET …/presence` meaningful
 *    cannot reach the presence store.
 *
 * When scoped rooms ship, this class grows a send side and presence becomes
 * real. Until then it is a listener.
 *
 * ## WHY A HEADER AND NOT A QUERY TOKEN
 *
 * The gateway accepts `?token=` when `WS_ALLOW_QUERY_TOKEN` is set, and this
 * client never uses it: a URL carrying a credential is written to proxy logs,
 * crash reports and `adb logcat`. The Authorization header is not.
 *
 * The caller owns reconnection. This class reports [ChatSocketEvent.Disconnected]
 * and stops; it does not loop, because a client that reconnects on its own
 * cannot be told to stop when the session ends.
 */
// `open` so session-manager tests can script connection outcomes — the
// permanent-rejection restart path is untestable against a real socket.
open class ChatSocket(
    private val client: OkHttpClient,
    private val wsBaseUrl: String,
    private val json: Json = Json { ignoreUnknownKeys = true },
) {

    /**
     * The live connection, held so the session manager can SEND the two
     * frames the gateway accepts from a chat client: `conversation.subscribe`
     * (with an owner-issued entitlement — the gateway verifies, never trusts)
     * and `conversation.unsubscribe`. Cleared on close/failure so a send
     * against a dead socket reports false instead of silently vanishing.
     */
    @Volatile
    private var active: WebSocket? = null

    /** Sends one frame on the live socket. False when there is none. */
    open fun send(frame: String): Boolean = active?.send(frame) ?: false

    /**
     * Opens the socket and emits until it closes.
     *
     * [tokenProvider] is read at CONNECT time, not captured once: a reconnect
     * after a token refresh must present the new token, and a stale one would
     * be rejected forever.
     */
    open fun connect(tokenProvider: () -> String?): Flow<ChatSocketEvent> = callbackFlow {
        val token = tokenProvider()
        if (token.isNullOrBlank()) {
            trySend(ChatSocketEvent.Disconnected(permanent = true))
            close()
            return@callbackFlow
        }

        val request = Request.Builder()
            .url(wsBaseUrl.trimEnd('/') + CONNECT_PATH)
            .header("Authorization", "Bearer $token")
            .build()

        val socket = client.newWebSocket(
            request,
            object : WebSocketListener() {
                override fun onOpen(webSocket: WebSocket, response: Response) {
                    active = webSocket
                    trySend(ChatSocketEvent.Connected)
                }

                override fun onMessage(webSocket: WebSocket, text: String) {
                    parseChatFrame(json, text)?.let { trySend(it) }
                }

                override fun onClosing(webSocket: WebSocket, code: Int, reason: String) {
                    webSocket.close(code, reason)
                }

                override fun onClosed(webSocket: WebSocket, code: Int, reason: String) {
                    active = null
                    trySend(ChatSocketEvent.Disconnected(permanent = false))
                    close()
                }

                override fun onFailure(webSocket: WebSocket, t: Throwable, response: Response?) {
                    // 401/403 mean the credential is wrong, and retrying with
                    // the same one is a way to get rate-limited rather than
                    // connected. Everything else is worth another attempt.
                    active = null
                    val permanent = response?.code == HTTP_UNAUTHORIZED ||
                        response?.code == HTTP_FORBIDDEN
                    trySend(ChatSocketEvent.Disconnected(permanent = permanent))
                    close()
                }
            },
        )

        awaitClose {
            active = null
            socket.close(NORMAL_CLOSURE, null)
        }
    }

    private companion object {
        const val CONNECT_PATH = "/v1/ws/connect"
        const val NORMAL_CLOSURE = 1000

        // A rejected credential, not a dropped connection.
        const val HTTP_UNAUTHORIZED = 401
        const val HTTP_FORBIDDEN = 403
    }
}

/**
 * Decodes one server frame.
 *
 * ## THE SHAPE IS NESTED, AND THE ID FIELD IS NOT THE REST ONE
 *
 * Every frame is `{"type": …, "payload": {…}}`. message-service builds the
 * message frame at `message.go:868` and the typing frame at `message.go:1419`,
 * and ws-gateway forwards valid JSON verbatim (`server.go:614`) — it does not
 * flatten, rename or re-wrap.
 *
 * Two traps, each of which produces a socket that looks alive and delivers
 * nothing:
 *
 *  - the realtime id is `message_id`, while the REST list calls the same value
 *    `msg_id`. A parser that reads `msg_id` here gets an empty string, every
 *    message collides on a blank key, and a keyed list shows one row or crashes;
 *  - the payload is nested. A parser reading `conversation_id` off the envelope
 *    finds nothing and files every message under "".
 *
 * A top-level function rather than a private method so a test can drive it with
 * captured bytes without opening a socket. That is the gap this closes: the
 * previous parser was never exercised by any test, and it recognised two frame
 * names (`typing.started`, `presence_update`) that no server code emits.
 *
 * Returns null when the text is not a JSON object or carries no `type` — the
 * only case worth dropping silently.
 */
private fun isCallSignalType(type: String): Boolean =
    type.startsWith("call_") || type == "ice_candidate"

private fun parseMessageFrame(type: String, payload: JsonObject): ChatSocketEvent {
    val id = payload.str("message_id").orEmpty()
    val conversationId = payload.str("conversation_id").orEmpty()
    val senderId = payload.str("sender_id").orEmpty()
    val missing = buildList {
        if (id.isBlank()) add("message_id")
        if (conversationId.isBlank()) add("conversation_id")
        if (senderId.isBlank()) add("sender_id")
    }
    if (missing.isNotEmpty()) {
        return ChatSocketEvent.Malformed(type, "missing or blank: " + missing.joinToString(", "))
    }
    val address = frameAddress(payload)
    return ChatSocketEvent.MessageReceived(
        Message(
            id = id,
            conversationId = conversationId,
            senderId = senderId,
            // Not on the wire. The thread fills it in from the member list it
            // already holds, so a group message never costs a profile request
            // per row.
            senderDisplayName = null,
            text = payload.str("text").orEmpty(),
            // THE PHOTO IS ON THE WIRE — read it.
            //
            // message-service publishes `media_id` alongside the text
            // (message.go, msgBody). Dropping it here is what made a photo
            // arrive as an empty bubble for the RECIPIENT: the realtime frame
            // carried no media, so nothing rendered, and only reopening the
            // thread — which refetches over HTTP where media_id survives —
            // produced the image. Blank is normalised to null so a text
            // message never claims an empty attachment.
            mediaId = payload.str("media_id")?.takeIf { it.isNotBlank() },
            // The quote rides the frame too — a realtime reply renders whole.
            replyToId = payload.str("reply_to_id")?.takeIf { it.isNotBlank() },
            replyToPreview = payload.str("reply_to_preview")?.takeIf { it.isNotBlank() },
            replyToSenderId = payload.str("reply_to_sender_id")?.takeIf { it.isNotBlank() },
            // A shared post's card rides the frame as `post_id` (message.go,
            // messageFrameBody); without it the recipient saw an empty bubble
            // until the thread was reopened.
            postId = payload.str("post_id")?.takeIf { it.isNotBlank() },
            // The Scylla address, when the frame carries it (added to
            // message-service's frame on 2026-10-18; older servers omit it).
            // With it, an echo of the viewer's own send is editable and
            // unsendable at once; without it the thread waits for the HTTP
            // copy to supply the address, as before. Both or neither: half
            // an address addresses nothing.
            bucket = address?.first.orEmpty(),
            ts = address?.second.orEmpty(),
            createdAt = payload.str("created_at")?.takeIf { it.isNotBlank() }
                ?: address?.second.orEmpty(),
        ).withParityFields(payload),
    )
}

/** `bucket` + `ts` from a message frame, or null unless BOTH are present and non-blank. */
private fun frameAddress(payload: JsonObject): Pair<String, String>? {
    val bucket = payload.str("bucket")?.takeIf { it.isNotBlank() } ?: return null
    val ts = payload.str("ts")?.takeIf { it.isNotBlank() } ?: return null
    return bucket to ts
}

/**
 * Chat parity: the typed fields a realtime frame may carry (voice, sticker,
 * gif, profile/sound/live, forwarded, media details), read through the same
 * tolerant DTO the REST list uses. A frame without them is unchanged.
 */
private fun Message.withParityFields(payload: JsonObject): Message {
    val dto = runCatching { parityJson.decodeFromJsonElement(MessageDto.serializer(), payload) }.getOrNull()
        ?: return this
    val typed = dto.toDomain()
    return copy(
        type = payload.str("type")?.takeIf { it.isNotBlank() } ?: typed.type,
        forwarded = typed.forwarded,
        voice = typed.voice,
        sticker = typed.sticker,
        gif = typed.gif,
        profileUserId = typed.profileUserId,
        soundId = typed.soundId,
        streamId = typed.streamId,
        location = typed.location,
        replyKind = typed.replyKind,
        media = typed.media,
    )
}

private val parityJson = Json { ignoreUnknownKeys = true; isLenient = true; coerceInputValues = true }

fun parseChatFrame(json: Json, text: String): ChatSocketEvent? {
    val frame = runCatching { json.parseToJsonElement(text).jsonObject }.getOrNull() ?: return null
    val type = frame.str("type") ?: return null

    // Call signaling is relayed FLAT (no payload object) and is owned by
    // :core:call — hand the raw frame across the seam untouched.
    if (isCallSignalType(type)) {
        return ChatSocketEvent.CallSignal(type, frame)
    }

    // ws-gateway re-wraps a non-JSON Redis payload as {"type":"message",
    // "payload":"<the raw string>"} (server.go:616). The payload is then a
    // string, not an object, and asking for .jsonObject would throw inside a
    // socket callback.
    val nested = runCatching { frame["payload"]?.jsonObject }.getOrNull()

    // Presence is relayed FLAT by ws-gateway (server.go: the presence map
    // with `type` injected); a nested copy is accepted too.
    if (type == FRAME_PRESENCE_UPDATE) return parsePresence(nested ?: frame)

    val payload = nested ?: return ChatSocketEvent.Unknown(type)

    return when (type) {
        FRAME_MESSAGE -> {
            // FAIL CLOSED ON THE THREE IDENTIFIERS.
            //
            // These used to be `.orEmpty()`, and every one of the empties was
            // load-bearing in the wrong direction:
            //
            //  - a blank `conversation_id` passed the thread's own filter,
            //    because that filter only rejected a NONBLANK mismatch. A
            //    truncated or foreign frame was therefore rendered inside
            //    whichever conversation the reader happened to have open —
            //    someone else's message in your thread, which is a privacy
            //    failure, not a rendering glitch;
            //  - a blank `message_id` collides with every other blank one, so
            //    de-duplication silently swallows real messages and a keyed
            //    LazyColumn gets duplicate keys;
            //  - a blank `sender_id` cannot be attributed, so the roster
            //    lookup misses and the row renders anonymous.
            //
            // Refused rather than repaired: there is no correct value to
            // invent for any of them, and a client that guesses an identifier
            // is a client that files a message under the wrong conversation.
            parseMessageFrame(type, payload)
        }

        FRAME_TYPING -> ChatSocketEvent.Typing(
            conversationId = payload.str("conversation_id").orEmpty(),
            userId = payload.str("user_id").orEmpty(),
            // Defaults TRUE: message-service publishes is_typing true and
            // never publishes a stop (`message.go:1419`); the indicator is
            // expired by its 3s TTL instead. Defaulting false would turn the
            // one frame the server does send into a no-op.
            isTyping = payload.bool("is_typing") ?: true,
        )

        FRAME_READ_RECEIPT -> parseReadReceipt(payload)

        FRAME_DELIVERY_RECEIPT -> parseDeliveryReceipt(payload)

        FRAME_SUBSCRIPTION_REVOKED -> parseSubscriptionRevoked(payload)

        FRAME_MESSAGE_DELETED -> parseMessageDeleted(payload)

        FRAME_REACTION -> parseReaction(payload)

        FRAME_DISAPPEARING_TIMER -> parseDisappearingTimer(payload)

        FRAME_PIN_UPDATE -> parsePinUpdate(payload)

        FRAME_MESSAGE_REQUEST_REMOVED -> payload.str("conversation_id")?.takeIf { it.isNotBlank() }
            ?.let { ChatSocketEvent.MessageRequestRemoved(it, payload.str("reason")) }
            ?: ChatSocketEvent.Malformed(FRAME_MESSAGE_REQUEST_REMOVED, "missing conversation")

        else -> ChatSocketEvent.Unknown(type)
    }
}

private fun parseMessageDeleted(payload: JsonObject): ChatSocketEvent {
    val conversationId = payload.str("conversation_id").orEmpty()
    val messageId = payload.str("message_id").orEmpty()
    return if (conversationId.isBlank() || messageId.isBlank()) {
        ChatSocketEvent.Malformed(FRAME_MESSAGE_DELETED, "missing or blank identifier")
    } else {
        ChatSocketEvent.MessageDeleted(
            conversationId = conversationId,
            messageId = messageId,
            expired = payload.str("reason") == DELETE_REASON_EXPIRED,
        )
    }
}

/**
 * `disappearing_timer_updated {conversation_id, disappear_after_ms, updated_by, updated_at}`.
 * Fail closed: no conversation, or no number, is malformed — a missing value
 * must not read as "turned off".
 */
private fun parseDisappearingTimer(payload: JsonObject): ChatSocketEvent {
    val conversationId = payload.str("conversation_id").orEmpty()
    val millis = runCatching { payload["disappear_after_ms"]?.jsonPrimitive?.longOrNull }.getOrNull()
    return if (conversationId.isBlank() || millis == null) {
        ChatSocketEvent.Malformed(FRAME_DISAPPEARING_TIMER, "missing conversation or timer")
    } else {
        ChatSocketEvent.DisappearingTimerUpdated(conversationId, millis, payload.str("updated_by"))
    }
}

/**
 * `pin_update {conversation_id, action, pin_id, message_id, by, at}`. The
 * action is `pinned` / `unpinned` from message-service (the web also
 * accepts `pin` / `unpin`). Fail closed on the conversation and the action:
 * a frame that does not say which chat, or which way, changes nothing.
 */
private fun parsePinUpdate(payload: JsonObject): ChatSocketEvent {
    val conversationId = payload.str("conversation_id").orEmpty()
    val pinned = when (payload.str("action")) {
        "pinned", "pin" -> true
        "unpinned", "unpin" -> false
        else -> null
    }
    return if (conversationId.isBlank() || pinned == null) {
        ChatSocketEvent.Malformed(FRAME_PIN_UPDATE, "missing conversation or action")
    } else {
        ChatSocketEvent.PinUpdate(
            conversationId = conversationId,
            pinned = pinned,
            pinId = payload.str("pin_id")?.takeIf { it.isNotBlank() },
            messageId = payload.str("message_id")?.takeIf { it.isNotBlank() },
            by = (payload.str("by") ?: payload.str("pinned_by"))?.takeIf { it.isNotBlank() },
            at = (payload.str("at") ?: payload.str("pinned_at"))?.takeIf { it.isNotBlank() },
        )
    }
}

/** `reaction {conversation_id, message_id, user_id, emoji, added}` (message.go ToggleReaction). */
private fun parseReaction(payload: JsonObject): ChatSocketEvent {
    val conversationId = payload.str("conversation_id").orEmpty()
    val messageId = payload.str("message_id").orEmpty()
    val userId = payload.str("user_id").orEmpty()
    val emoji = payload.str("emoji").orEmpty()
    val added = payload.bool("added")
    return if (conversationId.isBlank() || messageId.isBlank() || userId.isBlank() || emoji.isBlank() || added == null) {
        ChatSocketEvent.Malformed(FRAME_REACTION, "missing or blank field")
    } else {
        ChatSocketEvent.ReactionChanged(conversationId, messageId, userId, emoji, added)
    }
}

private fun parsePresence(payload: JsonObject): ChatSocketEvent {
    val userId = payload.str("user_id").orEmpty()
    // Older presence payloads say `status: "online"|"offline"` instead.
    val online = payload.bool("is_online") ?: payload.str("status")?.let { it == "online" }
    return if (userId.isBlank() || online == null) {
        ChatSocketEvent.Malformed(FRAME_PRESENCE_UPDATE, "missing user_id or state")
    } else {
        ChatSocketEvent.PresenceUpdate(userId, online, payload.str("last_active_at")?.takeIf { it.isNotBlank() })
    }
}

/**
 * The typing frames a client may send once scoped rooms are on (CONTRACT
 * §4): `{"type":"typing.start"|"typing.stop","conversation_id":…}`.
 */
fun typingFrame(json: Json, conversationId: String, typing: Boolean): String =
    json.encodeToString(
        TypingFrame.serializer(),
        TypingFrame(type = if (typing) "typing.start" else "typing.stop", conversationId = conversationId),
    )

@kotlinx.serialization.Serializable
internal data class TypingFrame(
    val type: String,
    @kotlinx.serialization.SerialName("conversation_id") val conversationId: String,
)

/**
 * Same fail-closed identifier rule as message frames: a receipt with a blank
 * conversation or user cannot be attributed, and a client that guesses files
 * it against the wrong thread.
 */
private fun parseReadReceipt(payload: JsonObject): ChatSocketEvent {
    val conversationId = payload.str("conversation_id").orEmpty()
    val userId = payload.str("user_id").orEmpty()
    val messageId = payload.str("message_id").orEmpty()
    val readUpTo = (payload.str("read_up_to") ?: payload.str("read_at"))?.takeIf { receiptInstant(it) != null }
    // A GROUP's aggregate receipt names no reader: only a watermark.
    if (payload.bool("aggregate") == true) {
        return if (conversationId.isBlank() || readUpTo == null) {
            ChatSocketEvent.Malformed(FRAME_READ_RECEIPT, "aggregate receipt without conversation or watermark")
        } else {
            ChatSocketEvent.ReceiptWatermark(conversationId, readUpTo = readUpTo)
        }
    }
    return if (conversationId.isBlank() || userId.isBlank() || messageId.isBlank()) {
        ChatSocketEvent.Malformed(FRAME_READ_RECEIPT, "missing or blank identifier")
    } else {
        ChatSocketEvent.ReadReceipt(conversationId, userId, messageId, readUpTo)
    }
}

/**
 * `delivery_receipt {conversation_id, user_id, message_id, delivered_at,
 * delivered_up_to}`: only the conversation and the recipient's aggregate
 * watermark matter here. Without either the frame says nothing.
 */
private fun parseDeliveryReceipt(payload: JsonObject): ChatSocketEvent {
    val conversationId = payload.str("conversation_id").orEmpty()
    val deliveredUpTo = payload.str("delivered_up_to")?.takeIf { receiptInstant(it) != null }
    return if (conversationId.isBlank() || deliveredUpTo == null) {
        ChatSocketEvent.Malformed(FRAME_DELIVERY_RECEIPT, "missing conversation or watermark")
    } else {
        ChatSocketEvent.ReceiptWatermark(conversationId, deliveredUpTo = deliveredUpTo)
    }
}

private fun parseSubscriptionRevoked(payload: JsonObject): ChatSocketEvent {
    val conversationId = payload.str("conversation_id").orEmpty()
    return if (conversationId.isBlank()) {
        ChatSocketEvent.Malformed(FRAME_SUBSCRIPTION_REVOKED, "missing or blank conversation_id")
    } else {
        ChatSocketEvent.SubscriptionRevoked(conversationId)
    }
}

/**
 * The two frames a chat client may SEND (`ws-gateway/server.go:330-395`):
 * built here so a test can pin the exact wire bytes without a socket.
 */
fun subscribeFrame(json: Json, entitlement: String): String =
    json.encodeToString(
        SubscribeFrame.serializer(),
        SubscribeFrame(entitlement = entitlement),
    )

fun unsubscribeFrame(json: Json, conversationId: String): String =
    json.encodeToString(
        UnsubscribeFrame.serializer(),
        UnsubscribeFrame(conversationId = conversationId),
    )

// `type` carries @EncodeDefault because the app-wide Json leaves
// encodeDefaults off — the exact omission that once sent every chat message
// without its `type` field (see SendMessageRequest).
@kotlinx.serialization.Serializable
internal data class SubscribeFrame(
    @kotlinx.serialization.EncodeDefault(kotlinx.serialization.EncodeDefault.Mode.ALWAYS)
    val type: String = "conversation.subscribe",
    val entitlement: String,
)

@kotlinx.serialization.Serializable
internal data class UnsubscribeFrame(
    @kotlinx.serialization.EncodeDefault(kotlinx.serialization.EncodeDefault.Mode.ALWAYS)
    val type: String = "conversation.unsubscribe",
    @kotlinx.serialization.SerialName("conversation_id") val conversationId: String,
)

private fun JsonObject.str(key: String): String? =
    runCatching { this[key]?.jsonPrimitive?.contentOrNull }.getOrNull()

private fun JsonObject.bool(key: String): Boolean? =
    runCatching { this[key]?.jsonPrimitive?.booleanOrNull }.getOrNull()

private const val FRAME_MESSAGE = "message"
private const val FRAME_TYPING = "typing"
private const val FRAME_READ_RECEIPT = "read_receipt"
private const val FRAME_DELIVERY_RECEIPT = "delivery_receipt"
private const val FRAME_SUBSCRIPTION_REVOKED = "subscription_revoked"
private const val FRAME_MESSAGE_DELETED = "message_deleted"
private const val FRAME_DISAPPEARING_TIMER = "disappearing_timer_updated"
private const val FRAME_PIN_UPDATE = "pin_update"

/** `message_deleted.reason` when a disappearing message's timer ran out. */
private const val DELETE_REASON_EXPIRED = "expired"

/** DM consent: a request left the recipient's Requests (it expired). */
private const val FRAME_MESSAGE_REQUEST_REMOVED = "message_request_removed"
private const val FRAME_REACTION = "reaction"
private const val FRAME_PRESENCE_UPDATE = "presence_update"

/**
 * How long to wait before the next reconnection attempt.
 *
 * Exponential ceiling with FULL JITTER: the result is a uniform sample from
 * `[floor, ceiling]`, not the ceiling itself.
 *
 * The jitter is the point. A deterministic schedule is fine for one phone and
 * wrong for a fleet: after a gateway restart, every client that was connected
 * computes the same 1s, then the same 2s, then the same 4s, and the reconnect
 * storm arrives in synchronised waves that keep knocking the gateway back over.
 * The fix is not a longer backoff, it is a spread one.
 *
 * The floor exists because full jitter's lower bound is zero, and a zero delay
 * against a socket that fails instantly is a hot loop that flattens a battery.
 *
 * [randomFraction] is a parameter so the schedule can be asserted without
 * waiting real seconds and without a flaky test: pass 0.0 and 1.0 to pin the
 * range, and leave it defaulted in production.
 */
fun reconnectDelayMillis(
    attempt: Int,
    randomFraction: Double = Random.nextDouble(),
): Long {
    if (attempt <= 0) return 0
    val ceiling = reconnectCeilingMillis(attempt)
    val sampled = (randomFraction.coerceIn(0.0, 1.0) * ceiling).toLong()
    return sampled.coerceAtLeast(MIN_RECONNECT_MILLIS)
}

/** The upper bound [reconnectDelayMillis] samples from for the same attempt. */
fun reconnectCeilingMillis(attempt: Int): Long {
    if (attempt <= 0) return 0
    val exponent = (attempt - 1).coerceAtMost(MAX_BACKOFF_EXPONENT)
    return (BASE_RECONNECT_MILLIS shl exponent).coerceAtMost(MAX_RECONNECT_MILLIS)
}

private const val BASE_RECONNECT_MILLIS = 1_000L
private const val MIN_RECONNECT_MILLIS = 250L

/** The reconnect ceiling never grows past this. */
const val MAX_RECONNECT_MILLIS = 30_000L
private const val MAX_BACKOFF_EXPONENT = 5
