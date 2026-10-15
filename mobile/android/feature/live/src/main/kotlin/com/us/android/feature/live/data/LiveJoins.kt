package com.us.android.feature.live.data

import java.time.Instant
import java.time.OffsetDateTime

/*
 * Join notices ("Asha joined") as system rows in the room's chat (T-30,
 * matching the web's features/live/joins.ts).
 *
 * live-service-v2 publishes `viewer.joined` at most once per viewer per
 * stream per minute, never for the host and never for a signed-out viewer.
 * There is no HTTP read of joins, so they are never persisted, never merged
 * into the chat history ([ChatLog]) and a missed frame is simply not shown.
 *
 *  - a join is stored once per person per burst window, and the store is capped;
 *  - the reader's own join is never shown;
 *  - joins that land together (no chat message between them, within
 *    [JOIN_BURST_MILLIS] of the first) collapse into one row:
 *    "Asha joined", "Asha and Ravi joined", "Asha, Ravi and Mei joined",
 *    "Asha, Ravi and 3 others joined";
 *  - the name is [chatAuthorName] (name, else @handle, else "Viewer"),
 *    never a piece of a user id.
 */

/** One arrival. [atMillis] is the frame's `joined_at`, or when it was received. */
data class LiveJoin(val userId: String, val author: LiveChatAuthorDto?, val atMillis: Long)

/** Raw joins kept per stream (oldest dropped first). */
const val MAX_JOINS = 40

/** Join rows shown at most (oldest dropped first). */
const val MAX_JOIN_ROWS = 12

/** Joins within this window of a row's first join collapse into that row. */
const val JOIN_BURST_MILLIS = 5_000L

/** Names written out before "and N others". */
const val JOIN_NAMES_SHOWN = 2

/** An RFC 3339 time in epoch millis, or null when it does not parse. */
fun parseInstantMillis(value: String): Long? {
    val text = value.trim()
    if (text.isEmpty()) return null
    return runCatching { Instant.parse(text).toEpochMilli() }.getOrNull()
        ?: runCatching { OffsetDateTime.parse(text).toInstant().toEpochMilli() }.getOrNull()
}

/** The join's time; [fallback] (the time it was received) when `joined_at` does not parse. */
fun joinTime(joinedAt: String, fallback: Long): Long = parseInstantMillis(joinedAt) ?: fallback

/**
 * Stores one join: a second join by the same person within the burst window
 * is the same arrival (a redelivered frame), and only the newest [max] are kept.
 */
fun addJoin(joins: List<LiveJoin>, join: LiveJoin, max: Int = MAX_JOINS): List<LiveJoin> {
    if (join.userId.isBlank()) return joins
    if (joins.any { it.userId == join.userId && kotlin.math.abs(it.atMillis - join.atMillis) < JOIN_BURST_MILLIS }) {
        return joins
    }
    return (joins + join).sortedBy { it.atMillis }.takeLast(max.coerceAtLeast(0))
}

/**
 * "Asha joined" · "Asha and Ravi joined" · "Asha, Ravi and Mei joined" ·
 * "Asha, Ravi and 3 others joined" · "4 viewers joined".
 */
fun joinLabel(authors: List<LiveChatAuthorDto?>): String {
    val total = authors.size
    if (total == 0) return ""
    val named = authors.map(::chatAuthorName).filter { it != CHAT_AUTHOR_FALLBACK }
    if (named.isEmpty()) return if (total == 1) "$CHAT_AUTHOR_FALLBACK joined" else "$total viewers joined"
    val shown = if (total <= JOIN_NAMES_SHOWN + 1 && named.size == total) named else named.take(JOIN_NAMES_SHOWN)
    val others = total - shown.size
    if (others == 0) {
        return if (shown.size == 1) "${shown[0]} joined" else "${shown.dropLast(1).joinToString(", ")} and ${shown.last()} joined"
    }
    return "${shown.joinToString(", ")} and $others ${if (others == 1) "other" else "others"} joined"
}

/** One row of the room's chat: a message, or a collapsed run of joins. */
sealed interface ChatTimelineItem {
    val key: String

    data class Message(val message: LiveChatMessageDto) : ChatTimelineItem {
        override val key: String get() = message.id
    }

    data class Joins(
        override val key: String,
        val atMillis: Long,
        val userIds: List<String>,
        val label: String,
    ) : ChatTimelineItem
}

/**
 * Chat messages and join rows in one list, NEWEST FIRST (what the reversed
 * chat list wants, and the order [ChatLog.messages] already has). Messages
 * keep their order; a join sits before the first message newer than it.
 * Consecutive joins within [burstMillis] of the row's first join collapse,
 * a message ends a burst, and only the newest [maxRows] join rows are kept.
 */
fun chatTimeline(
    messagesNewestFirst: List<LiveChatMessageDto>,
    joins: List<LiveJoin>,
    meId: String,
    burstMillis: Long = JOIN_BURST_MILLIS,
    maxRows: Int = MAX_JOIN_ROWS,
): List<ChatTimelineItem> {
    val me = meId.trim()
    val pending = joins.filter { it.userId.isNotBlank() && it.userId != me }.sortedBy { it.atMillis }
    if (pending.isEmpty()) return messagesNewestFirst.map(ChatTimelineItem::Message)

    val out = mutableListOf<Any>() // ChatTimelineItem.Message or JoinGroup, oldest first
    var group: JoinGroup? = null
    fun pushJoin(join: LiveJoin) {
        val open = group
        if (open != null && join.atMillis - open.atMillis <= burstMillis) {
            if (open.members.none { it.userId == join.userId }) open.members += join
            return
        }
        group = JoinGroup(join.atMillis, mutableListOf(join)).also { out += it }
    }

    var next = 0
    for (message in messagesNewestFirst.asReversed()) {
        val at = parseInstantMillis(message.createdAt) ?: Long.MAX_VALUE
        while (next < pending.size && pending[next].atMillis < at) pushJoin(pending[next++])
        group = null // a message ends a burst
        out += ChatTimelineItem.Message(message)
    }
    while (next < pending.size) pushJoin(pending[next++])

    val groups = out.filterIsInstance<JoinGroup>()
    val dropped = groups.take((groups.size - maxRows.coerceAtLeast(0)).coerceAtLeast(0)).toSet()
    val items = out.mapNotNull { entry ->
        when (entry) {
            is ChatTimelineItem -> entry
            is JoinGroup -> if (entry in dropped) {
                null
            } else {
                ChatTimelineItem.Joins(
                    key = "join-${entry.members.first().userId}-${entry.atMillis}",
                    atMillis = entry.atMillis,
                    userIds = entry.members.map { it.userId },
                    label = joinLabel(entry.members.map { it.author }),
                )
            }
            else -> null
        }
    }
    return items.asReversed()
}

/** A run of joins being collapsed. Identity, not equality: two runs can look alike. */
private class JoinGroup(val atMillis: Long, val members: MutableList<LiveJoin>)
