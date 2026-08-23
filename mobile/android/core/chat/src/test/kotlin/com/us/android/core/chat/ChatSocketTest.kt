package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.parseChatFrame
import kotlinx.coroutines.flow.take
import kotlinx.coroutines.flow.toList
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.OkHttpClient
import okhttp3.WebSocket
import okhttp3.WebSocketListener
import org.junit.After
import org.junit.Before
import org.junit.Test

/**
 * The socket, from bytes on the wire to a typed event.
 *
 * ## WHY THIS FILE EXISTS
 *
 * The previous implementation had no test at this layer at all. Its realtime
 * tests called the controller directly, so nothing exercised the parser — and
 * the parser was wrong: it recognised flat `typing.started` and
 * `presence_update` frames, neither of which any server code emits, and had no
 * case for `message`. The result was a socket that connected, stayed open, and
 * delivered nothing, with a green test suite over it.
 *
 * Every frame below is the exact shape message-service publishes
 * (`message.go:868` for messages, `message.go:1419` for typing) and ws-gateway
 * forwards verbatim (`server.go:614`).
 */
class ChatSocketTest {

    private val json = Json { ignoreUnknownKeys = true }
    private lateinit var server: MockWebServer
    private lateinit var client: OkHttpClient

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        client = OkHttpClient()
    }

    /**
     * The client is torn down BEFORE the server.
     *
     * MockWebServer refuses to shut down while a connection is still open and
     * fails the test with "Gave up waiting for queue to shut down" — in
     * `tearDown`, so the reported failure names the wrong test and says nothing
     * about the assertion, which passed.
     */
    @After
    fun tearDown() {
        client.dispatcher.executorService.shutdown()
        client.connectionPool.evictAll()
        server.close()
    }

    private fun socket() = ChatSocket(
        client = client,
        wsBaseUrl = server.url("/").toString().removeSuffix("/"),
        json = json,
    )

    // ── The parser, against captured frames ─────────────────────────────

    /**
     * The id is `message_id`, NOT the REST list's `msg_id`.
     *
     * A parser reading `msg_id` here decodes every message to a blank id. Every
     * row then collides on the same key, a keyed LazyColumn shows one of them,
     * and de-duplication silently swallows the rest.
     */
    @Test
    fun `a message frame decodes using message_id`() {
        val event = parseChatFrame(json, MESSAGE_FRAME)

        assertThat(event).isInstanceOf(ChatSocketEvent.MessageReceived::class.java)
        val message = (event as ChatSocketEvent.MessageReceived).message
        assertThat(message.id).isEqualTo("70efd586-df95-4bca-a63d-058ef9549bff")
        assertThat(message.conversationId).isEqualTo("4988558d-9914-41b8-9d74-e76cc7c94e96")
        assertThat(message.senderId).isEqualTo("3922df6c-3661-41fb-a794-b646c17c299e")
        assertThat(message.text).isEqualTo("reply from Bravo")
        assertThat(message.createdAt).isEqualTo("2026-08-21T09:09:19.084Z")
        // Not on the wire. The thread names it from the member roster, which
        // is why chat needs no per-message profile request.
        assertThat(message.senderDisplayName).isNull()
        assertThat(message.pending).isFalse()
    }

    /**
     * The payload is NESTED.
     *
     * A parser that reads `conversation_id` off the envelope finds nothing and
     * files every message under the empty string — which then matches no open
     * thread, so nothing renders.
     */
    @Test
    fun `a flat frame is not mistaken for a message`() {
        val flat = """{"type":"message","conversation_id":"c1","message_id":"m1"}"""

        // No `payload` object at all: reported as unmodelled rather than
        // decoded into a message with empty fields.
        assertThat(parseChatFrame(json, flat)).isEqualTo(ChatSocketEvent.Unknown("message"))
    }

    @Test
    fun `a typing frame decodes with its nested is_typing flag`() {
        val event = parseChatFrame(json, TYPING_FRAME)

        assertThat(event).isEqualTo(
            ChatSocketEvent.Typing(
                conversationId = "4988558d-9914-41b8-9d74-e76cc7c94e96",
                userId = "3922df6c-3661-41fb-a794-b646c17c299e",
                isTyping = true,
            ),
        )
    }

    /**
     * The frame names the OLD parser recognised.
     *
     * `typing.started` and `presence_update` are the two it handled, and
     * neither is published by any server code — `typing.started` is an INBOUND
     * client frame name that ws-gateway rejects while scoped rooms are off.
     * Pinning them as unmodelled is what stops the old contract creeping back.
     */
    @Test
    fun `frame names the old parser invented are not recognised`() {
        val started = """{"type":"typing.started","conversation_id":"c1","user_id":"u2"}"""
        val presence = """{"type":"presence_update","conversation_id":"c1","active_count":"2"}"""

        assertThat(parseChatFrame(json, started)).isEqualTo(ChatSocketEvent.Unknown("typing.started"))
        assertThat(parseChatFrame(json, presence)).isEqualTo(
            ChatSocketEvent.Unknown("presence_update"),
        )
    }

    /**
     * ws-gateway re-wraps a non-JSON Redis payload as
     * `{"type":"message","payload":"<raw string>"}` (`server.go:616`).
     *
     * The payload is then a STRING. Asking for `.jsonObject` on it throws, and
     * this parse runs inside an OkHttp callback where an exception kills the
     * socket rather than the message.
     */
    @Test
    fun `a string payload does not crash the parser`() {
        val wrapped = """{"type":"message","payload":"not json at all"}"""

        assertThat(parseChatFrame(json, wrapped)).isEqualTo(ChatSocketEvent.Unknown("message"))
    }

    @Test
    fun `junk and typeless frames are dropped rather than thrown`() {
        assertThat(parseChatFrame(json, "not json")).isNull()
        assertThat(parseChatFrame(json, "[1,2,3]")).isNull()
        assertThat(parseChatFrame(json, """{"payload":{}}""")).isNull()
    }

    // ── Fail closed on the three identifiers ────────────────────────────

    /**
     * A frame with NO `conversation_id` must never become a message.
     *
     * This is the launch blocker the closure review found. The parser used
     * `.orEmpty()` on all three identifiers, and the thread's own filter only
     * rejected a NONBLANK mismatch — so a blank one skipped the comparison and
     * the message rendered inside whichever conversation was open. A truncated
     * or foreign frame therefore showed a stranger's message in your thread.
     */
    @Test
    fun `a message frame with no conversation_id is refused`() {
        val frame = """
        {"type":"message","payload":{
          "message_id":"m1","sender_id":"u2","type":"text","text":"leak",
          "created_at":"2026-08-21T09:09:19.084Z"}}
        """

        val event = parseChatFrame(json, frame)

        assertThat(event).isInstanceOf(ChatSocketEvent.Malformed::class.java)
        assertThat((event as ChatSocketEvent.Malformed).reason).contains("conversation_id")
    }

    /** Present-but-empty is the same failure as absent, and was equally accepted. */
    @Test
    fun `a message frame with a blank conversation_id is refused`() {
        val frame = """
        {"type":"message","payload":{
          "conversation_id":"","message_id":"m1","sender_id":"u2","text":"leak"}}
        """

        val event = parseChatFrame(json, frame)

        assertThat(event).isInstanceOf(ChatSocketEvent.Malformed::class.java)
        assertThat((event as ChatSocketEvent.Malformed).reason).contains("conversation_id")
    }

    /**
     * A blank id collides with every other blank id, so de-duplication drops
     * real messages and a keyed list is handed duplicate keys.
     */
    @Test
    fun `a message frame with no message_id is refused`() {
        val frame = """
        {"type":"message","payload":{"conversation_id":"c1","sender_id":"u2","text":"hi"}}
        """

        val event = parseChatFrame(json, frame)

        assertThat(event).isInstanceOf(ChatSocketEvent.Malformed::class.java)
        assertThat((event as ChatSocketEvent.Malformed).reason).contains("message_id")
    }

    /** Without a sender the roster lookup misses and the row renders anonymous. */
    @Test
    fun `a message frame with no sender_id is refused`() {
        val frame = """
        {"type":"message","payload":{"conversation_id":"c1","message_id":"m1","text":"hi"}}
        """

        val event = parseChatFrame(json, frame)

        assertThat(event).isInstanceOf(ChatSocketEvent.Malformed::class.java)
        assertThat((event as ChatSocketEvent.Malformed).reason).contains("sender_id")
    }

    /** All three are reported at once, so one round trip names every problem. */
    @Test
    fun `an empty message payload names all three missing identifiers`() {
        val event = parseChatFrame(json, """{"type":"message","payload":{}}""")

        assertThat(event).isInstanceOf(ChatSocketEvent.Malformed::class.java)
        val reason = (event as ChatSocketEvent.Malformed).reason
        assertThat(reason).contains("message_id")
        assertThat(reason).contains("conversation_id")
        assertThat(reason).contains("sender_id")
    }

    /**
     * Refused, not silently dropped, and NOT reported as `Unknown`.
     *
     * "Unknown type=message" would tell whoever is debugging that this client
     * has no message support, which is the opposite of true.
     */
    @Test
    fun `a refused message frame is distinguishable from an unmodelled one`() {
        val malformed = parseChatFrame(json, """{"type":"message","payload":{}}""")
        val unmodelled = parseChatFrame(json, """{"type":"read_receipt","payload":{}}""")

        assertThat(malformed).isInstanceOf(ChatSocketEvent.Malformed::class.java)
        assertThat(unmodelled).isEqualTo(ChatSocketEvent.Unknown("read_receipt"))
    }

    /** A complete frame still decodes — the guard must not refuse valid traffic. */
    @Test
    fun `a complete frame is still accepted`() {
        assertThat(parseChatFrame(json, MESSAGE_FRAME))
            .isInstanceOf(ChatSocketEvent.MessageReceived::class.java)
    }

    // ── The socket, end to end ──────────────────────────────────────────

    /**
     * A real WebSocket upgrade, a real frame, a typed event.
     *
     * This is the path the acceptance criteria describe: bytes arriving on the
     * socket become an event a thread can apply, without a controller call
     * standing in for the transport.
     */
    @Test
    fun `an open socket delivers a decoded message`() = runBlocking {
        server.enqueue(
            MockResponse.Builder()
                .webSocketUpgrade(
                    object : WebSocketListener() {
                        override fun onOpen(webSocket: WebSocket, response: okhttp3.Response) {
                            webSocket.send(MESSAGE_FRAME)
                            // Closed from the server side after the frame is
                            // queued. Without this the server half stays open
                            // once the client cancels, and MockWebServer fails
                            // the whole test in tearDown with "gave up waiting
                            // for queue to shut down" — naming a shutdown
                            // problem instead of the assertion, which passed.
                            webSocket.close(NORMAL_CLOSURE, null)
                        }
                    },
                )
                .build(),
        )

        val events = withTimeout(SOCKET_TIMEOUT_MILLIS) {
            socket().connect { "test-token" }.take(2).toList()
        }

        assertThat(events.first()).isEqualTo(ChatSocketEvent.Connected)
        val received = events[1] as ChatSocketEvent.MessageReceived
        assertThat(received.message.id).isEqualTo("70efd586-df95-4bca-a63d-058ef9549bff")
    }

    /**
     * The credential travels in the Authorization header, never the URL.
     *
     * The gateway also accepts `?token=` when `WS_ALLOW_QUERY_TOKEN` is set,
     * and a URL carrying a credential ends up in proxy logs, crash reports and
     * `adb logcat`. Asserted rather than assumed, because switching to the
     * query parameter is a one-line change that leaks a session token.
     */
    @Test
    fun `the token is sent as a header and never in the url`() = runBlocking {
        server.enqueue(
            MockResponse.Builder()
                .webSocketUpgrade(
                    object : WebSocketListener() {
                        override fun onOpen(webSocket: WebSocket, response: okhttp3.Response) {
                            webSocket.close(NORMAL_CLOSURE, null)
                        }
                    },
                )
                .build(),
        )

        withTimeout(SOCKET_TIMEOUT_MILLIS) {
            socket().connect { "secret-token" }.take(1).toList()
        }

        val request = server.takeRequest()
        assertThat(request.headers["Authorization"]).isEqualTo("Bearer secret-token")
        assertThat(request.target).contains("/v1/ws/connect")
        assertThat(request.target).doesNotContain("secret-token")
    }

    /** No credential means no connection attempt, and no retry loop either. */
    @Test
    fun `a missing token fails permanently without opening a socket`() = runBlocking {
        val events = withTimeout(SOCKET_TIMEOUT_MILLIS) {
            socket().connect { null }.toList()
        }

        assertThat(events).containsExactly(ChatSocketEvent.Disconnected(permanent = true))
        assertThat(server.requestCount).isEqualTo(0)
    }

    private companion object {
        const val SOCKET_TIMEOUT_MILLIS = 10_000L
        const val NORMAL_CLOSURE = 1000

        /** Exactly the shape message-service publishes after a durable send. */
        const val MESSAGE_FRAME = """
        {"type":"message","payload":{
          "conversation_id":"4988558d-9914-41b8-9d74-e76cc7c94e96",
          "message_id":"70efd586-df95-4bca-a63d-058ef9549bff",
          "sender_id":"3922df6c-3661-41fb-a794-b646c17c299e",
          "type":"text","text":"reply from Bravo","media_id":null,
          "created_at":"2026-08-21T09:09:19.084Z"}}
        """

        const val TYPING_FRAME = """
        {"type":"typing","payload":{
          "conversation_id":"4988558d-9914-41b8-9d74-e76cc7c94e96",
          "user_id":"3922df6c-3661-41fb-a794-b646c17c299e",
          "is_typing":true}}
        """
    }
}
