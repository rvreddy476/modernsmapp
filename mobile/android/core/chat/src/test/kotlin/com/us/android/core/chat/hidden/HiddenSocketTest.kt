package com.us.android.core.chat.hidden

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.RoomNoticeKind
import com.us.android.core.chat.data.SubscriptionEntitlementDto
import com.us.android.core.chat.data.parseChatFrame
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.TokenProvider
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.onSubscription
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import org.junit.Test
import java.util.concurrent.CopyOnWriteArrayList
import java.util.concurrent.atomic.AtomicInteger

/**
 * A hidden chat on the socket: its room is subscribed with the open token
 * (never an entitlement), re-subscribed when the gateway lets it lapse, and
 * a refused token closes the chats.
 */
class HiddenSocketTest {

    private val json = Json { ignoreUnknownKeys = true }

    // ── Frames ────────────────────────────────────────────────────────────

    @Test
    fun `the gateway's room notices parse, flat or nested`() {
        assertThat(parseChatFrame(json, """{"type":"subscription_refused","conversation_id":"c1","reason":"chat_locked"}"""))
            .isEqualTo(ChatSocketEvent.RoomNotice(RoomNoticeKind.Refused, "c1", "chat_locked"))
        assertThat(parseChatFrame(json, """{"type":"subscription_expired","conversation_id":"c1"}"""))
            .isEqualTo(ChatSocketEvent.RoomNotice(RoomNoticeKind.Expired, "c1", null))
        assertThat(parseChatFrame(json, """{"type":"conversation_hidden","payload":{"conversation_id":"c2"}}"""))
            .isEqualTo(ChatSocketEvent.RoomNotice(RoomNoticeKind.ConversationHidden, "c2", null))
        assertThat(parseChatFrame(json, """{"type":"subscription_revoked","conversation_id":"c3"}"""))
            .isEqualTo(ChatSocketEvent.SubscriptionRevoked("c3"))
    }

    @Test
    fun `a notice that names no conversation is refused, not guessed`() {
        assertThat(parseChatFrame(json, """{"type":"subscription_expired"}"""))
            .isInstanceOf(ChatSocketEvent.Malformed::class.java)
        assertThat(parseChatFrame(json, """{"type":"subscription_refused","conversation_id":"  "}"""))
            .isInstanceOf(ChatSocketEvent.Malformed::class.java)
    }

    // ── The session ───────────────────────────────────────────────────────

    private class ScriptedSocket : ChatSocket(OkHttpClient(), "ws://unused") {
        val events = MutableSharedFlow<ChatSocketEvent>(extraBufferCapacity = 16)
        val sent = CopyOnWriteArrayList<String>()
        @Volatile var connected = false

        override fun connect(tokenProvider: () -> String?): Flow<ChatSocketEvent> =
            events.onSubscription { connected = true }

        override fun send(frame: String): Boolean {
            sent += frame
            return true
        }
    }

    private class EntitlementApi : ChatApi by unscriptedChatApi() {
        val calls = AtomicInteger(0)
        override suspend fun subscriptionEntitlement(conversationId: String): ApiEnvelope<SubscriptionEntitlementDto> {
            calls.incrementAndGet()
            return ApiEnvelope(data = SubscriptionEntitlementDto(token = "ent-$conversationId"))
        }
    }

    private val tokens = HiddenTokenStore(now = System::currentTimeMillis, timerScope = null)

    private fun session(socket: ScriptedSocket, api: ChatApi): ChatSessionManager {
        val repository = ChatRepository(api, ErrorMapper(json))
        return ChatSessionManager(
            socket = socket,
            store = testStore(FakeChatDao(), api, tokens),
            tokenProvider = object : TokenProvider {
                override fun currentAccessToken(): String? = "t"
            },
            repository = repository,
            hidden = tokens,
        )
    }

    private suspend fun connect(socket: ScriptedSocket, manager: ChatSessionManager) {
        manager.start()
        withTimeout(5_000) { while (!socket.connected) delay(10) }
        socket.events.emit(ChatSocketEvent.Connected)
        withTimeout(5_000) { while (manager.connection.value != ChatSessionManager.ConnectionState.Connected) delay(10) }
    }

    private suspend fun awaitSent(socket: ScriptedSocket, count: Int) {
        withTimeout(5_000) { while (socket.sent.size < count) delay(10) }
    }

    private val hiddenFrame = """{"type":"conversation.subscribe","conversation_id":"h1","open_token":"op"}"""

    @Test
    fun `an open hidden chat subscribes with its open token and no entitlement`() = runBlocking {
        val socket = ScriptedSocket()
        val api = EntitlementApi()
        val manager = session(socket, api)
        tokens.markLocked(listOf("h1"))
        tokens.setOpen("op", null)
        manager.subscribeRoom("h1")

        connect(socket, manager)
        awaitSent(socket, 1)

        assertThat(socket.sent).containsExactly(hiddenFrame)
        assertThat(api.calls.get()).isEqualTo(0)
        manager.stopAndJoin()
    }

    @Test
    fun `a normal chat still subscribes with an entitlement`() = runBlocking {
        val socket = ScriptedSocket()
        val api = EntitlementApi()
        val manager = session(socket, api)
        manager.subscribeRoom("c1")

        connect(socket, manager)
        awaitSent(socket, 1)

        assertThat(socket.sent.single()).doesNotContain("open_token")
        assertThat(api.calls.get()).isEqualTo(1)
        manager.stopAndJoin()
    }

    @Test
    fun `a hidden chat without an open token is not subscribed at all`() = runBlocking {
        val socket = ScriptedSocket()
        val api = EntitlementApi()
        val manager = session(socket, api)
        tokens.markLocked(listOf("h1"))
        manager.subscribeRoom("h1")

        connect(socket, manager)
        delay(200)

        assertThat(socket.sent).isEmpty()
        assertThat(api.calls.get()).isEqualTo(0)
        manager.stopAndJoin()
    }

    @Test
    fun `an expired room is subscribed again while the chat is open`() = runBlocking {
        val socket = ScriptedSocket()
        val manager = session(socket, EntitlementApi())
        tokens.markLocked(listOf("h1"))
        tokens.setOpen("op", null)
        manager.subscribeRoom("h1")
        connect(socket, manager)
        awaitSent(socket, 1)

        socket.events.emit(ChatSocketEvent.RoomNotice(RoomNoticeKind.Expired, "h1"))
        awaitSent(socket, 2)

        assertThat(socket.sent).containsExactly(hiddenFrame, hiddenFrame)
        manager.stopAndJoin()
    }

    @Test
    fun `an expired room of a closed chat is left alone`() = runBlocking {
        val socket = ScriptedSocket()
        val manager = session(socket, EntitlementApi())
        tokens.markLocked(listOf("h1"))
        tokens.setOpen("op", null)
        manager.subscribeRoom("h1")
        connect(socket, manager)
        awaitSent(socket, 1)
        manager.unsubscribeRoom("h1")
        awaitSent(socket, 2)

        socket.events.emit(ChatSocketEvent.RoomNotice(RoomNoticeKind.Expired, "h1"))
        delay(200)

        assertThat(socket.sent).hasSize(2)
        manager.stopAndJoin()
    }

    @Test
    fun `a refused open token closes the hidden chats`() = runBlocking {
        val socket = ScriptedSocket()
        val manager = session(socket, EntitlementApi())
        tokens.setReveal("rv", null)
        tokens.setOpen("op", null)
        connect(socket, manager)

        socket.events.emit(ChatSocketEvent.RoomNotice(RoomNoticeKind.Refused, "h7", "chat_locked"))
        withTimeout(5_000) { while (tokens.open() != null) delay(10) }

        assertThat(tokens.isLocked("h7")).isTrue()
        assertThat(tokens.reveal()).isEqualTo("rv")
        manager.stopAndJoin()
    }

    @Test
    fun `a room refused for another reason keeps the token`() = runBlocking {
        val socket = ScriptedSocket()
        val manager = session(socket, EntitlementApi())
        tokens.setOpen("op", null)
        connect(socket, manager)

        socket.events.emit(ChatSocketEvent.RoomNotice(RoomNoticeKind.Refused, "c1", "denied"))
        delay(200)

        assertThat(tokens.open()).isEqualTo("op")
        assertThat(tokens.isLocked("c1")).isFalse()
        manager.stopAndJoin()
    }

    @Test
    fun `a chat hidden on another device is learned`() = runBlocking {
        val socket = ScriptedSocket()
        val manager = session(socket, EntitlementApi())
        connect(socket, manager)

        socket.events.emit(ChatSocketEvent.RoomNotice(RoomNoticeKind.ConversationHidden, "c5"))
        withTimeout(5_000) { while (!tokens.isLocked("c5")) delay(10) }
        manager.stopAndJoin()
    }
}
