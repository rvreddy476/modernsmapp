package com.us.android.core.chat.hidden

import androidx.sqlite.db.SupportSQLiteQuery
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
import kotlinx.coroutines.flow.map
import kotlinx.serialization.json.Json
import javax.crypto.Cipher

/** A chat DAO over two maps, with live flows, for the hidden-chat list tests. */
internal class FakeChatDao : ChatDao {
    val rows = MutableStateFlow<Map<String, ChatConversationEntity>>(emptyMap())
    val messages = linkedMapOf<String, ChatMessageEntity>()

    override suspend fun upsertConversations(rows: List<ChatConversationEntity>) {
        this.rows.value = this.rows.value + rows.associateBy { it.id }
    }
    override fun conversations(): Flow<List<ChatConversationEntity>> = rows.map { m -> m.values.filter { !it.isRequest } }
    override fun requests(): Flow<List<ChatConversationEntity>> = rows.map { m -> m.values.filter { it.isRequest } }
    override fun unreadConversationCount(): Flow<Int> = rows.map { m -> m.values.count { it.hasUnread && !it.isRequest } }
    override suspend fun clearUnread(conversationId: String) = Unit
    override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
    override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
    override suspend fun rawCheckpoint(query: SupportSQLiteQuery): Int = 0
    override suspend fun deleteMessage(messageId: String) {
        messages.remove(messageId)
    }
    override suspend fun deleteConversation(conversationId: String) {
        rows.value = rows.value - conversationId
    }
    override suspend fun clearConversations() {
        rows.value = emptyMap()
    }
    override suspend fun insertMessages(rows: List<ChatMessageEntity>) {
        rows.forEach { messages.putIfAbsent(it.id, it) }
    }
    override suspend fun recentMessages(conversationId: String, limit: Int) =
        messages.values.filter { it.conversationId == conversationId }.take(limit)
    override suspend fun clearMessages() = messages.clear()
    override suspend fun enqueueSend(row: ChatPendingSendEntity) = Unit
    override suspend fun pendingSends(): List<ChatPendingSendEntity> = emptyList()
    override fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>> = MutableStateFlow(emptyList())
    override suspend fun recordAttempt(key: String) = Unit
    override suspend fun markFailed(key: String) = Unit
    override suspend fun retry(key: String) = Unit
    override suspend fun completeSend(key: String) = Unit
    override suspend fun clearPendingSends() = Unit
}

/** The inbox the fake server answers with. */
internal class InboxApi(var inbox: List<ConversationDto> = emptyList()) : ChatApi by unscriptedChatApi() {
    override suspend fun conversations(limit: Int, cursor: String?): ApiEnvelope<List<ConversationDto>> = ApiEnvelope(data = inbox)
    override suspend fun requests(): ApiEnvelope<List<ConversationDto>> = ApiEnvelope(data = emptyList())
}

internal object NoScrubOwed : ScrubRecoveryFlag {
    override fun isPending(): Boolean = false
    override fun setPending(pending: Boolean): Boolean = true
}

internal object NoScheduler : OutboxScheduler {
    override fun scheduleDrain() = Unit
    override fun cancelDrain() = Unit
}

internal fun testStore(dao: FakeChatDao, api: ChatApi, hidden: HiddenTokenStore?) = ChatStore(
    repository = ChatRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })),
    dao = dao,
    scheduler = NoScheduler,
    scrubRecovery = NoScrubOwed,
    hidden = hidden,
)

internal fun conversationRow(id: String, unread: Boolean = false, request: Boolean = false) = ChatConversationEntity(
    id = id,
    type = "direct",
    title = null,
    isRequest = request,
    membersJson = "[]",
    updatedAt = "2026-10-24T10:00:00Z",
    avatarMediaId = null,
    lastMessageAt = null,
    lastMessagePreview = "",
    lastMessageSender = null,
    hasUnread = unread,
    syncedAtMillis = 0L,
)

/** The vault without a Keystore: a map. Records every forget. */
internal class FakeVault : HiddenUnlockVault {
    val sealed = linkedMapOf<String, String>()
    val forgotten = mutableListOf<String?>()
    override fun has(userId: String) = userId in sealed
    override fun sealCipher(): Cipher? = null
    override fun seal(userId: String, cipher: Cipher, password: String): Boolean {
        sealed[userId] = password
        return true
    }
    override fun openCipher(userId: String): Cipher? = null
    override fun open(userId: String, cipher: Cipher): String? = sealed[userId]
    override fun forget(userId: String?) {
        forgotten += userId
        if (userId == null) sealed.clear() else sealed.remove(userId)
    }
}

internal fun unscriptedChatApi(): ChatApi = java.lang.reflect.Proxy.newProxyInstance(
    ChatApi::class.java.classLoader,
    arrayOf(ChatApi::class.java),
) { _, method, _ -> error("not scripted: ${method.name}") } as ChatApi
