package com.us.android.feature.doorstep.ui

import java.time.Instant
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.util.Locale

/** The pilot city's zone: every date and time on screen is India time (the contract's `Asia/Kolkata`). */
val IST: ZoneId = ZoneId.of("Asia/Kolkata")

private val SLOT_TIME: DateTimeFormatter = DateTimeFormatter.ofPattern("h:mm a", Locale.ENGLISH)
private val SLOT_DAY: DateTimeFormatter = DateTimeFormatter.ofPattern("EEE, d MMM", Locale.ENGLISH)
private val DAY_NAME: DateTimeFormatter = DateTimeFormatter.ofPattern("EEE", Locale.ENGLISH)
private val DAY_NUMBER: DateTimeFormatter = DateTimeFormatter.ofPattern("d", Locale.ENGLISH)

/** RFC 3339 → [Instant]; null when unreadable. */
fun instantOrNull(text: String?): Instant? {
    if (text.isNullOrBlank()) return null
    return try {
        OffsetDateTime.parse(text).toInstant()
    } catch (e: DateTimeParseException) {
        null
    }
}

/** "10:30 AM" in India time. */
fun slotTimeText(rfc3339: String): String = instantOrNull(rfc3339)?.let { SLOT_TIME.format(it.atZone(IST)) } ?: rfc3339

/** "Sat, 4 Oct · 10:30 AM – 12:00 PM" for a booked visit. */
fun slotRangeText(start: String, end: String): String {
    val from = instantOrNull(start)?.atZone(IST) ?: return start
    val to = instantOrNull(end)?.atZone(IST)
    val range = if (to != null) "${SLOT_TIME.format(from)} – ${SLOT_TIME.format(to)}" else SLOT_TIME.format(from)
    return "${SLOT_DAY.format(from)} · $range"
}

/** The date strip's two lines for a `YYYY-MM-DD`: ("Today"/"Sat", "4"). */
fun dayChip(date: String, today: LocalDate): Pair<String, String> {
    val day = runCatching { LocalDate.parse(date) }.getOrNull() ?: return date to ""
    val name = when (day) {
        today -> "Today"
        today.plusDays(1) -> "Tmrw"
        else -> DAY_NAME.format(day)
    }
    return name to DAY_NUMBER.format(day)
}

/** "1 h 25 min", "45 min". */
fun durationText(minutes: Int): String {
    val h = minutes / MINUTES_PER_HOUR
    val m = minutes % MINUTES_PER_HOUR
    return when {
        h == 0 -> "$m min"
        m == 0 -> "$h h"
        else -> "$h h $m min"
    }
}

private const val MINUTES_PER_HOUR = 60

/** A visit that has not been booked yet: its start plus the quoted duration, as [slotRangeText]. */
fun visitText(start: String, durationMinutes: Int): String {
    val from = instantOrNull(start) ?: return start
    return slotRangeText(start, from.plusSeconds(durationMinutes * SECONDS_PER_MINUTE).toString())
}

private const val SECONDS_PER_MINUTE = 60L
