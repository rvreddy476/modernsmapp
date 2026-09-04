package com.us.android.feature.tube.ui

import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.ui.formatCount
import java.time.Instant
import java.util.Locale

/**
 * A length as a clock reads it: `m:ss` under an hour, `h:mm:ss` from an hour
 * on (YouTube's badge). Nothing or a negative is `0:00`, never blank — the
 * badge is drawn only when a duration is known, so blank never shows.
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

/**
 * The line under a title: the pieces that exist, joined by " · ". A view
 * count is shown only when the row carries one — "0 views" on a fresh post
 * reads as a verdict — and a blank age (an unparseable timestamp) is left
 * out rather than rendered as a gap.
 */
fun videoMetaLine(authorName: String?, createdAt: String, views: Int, now: Instant = Instant.now()): String =
    listOfNotNull(
        authorName?.takeIf { it.isNotBlank() },
        formatRelativeTime(createdAt, now).takeIf { it.isNotBlank() },
        views.takeIf { it > 0 }?.let { "${formatCount(it)} views" },
    ).joinToString(" · ")

private const val MILLIS_PER_SECOND = 1_000L

/** Rounds to the nearest second, so 59.6 s reads 1:00 as the player's own clock would. */
private const val HALF_SECOND_MS = 500L
private const val SECONDS_PER_MINUTE = 60L
private const val SECONDS_PER_HOUR = 3_600L
