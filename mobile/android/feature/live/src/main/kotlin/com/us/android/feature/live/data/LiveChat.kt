// MatchingDeclarationName: the file is the chat's rules; ChatRole is one of several things it declares.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.live.data

/*
 * The chat's own rules (2026-10-02): who a row is by, which rows float over
 * the video, and what a draft may hold. Pure, so each is a table test.
 */

// ── Who wrote it ────────────────────────────────────────────────────────

/** What a chat row's author is to this stream. */
enum class ChatRole { Host, Moderator, Viewer }

/** The badge token the directory puts in `author.badges` for a founding creator. */
const val BADGE_FOUNDING_CREATOR = "founding_creator"

/** What a row prints when the directory had no name and no handle for its author. */
const val CHAT_AUTHOR_FALLBACK = "Viewer"

/**
 * The name a chat row prints: `author.name`, else `@handle`, else "Viewer"
 * (live-eligibility contract B). NEVER a piece of the user id: the old
 * "user ab12cd" label told nobody who was speaking (founder, 2026-10-02).
 */
fun chatAuthorName(author: LiveChatAuthorDto?): String {
    val name = author?.name?.trim().orEmpty()
    if (name.isNotEmpty()) return name
    val handle = author?.handle?.trim()?.trimStart('@').orEmpty()
    return if (handle.isNotEmpty()) "@$handle" else CHAT_AUTHOR_FALLBACK
}

/**
 * Host, moderator or viewer. The row's own `author.role` when the server
 * sent one; otherwise (an older server, a failed lookup) what this screen
 * already knows: the stream's creator, and the moderator list the host and
 * the moderators are shown.
 */
fun chatRoleOf(message: LiveChatMessageDto, hostId: String, moderators: Collection<String>): ChatRole {
    val wire = message.author?.role?.trim()?.lowercase().orEmpty()
    val userId = message.userId.ifBlank { message.author?.userId.orEmpty() }
    return when {
        wire == "host" || (userId.isNotBlank() && userId == hostId) -> ChatRole.Host
        wire == "moderator" || (userId.isNotBlank() && userId in moderators) -> ChatRole.Moderator
        else -> ChatRole.Viewer
    }
}

/** The Founding creator badge is shown when `author.badges` carries its token. */
fun isFoundingCreator(author: LiveChatAuthorDto?): Boolean =
    author?.badges.orEmpty().any { it.trim().equals(BADGE_FOUNDING_CREATOR, ignoreCase = true) }

/**
 * Everyone this screen has seen write, by user id: what the moderation
 * sheets print for a moderator or a banned user, who are known only by id
 * once their message has scrolled out of the page. A later row replaces an
 * earlier one, so a renamed author reads by the new name.
 */
fun chatPeople(
    known: Map<String, LiveChatAuthorDto>,
    rows: List<LiveChatMessageDto>,
): Map<String, LiveChatAuthorDto> {
    val seen = rows.mapNotNull { row ->
        val author = row.author ?: return@mapNotNull null
        val id = row.userId.ifBlank { author.userId }
        if (id.isBlank()) null else id to author
    }
    return if (seen.isEmpty()) known else known + seen
}

// ── Over the video ──────────────────────────────────────────────────────

/** How many comments float over the video. */
const val OVERLAY_COMMENT_COUNT = 3

/**
 * The comments drawn over the video (founder, 2026-10-02, TikTok's idiom):
 * the latest [limit], oldest first — so the newest sits at the bottom, where
 * it slid in, and the older ones fade above it.
 *
 * [ChatLog.messages] is newest first and already has no removed message in
 * it, so a removal — the moderator's own, or a poll that no longer carries
 * the row — takes the comment off the video too, and the next one down the
 * log takes its place.
 */
fun ChatLog.overlayComments(limit: Int = OVERLAY_COMMENT_COUNT): List<LiveChatMessageDto> =
    if (limit <= 0) emptyList() else messages.take(limit).asReversed().toList()

// ── What a draft may hold ───────────────────────────────────────────────

/** live-service-v2 refuses a message over 500 RUNES (`utf8.RuneCountInString`). */
const val MAX_CHAT_CODE_POINTS = 500

/**
 * A text's length the way the server counts it: Unicode code points, not
 * UTF-16 units. "😀" is 1 here and 2 in [String.length]; counting units
 * would refuse a 300-emoji message the server accepts.
 */
fun String.codePointLength(): Int = codePointCount(0, length)

/**
 * [text] cut to at most [max] code points WITHOUT breaking what the reader
 * sees as one character.
 *
 * `String.take` cuts UTF-16 units and can leave half a surrogate pair — a
 * broken emoji the server stores as U+FFFD. Cutting on code points alone is
 * still wrong for an emoji made of several: a skin-tone modifier, a
 * variation selector, a ZWJ sequence ("👩‍👩‍👧") or a flag's two regional
 * indicators. So the cut backs off to the start of the cluster it landed in.
 */
fun clampToCodePoints(text: String, max: Int): String {
    if (max <= 0) return ""
    if (text.codePointLength() <= max) return text
    val points = text.codePoints().toArray()
    var end = max
    // The first dropped code point continues the last kept cluster, or the
    // kept text ends on a joiner: step back until the cut is between clusters.
    while (end > 0 && (continuesCluster(points[end]) || points[end - 1] == ZERO_WIDTH_JOINER)) end--
    // A flag is two regional indicators: never keep an odd one.
    var indicators = 0
    while (indicators < end && isRegionalIndicator(points[end - 1 - indicators])) indicators++
    if (indicators % 2 == 1) end--
    return String(points, 0, end)
}

/** A draft as the field holds it: at most what the server accepts. */
fun clampChatDraft(text: String): String = clampToCodePoints(text, MAX_CHAT_CODE_POINTS)

/** Whether there is something to send: not blank, and within the server's limit. */
fun canSendChat(draft: String): Boolean {
    val text = draft.trim()
    return text.isNotEmpty() && text.codePointLength() <= MAX_CHAT_CODE_POINTS
}

private const val ZERO_WIDTH_JOINER = 0x200D
private const val VARIATION_SELECTOR_FIRST = 0xFE00
private const val VARIATION_SELECTOR_LAST = 0xFE0F
private const val SKIN_TONE_FIRST = 0x1F3FB
private const val SKIN_TONE_LAST = 0x1F3FF
private const val REGIONAL_INDICATOR_FIRST = 0x1F1E6
private const val REGIONAL_INDICATOR_LAST = 0x1F1FF
private const val KEYCAP = 0x20E3
private const val TAG_FIRST = 0xE0020
private const val TAG_LAST = 0xE007F

private fun isRegionalIndicator(codePoint: Int): Boolean =
    codePoint in REGIONAL_INDICATOR_FIRST..REGIONAL_INDICATOR_LAST

/** A code point that never starts a character of its own: it modifies or joins the one before. */
private fun continuesCluster(codePoint: Int): Boolean = codePoint == ZERO_WIDTH_JOINER ||
    codePoint == KEYCAP ||
    codePoint in VARIATION_SELECTOR_FIRST..VARIATION_SELECTOR_LAST ||
    codePoint in SKIN_TONE_FIRST..SKIN_TONE_LAST ||
    codePoint in TAG_FIRST..TAG_LAST ||
    when (Character.getType(codePoint)) {
        Character.NON_SPACING_MARK.toInt(),
        Character.ENCLOSING_MARK.toInt(),
        Character.COMBINING_SPACING_MARK.toInt(),
        -> true
        else -> false
    }
