package com.us.android.feature.doorsteppro.ui

import java.time.Instant
import java.time.LocalDate
import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.util.Locale

/*
 * Dates and times as a professional reads them. COPIED from :feature:doorstep's
 * ui/DoorstepFormat.kt, plus the pieces only the professional needs (distance,
 * the date strip of the jobs list, the weekday names of the hours editor).
 */

/** The pilot city's zone: every date and time on screen is India time (the contract's `Asia/Kolkata`). */
val IST: ZoneId = ZoneId.of("Asia/Kolkata")

private val SLOT_TIME: DateTimeFormatter = DateTimeFormatter.ofPattern("h:mm a", Locale.ENGLISH)
private val SLOT_DAY: DateTimeFormatter = DateTimeFormatter.ofPattern("EEE, d MMM", Locale.ENGLISH)
private val DAY_NAME: DateTimeFormatter = DateTimeFormatter.ofPattern("EEE", Locale.ENGLISH)
private val DAY_NUMBER: DateTimeFormatter = DateTimeFormatter.ofPattern("d", Locale.ENGLISH)
private val LONG_DATE: DateTimeFormatter = DateTimeFormatter.ofPattern("d MMM yyyy", Locale.ENGLISH)

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

/** "Sat, 4 Oct · 10:30 AM – 12:00 PM" for a visit. */
fun slotRangeText(start: String, end: String): String {
    val from = instantOrNull(start)?.atZone(IST) ?: return start
    val to = instantOrNull(end)?.atZone(IST)
    val range = if (to != null) "${SLOT_TIME.format(from)} – ${SLOT_TIME.format(to)}" else SLOT_TIME.format(from)
    return "${SLOT_DAY.format(from)} · $range"
}

/** "10:30 AM – 12:00 PM" without the day, for a list already grouped by day. */
fun timeRangeText(start: String, end: String): String {
    val from = instantOrNull(start)?.atZone(IST) ?: return start
    val to = instantOrNull(end)?.atZone(IST) ?: return SLOT_TIME.format(from)
    return "${SLOT_TIME.format(from)} – ${SLOT_TIME.format(to)}"
}

/** The India-time calendar date an RFC 3339 instant falls on; null when unreadable. */
fun istDateOf(rfc3339: String): LocalDate? = instantOrNull(rfc3339)?.atZone(IST)?.toLocalDate()

/** The date strip's two lines for a date: ("Today"/"Sat", "4"). */
fun dayChip(day: LocalDate, today: LocalDate): Pair<String, String> {
    val name = when (day) {
        today -> "Today"
        today.plusDays(1) -> "Tmrw"
        else -> DAY_NAME.format(day)
    }
    return name to DAY_NUMBER.format(day)
}

/** "4 Oct 2026" for a `YYYY-MM-DD`, or the text unchanged. */
fun longDateText(isoDate: String?): String {
    if (isoDate.isNullOrBlank()) return ""
    return runCatching { LONG_DATE.format(LocalDate.parse(isoDate)) }.getOrDefault(isoDate)
}

/** "850 m", "3.4 km". */
fun distanceText(meters: Int): String = when {
    meters < KILOMETRE -> "$meters m"
    else -> {
        val tenths = (meters + HALF_TENTH) / TENTH
        "${tenths / TENTHS_PER_KM}.${tenths % TENTHS_PER_KM} km"
    }
}

/** Weekday names, 0 = Sunday (the contract's numbering). */
val WEEKDAY_NAMES: List<String> = listOf("Sunday", "Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday")

/** "1 h 25 min", "45 min". */
fun durationText(minutes: Long): String {
    val h = minutes / MINUTES_PER_HOUR
    val m = minutes % MINUTES_PER_HOUR
    return when {
        h == 0L -> "$m min"
        m == 0L -> "$h h"
        else -> "$h h $m min"
    }
}

/** "4:05" for a countdown of seconds, "1:02:05" past an hour. */
fun countdownText(seconds: Long): String {
    val h = seconds / SECONDS_PER_HOUR
    val m = (seconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE
    val s = seconds % SECONDS_PER_MINUTE
    val mmss = "${if (h > 0) m.toString().padStart(2, '0') else m.toString()}:${s.toString().padStart(2, '0')}"
    return if (h > 0) "$h:$mmss" else mmss
}

/** "category-slug" → "Category slug". */
fun humanise(code: String): String =
    code.replace('_', ' ').replace('-', ' ').lowercase().replaceFirstChar { it.titlecase(Locale.ENGLISH) }

private const val KILOMETRE = 1_000
private const val TENTH = 100
private const val HALF_TENTH = 50
private const val TENTHS_PER_KM = 10
private const val MINUTES_PER_HOUR = 60L
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3_600L
