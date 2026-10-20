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

    data class Option(val value: String, val label: String, val description: String = "")

    // ── Who can message you (DM consent, founder-approved 2026-10-20) ──────
    //
    // Four choices, and only four. Following someone IS consent to hear from
    // them: under every choice but "No one", people you follow land straight
    // in your Chats; the choice decides what everyone else may do.

    const val MESSAGE_EVERYONE = "everyone_message_requests"
    const val MESSAGE_FOLLOWERS = "followers_message_requests"
    const val MESSAGE_PEOPLE_I_FOLLOW = "people_i_follow"
    const val MESSAGE_NO_ONE = "no_one"

    val whoCanMessage = listOf(
        Option(
            MESSAGE_EVERYONE,
            "Everyone",
            "People you follow message you directly. Anyone else can send you a request.",
        ),
        Option(
            MESSAGE_FOLLOWERS,
            "Followers",
            "People you follow message you directly. Your followers can send you a request.",
        ),
        Option(
            MESSAGE_PEOPLE_I_FOLLOW,
            "People I follow",
            "Only people you follow can message you. No one else can send a request.",
        ),
        Option(MESSAGE_NO_ONE, "No one", "No one can start a chat with you. Chats you already have stay."),
    )

    /**
     * The choice a stored [raw] value shows as. Reads return the four values,
     * but an older server or an older row may still hold a retired one; it
     * shows as the choice the server maps it to. Anything unknown shows as
     * the strictest choice short of No one — never wider than it is.
     */
    fun whoCanMessageChoice(raw: String): String = when (raw) {
        MESSAGE_EVERYONE, MESSAGE_FOLLOWERS, MESSAGE_PEOPLE_I_FOLLOW, MESSAGE_NO_ONE -> raw
        "friends_of_friends_requests" -> MESSAGE_FOLLOWERS
        else -> MESSAGE_PEOPLE_I_FOLLOW // connections_only, connections_and_mutual_followers, unknown
    }

    /** The choices offered: an under-18 account never sees Everyone or Followers (the server clamps them anyway). */
    fun whoCanMessageChoices(minor: Boolean): List<Option> =
        if (minor) whoCanMessage.filter { it.value == MESSAGE_PEOPLE_I_FOLLOW || it.value == MESSAGE_NO_ONE } else whoCanMessage

    /** What the screen marks as chosen, for [minor] too: a wider value than they may hold reads as People I follow. */
    fun shownWhoCanMessage(raw: String, minor: Boolean): String {
        val choice = whoCanMessageChoice(raw)
        return if (minor && whoCanMessageChoices(minor = true).none { it.value == choice }) MESSAGE_PEOPLE_I_FOLLOW else choice
    }

    /** `chat_availability`: the Messages master switch. */
    const val CHAT_ENABLED = "enabled"
    const val CHAT_PAUSED = "paused"

    val whoCanAddToGroups = listOf(
        Option("everyone_with_approval", "Everyone, with my approval"),
        Option("friends_of_friends_invites", "Friends of friends, as invites"),
        Option("connections_and_contacts", "Connections and contacts"),
        Option("connections_only", "Connections only"),
        Option("no_one", "No one"),
    )

    // `connections_only` is retired (accepting a request no longer connects
    // anyone); the server reads it as accepted_chats_only and returns only
    // these two. A stored legacy value still shows, via [withCurrent].
    val whoCanCall = listOf(
        Option("accepted_chats_only", "People I've accepted chats with"),
        Option("no_one", "No one"),
    )

    /** `who_can_see_online_status` and `who_can_see_read_receipts`. */
    val audience = listOf(
        Option("everyone", "Everyone"),
        Option("connections_only", "Connections only"),
        Option("no_one", "No one"),
    )

    val bubbleStyles = listOf(
        Option("default", "Classic"),
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
