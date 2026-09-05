package com.us.android.core.common.time

import java.util.Locale

/**
 * A length as a clock reads it: `m:ss` under an hour, `h:mm:ss` from an hour
 * on (YouTube's badge). Nothing or a negative is `0:00`, never blank — a
 * badge is drawn only when a duration is known, so blank never shows.
 *
 * Here rather than in Tube (where it began, 2026-09-05) because the search
 * results' duration pill and Tube's cards must read the same length the
 * same way.
 */
fun formatDuration(millis: Long): String {
    val totalSeconds = (millis.coerceAtLeast(0L) + HALF_SECOND_MS) / MILLIS_PER_SECOND
    val hours = totalSeconds / SECONDS_PER_HOUR
    val minutes = (totalSeconds % SECONDS_PER_HOUR) / SECONDS_PER_MINUTE
    val seconds = totalSeconds % SECONDS_PER_MINUTE
    return if (hours > 0) {
        String.format(Locale.US, "%d:%02d:%02d", hours, minutes, seconds)
    } else {
        String.format(Locale.US, "%d:%02d", minutes, seconds)
    }
}

private const val MILLIS_PER_SECOND = 1_000L

/** Rounds to the nearest second, so 59.6 s reads 1:00 as the player's own clock would. */
private const val HALF_SECOND_MS = 500L
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3_600L
