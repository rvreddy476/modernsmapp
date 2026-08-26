package com.us.android.core.chat

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.chat.data.isRetryableSend
import com.us.android.core.common.error.AppError
import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response
import java.io.IOException

/**
 * The outbox rules the Android durability debt hinged on (carried blocker,
 * final-verification item 4): per-conversation ordering, immediate parking of
 * permanent refusals, and a drain loop that cannot strand a row enqueued
 * while a pass was already running.
 */
class ChatOutboxDrainTest {

    // ── fakes ───────────────────────────────────────────────────────────

    /** In-memory ChatDao — only what the outbox path touches has behaviour. */
    private class FakeDao : ChatDao {
        val sends = linkedMapOf<String, ChatPendingSendEntity>()
        val messages = mutableListOf<ChatMessageEntity>()
        var onPendingSendsRead: (() -> Unit)? = null

        override suspend fun upsertConversations(rows: List<ChatConversationEntity>) = Unit
        override fun conversations(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
        override suspend fun clearUnread(conversationId: String) = Unit
        override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
        override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
        override suspend fun rawCheckpoint(query: androidx.sqlite.db.SupportSQLiteQuery): Int = 0
        override suspend fun deleteMessage(messageId: String) = Unit
        override suspend fun deleteConversation(conversationId: String) = Unit
        override suspend fun clearConversations() = Unit
        override suspend fun insertMessages(rows: List<ChatMessageEntity>) {
            rows.forEach { row -> if (messages.none { it.id == row.id }) messages += row }
        }
        override suspend fun recentMessages(conversationId: String, limit: Int) = emptyList<ChatMessageEntity>()
        override suspend fun clearMessages() = Unit
        override suspend fun enqueueSend(row: ChatPendingSendEntity) {
            sends.putIfAbsent(row.idempotencyKey, row)
        }
        override suspend fun pendingSends(): List<ChatPendingSendEntity> {
            onPendingSendsRead?.invoke()
            return sends.values.filter { !it.failed }.sortedBy { it.createdAtMillis }
        }
        override fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>> =
            MutableStateFlow(emptyList<ChatPendingSendEntity>()).map { it }
        override suspend fun recordAttempt(key: String) {
            sends[key]?.let { sends[key] = it.copy(attempts = it.attempts + 1) }
        }
        override suspend fun markFailed(key: String) {
            sends[key]?.let { sends[key] = it.copy(failed = true) }
        }
        override suspend fun retry(key: String) {
            sends[key]?.let { sends[key] = it.copy(failed = false) }
        }
        override suspend fun completeSend(key: String) {
            sends.remove(key)
        }
        override suspend fun clearPendingSends() = Unit
    }

    /** Scripted ChatApi: each send consults [script] by idempotency key. */
    private class FakeApi(
        val script: (idempotencyKey: String) -> ScriptedOutcome,
    ) : ChatApi by unusedChatApi() {
        val sentKeys = mutableListOf<String>()
        override suspend fun send(
            conversationId: String,
            idempotencyKey: String,
            body: SendMessageRequest,
        ): com.us.android.core.network.ApiEnvelope<MessageDto> {
            sentKeys += idempotencyKey
            return when (val outcome = script(idempotencyKey)) {
                is ScriptedOutcome.Ok -> com.us.android.core.network.ApiEnvelope(
                    data = MessageDto(
                        msgId = outcome.serverId,
                        conversationId = conversationId,
                        senderId = "sender",
                        text = body.text,
                        createdAt = "2026-08-25T10:00:00Z",
                    ),
                )
                ScriptedOutcome.Network -> throw IOException("socket reset")
                is ScriptedOutcome.Http -> throw HttpException(
                    Response.error<Any>(
                        outcome.code,
                        """{"error":{"code":"${outcome.errorCode}","message":"scripted"}}"""
                            .toResponseBody("application/json".toMediaType()),
                    ),
                )
            }
        }
    }

    sealed interface ScriptedOutcome {
        data class Ok(val serverId: String) : ScriptedOutcome
        data object Network : ScriptedOutcome
        data class Http(val code: Int, val errorCode: String) : ScriptedOutcome
    }

    private class RecordingScheduler : OutboxScheduler {
        var scheduled = 0
        override fun scheduleDrain() {
            scheduled++
        }
        override fun cancelDrain() = Unit
    }

    private class FakeScrubFlag : com.us.android.core.chat.data.ScrubRecoveryFlag {
        private var stored = false
        override fun isPending(): Boolean = stored
        override fun setPending(pending: Boolean) {
            stored = pending
        }
    }

    private fun store(dao: FakeDao, api: FakeApi): ChatStore =
        ChatStore(
            repository = ChatRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })),
            dao = dao,
            scheduler = RecordingScheduler(),
            scrubRecovery = FakeScrubFlag(),
        )

    private fun row(key: String, conv: String, at: Long, attempts: Int = 0) =
        ChatPendingSendEntity(
            idempotencyKey = key,
            conversationId = conv,
            text = "msg $key",
            mediaId = null,
            createdAtMillis = at,
            attempts = attempts,
            failed = false,
        )

    // ── the rules ───────────────────────────────────────────────────────

    @Test
    fun `a retryable failure blocks later rows of the SAME conversation only`() = runTest {
        val dao = FakeDao()
        dao.sends["a1"] = row("a1", "convA", at = 1)
        dao.sends["a2"] = row("a2", "convA", at = 2)
        dao.sends["b1"] = row("b1", "convB", at = 3)
        val api = FakeApi { key ->
            if (key == "a1") ScriptedOutcome.Network else ScriptedOutcome.Ok("srv-$key")
        }

        val delivered = store(dao, api).drainOutbox()

        assertThat(delivered).isFalse()
        // a2 was never attempted — a reply must not overtake the message it
        // answers — while convB drained past the failure.
        assertThat(api.sentKeys).containsExactly("a1", "b1").inOrder()
        assertThat(dao.sends.keys).containsExactly("a1", "a2")
        assertThat(dao.sends["a1"]!!.failed).isFalse()
    }

    @Test
    fun `a permanent refusal parks immediately instead of burning retries`() = runTest {
        val dao = FakeDao()
        dao.sends["p1"] = row("p1", "convA", at = 1)
        dao.sends["p2"] = row("p2", "convA", at = 2)
        val api = FakeApi { key ->
            if (key == "p1") {
                ScriptedOutcome.Http(403, "MESSAGING_NOT_ALLOWED")
            } else {
                ScriptedOutcome.Ok("srv-$key")
            }
        }

        val delivered = store(dao, api).drainOutbox()

        // The refusal will not change on replay: parked on the FIRST attempt,
        // visible for an explicit user decision, and the queue completes.
        assertThat(delivered).isTrue()
        assertThat(dao.sends["p1"]!!.failed).isTrue()
        assertThat(dao.sends["p1"]!!.attempts).isEqualTo(1)
        assertThat(dao.sends).doesNotContainKey("p2")
    }

    @Test
    fun `rows enqueued during a pass are drained by the same worker run`() = runTest {
        val dao = FakeDao()
        dao.sends["first"] = row("first", "convA", at = 1)
        // Simulate the unique-KEEP race: a new send lands AFTER the worker's
        // first queue read. The loop's re-read must pick it up; before the
        // fix it waited for the next app start.
        var raced = false
        dao.onPendingSendsRead = {
            if (!raced) {
                raced = true
                dao.sends["second"] = row("second", "convA", at = 2)
            }
        }
        val api = FakeApi { ScriptedOutcome.Ok("srv-${dao.sends.size}") }

        val delivered = store(dao, api).drainOutbox()

        assertThat(delivered).isTrue()
        assertThat(api.sentKeys).containsExactly("first", "second").inOrder()
        assertThat(dao.sends).isEmpty()
    }

    @Test
    fun `a retried row replays the SAME idempotency key`() = runTest {
        val dao = FakeDao()
        dao.sends["k1"] = row("k1", "convA", at = 1)
        var calls = 0
        val api = FakeApi {
            calls++
            if (calls == 1) ScriptedOutcome.Network else ScriptedOutcome.Ok("srv-1")
        }
        val store = store(dao, api)

        assertThat(store.drainOutbox()).isFalse()
        assertThat(store.drainOutbox()).isTrue()

        // Two attempts, ONE intent: the key is identical, so a lost response
        // replays server-side instead of duplicating.
        assertThat(api.sentKeys).containsExactly("k1", "k1")
        assertThat(dao.sends).isEmpty()
    }

    // ── the classification table ────────────────────────────────────────

    @Test
    fun `transport and server faults retry, policy refusals do not`() {
        assertThat(AppError.NoNetwork().isRetryableSend()).isTrue()
        assertThat(AppError.Timeout().isRetryableSend()).isTrue()
        assertThat(AppError.RateLimited(retryAfterSeconds = 1).isRetryableSend()).isTrue()
        assertThat(AppError.Malformed("half a body").isRetryableSend()).isTrue()
        assertThat(AppError.Server(statusCode = 502, code = null).isRetryableSend()).isTrue()
        assertThat(AppError.Unknown(code = null, statusCode = 503).isRetryableSend()).isTrue()
        assertThat(AppError.Unknown(code = null, statusCode = null).isRetryableSend()).isTrue()

        assertThat(AppError.InvalidRequest("bad").isRetryableSend()).isFalse()
        assertThat(AppError.AuthFailed().isRetryableSend()).isFalse()
        assertThat(AppError.Forbidden(code = "MESSAGING_NOT_ALLOWED").isRetryableSend()).isFalse()
        assertThat(AppError.NotFound().isRetryableSend()).isFalse()
        assertThat(AppError.Unknown(code = "X", statusCode = 422).isRetryableSend()).isFalse()
    }
}

/** A ChatApi whose every member fails — the delegate for scripted fakes. */
private fun unusedChatApi(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
