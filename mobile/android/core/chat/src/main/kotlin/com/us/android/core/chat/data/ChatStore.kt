package com.us.android.core.chat.data

import com.us.android.core.database.ChatConversationEntity
import com.us.android.core.database.ChatDao
import com.us.android.core.database.ChatMessageEntity
import com.us.android.core.database.ChatPendingSendEntity
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.map
import kotlinx.serialization.Serializable
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The durable chat boundary (directive §5.4): Room cache + send outbox over
 * the REST repository.
 *
 * ## WHAT IS DURABLE AND WHY
 *
 *  - the INBOX (conversations + requests) so it renders offline and the
 *    unread badge survives process death;
 *  - RECENT MESSAGES per thread, keyed by the server message id — the single
 *    row where HTTP, socket, push and retry copies collapse (CH-LB-4.5);
 *  - PENDING SENDS: a send is a Room row + a WorkManager job before it is a
 *    network call, so process death resumes the SAME idempotent intent
 *    (CH-LB-4.4).
 *
 * The REST list responses remain the source of truth; the cache is a
 * projection that any successful sync overwrites.
 */
/**
 * The scheduling seam: production wraps WorkManager (see ChatModule); tests
 * substitute a recorder. The outbox RULES (ordering, classification,
 * loop-until-empty) live in [ChatStore.drainOutbox] and are what the tests
 * pin — WorkManager itself is Android plumbing this boundary keeps off the
 * JVM test path.
 */
interface OutboxScheduler {
    fun scheduleDrain()
    fun cancelDrain()
}

@Singleton
class ChatStore @Inject constructor(
    private val repository: ChatRepository,
    private val dao: ChatDao,
    private val scheduler: OutboxScheduler,
) {

    private val json = Json { ignoreUnknownKeys = true }

    private val _reconnectPending = MutableStateFlow(false)

    /** True after a socket drop until the next successful [syncInbox]. */
    val reconnectPending: StateFlow<Boolean> = _reconnectPending.asStateFlow()

    // ── Inbox ───────────────────────────────────────────────────────────
    //
    // DOMAIN types out, entities in: the Room schema stays private to the
    // data boundary, so a feature module never couples to a column.

    fun conversationsFlow(): Flow<List<Conversation>> =
        dao.conversations().map { rows -> rows.map { it.toDomainConversation() } }

    fun requestsFlow(): Flow<List<Conversation>> =
        dao.requests().map { rows -> rows.map { it.toDomainConversation() } }

    fun unreadCount(): Flow<Int> = dao.unreadConversationCount()

    /**
     * Pulls the authoritative inbox + requests and replaces the cache rows.
     * Also the reconnect reconciliation (CH-LB-4.3): whatever the socket
     * missed is repaired here from durable history.
     */
    suspend fun syncInbox(): Boolean {
        val conversations = repository.conversations()
        val requests = repository.requests()
        val now = System.currentTimeMillis()
        var ok = false
        if (conversations is com.us.android.core.common.result.AppResult.Success) {
            dao.upsertConversations(conversations.data.map { it.toEntity(isRequestList = false, now) })
            ok = true
        }
        if (requests is com.us.android.core.common.result.AppResult.Success) {
            dao.upsertConversations(requests.data.map { it.toEntity(isRequestList = true, now) })
        }
        if (ok) _reconnectPending.value = false
        return ok
    }

    fun markReconnectPending() {
        _reconnectPending.value = true
    }

    /**
     * Writes the viewer's mute/pin switches to the server and mirrors the
     * confirmed values into the cache so the inbox reorders immediately.
     */
    suspend fun setConversationSettings(
        conversationId: String,
        settings: ConversationSettings,
    ): com.us.android.core.common.result.AppResult<ConversationSettings> {
        val result = repository.updateConversationSettings(conversationId, settings)
        if (result is com.us.android.core.common.result.AppResult.Success) {
            dao.updateSettingsFlags(
                conversationId = conversationId,
                pinned = result.data.isPinned,
                muted = result.data.isMuted,
            )
        }
        return result
    }

    /** Removes one message row from the local cache after a server delete. */
    suspend fun removeCachedMessage(messageId: String) = dao.deleteMessage(messageId)

    suspend fun clearUnread(conversationId: String) = dao.clearUnread(conversationId)

    // ── Messages ────────────────────────────────────────────────────────

    /** Caches a page of history; duplicates are ignored by primary key. */
    suspend fun cacheMessages(messages: List<Message>) {
        if (messages.isEmpty()) return
        dao.insertMessages(messages.filter { it.id.isNotBlank() }.map { it.toEntity() })
    }

    /** The offline fallback for an open thread. Newest first. */
    suspend fun recentCachedMessages(conversationId: String, limit: Int = 50): List<Message> =
        dao.recentMessages(conversationId, limit).map { it.toDomainMessage() }

    /**
     * Applies one realtime message durably: the message row, the inbox
     * preview and the unread flag — all BEFORE any screen sees the event, so
     * a message that arrives while the app sits on the feed still moves the
     * badge (CH-LB-4.6).
     */
    suspend fun applyRealtimeMessage(message: Message) {
        if (message.id.isBlank() || message.conversationId.isBlank()) return
        dao.insertMessages(listOf(message.toEntity()))
        dao.markUnread(
            conversationId = message.conversationId,
            preview = message.text.take(PREVIEW_LENGTH),
            at = message.createdAt,
        )
    }

    // ── The durable send outbox ─────────────────────────────────────────

    fun pendingSendsFor(conversationId: String): Flow<List<PendingSend>> =
        dao.pendingSendsFor(conversationId).map { rows ->
            rows.map {
                PendingSend(
                    idempotencyKey = it.idempotencyKey,
                    conversationId = it.conversationId,
                    text = it.text,
                    mediaId = it.mediaId,
                    createdAtMillis = it.createdAtMillis,
                    failed = it.failed,
                )
            }
        }

    /**
     * Enqueues a durable send: one Room row keyed by a FRESH idempotency key,
     * one unique WorkManager job that drains the outbox in order. Returns the
     * key so the optimistic UI row and the eventual server row can be tied
     * together.
     */
    suspend fun enqueueSend(conversationId: String, text: String, mediaId: String? = null): String {
        val key = ChatRepository.newIdempotencyKey()
        dao.enqueueSend(
            ChatPendingSendEntity(
                idempotencyKey = key,
                conversationId = conversationId,
                text = text,
                mediaId = mediaId,
                createdAtMillis = System.currentTimeMillis(),
                attempts = 0,
                failed = false,
            ),
        )
        scheduleDrain()
        return key
    }

    /** Puts a failed row back in the queue and kicks the worker. */
    suspend fun retrySend(idempotencyKey: String) {
        dao.retry(idempotencyKey)
        scheduleDrain()
    }

    suspend fun abandonSend(idempotencyKey: String) = dao.completeSend(idempotencyKey)

    /** Re-arms the outbox worker; also called from app start so sends queued
     *  before a process death resume without any user action. */
    fun scheduleDrain() = scheduler.scheduleDrain()

    /**
     * Drains the outbox. Called by [ChatSendWorker]. Returns false when a
     * RETRYABLE failure stopped a conversation's queue (the worker retries
     * with backoff); true when every row is delivered or parked.
     *
     * Three rules the final verification demanded of this loop:
     *
     *  - PER-CONVERSATION ORDER: a retryable failure of message N BLOCKS
     *    message N+1 in the same conversation — it is skipped this pass, so
     *    replies never overtake the message they answer. Other conversations
     *    keep draining.
     *  - PERMANENT FAILURES PARK IMMEDIATELY: a privacy denial, block,
     *    validation or auth failure will not change on retry — retrying it
     *    five times was noise. The row parks as failed for an explicit user
     *    decision, and the rest of its conversation may proceed past it (the
     *    user can see exactly which message did not send).
     *  - LOOP UNTIL EMPTY: rows enqueued while a pass was running used to be
     *    stranded until the next app start, because unique-KEEP scheduling
     *    discards the enqueue that raced the running worker. The worker now
     *    re-reads the queue after each pass and only stops when it is
     *    genuinely empty or blocked.
     */
    suspend fun drainOutbox(): Boolean {
        while (true) {
            val rows = dao.pendingSends()
            if (rows.isEmpty()) return true
            var progressed = false
            var retryableFailure = false
            val blockedConversations = mutableSetOf<String>()
            for (row in rows) {
                if (row.conversationId in blockedConversations) continue
                when (attemptSend(row)) {
                    SendAttempt.Delivered, SendAttempt.Parked -> progressed = true
                    SendAttempt.RetryLater -> {
                        retryableFailure = true
                        blockedConversations += row.conversationId
                    }
                }
            }
            if (retryableFailure) return false
            if (!progressed) return true
        }
    }

    private enum class SendAttempt { Delivered, Parked, RetryLater }

    private suspend fun attemptSend(row: ChatPendingSendEntity): SendAttempt {
        dao.recordAttempt(row.idempotencyKey)
        val mediaId = row.mediaId
        val result = if (mediaId != null) {
            repository.sendMedia(row.conversationId, mediaId, row.idempotencyKey, row.text)
        } else {
            repository.send(row.conversationId, row.text, row.idempotencyKey)
        }
        return when (result) {
            is com.us.android.core.common.result.AppResult.Success -> {
                cacheMessages(listOf(result.data))
                dao.completeSend(row.idempotencyKey)
                SendAttempt.Delivered
            }
            is com.us.android.core.common.result.AppResult.Failure -> {
                val permanent = !result.error.isRetryableSend()
                if (permanent || row.attempts + 1 >= MAX_SEND_ATTEMPTS) {
                    // Parked for an explicit user retry — never silently
                    // dropped, never retried forever.
                    dao.markFailed(row.idempotencyKey)
                    SendAttempt.Parked
                } else {
                    SendAttempt.RetryLater
                }
            }
        }
    }

    /** Logout/account switch: every cached row and queued send goes. */
    suspend fun wipeForLogout() {
        scheduler.cancelDrain()
        dao.wipeAll()
    }

    private fun Conversation.toEntity(isRequestList: Boolean, syncedAt: Long) =
        ChatConversationEntity(
            id = id,
            type = type,
            title = title,
            isRequest = isRequestList || isRequest,
            membersJson = json.encodeToString(
                ListSerializer(CachedMember.serializer()),
                members.map { CachedMember(it.userId, it.role, it.displayName) },
            ),
            updatedAt = updatedAt,
            avatarMediaId = avatarMediaId,
            lastMessageAt = lastMessageAt,
            lastMessagePreview = lastMessagePreview,
            lastMessageSender = lastMessageSender,
            hasUnread = hasUnread,
            syncedAtMillis = syncedAt,
            isPinned = isPinned,
            isMuted = isMuted,
        )

    private fun ChatConversationEntity.toDomainConversation() = Conversation(
        id = id,
        type = type,
        title = title,
        isRequest = isRequest,
        members = runCatching {
            json.decodeFromString(ListSerializer(CachedMember.serializer()), membersJson)
                .map { ConversationMember(it.userId, it.role, it.displayName) }
        }.getOrDefault(emptyList()),
        updatedAt = updatedAt,
        avatarMediaId = avatarMediaId,
        lastMessageAt = lastMessageAt,
        lastMessagePreview = lastMessagePreview,
        lastMessageSender = lastMessageSender,
        hasUnread = hasUnread,
        isPinned = isPinned,
        isMuted = isMuted,
    )

    private fun Message.toEntity() = ChatMessageEntity(
        id = id,
        conversationId = conversationId,
        senderId = senderId,
        senderDisplayName = senderDisplayName,
        text = text,
        mediaId = mediaId,
        createdAt = createdAt,
    )

    private fun ChatMessageEntity.toDomainMessage() = Message(
        id = id,
        conversationId = conversationId,
        senderId = senderId,
        senderDisplayName = senderDisplayName,
        text = text,
        mediaId = mediaId,
        createdAt = createdAt,
    )

    companion object {
        const val SEND_WORK_NAME = "chat-send-outbox"
        const val MAX_SEND_ATTEMPTS = 5
        private const val PREVIEW_LENGTH = 140
    }
}

@Serializable
internal data class CachedMember(
    val userId: String,
    val role: String,
    val displayName: String,
)

/**
 * Whether a failed send is worth an automatic retry.
 *
 * Retryable: the transport failed or the server was transiently unable
 * (network, timeout, 5xx, rate limit, unparseable response). NOT retryable:
 * the server UNDERSTOOD and REFUSED — privacy, block, auth, validation. The
 * refusal will not change on replay, and retrying a privacy denial five
 * times is how a client turns a policy answer into background noise.
 * Top-level so the outbox tests pin the classification table directly.
 */
fun com.us.android.core.common.error.AppError.isRetryableSend(): Boolean = when (this) {
    is com.us.android.core.common.error.AppError.NoNetwork,
    is com.us.android.core.common.error.AppError.Timeout,
    is com.us.android.core.common.error.AppError.RateLimited,
    is com.us.android.core.common.error.AppError.Malformed,
    -> true

    is com.us.android.core.common.error.AppError.Server -> statusCode >= HTTP_SERVER_ERROR

    is com.us.android.core.common.error.AppError.Unknown -> {
        val status = statusCode
        status == null || status >= HTTP_SERVER_ERROR
    }

    is com.us.android.core.common.error.AppError.InvalidRequest,
    is com.us.android.core.common.error.AppError.AuthFailed,
    is com.us.android.core.common.error.AppError.Forbidden,
    is com.us.android.core.common.error.AppError.NotFound,
    -> false
}

private const val HTTP_SERVER_ERROR = 500

/** One queued (or parked-failed) send, as the UI renders it. */
data class PendingSend(
    val idempotencyKey: String,
    val conversationId: String,
    val text: String,
    val mediaId: String?,
    val createdAtMillis: Long,
    val failed: Boolean,
)
