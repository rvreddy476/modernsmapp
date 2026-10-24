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
import kotlinx.coroutines.flow.first
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
        override fun conversations(): Flow<List<ChatConversationEntity>> = kotlinx.coroutines.flow.flow { emit(conversations.values.toList()) }
        override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
        override suspend fun clearUnread(conversationId: String) = Unit
        val unreadMarks = mutableListOf<Triple<String, String, String>>()
        override suspend fun markUnread(conversationId: String, preview: String, at: String) {
            unreadMarks += Triple(conversationId, preview, at)
        }
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
        override suspend fun allPendingSends(): List<ChatPendingSendEntity> = pending.values.toList()
        override suspend fun updatePendingSend(row: ChatPendingSendEntity) {
            if (row.idempotencyKey in pending) pending[row.idempotencyKey] = row
        }
    }

    private class FakeParityDao(
        private val messages: MutableMap<String, ChatMessageEntity>,
        private val conversations: MutableMap<String, ChatConversationEntity> = linkedMapOf(),
    ) : ChatParityDao {
        val prunes = mutableListOf<List<String>>()
        val marks = mutableListOf<String>()
        override suspend fun message(messageId: String) = messages[messageId]
        override suspend fun refreshMessage(messageId: String, type: String?, reactionsJson: String?, extrasJson: String?) {
            messages[messageId]?.let {
                messages[messageId] = it.copy(type = type, reactionsJson = reactionsJson, extrasJson = extrasJson)
            }
        }
        override suspend fun updateReactions(messageId: String, reactionsJson: String?) {
            messages[messageId]?.let { messages[messageId] = it.copy(reactionsJson = reactionsJson) }
        }
        override suspend fun clearConversationMessages(conversationId: String) = Unit
        override suspend fun markRead(conversationId: String) {
            marks += "read:$conversationId"
        }
        override suspend fun markUnreadOnly(conversationId: String) {
            marks += "unread:$conversationId"
        }
        val increments = mutableListOf<String>()
        val previews = mutableListOf<Triple<String, String, String>>()
        override suspend fun incrementUnread(conversationId: String) {
            increments += conversationId
        }
        override suspend fun updatePreview(conversationId: String, preview: String, at: String) {
            previews += Triple(conversationId, preview, at)
            conversations[conversationId]?.let {
                conversations[conversationId] = it.copy(lastMessagePreview = preview, lastMessageAt = at, updatedAt = at)
            }
        }
        override suspend fun conversation(conversationId: String) = conversations[conversationId]
        override suspend fun updateLastMessage(conversationId: String, senderId: String?, extrasJson: String?) {
            conversations[conversationId]?.let {
                conversations[conversationId] = it.copy(lastMessageSender = senderId, extrasJson = extrasJson)
            }
        }
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

    // ── a delivered send is cached WITH its server address ─────────────

    /** The text send endpoint, answering with the full server address. */
    private class AddressedSendApi : ChatApi by unscriptedChat() {
        override suspend fun send(conversationId: String, idempotencyKey: String, body: SendMessageRequest) =
            ApiEnvelope(
                data = MessageDto(
                    msgId = "srv-1",
                    conversationId = conversationId,
                    senderId = "me",
                    text = body.text,
                    bucket = "202610",
                    ts = "2026-10-18T10:00:00.123Z",
                    createdAt = "2026-10-18T10:00:00.123Z",
                ),
            )
    }

    @Test
    fun `a delivered text send is cached with its bucket and ts`() = runTest {
        val dao = FakeDao()
        val store = store(dao, FakeParityDao(dao.messages), AddressedSendApi())
        store.enqueueSend("c1", "hello")
        assertThat(store.drainOutbox()).isTrue()

        val cached = store.recentCachedMessages("c1").single()
        assertThat(cached.id).isEqualTo("srv-1")
        assertThat(cached.bucket).isEqualTo("202610")
        assertThat(cached.ts).isEqualTo("2026-10-18T10:00:00.123Z")
        assertThat(cached.addressable).isTrue()
    }

    @Test
    fun `a cached history page keeps each row's address for the offline tail`() = runTest {
        val dao = FakeDao()
        val store = store(dao, FakeParityDao(dao.messages), FakeApi(emptyList()))
        store.cacheMessages(
            listOf(
                com.us.android.core.chat.data.Message(
                    "m1", "c1", "u2", null, "hi", "2026-10-18T10:00:00Z",
                    bucket = "202610", ts = "2026-10-18T10:00:00Z",
                ),
            ),
        )
        val cached = store.recentCachedMessages("c1").single()
        assertThat(cached.bucket).isEqualTo("202610")
        assertThat(cached.ts).isEqualTo("2026-10-18T10:00:00Z")
    }

    @Test
    fun `a socket echo cached first is given the address when the send lands`() = runTest {
        val dao = FakeDao()
        val store = store(dao, FakeParityDao(dao.messages), AddressedSendApi())
        store.enqueueSend("c1", "hello")
        // The server's echo of the send beats the HTTP response; its frame has no address.
        store.applyRealtimeMessage(
            com.us.android.core.chat.data.Message("srv-1", "c1", "me", null, "hello", "2026-10-18T10:00:00.123Z"),
        )
        assertThat(store.recentCachedMessages("c1").single().addressable).isFalse()

        assertThat(store.drainOutbox()).isTrue()
        val cached = store.recentCachedMessages("c1")
        assertThat(cached).hasSize(1)
        assertThat(cached.single().addressable).isTrue()
        assertThat(cached.single().text).isEqualTo("hello")
    }

    @Test
    fun `without the parity seam a typed row parks instead of going out as text`() = runTest {
        val dao = FakeDao()
        val bare = ChatStore(ChatRepository(FakeApi(emptyList()), ErrorMapper(json)), dao, NoScheduler, NoScrubOwed)
        val key = bare.enqueueTypedSend("c1", TypedSend.sticker(com.us.android.core.chat.data.StickerInfo("s", "u", 1, 1)))!!
        bare.drainOutbox()
        assertThat(dao.pending.getValue(key).failed).isTrue()
    }

    // ── fix pass 2: mark read / unread through the server ───────────────

    /** Records `PUT .../unread`; answers per [unreadOk]. */
    private class MarkParityApi(private val unreadOk: Boolean = true) :
        ChatParityApi by unscriptedParity() {
        val unreadCalls = mutableListOf<Pair<String, Boolean>>()
        override suspend fun setUnread(conversationId: String, body: com.us.android.core.chat.data.UnreadRequest) {
            unreadCalls += conversationId to body.unread
            if (!unreadOk) throw java.io.IOException("offline")
        }
    }

    /** History of one message (or none) and a recorded `POST .../read`. */
    private class ReadApi(private val newest: String?, private val readOk: Boolean = true) : ChatApi by unscriptedChat() {
        val reads = mutableListOf<String>()
        override suspend fun messages(conversationId: String, limit: Int, cursor: String?): ApiEnvelope<List<MessageDto>> =
            ApiEnvelope(data = listOfNotNull(newest?.let { MessageDto(msgId = it, conversationId = conversationId, senderId = "u2") }))
        override suspend fun markRead(
            conversationId: String,
            body: com.us.android.core.chat.data.MarkReadRequest,
        ): ApiEnvelope<com.us.android.core.chat.data.StatusDto> {
            reads += body.messageId
            if (!readOk) throw java.io.IOException("offline")
            return ApiEnvelope(data = com.us.android.core.chat.data.StatusDto("ok"))
        }
    }

    private fun conversation(id: String, markedUnread: Boolean = false) = com.us.android.core.chat.data.Conversation(
        id = id, type = "direct", title = null, isRequest = false, members = emptyList(), updatedAt = "t",
        hasUnread = true, markedUnread = markedUnread,
    )

    @Test
    fun `mark unread writes the server mark, and a refusal puts the row back`() = runTest {
        val dao = FakeDao()
        val parityDao = FakeParityDao(dao.messages)
        val ok = MarkParityApi()
        assertThat(store(dao, parityDao, FakeApi(emptyList()), ok).markConversationUnread("c1"))
            .isInstanceOf(com.us.android.core.common.result.AppResult.Success::class.java)
        assertThat(ok.unreadCalls).containsExactly("c1" to true)
        assertThat(parityDao.marks).containsExactly("unread:c1")

        val refused = FakeParityDao(dao.messages)
        val offline = MarkParityApi(unreadOk = false)
        assertThat(store(dao, refused, FakeApi(emptyList()), offline).markConversationUnread("c1"))
            .isInstanceOf(com.us.android.core.common.result.AppResult.Failure::class.java)
        // No device-only mark survives a failed write.
        assertThat(refused.marks).containsExactly("unread:c1", "read:c1").inOrder()
    }

    @Test
    fun `mark read moves the cursor and clears a server mark`() = runTest {
        val dao = FakeDao()
        val parityDao = FakeParityDao(dao.messages)
        val parityApi = MarkParityApi()
        val api = ReadApi(newest = "m9")

        store(dao, parityDao, api, parityApi).markConversationRead(conversation("c1", markedUnread = true))

        assertThat(api.reads).containsExactly("m9")
        assertThat(parityApi.unreadCalls).containsExactly("c1" to false)
        assertThat(parityDao.marks).containsExactly("read:c1")
    }

    @Test
    fun `mark read on an empty marked chat clears the mark alone, and both failing restores the dot`() = runTest {
        val dao = FakeDao()
        val parityDao = FakeParityDao(dao.messages)
        val parityApi = MarkParityApi()
        store(dao, parityDao, ReadApi(newest = null), parityApi).markConversationRead(conversation("c1", markedUnread = true))
        assertThat(parityApi.unreadCalls).containsExactly("c1" to false)

        val failing = FakeParityDao(dao.messages)
        val result = store(dao, failing, ReadApi(newest = "m1", readOk = false), MarkParityApi(unreadOk = false))
            .markConversationRead(conversation("c2"))
        assertThat(result).isInstanceOf(com.us.android.core.common.result.AppResult.Failure::class.java)
        assertThat(failing.marks).containsExactly("read:c2", "unread:c2").inOrder()
    }

    // ── my own echo never marks a chat unread ──────────────────────────

    private fun signedInAs(userId: String) = javax.inject.Provider<com.us.android.core.auth.SessionStateProvider> {
        object : com.us.android.core.auth.SessionStateProvider {
            override val sessionState: kotlinx.coroutines.flow.StateFlow<com.us.android.core.model.SessionState> =
                MutableStateFlow(com.us.android.core.model.SessionState.Authenticated(userId = userId, sessionId = "s"))
        }
    }

    private fun sessionStore(dao: FakeDao, parityDao: FakeParityDao, api: ChatApi = FakeApi(emptyList())) = ChatStore(
        repository = ChatRepository(api, ErrorMapper(json)),
        dao = dao,
        scheduler = NoScheduler,
        scrubRecovery = NoScrubOwed,
        parityDao = parityDao,
        sessionState = signedInAs("me"),
    )

    private fun realtime(id: String, sender: String) =
        com.us.android.core.chat.data.Message(id, "c1", sender, null, "on my way", "2026-10-18T10:00:00Z")

    @Test
    fun `my own message echoed from another device moves the preview but never marks unread`() = runTest {
        val dao = FakeDao()
        val parityDao = FakeParityDao(dao.messages, dao.conversations)
        dao.conversations["c1"] = row("c1")
        sessionStore(dao, parityDao).applyRealtimeMessage(realtime("m1", sender = "me"))

        assertThat(dao.unreadMarks).isEmpty()
        assertThat(parityDao.increments).isEmpty()
        assertThat(parityDao.previews).containsExactly(Triple("c1", "on my way", "2026-10-18T10:00:00Z"))
        assertThat(dao.messages.keys).containsExactly("m1")
    }

    @Test
    fun `a message from someone else marks the chat unread and counts it`() = runTest {
        val dao = FakeDao()
        val parityDao = FakeParityDao(dao.messages)
        sessionStore(dao, parityDao).applyRealtimeMessage(realtime("m2", sender = "u2"))

        assertThat(dao.unreadMarks).containsExactly(Triple("c1", "on my way", "2026-10-18T10:00:00Z"))
        assertThat(parityDao.increments).containsExactly("c1")
        assertThat(parityDao.previews).isEmpty()
    }

    // ── the inbox row shows my newest message at once ──────────────────

    /** The emulator's row: my old message, already read by the peer an hour ago. */
    private val seenAnHourAgo = com.us.android.core.chat.data.CachedLastMessage(
        type = "text", preview = "old", senderId = "me", at = "2026-10-18T09:00:00Z", ownStatus = "read",
    )

    private fun row(
        id: String,
        last: com.us.android.core.chat.data.CachedLastMessage? = null,
        type: String = "direct",
    ) = ChatConversationEntity(
        id = id, type = type, title = null, isRequest = false, membersJson = "[]",
        updatedAt = "2026-10-18T09:00:00Z", avatarMediaId = null, lastMessageAt = "2026-10-18T09:00:00Z",
        lastMessagePreview = last?.preview.orEmpty(), lastMessageSender = last?.senderId, hasUnread = false,
        syncedAtMillis = 0,
        extrasJson = last?.let {
            json.encodeToString(
                com.us.android.core.chat.data.CachedConversationExtras.serializer(),
                com.us.android.core.chat.data.CachedConversationExtras(lastMessage = it),
            )
        },
    )

    private suspend fun ChatStore.inboxRow(id: String) = conversationsFlow().first().single { it.id == id }

    private fun stickerEcho(at: String = "2026-10-18T10:00:00.5Z") = com.us.android.core.chat.data.Message(
        "m-sticker", "c1", "me", null, "", at,
        type = "gif",
        gif = com.us.android.core.chat.data.GifInfo("giphy", "g1", "https://media/g1.gif", null, 200, 200, null, isSticker = true),
    )

    @Test
    fun `my own sticker echo makes the row say Sent, Sticker and now`() = runTest {
        val dao = FakeDao()
        dao.conversations["c1"] = row("c1", seenAnHourAgo)
        val store = sessionStore(dao, FakeParityDao(dao.messages, dao.conversations))

        store.applyRealtimeMessage(stickerEcho())

        val row = store.inboxRow("c1")
        assertThat(row.lastMessage?.type).isEqualTo("gif")
        assertThat(row.lastMessage?.preview).isEqualTo("Sticker")
        assertThat(row.lastMessage?.at).isEqualTo("2026-10-18T10:00:00.5Z")
        assertThat(row.lastMessagePreview).isEqualTo("Sticker")
        assertThat(com.us.android.core.chat.data.ownStatusLabel(row.lastMessage, "me")).isEqualTo("Sent")
        assertThat(row.hasUnread).isFalse()
    }

    @Test
    fun `a confirmed send moves the row even when no echo arrives`() = runTest {
        val dao = FakeDao()
        dao.conversations["c1"] = row("c1", seenAnHourAgo)
        val store = sessionStore(dao, FakeParityDao(dao.messages, dao.conversations), AddressedSendApi())

        store.enqueueSend("c1", "hello")
        assertThat(store.drainOutbox()).isTrue()

        val row = store.inboxRow("c1")
        assertThat(row.lastMessage?.preview).isEqualTo("hello")
        assertThat(row.lastMessage?.at).isEqualTo("2026-10-18T10:00:00.123Z")
        assertThat(row.lastMessagePreview).isEqualTo("hello")
        assertThat(com.us.android.core.chat.data.ownStatusLabel(row.lastMessage, "me")).isEqualTo("Sent")
    }

    @Test
    fun `an older message never moves the row back`() = runTest {
        val dao = FakeDao()
        val newer = seenAnHourAgo.copy(at = "2026-10-18T11:00:00Z", preview = "newest")
        dao.conversations["c1"] = row("c1", newer)
        val store = sessionStore(dao, FakeParityDao(dao.messages, dao.conversations))

        // Earlier as an instant, though it sorts LATER as text ('.' < 'Z').
        store.applyRealtimeMessage(stickerEcho(at = "2026-10-18T10:59:59.9Z"))

        val row = store.inboxRow("c1")
        assertThat(row.lastMessage?.preview).isEqualTo("newest")
        assertThat(row.lastMessagePreview).isEqualTo("newest")
    }

    @Test
    fun `the peer's message becomes the row's last message with no Sent line`() = runTest {
        val dao = FakeDao()
        dao.conversations["c1"] = row("c1", seenAnHourAgo)
        val store = sessionStore(dao, FakeParityDao(dao.messages, dao.conversations))

        store.applyRealtimeMessage(realtime("m2", sender = "u2"))

        val row = store.inboxRow("c1")
        assertThat(row.lastMessage?.senderId).isEqualTo("u2")
        assertThat(row.lastMessage?.at).isEqualTo("2026-10-18T10:00:00Z")
        assertThat(com.us.android.core.chat.data.ownStatusLabel(row.lastMessage, "me")).isNull()
    }

    @Test
    fun `the peer's read receipt turns Sent into Seen`() = runTest {
        val dao = FakeDao()
        dao.conversations["c1"] = row("c1", seenAnHourAgo)
        val store = sessionStore(dao, FakeParityDao(dao.messages, dao.conversations))
        store.applyRealtimeMessage(stickerEcho())

        store.applyReadReceipt("c1", readerId = "u2", messageId = "m-sticker")

        val row = store.inboxRow("c1")
        assertThat(com.us.android.core.chat.data.ownStatusLabel(row.lastMessage, "me")).isEqualTo("Seen")
        // The same message landing again (its HTTP copy) keeps the Seen.
        store.applyRealtimeMessage(stickerEcho())
        assertThat(com.us.android.core.chat.data.ownStatusLabel(store.inboxRow("c1").lastMessage, "me")).isEqualTo("Seen")
    }

    @Test
    fun `the session applies a read_receipt frame to the cached row`() = kotlinx.coroutines.runBlocking {
        val dao = FakeDao()
        dao.conversations["c1"] = row("c1", seenAnHourAgo)
        val store = sessionStore(dao, FakeParityDao(dao.messages, dao.conversations))
        store.applyRealtimeMessage(stickerEcho())
        val socket = object : com.us.android.core.chat.data.ChatSocket(okhttp3.OkHttpClient(), "ws://unused") {
            override fun connect(tokenProvider: () -> String?) = kotlinx.coroutines.flow.flowOf(
                com.us.android.core.chat.data.ChatSocketEvent.ReadReceipt("c1", "u2", "m-sticker"),
                com.us.android.core.chat.data.ChatSocketEvent.Disconnected(permanent = true),
            )
            override fun send(frame: String): Boolean = false
        }
        val session = com.us.android.core.chat.data.ChatSessionManager(
            socket = socket,
            store = store,
            tokenProvider = object : com.us.android.core.network.TokenProvider {
                override fun currentAccessToken(): String? = "t"
            },
            repository = ChatRepository(FakeApi(emptyList()), ErrorMapper(json)),
        )
        session.start()
        kotlinx.coroutines.withTimeout(10_000) {
            while (com.us.android.core.chat.data.ownStatusLabel(store.inboxRow("c1").lastMessage, "me") != "Seen") {
                kotlinx.coroutines.delay(10)
            }
        }
        session.stopAndJoin()
    }

    @Test
    fun `a receipt for an older message, from me, for an unknown message or in a group changes nothing`() = runTest {
        val dao = FakeDao()
        dao.conversations["c1"] = row("c1", seenAnHourAgo)
        dao.conversations["g1"] = row("g1", seenAnHourAgo, type = "group")
        val store = sessionStore(dao, FakeParityDao(dao.messages, dao.conversations))
        dao.messages["m-old"] = ChatMessageEntity("m-old", "c1", "me", null, "older", null, createdAt = "2026-10-18T09:30:00Z")
        store.applyRealtimeMessage(stickerEcho())
        store.applyRealtimeMessage(stickerEcho().copy(id = "g-sticker", conversationId = "g1"))

        store.applyReadReceipt("c1", readerId = "u2", messageId = "m-old")
        store.applyReadReceipt("c1", readerId = "me", messageId = "m-sticker")
        store.applyReadReceipt("c1", readerId = "u2", messageId = "never-cached")
        store.applyReadReceipt("g1", readerId = "u2", messageId = "g-sticker")

        assertThat(com.us.android.core.chat.data.ownStatusLabel(store.inboxRow("c1").lastMessage, "me")).isEqualTo("Sent")
        assertThat(com.us.android.core.chat.data.ownStatusLabel(store.inboxRow("g1").lastMessage, "me")).isEqualTo("Sent")
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
