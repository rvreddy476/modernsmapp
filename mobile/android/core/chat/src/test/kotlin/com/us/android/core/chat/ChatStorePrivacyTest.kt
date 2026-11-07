package com.us.android.core.chat

import androidx.sqlite.db.SupportSQLiteQuery
import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.AWAITING_REQUEST_ACCEPTANCE
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.Message
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatParityDao
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ApiMeta
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.flow.map
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.HttpException
import retrofit2.Response

/**
 * The chat cache's privacy guards (chat audit H2, L1, and the 7 Nov unsend
 * contract): what the server removed leaves the device, and nothing the
 * viewer unsent comes back.
 */
class ChatStorePrivacyTest {

    // ── fakes: an in-memory Room that honours the queries under test ────

    private class Db {
        val conversations = linkedMapOf<String, ChatConversationEntity>()
        val messages = linkedMapOf<String, ChatMessageEntity>()
        val pending = linkedMapOf<String, ChatPendingSendEntity>()
    }

    private class FakeDao(private val db: Db) : ChatDao {
        override suspend fun upsertConversations(rows: List<ChatConversationEntity>) {
            rows.forEach { db.conversations[it.id] = it }
        }
        override fun conversations(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
        override suspend fun clearUnread(conversationId: String) = Unit
        override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
        override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
        override suspend fun rawCheckpoint(query: SupportSQLiteQuery): Int = 0
        override suspend fun deleteMessage(messageId: String) {
            db.messages.remove(messageId)
        }
        override suspend fun deleteConversation(conversationId: String) {
            db.conversations.remove(conversationId)
        }
        override suspend fun clearConversations() = db.conversations.clear()
        override suspend fun insertMessages(rows: List<ChatMessageEntity>) {
            rows.forEach { db.messages.putIfAbsent(it.id, it) }
        }
        override suspend fun recentMessages(conversationId: String, limit: Int) =
            db.messages.values
                .filter { it.conversationId == conversationId }
                .sortedByDescending { it.createdAt }
                .take(limit)
        override suspend fun clearMessages() = db.messages.clear()
        override suspend fun enqueueSend(row: ChatPendingSendEntity) {
            db.pending[row.idempotencyKey] = row
        }
        override suspend fun pendingSends(): List<ChatPendingSendEntity> = db.pending.values.filter { !it.failed }
        override fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>> =
            MutableStateFlow(db.pending.values.filter { it.conversationId == conversationId })
        override suspend fun recordAttempt(key: String) {
            db.pending[key]?.let { db.pending[key] = it.copy(attempts = it.attempts + 1) }
        }
        override suspend fun markFailed(key: String) {
            db.pending[key]?.let { db.pending[key] = it.copy(failed = true) }
        }
        override suspend fun retry(key: String) = Unit
        override suspend fun completeSend(key: String) {
            db.pending.remove(key)
        }
        override suspend fun clearPendingSends() = db.pending.clear()
        override suspend fun allPendingSends(): List<ChatPendingSendEntity> = db.pending.values.toList()
        override suspend fun updatePendingSend(row: ChatPendingSendEntity) {
            db.pending[row.idempotencyKey] = row
        }
    }

    private class FakeParityDao(private val db: Db) : ChatParityDao {
        val requestPrunes = mutableListOf<List<String>>()
        override suspend fun message(messageId: String) = db.messages[messageId]
        override suspend fun refreshMessage(
            messageId: String,
            type: String?,
            reactionsJson: String?,
            extrasJson: String?,
        ) = Unit
        override suspend fun updateReactions(messageId: String, reactionsJson: String?) = Unit
        override suspend fun clearConversationMessages(conversationId: String) = Unit
        override suspend fun markRead(conversationId: String) = Unit
        override suspend fun markUnreadOnly(conversationId: String) = Unit
        override suspend fun incrementUnread(conversationId: String) = Unit
        override suspend fun updatePreview(conversationId: String, preview: String, at: String) = Unit
        override suspend fun conversation(conversationId: String) = db.conversations[conversationId]
        override suspend fun updateLastMessage(conversationId: String, senderId: String?, extrasJson: String?) = Unit
        override suspend fun updatePrefs(
            conversationId: String,
            pinned: Boolean,
            muted: Boolean,
            muteCalls: Boolean,
            alertTone: String?,
        ) = Unit
        override suspend fun alertTone(conversationId: String): String? = null
        override suspend fun pruneInbox(keep: List<String>) = Unit
        override suspend fun deleteConversation(conversationId: String) {
            db.conversations.remove(conversationId)
        }
        override suspend fun pruneRequests(keep: List<String>) {
            requestPrunes += keep
            db.conversations.values.removeAll { it.isRequest && it.id !in keep }
        }
        override suspend fun deleteExpiredMessages(nowMillis: Long): Int {
            val expired = db.messages.values.filter { row -> row.expiresAtMillis?.let { it <= nowMillis } == true }
            expired.forEach { db.messages.remove(it.id) }
            return expired.size
        }
        override suspend fun deleteMessages(messageIds: List<String>) {
            messageIds.forEach { db.messages.remove(it) }
        }
        override suspend fun markFailedWithCode(key: String, code: String?) {
            db.pending[key]?.let { db.pending[key] = it.copy(failed = true, failureCode = code) }
        }
    }

    /** Inbox and Requests as scripted; sends answer [sendAnswer]. */
    private class FakeApi(
        private val requestPages: List<ApiEnvelope<List<ConversationDto>>> = listOf(ApiEnvelope(data = emptyList())),
        private val failRequestPageAt: Int? = null,
        var sendAnswer: () -> ApiEnvelope<MessageDto> = { error("no send scripted") },
    ) : ChatApi by unscriptedPrivacyChat() {
        private var requestCalls = 0
        override suspend fun conversations(limit: Int, cursor: String?): ApiEnvelope<List<ConversationDto>> =
            ApiEnvelope(data = emptyList())
        override suspend fun requests(limit: Int, cursor: String?): ApiEnvelope<List<ConversationDto>> {
            val call = requestCalls++
            if (call == failRequestPageAt) {
                throw HttpException(Response.error<Any>(503, "{}".toResponseBody("application/json".toMediaType())))
            }
            return requestPages[call]
        }
        override suspend fun send(
            conversationId: String,
            idempotencyKey: String,
            body: SendMessageRequest,
        ) = sendAnswer()
    }

    private object NoScrubOwed : ScrubRecoveryFlag {
        override fun isPending(): Boolean = false
        override fun setPending(pending: Boolean): Boolean = true
    }

    private object NoScheduler : OutboxScheduler {
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
    }

    private val json = Json { ignoreUnknownKeys = true }

    private fun store(db: Db, api: ChatApi = FakeApi(), parityDao: FakeParityDao = FakeParityDao(db)) = ChatStore(
        repository = ChatRepository(api, ErrorMapper(json)),
        dao = FakeDao(db),
        scheduler = NoScheduler,
        scrubRecovery = NoScrubOwed,
        parityDao = parityDao,
    ).also { it.nowMillis = { NOW } }

    private fun message(id: String, at: String, expiresAtMillis: Long? = null, conversationId: String = "c1") =
        Message(id, conversationId, "u2", null, "text of $id", at, expiresAtMillis = expiresAtMillis)

    // ── H2: disappearing messages leave the disk ─────────────────────────

    @Test
    fun `an expired disappearing message is never served from the cache and is purged`() = runTest {
        val db = Db()
        val store = store(db)
        store.cacheMessages(
            listOf(
                message("gone", "2026-11-01T10:00:00Z", expiresAtMillis = NOW - 1),
                message("due-now", "2026-11-01T10:01:00Z", expiresAtMillis = NOW),
                message("later", "2026-11-01T10:02:00Z", expiresAtMillis = NOW + 60_000),
                message("plain", "2026-11-01T10:03:00Z"),
            ),
        )

        val served = store.recentCachedMessages("c1").map { it.id }

        assertThat(served).containsExactly("plain", "later").inOrder()
        assertThat(db.messages.keys).containsExactly("later", "plain")
    }

    @Test
    fun `an inbox sync purges messages that expired while nothing was listening`() = runTest {
        val db = Db()
        val store = store(db)
        store.cacheMessages(
            listOf(
                message("expired", "2026-11-01T10:00:00Z", expiresAtMillis = NOW - 1),
                message("kept", "2026-11-01T10:01:00Z", expiresAtMillis = NOW + 1),
            ),
        )

        store.syncInbox()

        assertThat(db.messages.keys).containsExactly("kept")
    }

    @Test
    fun `rows the newest page proves gone are dropped, rows outside its window are kept`() = runTest {
        val db = Db()
        val store = store(db)
        store.cacheMessages(
            listOf(
                message("older-than-page", "2026-11-01T09:00:00Z"),
                message("p1", "2026-11-01T10:00:00Z"),
                message("unsent-on-server", "2026-11-01T10:30:00Z"),
                message("p2", "2026-11-01T11:00:00Z"),
                message("newer-than-page", "2026-11-01T12:00:00Z"),
            ),
        )
        val page = listOf(message("p2", "2026-11-01T11:00:00Z"), message("p1", "2026-11-01T10:00:00Z"))

        store.reconcileCachedPage("c1", page, hasMore = true)

        assertThat(db.messages.keys).containsExactly("older-than-page", "p1", "p2", "newer-than-page")
    }

    @Test
    fun `a page that is the whole history drops every older row it does not hold`() = runTest {
        val db = Db()
        val store = store(db)
        store.cacheMessages(
            listOf(message("expired-long-ago", "2026-10-01T09:00:00Z"), message("p1", "2026-11-01T10:00:00Z")),
        )

        store.reconcileCachedPage("c1", listOf(message("p1", "2026-11-01T10:00:00Z")), hasMore = false)

        assertThat(db.messages.keys).containsExactly("p1")
    }

    // ── unsend: nothing the viewer unsent comes back ─────────────────────

    @Test
    fun `an unsent message is not written back by a late page, echo or replayed send`() = runTest {
        val db = Db()
        val api = FakeApi()
        val store = store(db, api)
        store.cacheMessages(listOf(message("m1", "2026-11-01T10:00:00Z", conversationId = "c1")))

        store.applyDeleted("m1")
        // A history page fetched before the unsend, landing after it.
        store.cacheMessages(listOf(message("m1", "2026-11-01T10:00:00Z")))
        // The held socket echo.
        store.applyRealtimeMessage(message("m1", "2026-11-01T10:00:00Z"))
        // The queued resend whose first attempt landed: its replay answers with m1.
        api.sendAnswer = {
            ApiEnvelope(data = MessageDto(msgId = "m1", conversationId = "c1", senderId = "me", text = "hi"))
        }
        store.enqueueSend("c1", "hi")
        store.drainOutbox()

        assertThat(db.messages).doesNotContainKey("m1")
        assertThat(store.recentCachedMessages("c1")).isEmpty()
        // The resend itself is done: no row left to retry forever.
        assertThat(db.pending).isEmpty()
    }

    @Test
    fun `a message deleted for me is forgotten as well`() = runTest {
        val db = Db()
        val store = store(db)
        store.cacheMessages(listOf(message("m1", "2026-11-01T10:00:00Z")))

        store.removeCachedMessage("m1")
        store.cacheMessages(listOf(message("m1", "2026-11-01T10:00:00Z")))

        assertThat(store.isForgotten("m1")).isTrue()
        assertThat(db.messages).doesNotContainKey("m1")
    }

    // ── L1: Requests are pruned only after a complete read ───────────────

    private fun request(id: String) =
        ConversationDto(id = id, type = "direct", isRequest = true, updatedAt = "2026-11-01T10:00:00Z")

    private fun staleRequest(db: Db, id: String) {
        db.conversations[id] = ChatConversationEntity(
            id = id, type = "direct", title = null, isRequest = true, membersJson = "[]",
            updatedAt = "2026-10-01T10:00:00Z", avatarMediaId = null, lastMessageAt = null,
            lastMessagePreview = "", lastMessageSender = null, hasUnread = false, syncedAtMillis = 0,
        )
    }

    @Test
    fun `a complete Requests read removes requests answered elsewhere`() = runTest {
        val db = Db()
        staleRequest(db, "declined-on-laptop")
        val api = FakeApi(
            requestPages = listOf(
                ApiEnvelope(data = listOf(request("r1")), meta = ApiMeta(nextCursor = "p2")),
                ApiEnvelope(data = listOf(request("r2"))),
            ),
        )
        val parityDao = FakeParityDao(db)

        store(db, api, parityDao).syncInbox()

        assertThat(parityDao.requestPrunes).containsExactly(listOf("r1", "r2"))
        assertThat(db.conversations.keys).containsExactly("r1", "r2")
    }

    @Test
    fun `a Requests read that stopped part way prunes nothing`() = runTest {
        val db = Db()
        staleRequest(db, "maybe-on-page-2")
        val api = FakeApi(
            requestPages = listOf(ApiEnvelope(data = listOf(request("r1")), meta = ApiMeta(nextCursor = "p2"))),
            failRequestPageAt = 1,
        )
        val parityDao = FakeParityDao(db)

        store(db, api, parityDao).syncInbox()

        assertThat(parityDao.requestPrunes).isEmpty()
        assertThat(db.conversations.keys).containsExactly("maybe-on-page-2", "r1")
    }

    // ── a parked send keeps the server's reason ───────────────────────────

    @Test
    fun `a refused request message is parked with AWAITING_REQUEST_ACCEPTANCE`() = runTest {
        val db = Db()
        val api = FakeApi(
            sendAnswer = {
                throw HttpException(
                    Response.error<Any>(
                        409,
                        """{"error":{"code":"AWAITING_REQUEST_ACCEPTANCE","message":"wait"}}"""
                            .toResponseBody("application/json".toMediaType()),
                    ),
                )
            },
        )
        val store = store(db, api)
        store.enqueueSend("c1", "second message")

        store.drainOutbox()

        val parked = store.pendingSendsFor("c1").first().single()
        assertThat(parked.failed).isTrue()
        assertThat(parked.failureCode).isEqualTo(AWAITING_REQUEST_ACCEPTANCE)
    }

    @Test
    fun `a row still queued reports no failure code`() = runTest {
        val db = Db()
        db.pending["k"] = ChatPendingSendEntity(
            idempotencyKey = "k", conversationId = "c1", text = "t", mediaId = null,
            createdAtMillis = 0, attempts = 1, failed = false, failureCode = "LEFT_OVER",
        )

        val row = store(db).pendingSendsFor("c1").map { it.single() }.first()

        assertThat(row.failureCode).isNull()
    }

    private companion object {
        /** 2026-11-08T00:00:00Z. */
        const val NOW = 1_794_096_000_000L
    }
}

private fun unscriptedPrivacyChat(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
