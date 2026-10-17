package com.us.android.feature.chat.ui.home

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.chat.data.DATING_SOURCE_APP
import com.us.android.core.chat.data.withoutDatingChats
import org.junit.Test

/**
 * Pulse match chats live in the dating feature, not Messages (founder,
 * 2026-10-18): the Chats and Groups lists, the requests door and the tabs'
 * unread dots all leave them out.
 */
class DatingChatsOutOfMessagesTest {

    private fun chat(
        id: String,
        type: String = "direct",
        sourceApp: String? = null,
        unread: Boolean = false,
        isRequest: Boolean = false,
    ) = Conversation(
        id = id,
        type = type,
        title = if (type == "group") "Group $id" else null,
        isRequest = isRequest,
        members = listOf(ConversationMember("me", "member", "Me"), ConversationMember("u-$id", "member", "Person $id")),
        updatedAt = "2026-10-18T10:00:00Z",
        hasUnread = unread,
        sourceApp = sourceApp,
    )

    @Test
    fun `the shared filter drops dating chats and keeps direct and group`() {
        val rows = listOf(
            chat("d1"),
            chat("g1", type = "group"),
            chat("p1", sourceApp = DATING_SOURCE_APP),
            chat("p2", sourceApp = " Dating "),
        )
        assertThat(rows.withoutDatingChats().map { it.id }).containsExactly("d1", "g1").inOrder()
    }

    @Test
    fun `another product's source is not mistaken for dating`() {
        assertThat(chat("x", sourceApp = "doorstep").isDatingChat).isFalse()
        assertThat(chat("x", sourceApp = null).isDatingChat).isFalse()
    }

    @Test
    fun `the Chats and Groups lists never show a match chat`() {
        val state = ChatHomeUiState(
            viewerId = "me",
            conversations = listOf(
                chat("d1"),
                chat("g1", type = "group"),
                chat("p1", sourceApp = DATING_SOURCE_APP),
                chat("pg", type = "group", sourceApp = DATING_SOURCE_APP),
            ),
        )
        assertThat(state.visibleChats.map { it.id }).containsExactly("d1")
        assertThat(state.visibleGroups.map { it.id }).containsExactly("g1")
    }

    @Test
    fun `an unread match chat lights no Messages dot`() {
        val onlyDating = ChatHomeUiState(
            conversations = listOf(
                chat("d1"),
                chat("p1", sourceApp = DATING_SOURCE_APP, unread = true),
                chat("pg", type = "group", sourceApp = DATING_SOURCE_APP, unread = true),
            ),
            requests = listOf(chat("pr", sourceApp = DATING_SOURCE_APP, isRequest = true)),
        )
        assertThat(onlyDating.unreadTabs).isEmpty()
        assertThat(onlyDating.requestCount).isEqualTo(0)

        val withSocial = onlyDating.copy(
            conversations = onlyDating.conversations + chat("d2", unread = true),
            requests = onlyDating.requests + chat("r1", isRequest = true),
        )
        assertThat(withSocial.unreadTabs).containsExactly(ChatHomeTab.Chats)
        assertThat(withSocial.requestCount).isEqualTo(1)
    }
}
