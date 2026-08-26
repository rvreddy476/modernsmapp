package com.us.android.core.chat

import androidx.sqlite.db.SupportSQLiteQuery
import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * The logout writer-quiescence barrier (review finding F2-LB-1) and the
 * fail-secure scrub recovery (F2-LB-3), proven on the ADVERSE interleavings
 * the happy-path device journey never creates:
 *
 *  - an outbox worker whose network call is IN FLIGHT when teardown begins
 *    (WorkManager cancellation is best-effort — the worker keeps running);
 *  - a detached inbox sync whose response lands AFTER the wipe;
 *  - a writer already INSIDE a DB write when teardown starts;
 *  - a scrub that cannot complete, which must be owed and repaid, never
 *    silently forgotten.
 */
class ChatLogoutQuiescenceTest {

    // ── fakes ───────────────────────────────────────────────────────────

    /** In-memory ChatDao whose stores actually wipe, with suspension hooks. */
    private class FakeDao : ChatDao {
        val conversations = linkedMapOf<String, ChatConversationEntity>()
        val messages = mutableListOf<ChatMessageEntity>()
        val sends = linkedMapOf<String, ChatPendingSendEntity>()

        /** When set, [insertMessages] suspends here BEFORE writing. */
        var insertMessagesGate: CompletableDeferred<Unit>? = null

        /** When set, [clearConversations] suspends here — holds a teardown open. */
        var clearConversationsGate: CompletableDeferred<Unit>? = null

        /** Scripted checkpoint result per raw statement. */
        var checkpointResult: (sql: String) -> Int = { 0 }
        val rawStatements = mutableListOf<String>()

        override suspend fun upsertConversations(rows: List<ChatConversationEntity>) {
            rows.forEach { conversations[it.id] = it }
        }
        override fun conversations(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
        override suspend fun clearUnread(conversationId: String) = Unit
        override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
        override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
        override suspend fun rawCheckpoint(query: SupportSQLiteQuery): Int {
            rawStatements += query.sql
            return checkpointResult(query.sql)
        }
        override suspend fun deleteMessage(messageId: String) = Unit
        override suspend fun deleteConversation(conversationId: String) {
            conversations.remove(conversationId)
        }
        override suspend fun clearConversations() {
            clearConversationsGate?.await()
            conversations.clear()
        }
        override suspend fun insertMessages(rows: List<ChatMessageEntity>) {
            insertMessagesGate?.await()
            rows.forEach { row -> if (messages.none { it.id == row.id }) messages += row }
        }
        override suspend fun recentMessages(conversationId: String, limit: Int) = emptyList<ChatMessageEntity>()
        override suspend fun clearMessages() = messages.clear()
        override suspend fun enqueueSend(row: ChatPendingSendEntity) {
            sends.putIfAbsent(row.idempotencyKey, row)
        }
        override suspend fun pendingSends(): List<ChatPendingSendEntity> =
            sends.values.filter { !it.failed }.sortedBy { it.createdAtMillis }
        override fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>> =
            MutableStateFlow(emptyList())
        override suspend fun recordAttempt(key: String) {
            sends[key]?.let { sends[key] = it.copy(attempts = it.attempts + 1) }
        }
        override suspend fun markFailed(key: String) {
            sends[key]?.let { sends[key] = it.copy(failed = true) }
        }
        override suspend fun retry(key: String) = Unit
        override suspend fun completeSend(key: String) {
            sends.remove(key)
        }
        override suspend fun clearPendingSends() = sends.clear()
    }

    /** ChatApi whose send and conversation-list calls hold on gates. */
    private class FakeApi : ChatApi by unusedQuiescenceChatApi() {
        var sendGate: CompletableDeferred<Unit>? = null
        var conversationsGate: CompletableDeferred<Unit>? = null

        override suspend fun send(
            conversationId: String,
            idempotencyKey: String,
            body: SendMessageRequest,
        ): ApiEnvelope<MessageDto> {
            sendGate?.await()
            return ApiEnvelope(
                data = MessageDto(
                    msgId = "srv-$idempotencyKey",
                    conversationId = conversationId,
                    senderId = "sender",
                    text = body.text,
                    createdAt = "2026-08-26T10:00:00Z",
                ),
            )
        }

        override suspend fun conversations(limit: Int, cursor: String?): ApiEnvelope<List<ConversationDto>> {
            conversationsGate?.await()
            return ApiEnvelope(
                data = listOf(
                    ConversationDto(id = "stale-conv", type = "direct", updatedAt = "2026-08-26T10:00:00Z"),
                ),
            )
        }

        override suspend fun requests(): ApiEnvelope<List<ConversationDto>> = ApiEnvelope(data = emptyList())
    }

    private class FakeScrubFlag : ScrubRecoveryFlag {
        var stored = false
        override fun isPending(): Boolean = stored
        override fun setPending(pending: Boolean) {
            stored = pending
        }
    }

    private class RecordingScheduler : OutboxScheduler {
        var cancels = 0
        override fun scheduleDrain() = Unit
        override fun cancelDrain() {
            cancels++
        }
    }

    private fun store(dao: FakeDao, api: FakeApi, flag: FakeScrubFlag = FakeScrubFlag()): ChatStore =
        ChatStore(
            repository = ChatRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })),
            dao = dao,
            scheduler = RecordingScheduler(),
            scrubRecovery = flag,
        )

    private fun pendingRow(key: String) = ChatPendingSendEntity(
        idempotencyKey = key,
        conversationId = "conv",
        text = "held plaintext $key",
        mediaId = null,
        createdAtMillis = 1L,
        attempts = 0,
        failed = false,
    )

    // ── the interleavings ───────────────────────────────────────────────

    @Test
    fun `a worker held mid-network cannot write after the wipe, even once the gate reopens`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val store = store(dao, api)
        dao.sends["k1"] = pendingRow("k1")
        api.sendGate = CompletableDeferred()

        // The "WorkManager worker": suspended inside its network call.
        val drain = async { store.drainOutbox() }
        runCurrent()
        assertThat(dao.sends).isNotEmpty() // still queued, send in flight

        // Teardown completes while the worker is held — cancellation is
        // best-effort, so the worker is NOT cancelled here on purpose.
        assertThat(store.wipeForLogout()).isTrue()
        assertThat(dao.sends).isEmpty()

        // The gate has reopened for the next session; the stale worker's
        // response now lands. Its generation is dead — nothing may write.
        api.sendGate!!.complete(Unit)
        advanceUntilIdle()

        assertThat(drain.await()).isTrue()
        assertThat(dao.messages).isEmpty()
        assertThat(dao.sends).isEmpty()
        assertThat(dao.conversations).isEmpty()
    }

    @Test
    fun `an inbox sync held mid-network cannot repopulate the wiped cache`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val store = store(dao, api)
        api.conversationsGate = CompletableDeferred()

        // The "detached reconciliation": suspended fetching the inbox.
        val sync = async { store.syncInbox() }
        runCurrent()

        assertThat(store.wipeForLogout()).isTrue()

        // The stale response lands after the wipe (and after gate reopen).
        api.conversationsGate!!.complete(Unit)
        advanceUntilIdle()

        assertThat(sync.await()).isFalse()
        assertThat(dao.conversations).isEmpty()
    }

    @Test
    fun `teardown awaits a writer already inside a database write`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val store = store(dao, api)
        dao.insertMessagesGate = CompletableDeferred()

        // A cache write enters the gate, then suspends INSIDE the DAO.
        val write = launch {
            store.cacheMessages(
                listOf(
                    com.us.android.core.chat.data.Message(
                        id = "m1",
                        conversationId = "conv",
                        senderId = "s",
                        senderDisplayName = null,
                        text = "mid-write plaintext",
                        mediaId = null,
                        createdAt = "2026-08-26T10:00:00Z",
                    ),
                ),
            )
        }
        runCurrent()

        var wiped = false
        val wipe = launch {
            store.wipeForLogout()
            wiped = true
        }
        runCurrent()
        // The barrier: the wipe MUST wait for the in-flight write.
        assertThat(wiped).isFalse()

        dao.insertMessagesGate!!.complete(Unit)
        advanceUntilIdle()
        write.join()
        wipe.join()

        assertThat(wiped).isTrue()
        // The writer committed BEFORE the wipe ran, so nothing survives it.
        assertThat(dao.messages).isEmpty()
    }

    @Test
    fun `writes after teardown are refused for the old generation but new sessions work`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val store = store(dao, api)

        assertThat(store.wipeForLogout()).isTrue()

        // A fresh (new-generation) sync repopulates normally — the gate is
        // closed to STALE writers, not to the next session.
        assertThat(store.syncInbox()).isTrue()
        assertThat(dao.conversations).containsKey("stale-conv")
    }

    @Test
    fun `a scrub that cannot complete is owed and repaid at the next session start`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val flag = FakeScrubFlag()
        val store = store(dao, api, flag)
        var busy = true
        dao.checkpointResult = { sql ->
            if (sql.contains("wal_checkpoint") && busy) 1 else 0
        }

        // Contended logout: every checkpoint attempt reports busy.
        assertThat(store.wipeForLogout()).isFalse()
        assertThat(flag.stored).isTrue()

        // Next session start: contention gone; the AWAITED readiness
        // barrier re-wipes and scrubs before returning.
        busy = false
        assertThat(store.ensureReady()).isTrue()
        assertThat(flag.stored).isFalse()

        // And once repaid, readiness is a fast no-op.
        dao.rawStatements.clear()
        assertThat(store.ensureReady()).isTrue()
        assertThat(dao.rawStatements).isEmpty()
    }

    // ── the final-pass adverse cases (review P0s) ───────────────────────

    @Test
    fun `a stale lease from before teardown can never write history or enqueue an attachment`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val store = store(dao, api)

        // The "held" feature operation: lease acquired, then its network
        // call (history fetch / attachment upload) outlives the logout.
        val staleLease = store.acquireWriteLease()
        assertThat(staleLease).isNotNull()

        assertThat(store.wipeForLogout()).isTrue()

        // The gate is open again for the next account; the stale operation's
        // response lands now. History write: refused.
        store.cacheMessages(
            listOf(
                com.us.android.core.chat.data.Message(
                    id = "m-stale",
                    conversationId = "conv",
                    senderId = "s",
                    senderDisplayName = null,
                    text = "old-account plaintext",
                    mediaId = null,
                    createdAt = "2026-08-26T10:00:00Z",
                ),
            ),
            staleLease,
        )
        // Attachment enqueue: refused.
        store.enqueueSend("conv", text = "", mediaId = "media-stale", lease = staleLease)

        assertThat(dao.messages).isEmpty()
        assertThat(dao.sends).isEmpty()

        // A lease acquired AFTER the reopen works — the fence is on the
        // stale session, not on the next one.
        val freshLease = store.acquireWriteLease()
        store.enqueueSend("conv", text = "new session", lease = freshLease)
        assertThat(dao.sends).hasSize(1)
    }

    @Test
    fun `no lease is issued while teardown is in progress`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val store = store(dao, api)
        dao.clearConversationsGate = CompletableDeferred()

        // Teardown held open mid-wipe.
        val wipe = launch { store.wipeForLogout() }
        runCurrent()

        // An operation starting DURING teardown gets no lease — it cannot
        // capture the post-increment generation and ride it past reopen.
        assertThat(store.acquireWriteLease()).isNull()
        assertThat(store.syncInbox()).isFalse()

        dao.clearConversationsGate!!.complete(Unit)
        advanceUntilIdle()
        wipe.join()

        assertThat(dao.conversations).isEmpty()
        // After the reopen, acquisition works again.
        assertThat(store.acquireWriteLease()).isNotNull()
    }

    @Test
    fun `a new session's queued send survives an owed-scrub repayment`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val flag = FakeScrubFlag().also { it.stored = true } // owed from last logout
        // A leftover old-account row that the repayment must remove.
        dao.sends["old-key"] = pendingRow("old-key")
        val store = store(dao, api, flag)

        // The new account's first send: enqueueSend AWAITS readiness — the
        // repayment wipe runs FIRST, then the enqueue. The new message can
        // never be the thing the recovery wipes (review P0 #2).
        val key = store.enqueueSend("conv", text = "new account's first message")

        assertThat(flag.stored).isFalse()
        assertThat(dao.sends.keys).containsExactly(key)
        assertThat(dao.sends[key]!!.text).isEqualTo("new account's first message")
    }

    @Test
    fun `failed repayment quarantines the store - no lease, no write, no schedule`() = runTest {
        val dao = FakeDao()
        val api = FakeApi()
        val flag = FakeScrubFlag().also { it.stored = true } // owed from last logout
        val store = store(dao, api, flag)
        dao.checkpointResult = { sql -> if (sql.contains("wal_checkpoint")) 1 else 0 }

        // Repayment keeps failing: the store must stay unavailable.
        assertThat(store.ensureReady()).isFalse()
        assertThat(store.acquireWriteLease()).isNull()
        assertThat(store.syncInbox()).isFalse()
        store.enqueueSend("conv", text = "must not be accepted")
        assertThat(dao.sends).isEmpty()
        assertThat(dao.messages).isEmpty()
        assertThat(dao.conversations).isEmpty()
        assertThat(flag.stored).isTrue() // still owed

        // Once the contention clears, readiness repays and the store opens.
        dao.checkpointResult = { 0 }
        assertThat(store.ensureReady()).isTrue()
        assertThat(flag.stored).isFalse()
        store.enqueueSend("conv", text = "accepted now")
        assertThat(dao.sends).hasSize(1)
    }
}

/** A ChatApi whose every unscripted member fails loudly. */
private fun unusedQuiescenceChatApi(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
