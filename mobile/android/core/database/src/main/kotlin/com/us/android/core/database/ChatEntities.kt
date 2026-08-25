package com.us.android.core.database

import androidx.room.Dao
import androidx.room.Entity
import androidx.room.Index
import androidx.room.Insert
import androidx.room.OnConflictStrategy
import androidx.room.Query
import kotlinx.coroutines.flow.Flow

/**
 * Production chat pass — the durable chat boundary (directive §5.4).
 *
 * Three tables, three jobs:
 *
 *  - [ChatConversationEntity] caches the inbox so it renders offline and
 *    keeps unread state across process death;
 *  - [ChatMessageEntity] caches recent thread history so an open thread
 *    renders offline; the PRIMARY KEY on the server message id is the
 *    de-duplication point where HTTP, socket, push and retry copies collapse
 *    to one row (CH-LB-4.5);
 *  - [ChatPendingSendEntity] is the durable OUTBOX: a send survives process
 *    death as a row + a WorkManager job, and its idempotency key rides with
 *    it so the retry replays the SAME intent (CH-LB-4.4).
 *
 * Plaintext note: message text lives here in the app's private storage, like
 * every other cached API response. When E2EE lands these columns hold
 * ciphertext and decryption moves behind the chat-unlock boundary.
 */
@Entity(tableName = "chat_conversation")
data class ChatConversationEntity(
    @androidx.room.PrimaryKey val id: String,
    val type: String,
    val title: String?,
    val isRequest: Boolean,
    val membersJson: String,
    val updatedAt: String,
    val avatarMediaId: String?,
    val lastMessageAt: String?,
    val lastMessagePreview: String,
    val lastMessageSender: String?,
    val hasUnread: Boolean,
    val syncedAtMillis: Long,
    /** The viewer's pin/mute switches, mirrored from the server (v5). */
    @androidx.room.ColumnInfo(defaultValue = "0") val isPinned: Boolean = false,
    @androidx.room.ColumnInfo(defaultValue = "0") val isMuted: Boolean = false,
)

@Entity(
    tableName = "chat_message",
    indices = [Index("conversationId", "createdAt")],
)
data class ChatMessageEntity(
    /** The SERVER's msg_id — the global de-duplication key. */
    @androidx.room.PrimaryKey val id: String,
    val conversationId: String,
    val senderId: String,
    val senderDisplayName: String?,
    val text: String,
    val mediaId: String?,
    val createdAt: String,
)

@Entity(tableName = "chat_pending_send")
data class ChatPendingSendEntity(
    /** The idempotency key IS the identity: one row, one send intent. */
    @androidx.room.PrimaryKey val idempotencyKey: String,
    val conversationId: String,
    val text: String,
    val mediaId: String?,
    val createdAtMillis: Long,
    val attempts: Int,
    /** Set when retries exhausted; the row waits for an explicit user retry. */
    val failed: Boolean,
)

@Dao
@Suppress("TooManyFunctions") // One DAO per durable chat boundary; splitting hides the transaction surface.
interface ChatDao {

    // ── Conversations ───────────────────────────────────────────────────

    @Insert(onConflict = OnConflictStrategy.REPLACE)
    suspend fun upsertConversations(rows: List<ChatConversationEntity>)

    /** Pinned rows first — the whole point of a pin — then by recency. */
    @Query("SELECT * FROM chat_conversation WHERE isRequest = 0 ORDER BY isPinned DESC, updatedAt DESC")
    fun conversations(): Flow<List<ChatConversationEntity>>

    @Query("SELECT * FROM chat_conversation WHERE isRequest = 1 ORDER BY updatedAt DESC")
    fun requests(): Flow<List<ChatConversationEntity>>

    @Query("SELECT COUNT(*) FROM chat_conversation WHERE hasUnread = 1 AND isRequest = 0")
    fun unreadConversationCount(): Flow<Int>

    @Query("UPDATE chat_conversation SET hasUnread = 0 WHERE id = :conversationId")
    suspend fun clearUnread(conversationId: String)

    @Query(
        "UPDATE chat_conversation SET hasUnread = 1, lastMessagePreview = :preview, " +
            "lastMessageAt = :at, updatedAt = :at WHERE id = :conversationId",
    )
    suspend fun markUnread(conversationId: String, preview: String, at: String)

    @Query("UPDATE chat_conversation SET isPinned = :pinned, isMuted = :muted WHERE id = :conversationId")
    suspend fun updateSettingsFlags(conversationId: String, pinned: Boolean, muted: Boolean)

    @Query("DELETE FROM chat_message WHERE id = :messageId")
    suspend fun deleteMessage(messageId: String)

    @Query("DELETE FROM chat_conversation WHERE id = :conversationId")
    suspend fun deleteConversation(conversationId: String)

    @Query("DELETE FROM chat_conversation")
    suspend fun clearConversations()

    // ── Messages ────────────────────────────────────────────────────────

    /** IGNORE, not REPLACE: the first durable copy of a message wins, and a
     *  replayed socket/push/history duplicate is a no-op. */
    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun insertMessages(rows: List<ChatMessageEntity>)

    @Query("SELECT * FROM chat_message WHERE conversationId = :conversationId ORDER BY createdAt DESC LIMIT :limit")
    suspend fun recentMessages(conversationId: String, limit: Int): List<ChatMessageEntity>

    @Query("DELETE FROM chat_message")
    suspend fun clearMessages()

    // ── Pending sends (the durable outbox) ──────────────────────────────

    @Insert(onConflict = OnConflictStrategy.IGNORE)
    suspend fun enqueueSend(row: ChatPendingSendEntity)

    @Query("SELECT * FROM chat_pending_send WHERE failed = 0 ORDER BY createdAtMillis ASC")
    suspend fun pendingSends(): List<ChatPendingSendEntity>

    @Query("SELECT * FROM chat_pending_send WHERE conversationId = :conversationId ORDER BY createdAtMillis ASC")
    fun pendingSendsFor(conversationId: String): Flow<List<ChatPendingSendEntity>>

    @Query("UPDATE chat_pending_send SET attempts = attempts + 1 WHERE idempotencyKey = :key")
    suspend fun recordAttempt(key: String)

    @Query("UPDATE chat_pending_send SET failed = 1 WHERE idempotencyKey = :key")
    suspend fun markFailed(key: String)

    @Query("UPDATE chat_pending_send SET failed = 0 WHERE idempotencyKey = :key")
    suspend fun retry(key: String)

    @Query("DELETE FROM chat_pending_send WHERE idempotencyKey = :key")
    suspend fun completeSend(key: String)

    @Query("DELETE FROM chat_pending_send")
    suspend fun clearPendingSends()

    /** Account switch/logout: every cached chat row for the old account goes. */
    @androidx.room.Transaction
    suspend fun wipeAll() {
        clearPendingSends()
        clearMessages()
        clearConversations()
    }
}
