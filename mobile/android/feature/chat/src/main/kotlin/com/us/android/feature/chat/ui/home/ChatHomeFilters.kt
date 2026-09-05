package com.us.android.feature.chat.ui.home

import com.us.android.core.chat.data.Community
import com.us.android.core.chat.data.CommunitySuggestion
import com.us.android.core.chat.data.Conversation
import com.us.android.core.chat.data.PersonSuggestion

/**
 * The search pill's rules, one per tab, pure so they are pinned by a test:
 *
 *  - Chats: direct conversations, matched by name or last message;
 *  - Groups: group conversations, matched by title;
 *  - Communities: matched by name or handle (the Discover section also asks
 *    the server with the same text);
 *  - Suggestions: people by name, communities by name or handle.
 *
 * A blank query returns the tab's whole list, untouched and in order.
 */
internal object ChatHomeFilters {

    const val GROUP_TYPE = "group"

    fun directChats(conversations: List<Conversation>, query: String, viewerId: String): List<Conversation> {
        val direct = conversations.filter { it.type != GROUP_TYPE }
        val needle = query.trim()
        if (needle.isBlank()) return direct
        return direct.filter { conversation ->
            conversation.displayTitle(viewerId).contains(needle, ignoreCase = true) ||
                conversation.lastMessagePreview.contains(needle, ignoreCase = true)
        }
    }

    fun groups(conversations: List<Conversation>, query: String): List<Conversation> {
        val groups = conversations.filter { it.type == GROUP_TYPE }
        val needle = query.trim()
        if (needle.isBlank()) return groups
        return groups.filter { it.title.orEmpty().contains(needle, ignoreCase = true) }
    }

    fun communities(communities: List<Community>, query: String): List<Community> {
        val needle = query.trim().removePrefix("@")
        if (needle.isBlank()) return communities
        return communities.filter {
            it.name.contains(needle, ignoreCase = true) || it.handle.contains(needle, ignoreCase = true)
        }
    }

    fun people(people: List<PersonSuggestion>, query: String): List<PersonSuggestion> {
        val needle = query.trim()
        if (needle.isBlank()) return people
        return people.filter { it.displayName.contains(needle, ignoreCase = true) }
    }

    fun suggestedCommunities(
        communities: List<CommunitySuggestion>,
        query: String,
    ): List<CommunitySuggestion> {
        val needle = query.trim().removePrefix("@")
        if (needle.isBlank()) return communities
        return communities.filter {
            it.name.contains(needle, ignoreCase = true) || it.handle.contains(needle, ignoreCase = true)
        }
    }

    /**
     * What the pill shows after the recogniser answers: the first non-blank
     * hypothesis, trimmed; an empty answer leaves the field as it was rather
     * than wiping what the user typed.
     */
    fun applyVoiceResult(current: String, results: List<String>): String =
        results.firstOrNull { it.isNotBlank() }?.trim() ?: current
}
