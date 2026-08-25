package com.us.android.core.common.time

import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.time.temporal.ChronoUnit

/**
 * Turns a server timestamp into the short age string a feed shows.
 *
 * WHY THIS EXISTS
 *
 * Observed on a device on 2026-08-18: posts were captioned
 * `2026-08-17T10:16:51.169391Z`. The server sends RFC 3339 because that is the
 * right wire format; it is not a thing to show a person.
 *
 * Kept in core/common as pure Kotlin so both the feed and the comment list use
 * one rule. Two independent implementations drift, and "3h" in one place next
 * to "3 hours ago" in another looks like a bug even when both are correct.
 *
 * Returns an empty string for anything unparseable. A blank timestamp is a
 * missing detail; echoing a raw or malformed value is a visible defect, and the
 * caller can choose to omit the line entirely.
 */
fun formatRelativeTime(isoInstant: String, now: Instant = Instant.now()): String {
    val then = parseInstant(isoInstant) ?: return ""

    // A clock skewed a little into the future is normal — the device clock and
    // the server clock are not synchronised — and "in 3 seconds" reads as
    // broken. Anything not yet past is simply "now".
    val seconds = ChronoUnit.SECONDS.between(then, now)
    if (seconds < MINUTE) return "now"

    return when {
        seconds < HOUR -> "${seconds / MINUTE}m"
        seconds < DAY -> "${seconds / HOUR}h"
        seconds < WEEK -> "${seconds / DAY}d"
        else -> formatAbsolute(then, now)
    }
}

/**
 * Past a week the age stops being useful and the date is what people want.
 *
 * The year is included only when it differs from the current one — "12 Aug" is
 * what a reader expects for something recent, and a year on every old post is
 * noise.
 */
private fun formatAbsolute(then: Instant, now: Instant): String {
    val zone = ZoneId.systemDefault()
    val thenDate = then.atZone(zone)
    val sameYear = thenDate.year == now.atZone(zone).year
    val pattern = if (sameYear) SAME_YEAR_PATTERN else OTHER_YEAR_PATTERN
    return DateTimeFormatter.ofPattern(pattern).format(thenDate)
}

/**
 * Tolerates a missing zone designator.
 *
 * Every timestamp captured from the gateway so far carries `Z`, but a value
 * without one would otherwise throw inside a Composable and take the whole
 * screen down over a cosmetic string.
 */
private fun parseInstant(raw: String): Instant? {
    if (raw.isBlank()) return null
    return try {
        Instant.parse(raw)
    } catch (_: DateTimeParseException) {
        null
    }
}

private const val MINUTE = 60L
private const val HOUR = 60L * MINUTE
private const val DAY = 24L * HOUR
private const val WEEK = 7L * DAY

private const val SAME_YEAR_PATTERN = "d MMM"
private const val OTHER_YEAR_PATTERN = "d MMM yyyy"
