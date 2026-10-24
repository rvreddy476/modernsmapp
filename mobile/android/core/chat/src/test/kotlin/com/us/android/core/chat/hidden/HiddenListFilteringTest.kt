package com.us.android.core.chat.hidden

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationDto
import kotlinx.coroutines.flow.first
import kotlinx.coroutines.test.runTest
import org.junit.Test

/**
 * A hidden chat is invisible everywhere normal: the inbox, the requests,
 * the unread count, and the cache itself — whether the row says `locked`
 * or this process learned it some other way.
 */
class HiddenListFilteringTest {

    private val tokens = HiddenTokenStore(now = { 0L }, timerScope = null)

    private fun conversation(id: String, locked: Boolean = false) =
        Conversation(id = id, type = "direct", title = null, isRequest = false, members = emptyList(), updatedAt = "", locked = locked)

    @Test
    fun `the normal-list rule leaves out locked rows and known hidden ids`() {
        val rows = listOf(conversation("a"), conversation("b", locked = true), conversation("c"))
        assertThat(rows.withoutHiddenChats(setOf("c")).map { it.id }).containsExactly("a")
        assertThat(rows.withoutHiddenChats(emptySet()).map { it.id }).containsExactly("a", "c")
    }

    @Test
    fun `the inbox flow, the requests flow and the unread count leave a hidden chat out`() = runTest {
        val dao = FakeChatDao()
        dao.upsertConversations(
            listOf(
                conversationRow("a", unread = true),
                conversationRow("h", unread = true),
                conversationRow("r", request = true),
                conversationRow("hr", request = true),
            ),
        )
        val store = testStore(dao, InboxApi(), tokens)
        assertThat(store.unreadCount().first()).isEqualTo(2)

        tokens.markLocked(listOf("h", "hr"))

        assertThat(store.conversationsFlow().first().map { it.id }).containsExactly("a")
        assertThat(store.requestsFlow().first().map { it.id }).containsExactly("r")
        assertThat(store.unreadCount().first()).isEqualTo(1)
    }

    @Test
    fun `a sync never caches a locked row and removes an older copy`() = runTest {
        val dao = FakeChatDao()
        dao.upsertConversations(listOf(conversationRow("h")))
        val api = InboxApi(
            inbox = listOf(
                ConversationDto(id = "a", type = "direct", updatedAt = "2026-10-24T10:00:00Z"),
                ConversationDto(id = "h", type = "direct", updatedAt = "2026-10-24T10:00:00Z", locked = true),
            ),
        )

        assertThat(testStore(dao, api, tokens).syncInbox()).isTrue()

        assertThat(dao.rows.value.keys).containsExactly("a")
        // And the process now knows it is hidden.
        assertThat(tokens.isLocked("h")).isTrue()
    }

    @Test
    fun `a sync also keeps out a chat this process knows is hidden`() = runTest {
        val dao = FakeChatDao()
        tokens.markLocked(listOf("h"))
        val api = InboxApi(
            inbox = listOf(
                ConversationDto(id = "a", type = "direct", updatedAt = "2026-10-24T10:00:00Z"),
                ConversationDto(id = "h", type = "direct", updatedAt = "2026-10-24T10:00:00Z"),
            ),
        )

        testStore(dao, api, tokens).syncInbox()

        assertThat(dao.rows.value.keys).containsExactly("a")
    }

    @Test
    fun `purging a hidden chat takes its row out of the cache`() = runTest {
        val dao = FakeChatDao()
        dao.upsertConversations(listOf(conversationRow("a"), conversationRow("h")))
        testStore(dao, InboxApi(), tokens).purgeHiddenLocally(listOf("h"))
        assertThat(dao.rows.value.keys).containsExactly("a")
    }

    @Test
    fun `a realtime message in an open hidden chat moves no inbox row`() = runTest {
        val dao = FakeChatDao()
        tokens.markLocked(listOf("h"))
        val store = testStore(dao, InboxApi(), tokens)
        store.applyRealtimeMessage(
            com.us.android.core.chat.data.Message(
                id = "m1",
                conversationId = "h",
                senderId = "peer",
                senderDisplayName = null,
                text = "hi",
                createdAt = "2026-10-24T10:00:00Z",
            ),
        )
        assertThat(dao.rows.value).isEmpty()
        assertThat(dao.messages.keys).containsExactly("m1")
    }

    @Test
    fun `without a token store nothing is hidden — the old behaviour`() = runTest {
        val dao = FakeChatDao()
        dao.upsertConversations(listOf(conversationRow("a"), conversationRow("h")))
        assertThat(testStore(dao, InboxApi(), hidden = null).conversationsFlow().first().map { it.id })
            .containsExactly("a", "h")
    }
}
