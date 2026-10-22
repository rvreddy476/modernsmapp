package com.us.android.feature.chat.ui

import android.content.Context
import androidx.lifecycle.SavedStateHandle
import androidx.room.Room
import androidx.test.core.app.ApplicationProvider
import com.google.common.truth.Truth.assertThat
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatPinsSource
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.PinnedMessage
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.database.UsDatabase
import com.us.android.core.media.upload.ChatAttachmentUploader
import com.us.android.core.media.upload.MediaUploadApi
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedUploader
import com.us.android.core.model.SessionState
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.TokenProvider
import com.us.android.core.notifications.AppForegroundState
import com.us.android.core.notifications.DoorstepOfferAlertCoordinator
import com.us.android.core.notifications.NotificationPresenter
import com.us.android.core.testing.MainDispatcherRule
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.withContext
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Rule
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.annotation.Config

/**
 * Pinned messages on the REAL thread ViewModel (2026-10-22): the pins are
 * read on open, the bar shows one and steps on when tapped, Pin waits for
 * the server and the fourth is refused first, and Unpin is optimistic with
 * its rollback. The pins seam is the only scripted collaborator; the rest is
 * the thread's own (an inert network and socket, a real Room store).
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ChatThreadPinsTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private var database: UsDatabase? = null

    @After
    fun tearDown() {
        database?.close()
    }

    private class FakePins : ChatPinsSource {
        var list: AppResult<List<PinnedMessage>> = AppResult.Success(emptyList())
        var pinAnswer: AppResult<PinnedMessage>? = null
        var unpinAnswer: AppResult<Unit> = AppResult.Success(Unit)
        var unpinGate: CompletableDeferred<Unit>? = null
        val calls = mutableListOf<String>()

        override suspend fun pins(conversationId: String): AppResult<List<PinnedMessage>> {
            calls += "list:$conversationId"
            return list
        }

        override suspend fun pin(conversationId: String, messageId: String): AppResult<PinnedMessage> {
            calls += "pin:$messageId"
            return requireNotNull(pinAnswer)
        }

        override suspend fun unpin(conversationId: String, pinId: String): AppResult<Unit> {
            calls += "unpin:$pinId"
            unpinGate?.await()
            return unpinAnswer
        }
    }

    private object NoopScheduler : OutboxScheduler {
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
    }

    private object NoFlag : ScrubRecoveryFlag {
        override fun isPending(): Boolean = false
        override fun setPending(pending: Boolean): Boolean = true
    }

    private fun viewModel(pins: ChatPinsSource): ChatThreadViewModel {
        val context = ApplicationProvider.getApplicationContext<Context>()
        database = Room.inMemoryDatabaseBuilder(context, UsDatabase::class.java).allowMainThreadQueries().build()
        val errorMapper = ErrorMapper(Json { ignoreUnknownKeys = true })
        val repository = ChatRepository(unusedApi(), errorMapper)
        val store = ChatStore(repository, database!!.chatDao(), NoopScheduler, NoFlag)
        val session = object : ChatSessionManager(
            socket = ChatSocket(OkHttpClient(), "ws://unused"),
            store = store,
            tokenProvider = object : TokenProvider {
                override fun currentAccessToken(): String? = "t"
            },
            repository = repository,
        ) {
            override fun start() = Unit
            override fun subscribeRoom(conversationId: String) = Unit
            override fun unsubscribeRoom(conversationId: String) = Unit
        }
        return ChatThreadViewModel(
            repository = repository,
            store = store,
            session = session,
            attachmentUploader = ChatAttachmentUploader(
                context,
                MediaUploader(unusedMediaApi(), PresignedUploader(OkHttpClient()), errorMapper),
            ),
            notificationPresenter = NotificationPresenter(context, AppForegroundState(), DoorstepOfferAlertCoordinator()),
            sessionState = object : SessionStateProvider {
                override val sessionState: StateFlow<SessionState> =
                    MutableStateFlow(SessionState.Authenticated(userId = "viewer", sessionId = "s"))
            },
            savedStateHandle = SavedStateHandle(mapOf("conversationId" to "conv")),
            pinsSource = pins,
        )
    }

    private suspend fun settle() = withContext(Dispatchers.Default) { delay(SETTLE_MILLIS) }

    private fun pinOf(id: String, message: String) = PinnedMessage(id = id, messageId = message, pinnedBy = "u2", pinnedAt = "")

    private fun message(id: String) =
        Message(id = id, conversationId = "conv", senderId = "u2", senderDisplayName = null, text = "hi $id", createdAt = "")

    @Test
    fun `the pins are read on open and the bar shows the newest`() = runTest {
        val pins = FakePins().apply { list = AppResult.Success(listOf(pinOf("p2", "m2"), pinOf("p1", "m1"))) }

        val vm = viewModel(pins)
        settle()

        assertThat(pins.calls).contains("list:conv")
        assertThat(vm.state.value.pinsEnabled).isTrue()
        assertThat(vm.state.value.shownPin?.id).isEqualTo("p2")
        assertThat(vm.state.value.isPinned(message("m1"))).isTrue()
    }

    @Test
    fun `no pins, or a read that fails, draws no bar`() = runTest {
        val failing = FakePins().apply { list = AppResult.Failure(AppError.NoNetwork()) }

        val vm = viewModel(failing)
        settle()

        assertThat(vm.state.value.shownPin).isNull()
        assertThat(vm.state.value.pins).isEmpty()
    }

    @Test
    fun `tapping the bar steps on to the next pin and wraps`() = runTest {
        val pins = FakePins().apply { list = AppResult.Success(listOf(pinOf("p2", "m2"), pinOf("p1", "m1"))) }
        val vm = viewModel(pins)
        settle()

        vm.openShownPin()
        assertThat(vm.state.value.shownPin?.id).isEqualTo("p1")
        settle()
        vm.openShownPin()
        assertThat(vm.state.value.shownPin?.id).isEqualTo("p2")
    }

    @Test
    fun `pin waits for the server, then the new pin is shown first`() = runTest {
        val pins = FakePins().apply {
            list = AppResult.Success(listOf(pinOf("p1", "m1")))
            pinAnswer = AppResult.Success(pinOf("p9", "m9"))
        }
        val vm = viewModel(pins)
        settle()

        vm.pin(message("m9"))
        settle()

        assertThat(pins.calls).contains("pin:m9")
        assertThat(vm.state.value.pins.map { it.id }).containsExactly("p9", "p1").inOrder()
        assertThat(vm.state.value.shownPin?.id).isEqualTo("p9")
    }

    @Test
    fun `a refused pin changes nothing and says so`() = runTest {
        val pins = FakePins().apply { pinAnswer = AppResult.Failure(AppError.NoNetwork()) }
        val vm = viewModel(pins)
        settle()

        vm.pin(message("m9"))
        settle()

        assertThat(vm.state.value.pins).isEmpty()
        assertThat(vm.state.value.notice?.text).isEqualTo("Couldn't pin that message")
    }

    @Test
    fun `a fourth pin is refused before any request, in the web's words`() = runTest {
        val pins = FakePins().apply {
            list = AppResult.Success(listOf(pinOf("p1", "m1"), pinOf("p2", "m2"), pinOf("p3", "m3")))
        }
        val vm = viewModel(pins)
        settle()

        vm.pin(message("m4"))

        assertThat(pins.calls.none { it.startsWith("pin:") }).isTrue()
        assertThat(vm.state.value.notice?.text).isEqualTo("You can pin up to 3 messages. Unpin one first.")
    }

    @Test
    fun `unpin takes the pin away at once, by its pin id`() = runTest {
        val pins = FakePins().apply {
            list = AppResult.Success(listOf(pinOf("p1", "m1"), pinOf("p2", "m2")))
            unpinGate = CompletableDeferred()
        }
        val vm = viewModel(pins)
        settle()

        vm.unpin("m1")
        assertThat(vm.state.value.pins.map { it.id }).containsExactly("p2")

        pins.unpinGate!!.complete(Unit)
        settle()
        assertThat(pins.calls).contains("unpin:p1")
        assertThat(vm.state.value.pins.map { it.id }).containsExactly("p2")
    }

    @Test
    fun `a refused unpin brings the pin back and says so`() = runTest {
        val pins = FakePins().apply {
            list = AppResult.Success(listOf(pinOf("p1", "m1"), pinOf("p2", "m2")))
            unpinAnswer = AppResult.Failure(AppError.Server(statusCode = 500, code = "INTERNAL_ERROR"))
        }
        val vm = viewModel(pins)
        settle()

        vm.unpin("m1")
        settle()

        assertThat(vm.state.value.pins.map { it.id }).containsExactly("p1", "p2").inOrder()
        assertThat(vm.state.value.notice?.text).isEqualTo("Couldn't unpin that message")
    }

    /** Someone else already unpinned it: it stays gone, with nothing said. */
    @Test
    fun `an unpin answered not found stays unpinned`() = runTest {
        val pins = FakePins().apply {
            list = AppResult.Success(listOf(pinOf("p1", "m1")))
            unpinAnswer = AppResult.Failure(AppError.NotFound())
        }
        val vm = viewModel(pins)
        settle()

        vm.unpin("m1")
        settle()

        assertThat(vm.state.value.pins).isEmpty()
        assertThat(vm.state.value.notice).isNull()
    }

    @Test
    fun `the bar's words`() {
        assertThat(pinnedBarTitle(0, 1)).isEqualTo("Pinned message")
        assertThat(pinnedBarTitle(1, 3)).isEqualTo("Pinned message 2 of 3")
        assertThat(pinnedBarPreview(null)).isEqualTo("Tap to see the message")
        assertThat(pinnedBarPreview(message("m1"))).isEqualTo("hi m1")
        assertThat(pinnedBarPreview(message("m1").copy(text = ""))).isEqualTo("Attachment")
        assertThat(pinnedBarPreview(message("m1").copy(deleted = true))).isEqualTo("This message was deleted")
    }

    private companion object {
        const val SETTLE_MILLIS = 150L
    }
}

/** Proxies whose members throw; `apiCall` maps the throw to a failure. */
private fun unusedApi(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi

private fun unusedMediaApi(): MediaUploadApi = java.lang.reflect.Proxy.newProxyInstance(
    MediaUploadApi::class.java.classLoader,
    arrayOf(MediaUploadApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as MediaUploadApi
