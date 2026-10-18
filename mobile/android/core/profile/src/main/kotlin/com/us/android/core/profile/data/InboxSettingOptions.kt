package com.us.android.core.profile.data

/**
 * The value sets of Settings › Inbox & messaging (chat parity, 2026-10-18),
 * as identity user-service defines them (B3), with our words for each.
 *
 * The server may CLAMP a value (strict or minor accounts), so a screen must
 * show what the server returned, even a value missing from these lists —
 * [labelOf] then answers with the raw value's own words rather than
 * pretending a listed option is selected.
 */
object InboxSettingOptions {

    data class Option(val value: String, val label: String)

    val whoCanMessage = listOf(
        Option("everyone_message_requests", "Everyone (others as requests)"),
        Option("followers_message_requests", "Followers (others as requests)"),
        Option("friends_of_friends_requests", "Friends of friends, as requests"),
        Option("connections_and_mutual_followers", "Connections and mutual followers"),
        Option("connections_only", "Connections only"),
        Option("no_one", "No one"),
    )

    val whoCanAddToGroups = listOf(
        Option("everyone_with_approval", "Everyone, with my approval"),
        Option("friends_of_friends_invites", "Friends of friends, as invites"),
        Option("connections_and_contacts", "Connections and contacts"),
        Option("connections_only", "Connections only"),
        Option("no_one", "No one"),
    )

    val whoCanCall = listOf(
        Option("accepted_chats_only", "People I've accepted chats with"),
        Option("connections_only", "Connections only"),
        Option("no_one", "No one"),
    )

    /** `who_can_see_online_status` and `who_can_see_read_receipts`. */
    val audience = listOf(
        Option("everyone", "Everyone"),
        Option("connections_only", "Connections only"),
        Option("no_one", "No one"),
    )

    val bubbleStyles = listOf(
        Option("default", "Mint"),
        Option("ocean", "Ocean"),
        Option("ember", "Ember"),
        Option("forest", "Forest"),
        Option("violet", "Violet"),
        Option("mono", "Graphite"),
    )

    /** The words for [value] in [options]; a value the list does not know reads as itself, tidied. */
    fun labelOf(options: List<Option>, value: String): String =
        options.firstOrNull { it.value == value }?.label
            ?: value.replace('_', ' ').replaceFirstChar { it.uppercase() }

    /** [options], plus [current] when the server returned a value the list does not carry (a clamp). */
    fun withCurrent(options: List<Option>, current: String): List<Option> =
        if (current.isBlank() || options.any { it.value == current }) options else options + Option(current, labelOf(options, current))

    /** At most this many filtered words (CONTRACT §7). */
    const val MAX_KEYWORDS = 100

    /** Each filtered word is 1..30 characters. */
    const val MAX_KEYWORD_LENGTH = 30

    /**
     * Adds [raw] to the filtered words the way the server will store it:
     * trimmed, lower-cased, de-duplicated; refused (null) when empty, too
     * long, or the list is full.
     */
    fun addKeyword(current: List<String>, raw: String): List<String>? {
        val word = raw.trim().lowercase()
        if (word.isEmpty() || word.length > MAX_KEYWORD_LENGTH) return null
        if (word in current) return current
        if (current.size >= MAX_KEYWORDS) return null
        return current + word
    }
}
