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
    ) : ChatSocketEvent

    /**
     * The viewer's room subscription for [conversationId] was revoked —
     * they were removed from the conversation. The session manager drops the
     * room; screens showing it fall back to their next refresh's 403.
     */
    data class SubscriptionRevoked(val conversationId: String) : ChatSocketEvent

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
class ChatSocket(
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
    fun send(frame: String): Boolean = active?.send(frame) ?: false

    /**
     * Opens the socket and emits until it closes.
     *
     * [tokenProvider] is read at CONNECT time, not captured once: a reconnect
     * after a token refresh must present the new token, and a stale one would
     * be rejected forever.
     */
    fun connect(tokenProvider: () -> String?): Flow<ChatSocketEvent> = callbackFlow {
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
fun parseChatFrame(json: Json, text: String): ChatSocketEvent? {
    val frame = runCatching { json.parseToJsonElement(text).jsonObject }.getOrNull() ?: return null
    val type = frame.str("type") ?: return null

    // ws-gateway re-wraps a non-JSON Redis payload as {"type":"message",
    // "payload":"<the raw string>"} (server.go:616). The payload is then a
    // string, not an object, and asking for .jsonObject would throw inside a
    // socket callback.
    val payload = runCatching { frame["payload"]?.jsonObject }.getOrNull()
        ?: return ChatSocketEvent.Unknown(type)

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
            val id = payload.str("message_id").orEmpty()
            val conversationId = payload.str("conversation_id").orEmpty()
            val senderId = payload.str("sender_id").orEmpty()
            val missing = buildList {
                if (id.isBlank()) add("message_id")
                if (conversationId.isBlank()) add("conversation_id")
                if (senderId.isBlank()) add("sender_id")
            }
            if (missing.isNotEmpty()) {
                ChatSocketEvent.Malformed(type, "missing or blank: " + missing.joinToString(", "))
            } else {
                ChatSocketEvent.MessageReceived(
                    Message(
                        id = id,
                        conversationId = conversationId,
                        senderId = senderId,
                        // Not on the wire. The thread fills it in from the
                        // member list it already holds, so a group message
                        // never costs a profile request per row.
                        senderDisplayName = null,
                        text = payload.str("text").orEmpty(),
                        createdAt = payload.str("created_at").orEmpty(),
                    ),
                )
            }
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

        FRAME_SUBSCRIPTION_REVOKED -> parseSubscriptionRevoked(payload)

        else -> ChatSocketEvent.Unknown(type)
    }
}

/**
 * Same fail-closed identifier rule as message frames: a receipt with a blank
 * conversation or user cannot be attributed, and a client that guesses files
 * it against the wrong thread.
 */
private fun parseReadReceipt(payload: JsonObject): ChatSocketEvent {
    val conversationId = payload.str("conversation_id").orEmpty()
    val userId = payload.str("user_id").orEmpty()
    val messageId = payload.str("message_id").orEmpty()
    return if (conversationId.isBlank() || userId.isBlank() || messageId.isBlank()) {
        ChatSocketEvent.Malformed(FRAME_READ_RECEIPT, "missing or blank identifier")
    } else {
        ChatSocketEvent.ReadReceipt(conversationId, userId, messageId)
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
private const val FRAME_SUBSCRIPTION_REVOKED = "subscription_revoked"

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
