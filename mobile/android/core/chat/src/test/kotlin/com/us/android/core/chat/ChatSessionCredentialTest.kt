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
import com.us.android.core.network.TokenRefresher
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.flowOf
import kotlinx.coroutines.runBlocking
import kotlinx.coroutines.withTimeout
import kotlinx.serialization.json.Json
import okhttp3.OkHttpClient
import org.junit.After
import org.junit.Test
import java.util.Collections
import java.util.concurrent.atomic.AtomicInteger

/**
 * Chat audit H1: the session socket must not stay dead for a signed-in user.
 *
 *  - at cold start there is no access token yet: the manager mints one
 *    BEFORE connecting, instead of connecting with nothing and giving up;
 *  - no token at all (a refresh that failed for a transient reason) is a
 *    retryable disconnect, not a permanent one;
 *  - a rejected token is not retried as it is, and does not end the
 *    session either: the next credential — or a [ChatSessionManager.start]
 *    from a screen — reconnects.
 *
 * Real dispatchers (the manager owns its scope on Dispatchers.IO), so these
 * poll with generous timeouts, as [ChatSessionRestartTest] does.
 */
class ChatSessionCredentialTest {

    /** Records the token each connect carried; every connection ends as [ending] says. */
    private class ScriptedSocket(
        private val ending: (token: String?) -> ChatSocketEvent.Disconnected,
    ) : ChatSocket(OkHttpClient(), "ws://unused") {
        val tokens: MutableList<String?> = Collections.synchronizedList(mutableListOf())
        val connectCalls = AtomicInteger(0)

        override fun connect(tokenProvider: () -> String?): Flow<ChatSocketEvent> {
            val token = tokenProvider()
            tokens += token
            connectCalls.incrementAndGet()
            return flowOf(ending(token))
        }

        override fun send(frame: String): Boolean = false
    }

    private class Tokens(@Volatile var current: String?) : TokenProvider {
        override fun currentAccessToken(): String? = current
    }

    /** A refresher that mints [minted] when asked; due while [due] holds. */
    private class Refresher(
        private val tokens: Tokens,
        private val minted: String?,
        @Volatile var due: Boolean,
    ) : TokenRefresher {
        val refreshes = AtomicInteger(0)

        override suspend fun refresh(): String? {
            refreshes.incrementAndGet()
            if (minted != null) {
                tokens.current = minted
                due = false
            }
            return minted
        }

        override suspend fun refreshIfStale(failedToken: String?): String? = refresh()

        override fun shouldRefreshBeforeRequest(): Boolean = due
    }

    private val managers = mutableListOf<ChatSessionManager>()

    @After
    fun tearDown() = runBlocking {
        managers.forEach { it.stopAndJoin() }
    }

    private fun manager(
        socket: ChatSocket,
        tokens: TokenProvider,
        refresher: TokenRefresher? = null,
    ): ChatSessionManager {
        val repository = ChatRepository(unusedCredentialChatApi(), ErrorMapper(Json { ignoreUnknownKeys = true }))
        val store = ChatStore(repository, NoopDao, NoopScheduler, NoFlag)
        return ChatSessionManager(
            socket = socket,
            store = store,
            tokenProvider = tokens,
            repository = repository,
            tokenRefresher = refresher,
        ).also { managers += it }
    }

    private suspend fun awaitConnects(socket: ScriptedSocket, count: Int) = withTimeout(5_000) {
        while (socket.connectCalls.get() < count) delay(20)
    }

    @Test
    fun `a cold start mints the access token before the first connect`() = runBlocking {
        val tokens = Tokens(current = null)
        val refresher = Refresher(tokens, minted = "fresh", due = true)
        val socket = ScriptedSocket { ChatSocketEvent.Disconnected(permanent = false) }

        manager(socket, tokens, refresher).start()
        awaitConnects(socket, 1)

        assertThat(socket.tokens.first()).isEqualTo("fresh")
        assertThat(refresher.refreshes.get()).isAtLeast(1)
    }

    @Test
    fun `no token yet is retried by the loop itself, never connected with nothing`() = runBlocking {
        val tokens = Tokens(current = null)
        // The refresh fails (transient): there is still no token.
        val refresher = Refresher(tokens, minted = null, due = true)
        val socket = ScriptedSocket { ChatSocketEvent.Disconnected(permanent = false) }

        manager(socket, tokens, refresher).start()
        withTimeout(5_000) { while (refresher.refreshes.get() < 2) delay(20) }
        assertThat(socket.connectCalls.get()).isEqualTo(0)

        // The next refresh lands: the SAME loop connects, no start() needed.
        tokens.current = "late"
        refresher.due = false
        awaitConnects(socket, 1)

        assertThat(socket.tokens).doesNotContain(null)
        assertThat(socket.tokens.first()).isEqualTo("late")
    }

    @Test
    fun `a rejected token is not retried as it is`() = runBlocking {
        val tokens = Tokens(current = "old")
        val socket = ScriptedSocket { ChatSocketEvent.Disconnected(permanent = true) }

        manager(socket, tokens).start()
        awaitConnects(socket, 1)
        // Well past the reconnect backoff: the same credential would only
        // earn a rate limit, so the loop waits for a different one.
        delay(1_500)

        assertThat(socket.connectCalls.get()).isEqualTo(1)
    }

    @Test
    fun `after a rejection, a screen's start reconnects with the new token`() = runBlocking {
        val tokens = Tokens(current = "old")
        val socket = ScriptedSocket { token ->
            ChatSocketEvent.Disconnected(permanent = token == "old")
        }
        val manager = manager(socket, tokens)

        manager.start()
        awaitConnects(socket, 1)
        tokens.current = "new"
        manager.start()
        awaitConnects(socket, 2)

        assertThat(socket.tokens.take(2)).containsExactly("old", "new").inOrder()
    }

    @Test
    fun `a token due for refresh is refreshed before connecting`() = runBlocking {
        // Audit M2: a token inside the gateway's expiry skew is closed right
        // after the upgrade; it is replaced before the connect instead.
        val tokens = Tokens(current = "ending")
        val refresher = Refresher(tokens, minted = "renewed", due = true)
        val socket = ScriptedSocket { ChatSocketEvent.Disconnected(permanent = false) }

        manager(socket, tokens, refresher).start()
        awaitConnects(socket, 1)

        assertThat(socket.tokens.first()).isEqualTo("renewed")
    }

    private object NoopDao : ChatDao {
        override suspend fun upsertConversations(rows: List<ChatConversationEntity>) = Unit
        override fun conversations(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
        override suspend fun clearUnread(conversationId: String) = Unit
        override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
        override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
        override suspend fun rawCheckpoint(query: SupportSQLiteQuery): Int = 0
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
        override suspend fun allPendingSends(): List<ChatPendingSendEntity> = emptyList()
        override suspend fun updatePendingSend(row: ChatPendingSendEntity) = Unit
    }

    private object NoFlag : ScrubRecoveryFlag {
        override fun isPending(): Boolean = false
        override fun setPending(pending: Boolean): Boolean = true
    }

    private object NoopScheduler : OutboxScheduler {
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
    }
}

/** A ChatApi whose every member fails loudly — these tests need none. */
private fun unusedCredentialChatApi(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
