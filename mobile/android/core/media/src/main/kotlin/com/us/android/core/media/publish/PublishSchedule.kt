package com.us.android.core.media.publish

import java.time.Instant
import java.time.ZoneId
import java.time.ZonedDateTime
import java.time.format.DateTimeFormatter
import java.util.Locale

/**
 * A scheduled post's instant as the grid tiles show it (2026-09-05):
 * "Scheduled · 6 Sep 18:30". Here beside the publish state because the
 * pending tile and the scheduled tile are one family, drawn by
 * `:feature:profile` from what `:feature:post` sent.
 */
object PublishSchedule {
    /** Back from the wire's RFC 3339, or null for anything that is not an instant. */
    fun parse(wire: String?): Instant? =
        wire?.takeIf { it.isNotBlank() }?.let { runCatching { Instant.parse(it) }.getOrNull() }

    /** "6 Sep 18:30" in the viewer's zone. */
    fun shortLabel(publishAt: Instant, zone: ZoneId = ZoneId.systemDefault()): String =
        DateTimeFormatter.ofPattern("d MMM HH:mm", Locale.ENGLISH).format(ZonedDateTime.ofInstant(publishAt, zone))

    /** The tile's caption, or null when the wire value is not an instant. */
    fun tileLabel(wire: String?, zone: ZoneId = ZoneId.systemDefault()): String? =
        parse(wire)?.let { "Scheduled · ${shortLabel(it, zone)}" }
}
