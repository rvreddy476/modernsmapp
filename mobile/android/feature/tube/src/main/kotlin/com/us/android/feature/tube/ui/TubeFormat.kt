package com.us.android.feature.tube.ui

import com.us.android.core.common.time.formatRelativeTime
import com.us.android.core.ui.formatCount
import java.time.Instant

/**
 * "1.2K views", or null when the row carries no count. Null, not "0
 * views": a fresh post's zero reads as a verdict, and a row from a server
 * that omits `view_count` decodes as zero too — so the segment is left out
 * rather than shown.
 */
fun viewsLabel(views: Int): String? = views.takeIf { it > 0 }?.let { "${formatCount(it)} views" }

/**
 * The line under a title — author · views · age (founder, 2026-09-05, from
 * YouTube's card): the pieces that exist, joined by " · ". A blank age (an
 * unparseable timestamp) is left out rather than rendered as a gap, and
 * the views segment follows [viewsLabel].
 */
fun videoMetaLine(authorName: String?, createdAt: String, views: Int, now: Instant = Instant.now()): String =
    listOfNotNull(
        authorName?.takeIf { it.isNotBlank() },
        viewsLabel(views),
        formatRelativeTime(createdAt, now).takeIf { it.isNotBlank() },
    ).joinToString(" · ")

/**
 * "No subscribers yet", "1 subscriber", "1.2K subscribers" (Tube
 * subscriptions, 2026-09-12). The same rule as the video count label: zero
 * is a sentence, not a number, because "0 subscribers" under a new channel
 * reads as a verdict. Larger counts are compact so the header line never
 * wraps on a channel that has taken off.
 */
fun subscriberCountLabel(count: Int): String = when {
    count <= 0 -> "No subscribers yet"
    count == 1 -> "1 subscriber"
    else -> "${formatCount(count)} subscribers"
}
