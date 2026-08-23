package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.chat.data.CreateDirectRequest
import com.us.android.core.chat.data.CreateGroupRequest
import com.us.android.core.chat.data.MAX_MESSAGE_LENGTH
import com.us.android.core.chat.data.MAX_RECONNECT_MILLIS
import com.us.android.core.chat.data.MarkReadRequest
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.chat.data.TYPING_TTL_MILLIS
import com.us.android.core.chat.data.ThreadController
import com.us.android.core.chat.data.TypingRequest
import com.us.android.core.chat.data.isValidMessage
import com.us.android.core.chat.data.parseChatFrame
import com.us.android.core.chat.data.reconnectCeilingMillis
import com.us.android.core.chat.data.reconnectDelayMillis
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test
import java.io.IOException

/**
 * Chat history paging, composer validation, send idempotency and retry.
 *
 * Driven through the real [ChatRepository] against a fake [ChatApi] so the
 * envelope and `meta.next_cursor` handling are exercised rather than stubbed.
 *
 * These mirror the comment controller's guarantees WITHOUT sharing its code —
 * chat is a separate data product with its own transport, and the architecture
 * decision keeps the controllers apart while the screens share design-system
 * primitives.
 */
class ThreadControllerTest {

    private val json = Json { ignoreUnknownKeys = true }
    private val conversationId = "c1"

    private class FakeApi : ChatApi {
        val pages = ArrayDeque<() -> ApiEnvelope<List<MessageDto>>>()
        val requestedCursors = mutableListOf<String?>()

        val sends = ArrayDeque<() -> ApiEnvelope<MessageDto>>()
        val idempotencyKeys = mutableListOf<String>()

        /** What was actually sent, so a replayed key cannot hide changed text. */
        val sentTexts = mutableListOf<String>()

        var readMessageIds = mutableListOf<String>()

        override suspend fun conversations(): Nothing = error("not used")
        override suspend fun conversation(conversationId: String): Nothing = error("not used")

        override suspend fun createDirect(
            idempotencyKey: String,
            body: CreateDirectRequest,
        ): Nothing = error("not used")

        override suspend fun createGroup(
            idempotencyKey: String,
            body: CreateGroupRequest,
        ): Nothing = error("not used")

        override suspend fun messages(
            conversationId: String,
            limit: Int,
            cursor: String?,
        ): ApiEnvelope<List<MessageDto>> {
            requestedCursors += cursor
            return pages.removeFirst().invoke()
        }

        override suspend fun send(
            conversationId: String,
            idempotencyKey: String,
            body: SendMessageRequest,
        ): ApiEnvelope<MessageDto> {
            idempotencyKeys += idempotencyKey
            sentTexts += body.text
            return sends.removeFirst().invoke()
        }

        override suspend fun markRead(
            conversationId: String,
            body: MarkReadRequest,
        ): ApiEnvelope<com.us.android.core.chat.data.StatusDto> {
            readMessageIds += body.messageId
            return ApiEnvelope(data = com.us.android.core.chat.data.StatusDto("ok"))
        }

        override suspend fun setTyping(
            conversationId: String,
            body: TypingRequest,
        ): Nothing = error("not used")

        override suspend fun presence(conversationId: String): Nothing = error("not used")
    }

    private fun controller(api: FakeApi) =
        ThreadController(conversationId, ChatRepository(api, ErrorMapper(json)))

    private fun dto(id: String) = MessageDto(
        msgId = id,
        conversationId = conversationId,
        senderId = "u1",
        text = "m$id",
        createdAt = "2026-08-21T09:00:00Z",
    )

    private fun page(vararg ids: String, next: String? = null) = {
        ApiEnvelope(data = ids.map(::dto), meta = ApiMeta(nextCursor = next))
    }

    private fun boom(): () -> Nothing = { throw IOException("offline") }

    // ── Paging ──────────────────────────────────────────────────────────

    @Test
    fun `the first page is requested without a cursor and consumes next_cursor`() = runTest {
        val api = FakeApi().apply { pages += page("1", "2", next = "cur-1") }

        val state = controller(api).refresh()

        assertThat(api.requestedCursors).containsExactly(null)
        assertThat(state.messages.map { it.id }).containsExactly("1", "2").inOrder()
        assertThat(state.nextCursor).isEqualTo("cur-1")
        assertThat(state.canLoadMore).isTrue()
    }

    @Test
    fun `older history appends behind the cursor`() = runTest {
        val api = FakeApi().apply {
            pages += page("2", next = "cur-1")
            pages += page("1", next = null)
        }
        val controller = controller(api)
        controller.refresh()

        val state = controller.loadMore()

        assertThat(api.requestedCursors).containsExactly(null, "cur-1").inOrder()
        assertThat(state.messages.map { it.id }).containsExactly("2", "1").inOrder()
        assertThat(state.canLoadMore).isFalse()
    }

    /**
     * A message sent while the boundary is being read can be returned twice.
     * Appending it blindly duplicates a row and crashes a keyed list.
     */
    @Test
    fun `a message repeated across pages is not duplicated`() = runTest {
        val api = FakeApi().apply {
            pages += page("3", "2", next = "cur-1")
            pages += page("2", "1", next = null)
        }
        val controller = controller(api)
        controller.refresh()

        val state = controller.loadMore()

        assertThat(state.messages.map { it.id }).containsExactly("3", "2", "1").inOrder()
    }

    @Test
    fun `a failed append keeps loaded history and retries the same cursor`() = runTest {
        val api = FakeApi().apply {
            pages += page("2", next = "cur-1")
            pages += boom()
            pages += page("1", next = null)
        }
        val controller = controller(api)
        controller.refresh()

        val failed = controller.loadMore()
        assertThat(failed.messages.map { it.id }).containsExactly("2")
        assertThat(failed.appendError).isNotNull()

        val retried = controller.loadMore()
        assertThat(api.requestedCursors).containsExactly(null, "cur-1", "cur-1").inOrder()
        assertThat(retried.messages.map { it.id }).containsExactly("2", "1").inOrder()
    }

    /** A failed refresh over a populated thread must not blank the conversation. */
    @Test
    fun `a failed refresh preserves loaded messages`() = runTest {
        val api = FakeApi().apply {
            pages += page("1", next = null)
            pages += boom()
        }
        val controller = controller(api)
        controller.refresh()

        val state = controller.refresh()

        assertThat(state.messages.map { it.id }).containsExactly("1")
        assertThat(state.refreshError).isNotNull()
    }

    // ── Composer ────────────────────────────────────────────────────────

    /**
     * The cap is the SERVER's 2,000, not a client-chosen number.
     *
     * `SendMessageRequest.Text` is bound `max=2000` in message-service's
     * handler. The client used to allow 4,000, so a 2,500-character message was
     * accepted by the composer, shown as sending, and rejected by gin with a
     * validation error carrying no field name. The exact boundary is asserted
     * because an off-by-one here is the difference between a working message
     * and an unexplained failure.
     */
    @Test
    fun `blank and oversized drafts cannot be sent`() {
        assertThat("   ".isValidMessage()).isFalse()
        assertThat("".isValidMessage()).isFalse()
        assertThat("real message".isValidMessage()).isTrue()

        assertThat(MAX_MESSAGE_LENGTH).isEqualTo(2_000)
        assertThat("x".repeat(2_000).isValidMessage()).isTrue()
        assertThat("x".repeat(2_001).isValidMessage()).isFalse()
        // Trimmed before measuring, so trailing whitespace does not eat the
        // last usable character.
        assertThat(("x".repeat(2_000) + "   ").isValidMessage()).isTrue()
    }

    @Test
    fun `an over-length draft is reported before send rather than after`() = runTest {
        val api = FakeApi().apply { pages += page(next = null) }
        val controller = controller(api)
        controller.refresh()

        val state = controller.onDraftChange("x".repeat(2_001))

        assertThat(state.draftTooLong).isTrue()
        assertThat(state.canSend).isFalse()
        // Nothing left the device: the point is to explain the limit, not to
        // let the server explain it with a 400.
        assertThat(controller.send().messages.none { it.pending }).isTrue()
        assertThat(api.idempotencyKeys).isEmpty()
    }

    @Test
    fun `a sent message replaces its optimistic row`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            sends += { ApiEnvelope(data = dto("server-1")) }
        }
        val controller = controller(api)
        controller.refresh()
        controller.onDraftChange("hello")

        val state = controller.send()

        assertThat(state.messages.map { it.id }).containsExactly("server-1")
        assertThat(state.messages.single().pending).isFalse()
        assertThat(state.draft).isEmpty()
        assertThat(api.sentTexts).containsExactly("hello")
    }

    /**
     * The server REQUIRES the idempotency key, so an unchanged retry reusing
     * it is what stops a lost response becoming two messages in someone's
     * conversation.
     */
    @Test
    fun `a retried send reuses the key and leaves one message`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            sends += boom()
            sends += { ApiEnvelope(data = dto("server-1")) }
        }
        val controller = controller(api)
        controller.refresh()
        controller.onDraftChange("hello")

        val failed = controller.send()
        assertThat(failed.sendError).isNotNull()
        // The draft survives so retry has something to send.
        assertThat(failed.draft).isEqualTo("hello")
        // The optimistic row is withdrawn; nothing pretends to be delivered.
        assertThat(failed.messages).isEmpty()

        val retried = controller.send()

        assertThat(api.idempotencyKeys).hasSize(2)
        assertThat(api.idempotencyKeys[0]).isEqualTo(api.idempotencyKeys[1])
        assertThat(retried.messages.map { it.id }).containsExactly("server-1")
    }

    /**
     * Editing after a failure must mint a NEW key. Reusing it would let the
     * server replay the ORIGINAL text and silently discard the correction.
     */
    @Test
    fun `editing after a failure mints a new key`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            sends += boom()
            sends += { ApiEnvelope(data = dto("server-1")) }
        }
        val controller = controller(api)
        controller.refresh()
        controller.onDraftChange("original")
        controller.send()

        controller.onDraftChange("corrected")
        controller.send()

        assertThat(api.idempotencyKeys).hasSize(2)
        assertThat(api.idempotencyKeys[0]).isNotEqualTo(api.idempotencyKeys[1])
        assertThat(api.sentTexts).containsExactly("original", "corrected").inOrder()
    }

    @Test
    fun `the next message gets a fresh key`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            sends += { ApiEnvelope(data = dto("s1")) }
            sends += { ApiEnvelope(data = dto("s2")) }
        }
        val controller = controller(api)
        controller.refresh()

        controller.onDraftChange("one")
        controller.send()
        controller.onDraftChange("two")
        controller.send()

        assertThat(api.idempotencyKeys[0]).isNotEqualTo(api.idempotencyKeys[1])
    }

    // ── Realtime ────────────────────────────────────────────────────────

    /**
     * The socket-to-state path, driven by a real [ChatSocketEvent].
     *
     * This is the test the previous implementation did not have, and its
     * absence is why the feature was broken while every test passed: the old
     * realtime tests called `onRealtimeMessage` directly, so nothing ever
     * exercised the step from a decoded frame to a rendered message — and the
     * ViewModel never took that step at all.
     */
    @Test
    fun `a decoded message frame reaches the thread`() = runTest {
        val api = FakeApi().apply { pages += page("1", next = null) }
        val controller = controller(api)
        controller.refresh()

        val event = parseChatFrame(json, MESSAGE_FRAME)
        assertThat(event).isInstanceOf(ChatSocketEvent.MessageReceived::class.java)

        val state = controller.onSocketEvent(event!!)

        assertThat(state).isNotNull()
        assertThat(state!!.messages.first().id).isEqualTo("70efd586-df95-4bca-a63d-058ef9549bff")
        assertThat(state.messages.first().text).isEqualTo("reply from Bravo")
    }

    /**
     * One socket carries EVERY conversation the user belongs to.
     *
     * ws-gateway subscribes the connection to `chat:<user_id>`, not to a room,
     * so an open thread receives messages from other threads too. Rendering
     * them would put a stranger's message in the conversation you are reading.
     */
    @Test
    fun `a message for another conversation is not applied`() = runTest {
        val api = FakeApi().apply { pages += page("1", next = null) }
        val controller = controller(api)
        controller.refresh()

        val applied = controller.onRealtimeMessage(
            Message(
                id = "elsewhere",
                conversationId = "some-other-conversation",
                senderId = "u9",
                senderDisplayName = null,
                text = "not for this thread",
                createdAt = "2026-08-21T09:10:00Z",
            ),
        )

        assertThat(applied).isNull()
        assertThat(controller.snapshot().messages.map { it.id }).containsExactly("1")
    }

    /**
     * THE LAUNCH BLOCKER, at the controller boundary.
     *
     * The check used to be `conversationId.isNotBlank() && conversationId !=
     * this one`, so a message carrying a BLANK conversation id skipped the
     * comparison and was accepted into whichever thread was open. Paired with
     * the parser's `.orEmpty()`, any truncated or foreign frame rendered as a
     * message in a conversation it did not belong to — someone else's words in
     * your thread.
     *
     * The parser now refuses such frames as well. This assertion exists
     * because `onRealtimeMessage` is public: the thread defends its own
     * boundary rather than trusting its caller to have done it.
     */
    @Test
    fun `a message with a blank conversation id is not accepted into this thread`() = runTest {
        val api = FakeApi().apply { pages += page("1", next = null) }
        val controller = controller(api)
        controller.refresh()

        val applied = controller.onRealtimeMessage(
            Message(
                id = "leak",
                conversationId = "",
                senderId = "u9",
                senderDisplayName = null,
                text = "should never render here",
                createdAt = "2026-08-21T09:10:00Z",
            ),
        )

        assertThat(applied).isNull()
        assertThat(controller.snapshot().messages.map { it.id }).containsExactly("1")
    }

    /**
     * A blank id collides with every other blank id: de-duplication then
     * swallows real messages and a keyed LazyColumn gets duplicate keys.
     */
    @Test
    fun `a message with a blank id is rejected`() = runTest {
        val api = FakeApi().apply { pages += page("1", next = null) }
        val controller = controller(api)
        controller.refresh()

        val applied = controller.onRealtimeMessage(
            Message(
                id = "",
                conversationId = conversationId,
                senderId = "u9",
                senderDisplayName = null,
                text = "no id",
                createdAt = "2026-08-21T09:10:00Z",
            ),
        )

        assertThat(applied).isNull()
        assertThat(controller.snapshot().messages.map { it.id }).containsExactly("1")
    }

    /** Without a sender the roster cannot attribute it and the row is anonymous. */
    @Test
    fun `a message with a blank sender is rejected`() = runTest {
        val api = FakeApi().apply { pages += page("1", next = null) }
        val controller = controller(api)
        controller.refresh()

        val applied = controller.onRealtimeMessage(
            Message(
                id = "m9",
                conversationId = conversationId,
                senderId = "",
                senderDisplayName = null,
                text = "no sender",
                createdAt = "2026-08-21T09:10:00Z",
            ),
        )

        assertThat(applied).isNull()
        assertThat(controller.snapshot().messages.map { it.id }).containsExactly("1")
    }

    /** The socket path must refuse it too, not just the direct call. */
    @Test
    fun `a malformed frame decoded from the wire never reaches the thread`() = runTest {
        val api = FakeApi().apply { pages += page("1", next = null) }
        val controller = controller(api)
        controller.refresh()

        val event = parseChatFrame(json, BLANK_CONVERSATION_FRAME)
        assertThat(event).isInstanceOf(ChatSocketEvent.Malformed::class.java)

        assertThat(controller.onSocketEvent(event!!)).isNull()
        assertThat(controller.snapshot().messages.map { it.id }).containsExactly("1")
    }

    /**
     * A realtime frame carries no `sender_display_name`, so a group message
     * would render anonymously without the member roster.
     */
    @Test
    fun `a realtime message is named from the member roster`() = runTest {
        val api = FakeApi().apply { pages += page(next = null) }
        val controller = controller(api)
        controller.refresh()
        controller.setMembers(
            listOf(
                ConversationMember("u1", "admin", "Alpha Btest"),
                ConversationMember("u2", "member", "Bravo Btest"),
            ),
        )

        val state = controller.onSocketEvent(
            ChatSocketEvent.MessageReceived(
                Message(
                    id = "incoming",
                    conversationId = conversationId,
                    senderId = "u2",
                    senderDisplayName = null,
                    text = "hi",
                    createdAt = "2026-08-21T09:10:00Z",
                ),
            ),
        )

        assertThat(state!!.messages.first().senderDisplayName).isEqualTo("Bravo Btest")
    }

    /**
     * The roster usually arrives after the first page of history, so rows
     * already on screen have to be re-named rather than left anonymous until
     * the next refresh.
     */
    @Test
    fun `the roster names messages that were already loaded`() = runTest {
        val api = FakeApi().apply { pages += page("1", next = null) }
        val controller = controller(api)
        controller.refresh()
        assertThat(controller.snapshot().messages.first().senderDisplayName).isNull()

        val state = controller.setMembers(
            listOf(ConversationMember("u1", "member", "Alpha Btest")),
        )

        assertThat(state.messages.first().senderDisplayName).isEqualTo("Alpha Btest")
    }

    /**
     * The SEND response omits `sender_display_name` while the LIST response
     * includes it, so without the roster the one message with a blank name in
     * the thread is the one the user just sent.
     */
    @Test
    fun `a just-sent message is named from the roster too`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            sends += { ApiEnvelope(data = dto("server-1").copy(senderDisplayName = null)) }
        }
        val controller = controller(api)
        controller.refresh()
        controller.setMembers(listOf(ConversationMember("u1", "member", "Alpha Btest")))
        controller.onDraftChange("hello")

        val state = controller.send()

        assertThat(state.messages.first().senderDisplayName).isEqualTo("Alpha Btest")
    }

    @Test
    fun `typing is applied for this conversation and ignored for another`() = runTest {
        val api = FakeApi().apply { pages += page(next = null) }
        val controller = controller(api)
        controller.refresh()

        val typing = parseChatFrame(json, TYPING_FRAME)
        assertThat(controller.onSocketEvent(typing!!)!!.typingUserIds).containsExactly("u2")

        assertThat(
            controller.onSocketEvent(
                ChatSocketEvent.Typing("another-conversation", "u3", isTyping = true),
            ),
        ).isNull()
        assertThat(controller.snapshot().typingUserIds).containsExactly("u2")

        // There is no stop frame on the wire; the caller expires it on the
        // server's TTL, and this is the call that does it.
        assertThat(controller.onTypingStopped("u2")!!.typingUserIds).isEmpty()
    }

    @Test
    fun `the typing TTL matches the server key expiry`() {
        assertThat(TYPING_TTL_MILLIS).isEqualTo(3_000)
    }

    @Test
    fun `a message from someone clears their typing indicator`() = runTest {
        val api = FakeApi().apply { pages += page(next = null) }
        val controller = controller(api)
        controller.refresh()
        controller.onTypingStarted("u2")

        val state = controller.onSocketEvent(
            ChatSocketEvent.MessageReceived(
                Message(
                    id = "m9",
                    conversationId = conversationId,
                    senderId = "u2",
                    senderDisplayName = null,
                    text = "sent it",
                    createdAt = "2026-08-21T09:10:00Z",
                ),
            ),
        )

        assertThat(state!!.typingUserIds).isEmpty()
    }

    @Test
    fun `connection events change no thread state`() = runTest {
        val api = FakeApi().apply { pages += page("1", next = null) }
        val controller = controller(api)
        controller.refresh()

        assertThat(controller.onSocketEvent(ChatSocketEvent.Connected)).isNull()
        assertThat(controller.onSocketEvent(ChatSocketEvent.Disconnected(permanent = false)))
            .isNull()
        assertThat(controller.onSocketEvent(ChatSocketEvent.Unknown("read_receipt"))).isNull()
    }

    /**
     * A sender receives their own message twice — once as the HTTP response,
     * once as the broadcast. Showing both is the classic chat double-send.
     *
     * message-service does NOT echo to the sender today (`message.go:897`
     * skips them during fan-out), so this guards the reconnect case where a
     * refresh and a live frame overlap — and the day that fan-out changes.
     */
    @Test
    fun `a realtime echo of an already-known message is ignored`() = runTest {
        val api = FakeApi().apply {
            pages += page(next = null)
            sends += { ApiEnvelope(data = dto("server-1")) }
        }
        val controller = controller(api)
        controller.refresh()
        controller.onDraftChange("hello")
        controller.send()

        val applied = controller.onRealtimeMessage(
            Message(
                id = "server-1",
                conversationId = conversationId,
                senderId = "u1",
                senderDisplayName = "Alpha",
                text = "hello",
                createdAt = "2026-08-21T09:00:00Z",
            ),
        )

        // Null means "changed nothing", so the caller can skip a pointless
        // state emission rather than re-rendering the whole thread.
        assertThat(applied).isNull()
        assertThat(controller.snapshot().messages.map { it.id }).containsExactly("server-1")
    }

    @Test
    fun `a realtime message from someone else is prepended`() = runTest {
        val api = FakeApi().apply { pages += page("1", next = null) }
        val controller = controller(api)
        controller.refresh()

        val state = controller.onRealtimeMessage(
            Message(
                id = "incoming",
                conversationId = conversationId,
                senderId = "u2",
                senderDisplayName = "Bravo",
                text = "hi",
                createdAt = "2026-08-21T09:10:00Z",
            ),
        )

        assertThat(state).isNotNull()
        assertThat(state!!.messages.map { it.id }).containsExactly("incoming", "1").inOrder()
    }

    // ── Read state ──────────────────────────────────────────────────────

    @Test
    fun `mark read sends the newest confirmed message and ignores pending rows`() = runTest {
        val api = FakeApi().apply { pages += page("2", "1", next = null) }
        val controller = controller(api)
        controller.refresh()

        controller.markRead()

        assertThat(api.readMessageIds).containsExactly("2")
    }

    @Test
    fun `mark read on an empty thread does nothing`() = runTest {
        val api = FakeApi().apply { pages += page(next = null) }
        val controller = controller(api)
        controller.refresh()

        assertThat(controller.markRead()).isNull()
        assertThat(api.readMessageIds).isEmpty()
    }

    // ── Reconnection ────────────────────────────────────────────────────

    /**
     * The CEILING is exponential and capped. A phone moving between cells drops
     * the socket constantly, and a fixed short delay turns that into a
     * reconnect storm that drains the battery and looks like an attack.
     */
    @Test
    fun `the reconnect ceiling backs off exponentially and stops growing`() {
        assertThat(reconnectCeilingMillis(0)).isEqualTo(0)
        assertThat(reconnectCeilingMillis(1)).isEqualTo(1_000)
        assertThat(reconnectCeilingMillis(2)).isEqualTo(2_000)
        assertThat(reconnectCeilingMillis(3)).isEqualTo(4_000)
        assertThat(reconnectCeilingMillis(4)).isEqualTo(8_000)
        assertThat(reconnectCeilingMillis(5)).isEqualTo(16_000)
        // Capped, so a long outage does not schedule a reconnect an hour out.
        assertThat(reconnectCeilingMillis(6)).isEqualTo(MAX_RECONNECT_MILLIS)
        assertThat(reconnectCeilingMillis(50)).isEqualTo(MAX_RECONNECT_MILLIS)
    }

    /**
     * The DELAY is a range, not a number — and that is the fix.
     *
     * The previous schedule was deterministic: every client that lost the same
     * gateway computed the same 1s, then the same 2s, then the same 4s, and
     * reconnected in synchronised waves that keep a recovering gateway down.
     * Asserting one exact delay is how a fleet-scale defect passes a unit test,
     * so the assertion here is on the range and the floor instead.
     */
    @Test
    fun `reconnect delay is jittered within its ceiling`() {
        assertThat(reconnectDelayMillis(0, randomFraction = 0.5)).isEqualTo(0)

        for (attempt in 1..10) {
            val ceiling = reconnectCeilingMillis(attempt)
            assertThat(reconnectDelayMillis(attempt, randomFraction = 1.0)).isEqualTo(ceiling)
            // Full jitter's lower bound is zero; the floor keeps a socket that
            // fails instantly from becoming a hot loop.
            val lowest = reconnectDelayMillis(attempt, randomFraction = 0.0)
            assertThat(lowest).isGreaterThan(0)
            assertThat(lowest).isAtMost(ceiling)

            val mid = reconnectDelayMillis(attempt, randomFraction = 0.5)
            assertThat(mid).isAtLeast(lowest)
            assertThat(mid).isAtMost(ceiling)
        }
    }

    /** Two clients backing off at the same attempt must not agree. */
    @Test
    fun `two clients at the same attempt get different delays`() {
        val a = reconnectDelayMillis(attempt = 5, randomFraction = 0.10)
        val b = reconnectDelayMillis(attempt = 5, randomFraction = 0.90)
        assertThat(a).isNotEqualTo(b)
    }

    private companion object {
        /**
         * The message frame, exactly as message-service publishes it.
         *
         * The id field is `message_id` — the REST list calls the same value
         * `msg_id`, and reading the wrong one here yields a blank id on every
         * message.
         */
        const val MESSAGE_FRAME = """
        {"type":"message","payload":{
          "conversation_id":"c1","message_id":"70efd586-df95-4bca-a63d-058ef9549bff",
          "sender_id":"u2","type":"text","text":"reply from Bravo","media_id":null,
          "created_at":"2026-08-21T09:09:19.084Z"}}
        """

        const val TYPING_FRAME = """
        {"type":"typing","payload":{"conversation_id":"c1","user_id":"u2","is_typing":true}}
        """

        /** A message frame whose conversation id is present but empty. */
        const val BLANK_CONVERSATION_FRAME = """
        {"type":"message","payload":{
          "conversation_id":"","message_id":"leak","sender_id":"u9","text":"not for this thread"}}
        """
    }
}
