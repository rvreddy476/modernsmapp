package com.us.android.core.database

import androidx.room.Dao
import androidx.room.Query

/**
 * Chat parity (schema v9, 2026-10-18): the reads and writes over the new
 * chat columns.
 *
 * Its own DAO rather than more members on [ChatDao]: several tests
 * implement [ChatDao] member by member, and an abstract member there would
 * break each of them for queries they never run.
 */
@Dao
@Suppress("TooManyFunctions") // One query per inbox/thread action.
interface ChatParityDao {

    @Query("SELECT * FROM chat_message WHERE id = :messageId")
    suspend fun message(messageId: String): ChatMessageEntity?

    /**
     * Refreshes the server-owned parts of an already-cached row. The insert
     * is IGNORE (first copy wins), so without this a reaction or an unsend
     * seen in a later history page would never reach the cache.
     */
    @Query("UPDATE chat_message SET type = :type, reactionsJson = :reactionsJson, extrasJson = :extrasJson WHERE id = :messageId")
    suspend fun refreshMessage(messageId: String, type: String?, reactionsJson: String?, extrasJson: String?)

    @Query("UPDATE chat_message SET reactionsJson = :reactionsJson WHERE id = :messageId")
    suspend fun updateReactions(messageId: String, reactionsJson: String?)

    /** Clear chat / delete for me: my copy of the history goes. */
    @Query("DELETE FROM chat_message WHERE conversationId = :conversationId")
    suspend fun clearConversationMessages(conversationId: String)

    @Query("UPDATE chat_conversation SET hasUnread = 0, unreadCount = 0 WHERE id = :conversationId")
    suspend fun markRead(conversationId: String)

    /** Mark unread from the inbox: the dot comes back; the count stays the server's. */
    @Query("UPDATE chat_conversation SET hasUnread = 1 WHERE id = :conversationId")
    suspend fun markUnreadOnly(conversationId: String)

    @Query("UPDATE chat_conversation SET unreadCount = unreadCount + 1 WHERE id = :conversationId")
    suspend fun incrementUnread(conversationId: String)

    @Query(
        "UPDATE chat_conversation SET isPinned = :pinned, isMuted = :muted, muteCalls = :muteCalls, " +
            "alertTone = :alertTone WHERE id = :conversationId",
    )
    suspend fun updatePrefs(conversationId: String, pinned: Boolean, muted: Boolean, muteCalls: Boolean, alertTone: String?)

    @Query("SELECT alertTone FROM chat_conversation WHERE id = :conversationId")
    suspend fun alertTone(conversationId: String): String?

    /**
     * Drops inbox rows the server no longer lists (archived, hidden for me)
     * after a COMPLETE primary sync. Requests are not touched — they come
     * from their own list.
     */
    @Query("DELETE FROM chat_conversation WHERE isRequest = 0 AND id NOT IN (:keep)")
    suspend fun pruneInbox(keep: List<String>)

    @Query("DELETE FROM chat_conversation WHERE id = :conversationId")
    suspend fun deleteConversation(conversationId: String)
}
