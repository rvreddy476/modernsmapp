package com.us.android.feature.post.createhub

import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.util.Locale

/**
 * When a post may be scheduled for (2026-09-05): five minutes to thirty
 * days ahead — the server's window, mirrored so the picker refuses what
 * the server would. Pure over [Instant]s, so the edges are a table test;
 * the server stays the authority and its 400 is shown in its words.
 */
object ScheduleWindow {
    /** The earliest a post may be scheduled: five minutes from now. */
    const val MIN_AHEAD_MILLIS: Long = 5L * 60L * 1_000L

    /** The latest: thirty days from now. */
    const val MAX_AHEAD_MILLIS: Long = 30L * 24L * 60L * 60L * 1_000L

    sealed interface Check {
        data object Ok : Check
        data object TooSoon : Check
        data object TooLate : Check
    }

    fun check(publishAt: Instant, now: Instant): Check {
        val ahead = publishAt.toEpochMilli() - now.toEpochMilli()
        return when {
            ahead < MIN_AHEAD_MILLIS -> Check.TooSoon
            ahead > MAX_AHEAD_MILLIS -> Check.TooLate
            else -> Check.Ok
        }
    }

    /** What the picker says under an instant it will not take. */
    fun message(check: Check): String? = when (check) {
        Check.Ok -> null
        Check.TooSoon -> "Pick a time at least 5 minutes from now."
        Check.TooLate -> "Pick a time within the next 30 days."
    }

    /** The instant on the wire: RFC 3339 in UTC, to the second — `2026-09-06T13:00:00Z`. */
    fun wire(publishAt: Instant): String =
        DateTimeFormatter.ISO_INSTANT.format(Instant.ofEpochSecond(publishAt.epochSecond))

    /** Back from the wire, or null for anything that is not an instant. */
    fun parse(wire: String?): Instant? = wire?.let { runCatching { Instant.parse(it) }.getOrNull() }

    /** "Fri 6 Sep, 18:30" — the button's and the tile's wording, in the viewer's zone. */
    fun label(publishAt: Instant, zone: ZoneId): String =
        DateTimeFormatter.ofPattern("EEE d MMM, HH:mm", Locale.ENGLISH).format(ZonedDateTime.ofInstant(publishAt, zone))

    /** "6 Sep 18:30" — the short form on a grid tile. */
    fun shortLabel(publishAt: Instant, zone: ZoneId): String =
        DateTimeFormatter.ofPattern("d MMM HH:mm", Locale.ENGLISH).format(ZonedDateTime.ofInstant(publishAt, zone))
}
