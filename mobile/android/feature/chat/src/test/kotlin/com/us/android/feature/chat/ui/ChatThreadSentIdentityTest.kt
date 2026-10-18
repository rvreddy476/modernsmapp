package com.us.android.feature.chat.ui

import android.content.Context
import androidx.lifecycle.SavedStateHandle
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.database.UsDatabase
import com.us.android.core.media.upload.ChatAttachmentUploader
import com.us.android.core.media.upload.MediaUploadApi
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedUploader
import com.us.android.core.model.SessionState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.TokenProvider
import com.us.android.core.notifications.AppForegroundState
import com.us.android.core.notifications.DoorstepOfferAlertCoordinator
import com.us.android.core.notifications.NotificationPresenter
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.SupervisorJob
import kotlinx.coroutines.cancel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.OkHttpClient
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * The bubble a send leaves on screen carries the SERVER identity — msg_id,
 * bucket, ts — the moment the send succeeds, and appears exactly once.
 *
 * Found on the emulator: a just-sent message offered only Reply, Copy and
 * Forward on long-press; Edit, Delete for me and Unsend need the address and
 * only appeared after reopening the thread. Two causes: the Room cache the
 * outbox writes the confirmed copy to dropped bucket/ts, and the server's
 * echo of the send (no address on the socket frame) usually reached the
 * thread first and won every de-duplication after it.
 *
 * Real ViewModel, real [ChatStore], real Room (both DAOs), the real outbox
 * drain; only the HTTP send and the socket stream are scripted.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ChatThreadSentIdentityTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private var database: UsDatabase? = null
    private val drainScope = CoroutineScope(SupervisorJob() + Dispatchers.Default)

    @After
    fun tearDown() {
        drainScope.cancel()
        database?.close()
    }

    /** The send endpoint, held at [gate] when one is set; answers with the full address. */
    private class ScriptedApi : ChatApi by unusedChatApi() {
        var gate: CompletableDeferred<Unit>? = null
        val started = CompletableDeferred<Unit>()

        /** Answer 403: a permanent refusal, so the outbox row parks as failed. */
        var refuse = false

        override suspend fun send(
            conversationId: String,
            idempotencyKey: String,
            body: SendMessageRequest,
        ): ApiEnvelope<MessageDto> {
            started.complete(Unit)
            gate?.await()
            if (refuse) {
                throw retrofit2.HttpException(
                    retrofit2.Response.error<Any>(HTTP_FORBIDDEN, "{}".toResponseBody("application/json".toMediaType())),
                )
            }
            return ApiEnvelope(data = serverCopy(conversationId, body.text))
        }
    }

    /** Runs the in-process drain the way production's app-scope launcher does. */
    private inner class InlineScheduler : OutboxScheduler {
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
        override fun drainNow(drain: suspend () -> Unit) {
            drainScope.launch { runCatching { drain() } }
        }
    }

    private object NoScrubOwed : ScrubRecoveryFlag {
        override fun isPending(): Boolean = false
        override fun setPending(pending: Boolean): Boolean = true
    }

    private class Harness(
        val vm: ChatThreadViewModel,
        val api: ScriptedApi,
        val store: ChatStore,
        val events: MutableSharedFlow<ChatSocketEvent>,
    )

    private fun harness(): Harness {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val db = Room.inMemoryDatabaseBuilder(context, UsDatabase::class.java)
            .allowMainThreadQueries()
            .build()
            .also { database = it }
        val json = Json { ignoreUnknownKeys = true }
        val errorMapper = ErrorMapper(json)
        val api = ScriptedApi()
        val repository = ChatRepository(api, errorMapper)
        val store = ChatStore(
            repository = repository,
            dao = db.chatDao(),
            scheduler = InlineScheduler(),
            scrubRecovery = NoScrubOwed,
            parityDao = db.chatParityDao(),
        )
        val events = MutableSharedFlow<ChatSocketEvent>(extraBufferCapacity = 16)
        val session = object : ChatSessionManager(
            socket = ChatSocket(OkHttpClient(), "ws://unused"),
            store = store,
            tokenProvider = object : TokenProvider {
                override fun currentAccessToken(): String? = "t"
            },
            repository = repository,
        ) {
            override val events: SharedFlow<ChatSocketEvent> = events
            override fun start() = Unit
            override fun subscribeRoom(conversationId: String) = Unit
            override fun unsubscribeRoom(conversationId: String) = Unit
        }
        val vm = ChatThreadViewModel(
            repository = repository,
            store = store,
            session = session,
            attachmentUploader = ChatAttachmentUploader(
                context,
                MediaUploader(unusedUploadApi(), PresignedUploader(OkHttpClient()), errorMapper),
            ),
            notificationPresenter = NotificationPresenter(context, AppForegroundState(), DoorstepOfferAlertCoordinator()),
            sessionState = object : SessionStateProvider {
                override val sessionState: StateFlow<SessionState> =
                    MutableStateFlow(SessionState.Authenticated(userId = VIEWER, sessionId = "s"))
            },
            savedStateHandle = SavedStateHandle(mapOf("conversationId" to CONVERSATION)),
        )
        return Harness(vm, api, store, events)
    }

    /**
     * What the session manager does with a `message` frame: durable first
     * (the Room row, with no address — the frame has none), then the event.
     */
    private suspend fun Harness.deliverEcho(message: Message = echoOf(TEXT)) {
        store.applyRealtimeMessage(message)
        events.emit(ChatSocketEvent.MessageReceived(message))
    }

    private fun Harness.copiesOf(id: String) = vm.state.value.thread.messages.filter { it.id == id }

    private suspend fun awaitUntil(timeoutMillis: Long = 30_000, condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (!condition() && System.currentTimeMillis() < deadline) {
            withContext(Dispatchers.Default) { delay(POLL_MILLIS) }
        }
        check(condition()) { "condition still false after ${timeoutMillis}ms" }
    }

    private suspend fun settle() = withContext(Dispatchers.Default) { delay(SETTLE_MILLIS) }

    private suspend fun Harness.sendText() {
        settle() // the init refresh settles (history is inert)
        vm.onDraftChange(TEXT)
        vm.send()
    }

    private suspend fun Harness.awaitDelivered() = awaitUntil {
        vm.state.value.pendingSends.isEmpty() && copiesOf(SERVER_ID).isNotEmpty()
    }

    private fun Harness.assertOneAddressableBubble() {
        val copies = copiesOf(SERVER_ID)
        assertThat(copies).hasSize(1)
        val bubble = copies.single()
        assertThat(bubble.bucket).isEqualTo(BUCKET)
        assertThat(bubble.ts).isEqualTo(TS)
        assertThat(bubble.addressable).isTrue()
        // What the long-press sheet asks before it offers Edit.
        assertThat(bubble.canEdit(VIEWER)).isTrue()
        assertThat(vm.state.value.pendingSends).isEmpty()
    }

    // ── the journeys ───────────────────────────────────────────────────

    @Test
    fun `a delivered send leaves one bubble that carries the server address`() = runTest {
        val h = harness()
        h.sendText()
        h.awaitDelivered()
        h.assertOneAddressableBubble()
    }

    @Test
    fun `an echo that arrives before the HTTP response draws no second bubble and the send ends addressable`() = runTest {
        val h = harness()
        h.api.gate = CompletableDeferred()
        h.sendText()
        h.api.started.await()
        awaitUntil { h.vm.state.value.pendingSends.isNotEmpty() }

        h.deliverEcho()
        settle()
        // Still sending: the outbox row is the ONE bubble; the echo waits.
        assertThat(h.vm.state.value.pendingSends).hasSize(1)
        assertThat(h.copiesOf(SERVER_ID)).isEmpty()

        h.api.gate!!.complete(Unit)
        h.awaitDelivered()
        h.assertOneAddressableBubble()
    }

    @Test
    fun `an echo that arrives after the HTTP response merges into the same bubble`() = runTest {
        val h = harness()
        h.sendText()
        h.awaitDelivered()

        h.deliverEcho()
        settle()
        h.assertOneAddressableBubble()
    }

    @Test
    fun `an echo shown before the confirmed copy adopts its address in place`() = runTest {
        // The echo reaches the thread before the outbox row is even visible
        // to it — nothing to hold it against, so it is drawn. The confirmed
        // copy must then give that row its address, not be dropped.
        val h = harness()
        settle()
        h.deliverEcho()
        awaitUntil { h.copiesOf(SERVER_ID).isNotEmpty() }
        assertThat(h.copiesOf(SERVER_ID).single().addressable).isFalse()

        h.vm.onDraftChange(TEXT)
        h.vm.send()
        awaitUntil { h.copiesOf(SERVER_ID).singleOrNull()?.addressable == true }
        awaitUntil { h.vm.state.value.pendingSends.isEmpty() }
        h.assertOneAddressableBubble()
    }

    @Test
    fun `my message from another device is shown at once while a different send is in flight`() = runTest {
        val h = harness()
        h.api.gate = CompletableDeferred()
        h.sendText()
        h.api.started.await()
        awaitUntil { h.vm.state.value.pendingSends.isNotEmpty() }

        val elsewhere = echoOf("sent from my laptop", id = "srv-laptop")
        h.deliverEcho(elsewhere)
        awaitUntil { h.copiesOf("srv-laptop").isNotEmpty() }
        assertThat(h.copiesOf("srv-laptop")).hasSize(1)

        h.api.gate!!.complete(Unit)
        h.awaitDelivered()
        h.assertOneAddressableBubble()
    }

    @Test
    fun `a held echo is released when its send parks instead of landing`() = runTest {
        // Same text from my laptop while this phone's copy is in flight: it
        // looks like our echo and is held. This phone's send is then refused
        // — nothing will confirm it — so the held message must still show.
        val h = harness()
        h.api.gate = CompletableDeferred()
        h.api.refuse = true
        h.sendText()
        h.api.started.await()
        awaitUntil { h.vm.state.value.pendingSends.isNotEmpty() }

        h.deliverEcho(echoOf(TEXT, id = "srv-laptop"))
        settle()
        assertThat(h.copiesOf("srv-laptop")).isEmpty()

        h.api.gate!!.complete(Unit)
        awaitUntil { h.vm.state.value.pendingSends.singleOrNull()?.failed == true }
        awaitUntil { h.copiesOf("srv-laptop").isNotEmpty() }
        assertThat(h.copiesOf("srv-laptop")).hasSize(1)
    }

    private companion object {
        const val VIEWER = "viewer"
        const val CONVERSATION = "conv"
        const val SERVER_ID = "srv-1"
        const val TEXT = "hello there"
        const val BUCKET = "202610"
        const val TS = "2026-10-18T10:00:00.123Z"
        const val POLL_MILLIS = 5L
        const val SETTLE_MILLIS = 150L
        const val HTTP_FORBIDDEN = 403

        fun serverCopy(conversationId: String, text: String) = MessageDto(
            msgId = SERVER_ID,
            conversationId = conversationId,
            senderId = VIEWER,
            text = text,
            bucket = BUCKET,
            ts = TS,
            createdAt = TS,
        )

        /** The socket frame of the viewer's own send: same id, no address. */
        fun echoOf(text: String, id: String = SERVER_ID) = Message(
            id = id,
            conversationId = CONVERSATION,
            senderId = VIEWER,
            senderDisplayName = null,
            text = text,
            createdAt = TS,
        )
    }
}

private fun unusedChatApi(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi

private fun unusedUploadApi(): MediaUploadApi = java.lang.reflect.Proxy.newProxyInstance(
    MediaUploadApi::class.java.classLoader,
    arrayOf(MediaUploadApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as MediaUploadApi
