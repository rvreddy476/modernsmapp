package com.us.android.core.chat.data

import kotlinx.serialization.SerialName
import kotlinx.serialization.Serializable
import java.util.Locale
import kotlin.math.roundToLong

/**
 * A shared location (founder, 23 Oct 2026: WhatsApp's "Send your current
 * location"): the wire, cache and outbox shape of `type = "location"`.
 *
 * Privacy: coordinates are never logged and never put in a preview — the
 * inbox says [LOCATION_PREVIEW]. message-service seals them with the rest of
 * the message payload; a pending message request refuses them, like media.
 */
@Serializable
data class ChatLocation(
    val lat: Double,
    val lng: Double,
    @SerialName("accuracy_m") val accuracyM: Double? = null,
    val label: String? = null,
    val address: String? = null,
) {
    /** A real place on Earth (the server refuses anything else). */
    val valid: Boolean
        get() = lat.isFinite() && lng.isFinite() && lat in -MAX_LAT..MAX_LAT && lng in -MAX_LNG..MAX_LNG
}

const val LOCATION_MESSAGE = "location"

/** The inbox / notification-free preview of a location: never the place itself. */
const val LOCATION_PREVIEW = "📍 Location"

/** Label and address cap (message-service LocationPlaceMaxRunes), in code points. */
const val LOCATION_PLACE_MAX = 200

private const val MAX_LAT = 90.0
private const val MAX_LNG = 180.0
private const val COORD_SCALE = 100_000.0
private const val MAX_ACCURACY_M = 100_000.0
private const val METRES_PER_KM = 1_000.0

/** 5 decimals (~1 m), as the server stores it; never "-0". A non-finite value passes through (and fails [ChatLocation.valid]). */
fun roundCoord(v: Double): Double = if (v.isFinite()) (kotlin.math.round(v * COORD_SCALE) / COORD_SCALE) + 0.0 else v

/** Trimmed, control characters flattened, capped at [LOCATION_PLACE_MAX] code points; null when empty. */
fun capPlaceText(s: String?): String? {
    val flat = s.orEmpty().map { if (it.isISOControl()) ' ' else it }.joinToString("").trim()
    if (flat.isEmpty()) return null
    val points = flat.codePointCount(0, flat.length)
    if (points <= LOCATION_PLACE_MAX) return flat
    return flat.substring(0, flat.offsetByCodePoints(0, LOCATION_PLACE_MAX)).trim().ifEmpty { null }
}

/**
 * The location to SEND from a device fix: rounded, accuracy in whole metres
 * (dropped when meaningless), text capped. Null when the fix is not a place.
 */
fun chatLocationOf(lat: Double, lng: Double, accuracyM: Double?, label: String?, address: String?): ChatLocation? {
    val location = ChatLocation(
        lat = roundCoord(lat),
        lng = roundCoord(lng),
        accuracyM = accuracyM?.takeIf { it.isFinite() && it >= 0 }?.let { minOf(MAX_ACCURACY_M, it.roundToLong().toDouble()) },
        label = capPlaceText(label),
        address = capPlaceText(address),
    )
    return location.takeIf { it.valid }
}

/** "12.97160, 77.59460" — the place line when there is no label or address. */
fun coordsText(location: ChatLocation): String =
    String.format(Locale.ENGLISH, "%.5f, %.5f", roundCoord(location.lat), roundCoord(location.lng))

/** "Accurate to 12 m" / "Accurate to 1.4 km"; null when unknown. */
fun accuracyText(accuracyM: Double?): String? {
    val m = accuracyM?.takeIf { it.isFinite() && it >= 0 } ?: return null
    return if (m < METRES_PER_KM) {
        "Accurate to ${maxOf(1L, m.roundToLong())} m"
    } else {
        "Accurate to ${String.format(Locale.ENGLISH, "%.1f", m / METRES_PER_KM)} km"
    }
}

/** "Open in Maps" on the web: Google's universal search link. */
fun mapsWebUrl(location: ChatLocation): String =
    "https://www.google.com/maps/search/?api=1&query=${plain(location.lat)},${plain(location.lng)}"

/**
 * The `geo:` URI any installed maps app opens, with a pin. [encode] is the
 * caller's URI encoder (android.net.Uri.encode on device), so this stays
 * JVM-testable.
 */
fun geoUri(location: ChatLocation, encode: (String) -> String): String {
    val at = "${plain(location.lat)},${plain(location.lng)}"
    val name = location.label ?: location.address
    val query = if (name.isNullOrBlank()) at else "$at(${encode(name)})"
    return "geo:$at?q=$query"
}

private fun plain(v: Double): String = java.math.BigDecimal.valueOf(roundCoord(v)).stripTrailingZeros().toPlainString()
