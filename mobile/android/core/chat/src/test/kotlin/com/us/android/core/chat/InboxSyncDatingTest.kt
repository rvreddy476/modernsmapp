package com.us.android.core.chat

import androidx.sqlite.db.SupportSQLiteQuery
import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ScrubRecoveryFlag
import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.test.runTest
import kotlinx.serialization.json.Json
import org.junit.Test

/**
 * Pulse match chats stay out of the Messages cache (founder, 2026-10-18).
 *
 * The cache has no source column, so every Messages list and unread dot
 * built on it is dating-free only because the sync keeps those rows out —
 * and removes one an older build already cached.
 */
class InboxSyncDatingTest {

    private class FakeDao : ChatDao {
        val conversations = linkedMapOf<String, ChatConversationEntity>()

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
        override suspend fun deleteMessage(messageId: String) = Unit
        override suspend fun deleteConversation(conversationId: String) {
            conversations.remove(conversationId)
        }
        override suspend fun clearConversations() = conversations.clear()
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

    private class FakeApi(
        private val inbox: List<ConversationDto>,
        private val pending: List<ConversationDto>,
    ) : ChatApi by unscripted() {
        override suspend fun conversations(limit: Int, cursor: String?): ApiEnvelope<List<ConversationDto>> =
            ApiEnvelope(data = inbox)

        override suspend fun requests(): ApiEnvelope<List<ConversationDto>> = ApiEnvelope(data = pending)
    }

    private object NoScrubOwed : ScrubRecoveryFlag {
        override fun isPending(): Boolean = false
        override fun setPending(pending: Boolean): Boolean = true
    }

    private object NoScheduler : OutboxScheduler {
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
    }

    private fun dto(id: String, type: String = "direct", sourceApp: String? = null, unread: Boolean = false) =
        ConversationDto(
            id = id,
            type = type,
            updatedAt = "2026-10-18T10:00:00Z",
            hasUnread = unread,
            sourceApp = sourceApp,
        )

    private fun store(dao: FakeDao, api: FakeApi) = ChatStore(
        repository = ChatRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })),
        dao = dao,
        scheduler = NoScheduler,
        scrubRecovery = NoScrubOwed,
    )

    @Test
    fun `a sync caches direct and group chats but never a match chat`() = runTest {
        val dao = FakeDao()
        val api = FakeApi(
            inbox = listOf(
                dto("d1"),
                dto("g1", type = "group"),
                dto("p1", sourceApp = "dating", unread = true),
            ),
            pending = listOf(dto("r1"), dto("pr", sourceApp = "dating")),
        )

        assertThat(store(dao, api).syncInbox()).isTrue()

        assertThat(dao.conversations.keys).containsExactly("d1", "g1", "r1")
    }

    @Test
    fun `a request I sent stays a chat and only incoming requests are cached as requests`() = runTest {
        val dao = FakeDao()
        val api = FakeApi(
            inbox = listOf(dto("d1"), dto("mine").copy(isRequest = true)),
            pending = listOf(dto("theirs")),
        )

        assertThat(store(dao, api).syncInbox()).isTrue()

        assertThat(dao.conversations.getValue("mine").isRequest).isFalse()
        assertThat(dao.conversations.getValue("d1").isRequest).isFalse()
        assertThat(dao.conversations.getValue("theirs").isRequest).isTrue()
    }

    @Test
    fun `a match chat an older build cached is removed on the next sync`() = runTest {
        val dao = FakeDao()
        dao.conversations["p1"] = ChatConversationEntity(
            id = "p1",
            type = "direct",
            title = null,
            isRequest = false,
            membersJson = "[]",
            updatedAt = "2026-10-01T10:00:00Z",
            avatarMediaId = null,
            lastMessageAt = null,
            lastMessagePreview = "hey",
            lastMessageSender = null,
            hasUnread = true,
            syncedAtMillis = 1L,
        )
        val api = FakeApi(inbox = listOf(dto("d1"), dto("p1", sourceApp = "dating", unread = true)), pending = emptyList())

        assertThat(store(dao, api).syncInbox()).isTrue()

        assertThat(dao.conversations.keys).containsExactly("d1")
    }
}

private fun unscripted(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
