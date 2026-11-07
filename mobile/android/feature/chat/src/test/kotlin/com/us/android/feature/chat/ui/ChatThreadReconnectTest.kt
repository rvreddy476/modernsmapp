package com.us.android.feature.chat.ui

import android.content.Context
import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.viewModelScope
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
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.ConversationMemberDto
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.database.UsDatabase
import com.us.android.core.media.upload.ChatAttachmentUploader
import com.us.android.core.media.upload.MediaUploadApi
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedUploader
import com.us.android.core.model.SessionState
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.TokenProvider
import com.us.android.core.notifications.AppForegroundState
import com.us.android.core.notifications.DoorstepOfferAlertCoordinator
import com.us.android.core.notifications.NotificationPresenter
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.cancelAndJoin
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.job
import kotlinx.coroutines.runBlocking
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
import java.io.IOException
import java.util.concurrent.ConcurrentLinkedQueue

/**
 * The open thread across a socket reconnect, and opened offline:
 *
 *  - audit M2: a reconnect merges the newest page — the pages scrolled back
 *    to stay;
 *  - audit M3: the reconnect read runs off the session collector, so frames
 *    behind it (a new message, call signaling) are not held up by it;
 *  - audit M4: the offline fallback draws the cached tail newest first, the
 *    same order as the live thread.
 *
 * Real ViewModel, real [ChatStore], real Room; only history pages and the
 * socket stream are scripted.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ChatThreadReconnectTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private var database: UsDatabase? = null
    private var viewModel: ChatThreadViewModel? = null

    /**
     * The thread's coroutines stop before Room closes: one still reading when
     * the database goes would fail into the NEXT test as an uncaught exception.
     */
    @After
    fun tearDown() {
        viewModel?.let { vm -> runBlocking { vm.viewModelScope.coroutineContext.job.cancelAndJoin() } }
        database?.close()
    }

    /** History pages in order; with none scripted, the read fails as offline. */
    private class PagesApi : ChatApi by unusedReconnectApi() {
        val pages = ConcurrentLinkedQueue<suspend () -> ApiEnvelope<List<MessageDto>>>()

        /** The conversation the thread reads; unscripted, the read fails. */
        @Volatile var conversation: ConversationDto? = null

        override suspend fun conversation(conversationId: String): ApiEnvelope<ConversationDto> =
            ApiEnvelope(data = conversation ?: throw IOException("offline"))

        override suspend fun messages(
            conversationId: String,
            limit: Int,
            cursor: String?,
        ): ApiEnvelope<List<MessageDto>> {
            val next = pages.poll() ?: throw IOException("offline")
            return next()
        }
    }

    private object NoScheduler : OutboxScheduler {
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
    }

    private object NoScrubOwed : ScrubRecoveryFlag {
        override fun isPending(): Boolean = false
        override fun setPending(pending: Boolean): Boolean = true
    }

    private val signedIn = object : SessionStateProvider {
        override val sessionState: StateFlow<SessionState> =
            MutableStateFlow(SessionState.Authenticated(userId = VIEWER, sessionId = "s"))
    }

    private class Harness(
        val vm: ChatThreadViewModel,
        val api: PagesApi,
        val events: MutableSharedFlow<ChatSocketEvent>,
        /** Every typing frame the thread asked the socket to send. */
        val typingFrames: MutableList<Boolean>,
    )

    /** [cached] is in Room before the thread opens; [pages] answer the thread's reads in order. */
    private fun harness(
        cached: List<Message> = emptyList(),
        pages: List<suspend () -> ApiEnvelope<List<MessageDto>>> = emptyList(),
        conversation: ConversationDto? = null,
    ): Harness {
        val context = ApplicationProvider.getApplicationContext<Context>()
        val db = Room.inMemoryDatabaseBuilder(context, UsDatabase::class.java)
            .allowMainThreadQueries()
            .build()
            .also { database = it }
        val errorMapper = ErrorMapper(Json { ignoreUnknownKeys = true })
        val api = PagesApi().also {
            it.pages += pages
            it.conversation = conversation
        }
        val typingFrames = java.util.Collections.synchronizedList(mutableListOf<Boolean>())
        val repository = ChatRepository(api, errorMapper)
        val store = ChatStore(
            repository = repository,
            dao = db.chatDao(),
            scheduler = NoScheduler,
            scrubRecovery = NoScrubOwed,
            parityDao = db.chatParityDao(),
            sessionState = { signedIn },
        )
        if (cached.isNotEmpty()) runBlocking { store.cacheMessages(cached) }
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
            override fun sendTyping(conversationId: String, typing: Boolean): Boolean {
                typingFrames += typing
                return true
            }
        }
        val vm = ChatThreadViewModel(
            repository = repository,
            store = store,
            session = session,
            attachmentUploader = ChatAttachmentUploader(
                context,
                MediaUploader(unusedReconnectUploadApi(), PresignedUploader(OkHttpClient()), errorMapper),
            ),
            notificationPresenter = NotificationPresenter(
                context,
                AppForegroundState(),
                DoorstepOfferAlertCoordinator(),
            ),
            sessionState = signedIn,
            savedStateHandle = SavedStateHandle(mapOf("conversationId" to CONVERSATION)),
        ).also { viewModel = it }
        return Harness(vm, api, events, typingFrames)
    }

    private fun at(n: Int) = "2026-11-01T10:%02d:00Z".format(n)

    private fun dto(n: Int) =
        MessageDto(msgId = "m$n", conversationId = CONVERSATION, senderId = PEER, text = "text $n", createdAt = at(n))

    private fun page(vararg ns: Int, next: String? = null): suspend () -> ApiEnvelope<List<MessageDto>> =
        { ApiEnvelope(data = ns.map(::dto), meta = ApiMeta(nextCursor = next)) }

    private fun message(n: Int) = Message("m$n", CONVERSATION, PEER, null, "text $n", at(n))

    private fun Harness.shownIds() = vm.state.value.thread.messages.map { it.id }

    private suspend fun awaitUntil(timeoutMillis: Long = 30_000, condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (!condition() && System.currentTimeMillis() < deadline) {
            withContext(Dispatchers.Default) { delay(POLL_MILLIS) }
        }
        check(condition()) { "condition still false after ${timeoutMillis}ms" }
    }

    @Test
    fun `opened offline, the cached tail reads newest first`() = runTest {
        val h = harness(cached = listOf(message(1), message(2), message(3)))

        awaitUntil { h.vm.state.value.offline && h.shownIds().size == 3 }

        assertThat(h.shownIds()).containsExactly("m3", "m2", "m1").inOrder()
    }

    @Test
    fun `a reconnect keeps the pages the reader scrolled back to`() = runTest {
        val h = harness(pages = listOf(page(5, 4, 3, next = "older-1"), page(2, 1, next = "older-2")))
        awaitUntil { h.shownIds().size == 3 }
        h.vm.loadMore()
        awaitUntil { h.shownIds().size == 5 }

        h.api.pages += page(6, 5, 4, next = "fresh-1")
        h.events.emit(ChatSocketEvent.Connected)

        awaitUntil { "m6" in h.shownIds() }
        assertThat(h.shownIds()).containsExactly("m6", "m5", "m4", "m3", "m2", "m1").inOrder()
        assertThat(h.vm.state.value.thread.nextCursor).isEqualTo("older-2")
    }

    @Test
    fun `a slow reconnect read does not hold up the frames behind it`() = runTest {
        val h = harness(pages = listOf(page(2, 1)))
        awaitUntil { h.shownIds().size == 2 }
        val slowRead = CompletableDeferred<Unit>()
        h.api.pages += {
            slowRead.await()
            page(2, 1)()
        }

        h.events.emit(ChatSocketEvent.Connected)
        h.events.emit(ChatSocketEvent.MessageReceived(message(3)))

        // The new message is on screen while the reconnect read is still out.
        awaitUntil { "m3" in h.shownIds() }
        assertThat(slowRead.isCompleted).isFalse()
        slowRead.complete(Unit)
        awaitUntil { h.api.pages.isEmpty() }
    }

    private fun direct(isRequest: Boolean) = ConversationDto(
        id = CONVERSATION,
        type = "direct",
        createdBy = VIEWER,
        isRequest = isRequest,
        members = listOf(ConversationMemberDto(VIEWER, "member"), ConversationMemberDto(PEER, "member")),
    )

    @Test
    fun `typing in a request the viewer sent tells the other person nothing`() = runTest {
        // The server refuses it too (7 Nov): typing follows the request side.
        val h = harness(pages = listOf(page()), conversation = direct(isRequest = true))
        awaitUntil { h.vm.state.value.outgoingRequest }

        h.vm.onDraftChange("hello")
        h.vm.sendTyping()

        assertThat(h.typingFrames).isEmpty()
    }

    @Test
    fun `typing in an accepted chat is sent`() = runTest {
        val h = harness(pages = listOf(page()), conversation = direct(isRequest = false))
        awaitUntil { h.vm.state.value.conversation != null }

        h.vm.sendTyping()

        assertThat(h.typingFrames).contains(true)
    }

    @Test
    fun `a thread the viewer is no longer in shows no cached history and offers no retry`() = runTest {
        // Audit L4: message-service answers a non-member 404 NOT_FOUND.
        val notMember: suspend () -> ApiEnvelope<List<MessageDto>> = {
            throw retrofit2.HttpException(
                retrofit2.Response.error<Any>(
                    404,
                    """{"error":{"code":"NOT_FOUND","message":"not found"}}"""
                        .toResponseBody("application/json".toMediaType()),
                ),
            )
        }
        val h = harness(cached = listOf(message(1), message(2)), pages = listOf(notMember))

        awaitUntil { h.vm.state.value.thread.refreshError != null }
        // Settled: the fallback would have drawn the cache by now.
        withContext(Dispatchers.Default) { delay(200) }

        val error = checkNotNull(h.vm.state.value.thread.refreshError)
        assertThat(error.isThreadUnavailable()).isTrue()
        assertThat(h.shownIds()).isEmpty()
        assertThat(h.vm.state.value.offline).isFalse()
        assertThat(threadLoadErrorText(error)).isEqualTo(THREAD_UNAVAILABLE_TEXT)
    }

    @Test
    fun `a server that is briefly down is a retry, not a lost conversation`() {
        val unavailable = com.us.android.core.common.error.AppError.Server(statusCode = 503, code = "UNAVAILABLE")

        assertThat(unavailable.isThreadUnavailable()).isFalse()
        assertThat(threadLoadErrorText(unavailable)).isEqualTo("We couldn't load this conversation.")
    }

    private companion object {
        const val CONVERSATION = "conv"
        const val VIEWER = "viewer"
        const val PEER = "peer"
        const val POLL_MILLIS = 5L
    }
}

private fun unusedReconnectApi(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi

private fun unusedReconnectUploadApi(): MediaUploadApi = java.lang.reflect.Proxy.newProxyInstance(
    MediaUploadApi::class.java.classLoader,
    arrayOf(MediaUploadApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as MediaUploadApi
