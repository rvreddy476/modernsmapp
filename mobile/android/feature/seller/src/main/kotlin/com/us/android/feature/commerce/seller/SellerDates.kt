package com.us.android.feature.commerce.seller

import java.time.OffsetDateTime
import java.time.ZoneId
import java.time.format.DateTimeFormatter
import java.util.Locale

/**
 * Renders a server timestamp for a seller.
 *
 * The seller routes send Go's `time.Time` as RFC 3339, which the buyer routes
 * do not (they send an epoch), so this is the one place the app parses a
 * date string. Anything that fails to parse is shown as received rather
 * than hidden: a timestamp the seller can read raw beats a blank where a
 * courier handover should be.
 */
fun formatSellerTimestamp(iso: String?): String? {
    val raw = iso?.takeIf { it.isNotBlank() } ?: return null
    return runCatching {
        OffsetDateTime.parse(raw)
            .atZoneSameInstant(ZoneId.systemDefault())
            .format(SELLER_TIMESTAMP)
    }.getOrDefault(raw)
}

private val SELLER_TIMESTAMP: DateTimeFormatter =
    DateTimeFormatter.ofPattern("d MMM yyyy, HH:mm", Locale.getDefault())
