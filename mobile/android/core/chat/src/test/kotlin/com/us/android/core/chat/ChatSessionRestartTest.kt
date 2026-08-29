package com.us.android.core.chat

import androidx.sqlite.db.SupportSQLiteQuery
import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatSessionManager
import com.us.android.core.chat.data.ChatSocket
import com.us.android.core.chat.data.ChatSocketEvent
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.TokenProvider
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import org.junit.Test
import java.util.concurrent.atomic.AtomicInteger

/**
 * Session-manager lifecycle on the adverse paths the review named:
 *
 *  - a PERMANENT credential rejection ends the loop; a later re-auth
 *    `start()` must begin a fresh session (the pre-fix supervisor root
 *    stayed active and swallowed every restart — review P0 #3);
 *  - a failed owed-scrub repayment must prevent the socket from ever
 *    starting (F2-LB-3 quarantine boundary).
 *
 * Real dispatchers (the manager owns its scope on Dispatchers.IO), so these
 * tests poll with generous timeouts instead of using virtual time.
 */
class ChatSessionRestartTest {

    /** A socket whose every connection is scripted as permanently rejected. */
    private class RejectingSocket : ChatSocket(OkHttpClient(), "ws://unused") {
        val connectCalls = AtomicInteger(0)
        override fun connect(tokenProvider: () -> String?): Flow<ChatSocketEvent> {
            connectCalls.incrementAndGet()
            return flowOf(ChatSocketEvent.Disconnected(permanent = true))
        }

        override fun send(frame: String): Boolean = false
    }

    private class MinimalDao : ChatDao {
        var checkpointBusy = false
        override suspend fun upsertConversations(rows: List<ChatConversationEntity>) = Unit
        override fun conversations(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
        override suspend fun clearUnread(conversationId: String) = Unit
        override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
        override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
        override suspend fun rawCheckpoint(query: SupportSQLiteQuery): Int =
            if (checkpointBusy && query.sql.contains("wal_checkpoint")) 1 else 0
        override suspend fun deleteMessage(messageId: String) = Unit
        override suspend fun deleteConversation(conversationId: String) = Unit
        override suspend fun clearConversations() = Unit
        override suspend fun insertMessages(rows: List<ChatMessageEntity>) = Unit
        override suspend fun recentMessages(conversationId: String, limit: Int) = emptyList<ChatMessageEntity>()
        override suspend fun clearMessages() = Unit
        override suspend fun enqueueSend(row: ChatPendingSendEntity) = Unit
        override suspend fun pendingSends(): List<ChatPendingSendEntity> = emptyList()
        override fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>> =
            MutableStateFlow(emptyList())
        override suspend fun recordAttempt(key: String) = Unit
        override suspend fun markFailed(key: String) = Unit
        override suspend fun retry(key: String) = Unit
        override suspend fun completeSend(key: String) = Unit
        override suspend fun clearPendingSends() = Unit
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

    private fun manager(
        socket: ChatSocket,
        dao: MinimalDao = MinimalDao(),
        flag: Flag = Flag(),
    ): ChatSessionManager {
        val repository = ChatRepository(unusedRestartChatApi(), ErrorMapper(Json { ignoreUnknownKeys = true }))
        val store = ChatStore(repository, dao, NoopScheduler, flag)
        return ChatSessionManager(
            socket = socket,
            store = store,
            tokenProvider = object : TokenProvider {
                override fun currentAccessToken(): String? = "test-token"
            },
            repository = repository,
        )
    }

    @Test
    fun `start works again after a permanent credential rejection ended the loop`() = runBlocking {
        val socket = RejectingSocket()
        val manager = manager(socket)

        manager.start()
        withTimeout(5_000) {
            while (socket.connectCalls.get() < 1) delay(20)
        }

        // The pre-fix supervisor root stayed active after the permanent
        // rejection, so every later start() returned without connecting.
        // Now the root is released when the loop ends; re-auth restarts.
        withTimeout(5_000) {
            while (socket.connectCalls.get() < 2) {
                manager.start()
                delay(30)
            }
        }
        assertThat(socket.connectCalls.get()).isAtLeast(2)
    }

    @Test
    fun `a failed owed-scrub repayment never starts the socket`() = runBlocking {
        val socket = RejectingSocket()
        val dao = MinimalDao().also { it.checkpointBusy = true }
        val flag = Flag(stored = true) // scrub owed from the previous logout
        val manager = manager(socket, dao, flag)

        manager.start()
        // Repayment (3 checkpoint attempts + retries) completes well inside
        // this window; the socket must never have been asked to connect.
        delay(2_000)

        assertThat(socket.connectCalls.get()).isEqualTo(0)
        assertThat(flag.stored).isTrue() // still owed — quarantine holds

        // Once the contention clears, the next start() repays and connects.
        dao.checkpointBusy = false
        manager.start()
        withTimeout(5_000) {
            while (socket.connectCalls.get() < 1) delay(20)
        }
        assertThat(flag.stored).isFalse()
    }
}

/** A ChatApi whose every member fails loudly — the manager needs none here. */
private fun unusedRestartChatApi(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
