package com.us.android.core.chat.hidden

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.ChatApi
import com.us.android.core.chat.data.ChatRepository
import com.us.android.core.chat.data.ChatStore
import com.us.android.core.chat.data.ConversationDto
import com.us.android.core.chat.data.HiddenOutboxStatus
import com.us.android.core.chat.data.MessageDto
import com.us.android.core.chat.data.OutboxScheduler
import com.us.android.core.chat.data.ReplyRef
import com.us.android.core.chat.data.SendMessageRequest
import com.us.android.core.chat.data.TypedSend
import com.us.android.core.chat.data.hiddenOutboxFailedNote
import com.us.android.core.chat.data.hiddenOutboxWaitingNote
import com.us.android.core.chat.data.isSealed
import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatPendingSendEntity
import com.us.android.core.network.ApiEnvelope
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
 * Hidden chats × the durable outbox (24 Oct 2026): a hidden chat's queued
 * send is sealed on disk, waits (unattempted) while the chats are locked,
 * goes once they are opened, and leaves with a reset, a delete or sign-out.
 * Normal chats' rows are exactly as before.
 */
class HiddenOutboxTest {

    // ── fakes ───────────────────────────────────────────────────────────

    /** The outbox and message tables over maps, with a live per-chat flow. */
    private class OutboxDao : ChatDao {
        val sends = MutableStateFlow<Map<String, ChatPendingSendEntity>>(emptyMap())
        val messages = mutableListOf<ChatMessageEntity>()
        var checkpoints = 0

        fun row(key: String) = sends.value[key]
        fun put(row: ChatPendingSendEntity) {
            sends.value = sends.value + (row.idempotencyKey to row)
        }

        override suspend fun upsertConversations(rows: List<ChatConversationEntity>) = Unit
        override fun conversations(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun requests(): Flow<List<ChatConversationEntity>> = MutableStateFlow(emptyList())
        override fun unreadConversationCount(): Flow<Int> = MutableStateFlow(0)
        override suspend fun clearUnread(conversationId: String) = Unit
        override suspend fun markUnread(conversationId: String, preview: String, at: String) = Unit
        override suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean) = Unit
        override suspend fun rawCheckpoint(query: androidx.sqlite.db.SupportSQLiteQuery): Int {
            checkpoints++
            return 0
        }
        override suspend fun deleteMessage(messageId: String) = Unit
        override suspend fun deleteConversation(conversationId: String) = Unit
        override suspend fun clearConversations() = Unit
        override suspend fun insertMessages(rows: List<ChatMessageEntity>) {
            rows.forEach { row -> if (messages.none { it.id == row.id }) messages += row }
        }
        override suspend fun recentMessages(conversationId: String, limit: Int) = emptyList<ChatMessageEntity>()
        override suspend fun clearMessages() = messages.clear()
        override suspend fun enqueueSend(row: ChatPendingSendEntity) {
            if (row.idempotencyKey !in sends.value) put(row)
        }
        override suspend fun pendingSends(): List<ChatPendingSendEntity> =
            sends.value.values.filter { !it.failed }.sortedBy { it.createdAtMillis }
        override fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>> =
            sends.map { m -> m.values.filter { it.conversationId == conversationId }.sortedBy { it.createdAtMillis } }
        override suspend fun allPendingSends(): List<ChatPendingSendEntity> = sends.value.values.sortedBy { it.createdAtMillis }
        override suspend fun updatePendingSend(row: ChatPendingSendEntity) {
            if (row.idempotencyKey in sends.value) put(row)
        }
        override suspend fun recordAttempt(key: String) {
            row(key)?.let { put(it.copy(attempts = it.attempts + 1)) }
        }
        override suspend fun markFailed(key: String) {
            row(key)?.let { put(it.copy(failed = true)) }
        }
        override suspend fun retry(key: String) {
            row(key)?.let { put(it.copy(failed = false)) }
        }
        override suspend fun completeSend(key: String) {
            sends.value = sends.value - key
        }
        override suspend fun clearPendingSends() {
            sends.value = emptyMap()
        }
    }

    /**
     * The chat server: a hidden chat's send without a live open token is 403
     * CHAT_LOCKED (and, like the headers interceptor, the id is learned).
     */
    private class ServerApi(
        private val tokens: HiddenTokenStore,
        private val hiddenOnServer: Set<String>,
    ) : ChatApi by unscriptedChatApi() {
        val sent = mutableListOf<Pair<String, SendMessageRequest>>()
        val refusedLocked = mutableListOf<String>()

        override suspend fun send(
            conversationId: String,
            idempotencyKey: String,
            body: SendMessageRequest,
        ): ApiEnvelope<MessageDto> {
            val headerWouldRide = tokens.isLocked(conversationId) && tokens.open() != null
            if (conversationId in hiddenOnServer && !headerWouldRide) {
                refusedLocked += idempotencyKey
                tokens.markLocked(listOf(conversationId))
                throw HttpException(
                    Response.error<Any>(
                        403,
                        """{"error":{"code":"CHAT_LOCKED","message":"locked"}}""".toResponseBody("application/json".toMediaType()),
                    ),
                )
            }
            sent += idempotencyKey to body
            return ApiEnvelope(
                data = MessageDto(
                    msgId = "srv-$idempotencyKey",
                    conversationId = conversationId,
                    senderId = "me",
                    text = body.text,
                    createdAt = "2026-10-24T10:00:00Z",
                ),
            )
        }

        var inbox: List<ConversationDto> = emptyList()
        override suspend fun conversations(limit: Int, cursor: String?): ApiEnvelope<List<ConversationDto>> = ApiEnvelope(data = inbox)
        override suspend fun requests(): ApiEnvelope<List<ConversationDto>> = ApiEnvelope(data = emptyList())
    }

    /** Records the foreground drains the store asks for, so a test can run them. */
    private class Scheduler : OutboxScheduler {
        val drains = mutableListOf<suspend () -> Unit>()
        override fun scheduleDrain() = Unit
        override fun cancelDrain() = Unit
        override fun drainNow(drain: suspend () -> Unit) {
            drains += drain
        }
    }

    private val keys = SoftwareOutboxKeys()
    private val cipher = AesGcmOutboxCipher(keys)
    private val tokens = HiddenTokenStore(now = { 1_000L }, timerScope = null)
    private val dao = OutboxDao()
    private val scheduler = Scheduler()
    private val api = ServerApi(tokens, hiddenOnServer = setOf(HIDDEN))

    private fun store(withCipher: Boolean = true) = ChatStore(
        repository = ChatRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })),
        dao = dao,
        scheduler = scheduler,
        scrubRecovery = NoScrubOwed,
        hidden = tokens,
        outboxCipher = if (withCipher) cipher else null,
    )

    private fun openChats() = tokens.setOpen("op-1", "2099-01-01T00:00:00Z")

    private fun plainRow(key: String, conversationId: String, text: String, at: Long = 1) = ChatPendingSendEntity(
        idempotencyKey = key,
        conversationId = conversationId,
        text = text,
        mediaId = "media-$key",
        replyToId = "orig-1",
        replyToPreview = "quoted $text",
        replyToSenderId = "peer",
        createdAtMillis = at,
        attempts = 0,
        failed = false,
    )

    /** Everything Room would hold for the row, as one string. */
    private fun stored(row: ChatPendingSendEntity?) = row.toString()

    // ── the cipher ──────────────────────────────────────────────────────

    @Test
    fun `the cipher round-trips, uses a fresh iv, and refuses another row's aad or a changed byte`() {
        val plain = "meet me at 9".toByteArray()
        val a = cipher.seal(plain, "aad-1".toByteArray())!!
        val b = cipher.seal(plain, "aad-1".toByteArray())!!

        assertThat(String(cipher.open(a, "aad-1".toByteArray())!!)).isEqualTo("meet me at 9")
        assertThat(a).isNotEqualTo(b)
        assertThat(cipher.open(a, "aad-2".toByteArray())).isNull()
        val raw = java.util.Base64.getDecoder().decode(a)
        raw[raw.size - 1] = (raw[raw.size - 1].toInt() xor 1).toByte()
        assertThat(cipher.open(java.util.Base64.getEncoder().encodeToString(raw), "aad-1".toByteArray())).isNull()

        cipher.destroyKey()
        assertThat(cipher.open(b, "aad-1".toByteArray())).isNull()
    }

    // ── encrypt on write ────────────────────────────────────────────────

    @Test
    fun `a hidden chat's send is stored sealed and no column holds the plain text`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        openChats()
        val s = store()

        val key = s.enqueueSend(HIDDEN, "the secret plan", mediaId = "m-77", replyTo = ReplyRef("orig-9", "quoted secret", "peer"))!!
        val typedKey = s.enqueueTypedSend(
            HIDDEN,
            TypedSend(type = "sticker", stickerId = "st-secret", stickerUrl = "https://cdn.example/secret-sticker.png"),
        )!!

        val row = dao.row(key)!!
        assertThat(row.isSealed()).isTrue()
        assertThat(row.text).isEmpty()
        assertThat(row.mediaId).isNull()
        assertThat(row.replyToPreview).isNull()
        assertThat(row.type).isNull()
        listOf("the secret plan", "secret", "m-77", "orig-9", "quoted").forEach { assertThat(stored(row)).doesNotContain(it) }
        val typed = dao.row(typedKey)!!
        assertThat(typed.isSealed()).isTrue()
        listOf("st-secret", "secret-sticker", "sticker").forEach { assertThat(stored(typed)).doesNotContain(it) }
    }

    @Test
    fun `without a cipher a hidden chat's send is refused, never queued in plain text`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        openChats()

        assertThat(store(withCipher = false).enqueueSend(HIDDEN, "the secret plan")).isNull()
        assertThat(dao.sends.value).isEmpty()
    }

    @Test
    fun `a sealing failure refuses the write too`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        val broken = object : HiddenOutboxCipher {
            override fun seal(plain: ByteArray, aad: ByteArray): String? = null
            override fun open(sealed: String, aad: ByteArray): ByteArray? = null
            override fun destroyKey() = Unit
        }
        val s = ChatStore(
            repository = ChatRepository(api, ErrorMapper(Json { ignoreUnknownKeys = true })),
            dao = dao,
            scheduler = scheduler,
            scrubRecovery = NoScrubOwed,
            hidden = tokens,
            outboxCipher = broken,
        )

        assertThat(s.enqueueSend(HIDDEN, "the secret plan")).isNull()
        assertThat(dao.sends.value).isEmpty()
    }

    // ── normal chats unchanged ──────────────────────────────────────────

    @Test
    fun `a normal chat's row is stored exactly as before and sends while hidden chats are locked`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        val s = store()

        val key = s.enqueueSend(NORMAL, "hello there", replyTo = ReplyRef("orig-1", "hi", "peer"))!!
        val row = dao.row(key)!!
        assertThat(row.isSealed()).isFalse()
        assertThat(row.text).isEqualTo("hello there")
        assertThat(row.replyToPreview).isEqualTo("hi")
        assertThat(row.extrasJson).isNull()
        assertThat(s.pendingSendsFor(NORMAL).first().map { it.text }).containsExactly("hello there")
        assertThat(s.hiddenOutboxFor(NORMAL).first()).isEqualTo(HiddenOutboxStatus())

        assertThat(s.drainOutbox()).isTrue()
        assertThat(api.sent.map { it.second.text }).containsExactly("hello there")
        assertThat(dao.sends.value).isEmpty()
    }

    @Test
    fun `a normal chat's text that looks like the envelope is still a plain row`() = runTest {
        val s = store()
        val key = s.enqueueSend(NORMAL, """{"hidden_outbox_v1":"AAAA"}""")!!
        assertThat(dao.row(key)!!.isSealed()).isFalse()
        s.drainOutbox()
        assertThat(api.sent.map { it.second.text }).containsExactly("""{"hidden_outbox_v1":"AAAA"}""")
    }

    // ── skip while locked, resume after open ─────────────────────────────

    @Test
    fun `while locked a hidden row is not attempted at all, and other chats still drain`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        openChats()
        val s = store()
        val hiddenKey = s.enqueueSend(HIDDEN, "the secret plan")!!
        tokens.drop(HiddenDropReason.Background) // re-locked before it went
        s.enqueueSend(NORMAL, "hello")

        assertThat(s.drainOutbox()).isTrue() // nothing for the worker to retry
        assertThat(api.refusedLocked).isEmpty() // no pointless 403s
        assertThat(api.sent.map { it.second.text }).containsExactly("hello")
        val waiting = dao.row(hiddenKey)!!
        assertThat(waiting.attempts).isEqualTo(0)
        assertThat(waiting.failed).isFalse()
        assertThat(waiting.isSealed()).isTrue()
        // Never drawn as a bubble; the thread gets a count.
        assertThat(s.pendingSendsFor(HIDDEN).first()).isEmpty()
        assertThat(s.hiddenOutboxFor(HIDDEN).first()).isEqualTo(HiddenOutboxStatus(waiting = 1))
    }

    @Test
    fun `a locked hidden row holds back the later rows of its own chat`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        val s = store()
        dao.put(plainRow("h-1", HIDDEN, "first", at = 1))
        dao.put(plainRow("h-2", HIDDEN, "second", at = 2))

        s.drainOutbox()

        assertThat(api.sent).isEmpty()
        assertThat(dao.sends.value.values.map { it.attempts }).containsExactly(0, 0)
    }

    @Test
    fun `after the chats are opened the waiting rows send, decrypted, in order`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        openChats()
        val s = store()
        s.enqueueSend(HIDDEN, "first secret")
        s.enqueueSend(HIDDEN, "second secret", replyTo = ReplyRef("orig-1", "quoted", "peer"))
        tokens.drop(HiddenDropReason.Idle)
        s.drainOutbox()
        assertThat(api.sent).isEmpty()
        scheduler.drains.clear()

        openChats()
        s.resumeHiddenSends()
        assertThat(scheduler.drains).isNotEmpty()
        scheduler.drains.forEach { it() }

        assertThat(api.sent.map { it.second.text }).containsExactly("first secret", "second secret").inOrder()
        assertThat(api.sent[1].second.replyToId).isEqualTo("orig-1")
        assertThat(api.refusedLocked).isEmpty()
        assertThat(dao.sends.value).isEmpty()
        assertThat(s.hiddenOutboxFor(HIDDEN).first()).isEqualTo(HiddenOutboxStatus())
    }

    @Test
    fun `locked while on the wire - a 403 CHAT_LOCKED keeps the row queued, not failed`() = runTest {
        // Open, but this process does not know the chat is hidden yet, so the
        // open token does not ride: the server says CHAT_LOCKED.
        openChats()
        val s = store()
        dao.put(plainRow("legacy", HIDDEN, "queued before the fix"))

        s.drainOutbox()

        val row = dao.row("legacy")!!
        assertThat(api.refusedLocked).containsExactly("legacy")
        assertThat(row.failed).isFalse()
        // Learned hidden on the refusal: sealed now, and waiting.
        assertThat(row.isSealed()).isTrue()
        assertThat(stored(row)).doesNotContain("queued before the fix")
        // Next drain, open and known hidden: it goes, as it was typed.
        s.drainOutbox()
        assertThat(api.sent.map { it.second.text }).containsExactly("queued before the fix")
    }

    @Test
    fun `a sealed row whose key is gone parks instead of sending`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        openChats()
        val s = store()
        val key = s.enqueueSend(HIDDEN, "the secret plan")!!
        keys.destroy()

        s.drainOutbox()

        assertThat(api.sent).isEmpty()
        assertThat(dao.row(key)!!.failed).isTrue()
        assertThat(s.hiddenOutboxFor(HIDDEN).first()).isEqualTo(HiddenOutboxStatus(failed = 1))
    }

    // ── migration of existing plain rows ─────────────────────────────────

    @Test
    fun `existing plain rows of a hidden chat are sealed on the next drain and the disk is scrubbed`() = runTest {
        dao.put(plainRow("old-h", HIDDEN, "legacy secret"))
        dao.put(plainRow("old-n", NORMAL, "legacy normal", at = 2))
        tokens.markLocked(listOf(HIDDEN))

        store().drainOutbox()

        val sealed = dao.row("old-h")!!
        assertThat(sealed.isSealed()).isTrue()
        listOf("legacy secret", "media-old-h", "orig-1").forEach { assertThat(stored(sealed)).doesNotContain(it) }
        assertThat(dao.checkpoints).isGreaterThan(0) // VACUUM + checkpoint after the rewrite
        // The normal row went out untouched.
        assertThat(api.sent.map { it.second.text }).containsExactly("legacy normal")
    }

    @Test
    fun `the inbox sync that learns a chat is hidden seals its plain rows, failed ones too`() = runTest {
        dao.put(plainRow("old-h", HIDDEN, "legacy secret").copy(failed = true))
        dao.put(plainRow("old-n", NORMAL, "legacy normal", at = 2).copy(failed = true))
        api.inbox = listOf(ConversationDto(id = HIDDEN, type = "direct", locked = true), ConversationDto(id = NORMAL, type = "direct"))

        store().syncInbox()

        assertThat(dao.row("old-h")!!.isSealed()).isTrue()
        assertThat(stored(dao.row("old-h"))).doesNotContain("legacy secret")
        assertThat(dao.row("old-n")!!.text).isEqualTo("legacy normal")
        assertThat(dao.row("old-n")!!.isSealed()).isFalse()
    }

    @Test
    fun `a re-lock purge seals a chat that just became hidden with sends queued`() = runTest {
        dao.put(plainRow("q", HIDDEN, "typed before Hide"))
        tokens.markLocked(listOf(HIDDEN))

        store().purgeHiddenLocally(listOf(HIDDEN))

        assertThat(dao.row("q")!!.isSealed()).isTrue()
        assertThat(stored(dao.row("q"))).doesNotContain("typed before Hide")
    }

    // ── wipes ───────────────────────────────────────────────────────────

    @Test
    fun `a hidden-chats reset drops every hidden row and the key, normal rows stay`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        openChats()
        val s = store()
        s.enqueueSend(HIDDEN, "secret one")
        dao.put(plainRow("plain-h", HIDDEN, "legacy", at = 5)) // not yet sealed
        val normal = s.enqueueSend(NORMAL, "hello")!!
        tokens.forgetAll() // a process restart: the in-memory hidden set is empty
        tokens.markLocked(listOf(HIDDEN))

        s.dropHiddenOutbox()

        assertThat(dao.sends.value.keys).containsExactly(normal)
        assertThat(keys.destroyed).isEqualTo(1)
    }

    @Test
    fun `a sealed row is dropped on reset even when this process has not learned its chat is hidden`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        openChats()
        val s = store()
        s.enqueueSend(HIDDEN, "secret one")
        tokens.forgetAll()

        s.dropHiddenOutbox()

        assertThat(dao.sends.value).isEmpty()
    }

    @Test
    fun `delete after unhide drops that chat's sealed rows, a normal chat's delete keeps its queue`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        openChats()
        val s = store()
        s.enqueueSend(HIDDEN, "secret one")
        val normal = s.enqueueSend(NORMAL, "hello")!!
        tokens.unmarkLocked(HIDDEN) // Unhide

        s.removeConversationLocally(HIDDEN)
        s.removeConversationLocally(NORMAL)

        assertThat(dao.sends.value.keys).containsExactly(normal)
    }

    @Test
    fun `sign-out wipes every row and destroys the outbox key`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        openChats()
        val s = store()
        s.enqueueSend(HIDDEN, "secret one")
        s.enqueueSend(NORMAL, "hello")

        s.wipeForLogout()

        assertThat(dao.sends.value).isEmpty()
        assertThat(keys.destroyed).isEqualTo(1)
    }

    // ── the note ────────────────────────────────────────────────────────

    @Test
    fun `the thread note counts, never quotes`() {
        assertThat(hiddenOutboxWaitingNote(HiddenOutboxStatus(waiting = 1))).isEqualTo("1 message waiting — sending now")
        assertThat(hiddenOutboxWaitingNote(HiddenOutboxStatus(waiting = 3))).isEqualTo("3 messages waiting — sending now")
        assertThat(hiddenOutboxWaitingNote(HiddenOutboxStatus())).isNull()
        assertThat(hiddenOutboxFailedNote(HiddenOutboxStatus(failed = 2))).isEqualTo("2 messages not sent")
        assertThat(hiddenOutboxFailedNote(HiddenOutboxStatus(waiting = 2))).isNull()
    }

    @Test
    fun `retry and discard on the note touch only the hidden chat's parked rows`() = runTest {
        tokens.markLocked(listOf(HIDDEN))
        val s = store()
        dao.put(plainRow("h", HIDDEN, "x").copy(failed = true))
        dao.put(plainRow("n", NORMAL, "y", at = 2).copy(failed = true))
        s.purgeHiddenLocally(listOf(HIDDEN)) // seals "h"

        s.retryHiddenFailed(HIDDEN)
        assertThat(dao.row("h")!!.failed).isFalse()
        assertThat(dao.row("n")!!.failed).isTrue()

        dao.put(dao.row("h")!!.copy(failed = true))
        s.discardHiddenFailed(HIDDEN)
        assertThat(dao.sends.value.keys).containsExactly("n")
    }

    private companion object {
        const val HIDDEN = "h1"
        const val NORMAL = "n1"
    }
}
