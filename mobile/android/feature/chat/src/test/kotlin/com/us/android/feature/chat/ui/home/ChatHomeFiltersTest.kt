package com.us.android.feature.chat.ui.home

import com.google.common.truth.Truth.assertThat
import com.us.android.core.chat.data.Community
import com.us.android.core.chat.data.CommunitySuggestion
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.ConversationMember
import com.us.android.core.chat.data.PersonSuggestion
import org.junit.Test

/**
 * The search pill's rules, one per tab: chats by name or last message,
 * groups by title, communities by name or handle, suggestions by name; a
 * blank pill returns the tab's whole list; a voice answer fills the field.
 */
class ChatHomeFiltersTest {

    private val viewer = "me"

    private fun direct(id: String, other: String, preview: String = "") = Conversation(
        id = id,
        type = "direct",
        title = null,
        isRequest = false,
        members = listOf(ConversationMember(viewer, "member", "Me"), ConversationMember("u-$id", "member", other)),
        updatedAt = "",
        lastMessagePreview = preview,
    )

    private fun group(id: String, title: String) = Conversation(
        id = id,
        type = "group",
        title = title,
        isRequest = false,
        members = emptyList(),
        updatedAt = "",
    )

    private fun community(id: String, name: String, handle: String) = Community(
        id = id,
        ownerId = "o",
        handle = handle,
        name = name,
        description = "",
        avatarMediaId = null,
        visibility = "public",
        memberCount = 1,
        updateCount = 0,
        isVerified = false,
        viewerRole = "",
        viewerMuted = false,
        canPost = false,
    )

    private val rows = listOf(
        direct("1", "Alpha Btest", "see you at nine"),
        direct("2", "Bravo Btest", "photo"),
        group("3", "Weekend Riders"),
        group("4", "Book club"),
    )

    @Test
    fun `chats are the direct conversations only, in order, when the pill is blank`() {
        assertThat(ChatHomeFilters.directChats(rows, "", viewer).map { it.id }).containsExactly("1", "2").inOrder()
    }

    @Test
    fun `chats match the other person's name or the last message`() {
        assertThat(ChatHomeFilters.directChats(rows, "bravo", viewer).map { it.id }).containsExactly("2")
        assertThat(ChatHomeFilters.directChats(rows, "NINE", viewer).map { it.id }).containsExactly("1")
        assertThat(ChatHomeFilters.directChats(rows, "riders", viewer)).isEmpty()
    }

    @Test
    fun `groups are the group conversations only and match by title`() {
        assertThat(ChatHomeFilters.groups(rows, "").map { it.id }).containsExactly("3", "4").inOrder()
        assertThat(ChatHomeFilters.groups(rows, "rider").map { it.id }).containsExactly("3")
        assertThat(ChatHomeFilters.groups(rows, "alpha")).isEmpty()
    }

    @Test
    fun `communities match by name or handle, with or without the at sign`() {
        val list = listOf(community("a", "Weekend Riders", "riders"), community("b", "Book Club", "books"))
        assertThat(ChatHomeFilters.communities(list, "").map { it.id }).containsExactly("a", "b").inOrder()
        assertThat(ChatHomeFilters.communities(list, "@book").map { it.id }).containsExactly("b")
        assertThat(ChatHomeFilters.communities(list, "weekend").map { it.id }).containsExactly("a")
    }

    @Test
    fun `people match by name and suggested communities by name or handle`() {
        val people = listOf(
            PersonSuggestion("p1", "Charlie", 0.5, "", 0, emptyList()),
            PersonSuggestion("p2", "Dana", 0.4, "", 0, emptyList()),
        )
        assertThat(ChatHomeFilters.people(people, "dan").map { it.userId }).containsExactly("p2")
        assertThat(ChatHomeFilters.people(people, "").map { it.userId }).containsExactly("p1", "p2").inOrder()

        val communities = listOf(CommunitySuggestion("c1", "riders", "Weekend Riders", "", null, 3, "", emptyList()))
        assertThat(
            ChatHomeFilters.suggestedCommunities(communities, "@RIDERS").map { it.communityId }
        ).containsExactly("c1")
        assertThat(ChatHomeFilters.suggestedCommunities(communities, "books")).isEmpty()
    }

    @Test
    fun `a voice answer fills the field with its first hypothesis, trimmed`() {
        assertThat(ChatHomeFilters.applyVoiceResult("old", listOf("  weekend riders ", "weekend writers")))
            .isEqualTo("weekend riders")
    }

    @Test
    fun `an empty voice answer leaves the field as it was`() {
        assertThat(ChatHomeFilters.applyVoiceResult("old", emptyList())).isEqualTo("old")
        assertThat(ChatHomeFilters.applyVoiceResult("old", listOf("", "   "))).isEqualTo("old")
    }
}
