package com.us.android.feature.settings.deleted

import com.us.android.core.common.time.formatRelativeTime
import java.time.Instant
import java.time.format.DateTimeParseException
import java.time.temporal.ChronoUnit

/**
 * The one line under a deleted post's title:
 * "Deleted 2h ago · Permanently removed in 28 days".
 *
 * Both halves come from server timestamps (`deleted_at`, `purge_at`) — the
 * client never computes the purge moment itself, because the 30-day window
 * is the server's rule and the server's clock. Pure functions, so the
 * countdown is pinned by a test rather than eyeballed on a device.
 */
fun deletedRowSubtitle(deletedAt: String, purgeAt: String, now: Instant = Instant.now()): String =
    listOf(deletedLabel(deletedAt, now), purgeLabel(purgeAt, now))
        .filter { it.isNotBlank() }
        .joinToString(SEPARATOR)

/**
 * "Deleted 2h ago", "Deleted just now", or "Deleted 12 Aug" once the feed's
 * age string turns into a date. Reuses [formatRelativeTime] so the age reads
 * the same here as on a card; blank when the server sent nothing.
 */
fun deletedLabel(deletedAt: String, now: Instant = Instant.now()): String {
    val age = formatRelativeTime(deletedAt, now)
    return when {
        age.isBlank() -> "Deleted"
        age == "now" -> "Deleted just now"
        AGE_TOKEN.matches(age) -> "Deleted $age ago"
        else -> "Deleted $age"
    }
}

/**
 * How long the post can still be brought back. Days are rounded UP: a post
 * purged in 27 days and 6 hours is "in 28 days", because "27" would promise
 * a day the viewer does not have. Under a day it counts hours, under an hour
 * it says so, and a purge moment already past is "being removed".
 */
fun purgeLabel(purgeAt: String, now: Instant = Instant.now()): String {
    val purge = parseInstant(purgeAt) ?: return ""
    val seconds = ChronoUnit.SECONDS.between(now, purge)
    return when {
        seconds <= 0 -> "Being permanently removed"
        seconds < HOUR -> "Permanently removed in less than an hour"
        seconds < DAY -> "Permanently removed in ${plural(ceilDiv(seconds, HOUR), "hour")}"
        else -> "Permanently removed in ${plural(ceilDiv(seconds, DAY), "day")}"
    }
}

private fun ceilDiv(value: Long, unit: Long): Long = (value + unit - 1) / unit

private fun plural(count: Long, unit: String): String = if (count == 1L) "1 $unit" else "$count ${unit}s"

private fun parseInstant(raw: String): Instant? {
    if (raw.isBlank()) return null
    return try {
        Instant.parse(raw)
    } catch (_: DateTimeParseException) {
        null
    }
}

private val AGE_TOKEN = Regex("\\d+[mhd]")
private const val SEPARATOR = " · "
private const val HOUR = 60L * 60L
private const val DAY = 24L * HOUR
