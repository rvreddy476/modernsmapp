package com.us.android.core.chat

import androidx.sqlite.db.SupportSQLiteQuery
import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatParityApi
import com.us.android.core.chat.data.ChatParityRepository
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.chat.data.TypedSend
import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatParityDao
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test

/** Chat parity: the store's prune guard, typed outbox sends and realtime reactions. */
class ChatStoreParityTest {

    private class FakeDao : ChatDao {
        val conversations = linkedMapOf<String, ChatConversationEntity>()
        val pending = linkedMapOf<String, ChatPendingSendEntity>()
        val messages = linkedMapOf<String, ChatMessageEntity>()

        override suspend fun upsertConversations(rows: List<ChatConversationEntity>) {
            rows.forEach { conversations[it.id] = it }
        }
        override fun conversations(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
        override suspend fun clearUnread(conversationId: String) = Unit
        override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
        override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
        override suspend fun rawCheckpoint(query: SupportSQLiteQuery): Int = 0
        override suspend fun deleteMessage(messageId: String) {
            messages.remove(messageId)
        }
        override suspend fun deleteConversation(conversationId: String) {
            conversations.remove(conversationId)
        }
        override suspend fun clearConversations() = conversations.clear()
        override suspend fun insertMessages(rows: List<ChatMessageEntity>) {
            rows.forEach { messages.putIfAbsent(it.id, it) }
        }
        override suspend fun recentMessages(conversationId: String, limit: Int) = messages.values.toList()
        override suspend fun clearMessages() = messages.clear()
        override suspend fun enqueueSend(row: ChatPendingSendEntity) {
            pending[row.idempotencyKey] = row
        }
        override suspend fun pendingSends(): List<ChatPendingSendEntity> = pending.values.filter { !it.failed }
        override fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>> = MutableStateFlow(emptyList())
        override suspend fun recordAttempt(key: String) = Unit
        override suspend fun markFailed(key: String) {
            pending[key]?.let { pending[key] = it.copy(failed = true) }
        }
        override suspend fun retry(key: String) = Unit
        override suspend fun completeSend(key: String) {
            pending.remove(key)
        }
        override suspend fun clearPendingSends() = pending.clear()
    }

    private class FakeParityDao(private val messages: MutableMap<String, ChatMessageEntity>) : ChatParityDao {
        val prunes = mutableListOf<List<String>>()
        override suspend fun message(messageId: String) = messages[messageId]
        override suspend fun refreshMessage(messageId: String, type: String?, reactionsJson: String?, extrasJson: String?) = Unit
        override suspend fun updateReactions(messageId: String, reactionsJson: String?) {
            messages[messageId]?.let { messages[messageId] = it.copy(reactionsJson = reactionsJson) }
        }
        override suspend fun clearConversationMessages(conversationId: String) = Unit
        override suspend fun markRead(conversationId: String) = Unit
        override suspend fun markUnreadOnly(conversationId: String) = Unit
        override suspend fun incrementUnread(conversationId: String) = Unit
        override suspend fun updatePrefs(conversationId: String, pinned: Boolean, muted: Boolean, muteCalls: Boolean, alertTone: String?) = Unit
        override suspend fun alertTone(conversationId: String): String? = null
        override suspend fun pruneInbox(keep: List<String>) {
            prunes += keep
        }
        override suspend fun deleteConversation(conversationId: String) = Unit
    }

    private class FakeApi(private val inbox: List<ConversationDto>) : ChatApi by unscriptedChat() {
        override suspend fun conversations(limit: Int, cursor: String?): ApiEnvelope<List<ConversationDto>> = ApiEnvelope(data = inbox)
        override suspend fun requests(): ApiEnvelope<List<ConversationDto>> = ApiEnvelope(data = emptyList())
    }

    private class FakeParityApi : ChatParityApi by unscriptedParity() {
        val sent = mutableListOf<Pair<String, SendMessageRequest>>()
        override suspend fun send(conversationId: String, idempotencyKey: String, body: SendMessageRequest): ApiEnvelope<MessageDto> {
            sent += idempotencyKey to body
            return ApiEnvelope(data = MessageDto(msgId = "srv-1", conversationId = conversationId, senderId = "me", type = body.type))
        }
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

    private fun store(dao: FakeDao, parityDao: FakeParityDao, api: ChatApi, parityApi: ChatParityApi = FakeParityApi()) = ChatStore(
        repository = ChatRepository(api, ErrorMapper(json)),
        dao = dao,
        scheduler = NoScheduler,
        scrubRecovery = NoScrubOwed,
        parityDao = parityDao,
        parity = ChatParityRepository(parityApi, ErrorMapper(json)),
    )

    private fun dto(id: String) = ConversationDto(id = id, type = "direct", updatedAt = "2026-10-18T10:00:00Z")

    @Test
    fun `a short complete inbox prunes rows the server no longer lists`() = runTest {
        val dao = FakeDao()
        val parityDao = FakeParityDao(dao.messages)
        assertThat(store(dao, parityDao, FakeApi(listOf(dto("a"), dto("b")))).syncInbox()).isTrue()
        assertThat(parityDao.prunes).containsExactly(listOf("a", "b"))
    }

    @Test
    fun `a full page never prunes - it may not be the whole inbox`() = runTest {
        val dao = FakeDao()
        val parityDao = FakeParityDao(dao.messages)
        val full = (1..100).map { dto("c$it") }
        store(dao, parityDao, FakeApi(full)).syncInbox()
        assertThat(parityDao.prunes).isEmpty()
    }

    @Test
    fun `a typed send queues durably and drains through the parity send under its key`() = runTest {
        val dao = FakeDao()
        val parityApi = FakeParityApi()
        val store = store(dao, FakeParityDao(dao.messages), FakeApi(emptyList()), parityApi)

        val key = store.enqueueTypedSend("c1", TypedSend.voice("a1", 1500, listOf(1, 2, 3)))
        assertThat(key).isNotNull()
        assertThat(dao.pending.values.single().type).isEqualTo("voice")

        assertThat(store.drainOutbox()).isTrue()
        val (sentKey, body) = parityApi.sent.single()
        assertThat(sentKey).isEqualTo(key)
        assertThat(body.type).isEqualTo("voice")
        assertThat(body.waveform).containsExactly(1, 2, 3).inOrder()
        assertThat(dao.pending).isEmpty()
        assertThat(dao.messages.keys).containsExactly("srv-1")
    }

    @Test
    fun `without the parity seam a typed row parks instead of going out as text`() = runTest {
        val dao = FakeDao()
        val bare = ChatStore(ChatRepository(FakeApi(emptyList()), ErrorMapper(json)), dao, NoScheduler, NoScrubOwed)
        val key = bare.enqueueTypedSend("c1", TypedSend.sticker(com.us.android.core.chat.data.StickerInfo("s", "u", 1, 1)))!!
        bare.drainOutbox()
        assertThat(dao.pending.getValue(key).failed).isTrue()
    }

    @Test
    fun `a realtime reaction lands on the cached row once`() = runTest {
        val dao = FakeDao()
        val parityDao = FakeParityDao(dao.messages)
        val store = store(dao, parityDao, FakeApi(emptyList()))
        dao.messages["m1"] = ChatMessageEntity("m1", "c1", "u2", null, "hi", null, createdAt = "t")

        store.applyReaction("m1", "❤️", "u3", added = true)
        store.applyReaction("m1", "❤️", "u3", added = true)
        val reactions = dao.messages.getValue("m1").reactionsJson!!
        assertThat(Regex("u3").findAll(reactions).count()).isEqualTo(1)

        store.applyReaction("m1", "❤️", "u3", added = false)
        assertThat(dao.messages.getValue("m1").reactionsJson).isNull()
    }
}

private fun unscriptedChat(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi

private fun unscriptedParity(): ChatParityApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatParityApi::class.java.classLoader,
    arrayOf(ChatParityApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatParityApi
