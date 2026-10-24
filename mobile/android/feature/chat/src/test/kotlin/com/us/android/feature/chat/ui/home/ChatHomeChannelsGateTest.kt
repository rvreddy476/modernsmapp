package com.us.android.feature.chat.ui.home

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.CommunitySuggestion
import org.junit.Test

/**
 * Founder decision C (24 Oct 2026): Communities ride on broadcast channels,
 * so while `GET /v1/features` has them closed (or there is no answer at all)
 * the tab and the communities to join are gone. Groups are message-service
 * group chats, not the parked product, and always show.
 */
class ChatHomeChannelsGateTest {

    private val community = CommunitySuggestion(
        communityId = "c1",
        handle = "makers",
        name = "Makers",
        description = "",
        avatarMediaId = null,
        memberCount = 12,
        explainText = "",
        reasonCodes = emptyList(),
    )

    @Test
    fun `closed, Messages shows Chats, Groups and Suggestions`() {
        assertThat(chatHomeTabs(channelsOpen = false))
            .containsExactly(ChatHomeTab.Chats, ChatHomeTab.Groups, ChatHomeTab.Suggestions).inOrder()
    }

    @Test
    fun `open, the Communities tab is back in its place`() {
        assertThat(chatHomeTabs(channelsOpen = true)).containsExactly(
            ChatHomeTab.Chats, ChatHomeTab.Groups, ChatHomeTab.Communities, ChatHomeTab.Suggestions,
        ).inOrder()
    }

    @Test
    fun `the screen starts closed, with no communities to join`() {
        val state = ChatHomeUiState(suggestedCommunities = listOf(community))

        assertThat(state.channelsOpen).isFalse()
        assertThat(state.tabs).doesNotContain(ChatHomeTab.Communities)
        assertThat(state.visibleSuggestedCommunities).isEmpty()
    }

    @Test
    fun `open, the communities to join show again`() {
        val state = ChatHomeUiState(suggestedCommunities = listOf(community), channelsOpen = true)

        assertThat(state.tabs).contains(ChatHomeTab.Communities)
        assertThat(state.visibleSuggestedCommunities).containsExactly(community)
    }
}
