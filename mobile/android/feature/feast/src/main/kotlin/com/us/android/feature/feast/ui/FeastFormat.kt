package com.us.android.feature.feast.ui

import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.FeastOrderDto
import java.time.Duration
import java.time.Instant
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.time.format.DateTimeParseException
import java.util.Locale

private val IST: ZoneId = ZoneId.of("Asia/Kolkata")
private val PLACED_FORMAT: DateTimeFormatter = DateTimeFormatter.ofPattern("d MMM, h:mm a", Locale.ENGLISH)

/** "13 Sep, 12:00 PM" in India time, or the raw text when it is not RFC 3339. */
fun formatPlacedAt(text: String?): String {
    if (text.isNullOrBlank()) return ""
    return try {
        PLACED_FORMAT.format(Instant.parse(text).atZone(IST))
    } catch (e: DateTimeParseException) {
        text
    }
}

/**
 * The order's payable total as the server stated it: `money.totals_paise`
 * when present, otherwise the legacy `totals.final_amount` (decoded exactly).
 */
fun FeastOrderDto.serverTotal(): Paise = money?.totalsPaise?.finalAmountPaise ?: totals.finalAmount

/** "Arriving in 12 min", "Arriving any minute now", or null when there is no ETA. */
fun etaText(etaAt: Instant?, now: Instant): String? {
    etaAt ?: return null
    val minutes = Duration.between(now, etaAt).toMinutes()
    return when {
        minutes <= 0 -> "Arriving any minute now"
        minutes == 1L -> "Arriving in 1 min"
        else -> "Arriving in $minutes min"
    }
}

/** "Updated just now", "Updated 40 s ago", "Updated 3 min ago". */
fun updatedText(at: Instant?, now: Instant): String? {
    at ?: return null
    val seconds = Duration.between(at, now).seconds.coerceAtLeast(0)
    return when {
        seconds < JUST_NOW_SECONDS -> "Updated just now"
        seconds < SECONDS_PER_MINUTE -> "Updated $seconds s ago"
        else -> "Updated ${seconds / SECONDS_PER_MINUTE} min ago"
    }
}

/** "1.2 km away" / "350 m away". */
fun distanceText(km: Double): String =
    if (km < 1.0) "${(km * METRES_PER_KM).toInt().coerceAtLeast(0)} m away" else String.format(Locale.ENGLISH, "%.1f km away", km)

private const val JUST_NOW_SECONDS = 5
private const val SECONDS_PER_MINUTE = 60
private const val METRES_PER_KM = 1000
