package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.CALL_MESSAGE
import com.us.android.core.chat.data.ChatPinsApi
import com.us.android.core.chat.data.ChatPinsRepository
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.PinnedMessage
import com.us.android.core.chat.data.applyPinUpdate
import com.us.android.core.chat.data.canPin
import com.us.android.core.chat.data.nextPinIndex
import com.us.android.core.chat.data.parseChatFrame
import com.us.android.core.common.result.AppResult
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory

/**
 * Pinned messages (2026-10-22) against message-service's handlers
 * (`messenger_extras_handler.go`: `PinMessage` 201 with the pin row,
 * `UnpinMessage` 200 `{"status":"message unpinned"}`, `GetConversationPins`
 * newest first; the cap is 400 `INVALID_REQUEST`), the `pin_update` frame
 * `publishPinUpdate` sends, and the rules the bar and the long-press use.
 */
class ChatPinsTest {

    /** NetworkModule.provideJson, exactly. */
    private val json = Json {
        ignoreUnknownKeys = true
        explicitNulls = false
        coerceInputValues = true
        isLenient = true
    }

    private lateinit var server: MockWebServer
    private lateinit var pins: ChatPinsRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .client(OkHttpClient())
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(ChatPinsApi::class.java)
        pins = ChatPinsRepository(api, ErrorMapper(json))
    }

    @After
    fun tearDown() = server.close()

    private fun respond(body: String, code: Int = 200) = server.enqueue(
        MockResponse.Builder().code(code).setHeader("Content-Type", "application/json").body(body).build(),
    )

    private fun pinRow(id: String, message: String, at: String) =
        """{"id":"$id","conversation_id":"c1","message_id":"$message","pinned_by":"u2","pinned_at":"$at"}"""

    @Test
    fun `the pins are a GET on the conversation's pins, kept in the server's order`() = runTest {
        respond("""{"data":[${pinRow("p2", "m2", "2026-10-22T10:05:00Z")},${pinRow("p1", "m1", "2026-10-22T10:00:00Z")}]}""")

        val result = pins.pins("c1")

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("GET")
        assertThat(request.target).isEqualTo("/v1/chat/conversations/c1/pins")
        assertThat((result as AppResult.Success).data.map { it.id }).containsExactly("p2", "p1").inOrder()
        assertThat(result.data.first()).isEqualTo(PinnedMessage("p2", "m2", "u2", "2026-10-22T10:05:00Z"))
    }

    /** A Go nil slice is `"data": null`: no pins, not a failure. */
    @Test
    fun `no pins is an empty list`() = runTest {
        respond("""{"data":null}""")

        assertThat((pins.pins("c1") as AppResult.Success).data).isEmpty()
    }

    @Test
    fun `pin posts the message id and answers the new pin`() = runTest {
        respond("""{"data":${pinRow("p9", "m9", "2026-10-22T11:00:00Z")}}""", code = 201)

        val result = pins.pin("c1", "m9")

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("POST")
        assertThat(request.target).isEqualTo("/v1/chat/conversations/c1/pins")
        assertThat(request.body?.utf8()).isEqualTo("""{"message_id":"m9"}""")
        assertThat((result as AppResult.Success).data.id).isEqualTo("p9")
    }

    @Test
    fun `a fourth pin is refused by the server, and that is a failure`() = runTest {
        respond("""{"error":{"code":"INVALID_REQUEST","message":"maximum 3 pinned messages per conversation"}}""", code = 400)

        assertThat(pins.pin("c1", "m4")).isInstanceOf(AppResult.Failure::class.java)
    }

    @Test
    fun `unpin is a DELETE on the PIN id, not the message id`() = runTest {
        respond("""{"data":{"status":"message unpinned"}}""")

        val result = pins.unpin("c1", "p9")

        val request = server.takeRequest()
        assertThat(request.method).isEqualTo("DELETE")
        assertThat(request.target).isEqualTo("/v1/chat/conversations/c1/pins/p9")
        assertThat(result).isInstanceOf(AppResult.Success::class.java)
    }

    @Test
    fun `an unpin of a pin that is gone is a failure the thread can tell apart`() = runTest {
        respond("""{"error":{"code":"NOT_FOUND","message":"pin not found"}}""", code = 404)

        assertThat(pins.unpin("c1", "gone")).isInstanceOf(AppResult.Failure::class.java)
    }

    // ── The frame ───────────────────────────────────────────────────────

    @Test
    fun `the pin_update frame decodes both ways, and fails closed without its chat or action`() {
        assertThat(
            parseChatFrame(
                json,
                """{"type":"pin_update","payload":{"conversation_id":"c1","action":"pinned","pin_id":"p1",""" +
                    """"message_id":"m1","by":"u2","at":"2026-10-22T10:00:00Z"}}""",
            ),
        ).isEqualTo(ChatSocketEvent.PinUpdate("c1", pinned = true, pinId = "p1", messageId = "m1", by = "u2", at = "2026-10-22T10:00:00Z"))
        assertThat(
            parseChatFrame(json, """{"type":"pin_update","payload":{"conversation_id":"c1","action":"unpinned","pin_id":"p1"}}"""),
        ).isEqualTo(ChatSocketEvent.PinUpdate("c1", pinned = false, pinId = "p1"))
        assertThat(parseChatFrame(json, """{"type":"pin_update","payload":{"action":"pinned"}}"""))
            .isInstanceOf(ChatSocketEvent.Malformed::class.java)
        assertThat(parseChatFrame(json, """{"type":"pin_update","payload":{"conversation_id":"c1","action":"moved"}}"""))
            .isInstanceOf(ChatSocketEvent.Malformed::class.java)
    }

    // ── The rules ───────────────────────────────────────────────────────

    private val p1 = PinnedMessage("p1", "m1", "u", "")
    private val p2 = PinnedMessage("p2", "m2", "u", "")

    @Test
    fun `a pin frame joins at the front once, an unpin frame removes by pin or message id`() {
        val pinned = ChatSocketEvent.PinUpdate("c1", pinned = true, pinId = "p3", messageId = "m3")
        assertThat(applyPinUpdate(listOf(p1, p2), pinned)!!.map { it.id }).containsExactly("p3", "p1", "p2").inOrder()
        assertThat(applyPinUpdate(listOf(p1), ChatSocketEvent.PinUpdate("c1", true, "p1", "m1"))).containsExactly(p1)

        assertThat(applyPinUpdate(listOf(p1, p2), ChatSocketEvent.PinUpdate("c1", false, pinId = "p1"))).containsExactly(p2)
        assertThat(applyPinUpdate(listOf(p1, p2), ChatSocketEvent.PinUpdate("c1", false, messageId = "m2"))).containsExactly(p1)
    }

    /** A pin with no id cannot be unpinned later: the thread reads the list instead. */
    @Test
    fun `a pin frame without its pin id asks for a fresh read`() {
        assertThat(applyPinUpdate(listOf(p1), ChatSocketEvent.PinUpdate("c1", pinned = true, messageId = "m3"))).isNull()
    }

    @Test
    fun `the bar steps through the pins and wraps`() {
        assertThat(nextPinIndex(0, 3)).isEqualTo(1)
        assertThat(nextPinIndex(2, 3)).isEqualTo(0)
        assertThat(nextPinIndex(0, 1)).isEqualTo(0)
        assertThat(nextPinIndex(4, 0)).isEqualTo(0)
    }

    @Test
    fun `any message a member sees may be pinned, but not a call line, one sending, or one unsent`() {
        val text = Message(id = "m1", conversationId = "c1", senderId = "u", senderDisplayName = null, text = "hi", createdAt = "")
        assertThat(text.canPin()).isTrue()
        assertThat(text.copy(type = CALL_MESSAGE).canPin()).isFalse()
        assertThat(text.copy(pending = true).canPin()).isFalse()
        assertThat(text.copy(deleted = true).canPin()).isFalse()
        assertThat(text.copy(id = "").canPin()).isFalse()
    }
}
