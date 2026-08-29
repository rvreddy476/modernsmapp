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
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.database.UsDatabase
import com.us.android.core.media.upload.ChatAttachmentUploader
import com.us.android.core.media.upload.MediaUploadApi
import com.us.android.core.media.upload.MediaUploader
import com.us.android.core.media.upload.PresignedUploader
import com.us.android.core.model.SessionState
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.TokenProvider
import com.us.android.core.notifications.AppForegroundState
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
 * The REAL [ChatThreadViewModel] send state transition on the review's
 * adverse interleavings (final F2-LB-3 correction):
 *
 *  - a rapid double tap must produce exactly ONE outbox row;
 *  - typing while the enqueue is suspended must preserve the NEWER draft;
 *  - a quarantined send must retain the draft, expose the unavailable/retry
 *    state, and retry cleanly once the owed scrub is repaid.
 *
 * Real ViewModel, real [ChatStore], real Room outbox: the row assertions are
 * against actual `chat_pending_send` rows, not a fake's bookkeeping. Only the
 * network (ChatApi/MediaUploadApi) and the socket session are inert, because
 * this journey is decided entirely before any network call.
 */
@RunWith(RobolectricTestRunner::class)
@Config(sdk = [34])
class ChatThreadViewModelSendTest {

    @get:Rule
    val mainDispatcherRule = MainDispatcherRule()

    private var database: UsDatabase? = null

    @After
    fun tearDown() {
        database?.close()
    }

    /**
     * Wraps the REAL Room DAO so one insert can be held open — the window in
     * which a second tap or a keystroke arrives. Everything else delegates.
     */
    private class GatingDao(private val real: ChatDao) : ChatDao by real {
        var enqueueGate: CompletableDeferred<Unit>? = null
        var checkpointBusy = false

        override suspend fun enqueueSend(row: ChatPendingSendEntity) {
            enqueueGate?.await()
            real.enqueueSend(row)
        }

        override suspend fun rawCheckpoint(query: androidx.sqlite.db.SupportSQLiteQuery): Int =
            if (checkpointBusy && query.sql.contains("wal_checkpoint")) 1 else real.rawCheckpoint(query)
    }

    private class Flag(var stored: Boolean = false) : ScrubRecoveryFlag {
        override fun isPending(): Boolean = stored
        override fun setPending(pending: Boolean): Boolean {
            stored = pending
            return true
        }
    }

    private object NoopScheduler : OutboxScheduler {
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
    }

    private fun harness(flag: Flag = Flag()): Pair<ChatThreadViewModel, GatingDao> {
        val context = ApplicationProvider.getApplicationContext<Context>()
        database = Room.inMemoryDatabaseBuilder(context, UsDatabase::class.java)
            .allowMainThreadQueries()
            .build()
        val dao = GatingDao(database!!.chatDao())
        val json = Json { ignoreUnknownKeys = true }
        val errorMapper = ErrorMapper(json)
        val repository = ChatRepository(unusedApi(), errorMapper)
        val store = ChatStore(repository, dao, NoopScheduler, flag)

        // Inert session: this journey never reaches the socket.
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

        val viewModel = ChatThreadViewModel(
            repository = repository,
            store = store,
            session = session,
            attachmentUploader = ChatAttachmentUploader(
                context,
                MediaUploader(unusedMediaApi(), PresignedUploader(OkHttpClient()), errorMapper),
            ),
            notificationPresenter = NotificationPresenter(context, AppForegroundState()),
            sessionState = object : SessionStateProvider {
                override val sessionState: StateFlow<SessionState> =
                    MutableStateFlow(SessionState.Authenticated(userId = "viewer", sessionId = "s"))
            },
            savedStateHandle = SavedStateHandle(mapOf("conversationId" to "conv")),
        )
        return viewModel to dao
    }

    /**
     * Real-time settle. Room dispatches DAO work to its OWN executor, so
     * virtual time cannot observe it; the unconfined Main dispatcher resumes
     * the ViewModel's coroutines on whatever thread completes them, and this
     * just waits for the observable result.
     */
    private suspend fun awaitUntil(timeoutMillis: Long = 30_000, condition: () -> Boolean) {
        val deadline = System.currentTimeMillis() + timeoutMillis
        while (!condition() && System.currentTimeMillis() < deadline) {
            withContext(Dispatchers.Default) { delay(SETTLE_POLL_MILLIS) }
        }
        check(condition()) { "condition still false after ${timeoutMillis}ms" }
    }

    private suspend fun settle() = withContext(Dispatchers.Default) { delay(SETTLE_MILLIS) }

    // ── the interleavings ───────────────────────────────────────────────

    @Test
    fun `a rapid double tap creates exactly one outbox row`() = runTest {
        val (vm, dao) = harness()
        settle() // the init refresh settles (network is inert)

        dao.enqueueGate = CompletableDeferred()
        vm.onDraftChange("double tap me")

        vm.send() // first tap — will suspend inside the Room insert
        awaitUntil { vm.state.value.sendInFlight }
        vm.send() // second tap while in flight — MUST be a no-op
        vm.send() // and a third, for good measure

        dao.enqueueGate!!.complete(Unit)
        awaitUntil { !vm.state.value.sendInFlight }

        val rows = dao.pendingSends()
        assertThat(rows).hasSize(1)
        assertThat(rows.single().text).isEqualTo("double tap me")
        assertThat(vm.state.value.thread.draft).isEmpty()
    }

    @Test
    fun `typing during the enqueue preserves the newer draft`() = runTest {
        val (vm, dao) = harness()
        settle()

        dao.enqueueGate = CompletableDeferred()
        vm.onDraftChange("text A")
        vm.send()
        awaitUntil { vm.state.value.sendInFlight } // held inside the insert

        // The user keeps typing while the enqueue is in flight.
        vm.onDraftChange("text B, newer than what was sent")

        dao.enqueueGate!!.complete(Unit)
        awaitUntil { !vm.state.value.sendInFlight }

        // A was queued; B — a revision that was never sent — survives.
        assertThat(dao.pendingSends().single().text).isEqualTo("text A")
        assertThat(vm.state.value.thread.draft).isEqualTo("text B, newer than what was sent")

        // And B can still be sent afterwards: two rows, no lost message.
        vm.send()
        awaitUntil { vm.state.value.thread.draft.isEmpty() }
        assertThat(dao.pendingSends().map { it.text })
            .containsExactly("text A", "text B, newer than what was sent")
    }

    @Test
    fun `a quarantined send retains the draft, flags unavailable, and retries cleanly`() = runTest {
        val flag = Flag(stored = true) // an owed scrub from the previous logout
        val (vm, dao) = harness(flag)
        dao.checkpointBusy = true
        settle()

        vm.onDraftChange("held message")
        vm.send()
        awaitUntil { vm.state.value.sendUnavailable }

        // Refused: draft retained, unavailable exposed, no phantom row.
        assertThat(vm.state.value.thread.draft).isEqualTo("held message")
        assertThat(dao.pendingSends()).isEmpty()

        // Contention clears; the user taps Send again with the same draft.
        dao.checkpointBusy = false
        vm.send()
        awaitUntil { vm.state.value.thread.draft.isEmpty() }

        assertThat(dao.pendingSends().single().text).isEqualTo("held message")
        assertThat(vm.state.value.sendUnavailable).isFalse()
        assertThat(flag.stored).isFalse() // the owed scrub was repaid first
    }

    private companion object {
        const val SETTLE_POLL_MILLIS = 5L
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
