package com.us.android.feature.rider.job

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import com.us.android.core.food.network.DeliveryAssignmentDto
import java.net.URLEncoder

/** A maps hand-off: a URI for `Intent.ACTION_VIEW`, and where it came from. */
data class NavIntent(val uri: String, val source: Source) {
    enum class Source {
        /** food-service's Google Maps two-wheeler link (`navigation.pickup_url` / `drop_url`). */
        SERVER_LINK,

        /** A `geo:` pin from the coordinates, when the server sent no link. */
        COORDINATES,

        /** A `geo:` search by the restaurant's name: no link and no pin. */
        NAME_SEARCH,
    }
}

/** Where the rider can navigate for a job. Null means no button. */
data class JobNavigation(val pickup: NavIntent?, val drop: NavIntent?) {
    companion object {
        val NONE = JobNavigation(pickup = null, drop = null)
    }
}

/**
 * Maps an assignment to its navigation hand-offs. Pure.
 *
 *  - a finished job (delivered, cancelled, released, failed) has none;
 *  - pickup: the server's `pickup_url`, else a `geo:` pin from the restaurant's
 *    coordinates, else a search by the restaurant's name;
 *  - drop: ONLY while the assignment carries `drop` (from ACCEPTED until the
 *    job closes): the server's `drop_url`, else a `geo:` pin from the drop's
 *    coordinates, else nothing.
 *
 * Only `https:` links are taken from the server; anything else falls back as if
 * absent, so a payload can never hand an arbitrary scheme to ACTION_VIEW.
 */
object JobLocations {

    fun of(assignment: DeliveryAssignmentDto): JobNavigation {
        if (!JobActions.of(assignment).isActive) return JobNavigation.NONE
        return JobNavigation(pickup = pickup(assignment), drop = drop(assignment))
    }

    private fun pickup(assignment: DeliveryAssignmentDto): NavIntent {
        serverLink(assignment.navigation?.pickupUrl)?.let { return NavIntent(it, NavIntent.Source.SERVER_LINK) }
        val name = assignment.restaurant?.name?.takeIf { it.isNotBlank() } ?: assignment.restaurantName
        val restaurant = assignment.restaurant
        geo(restaurant?.latitude, restaurant?.longitude, label = name)?.let { return NavIntent(it, NavIntent.Source.COORDINATES) }
        return NavIntent("geo:0,0?q=${encode(name)}", NavIntent.Source.NAME_SEARCH)
    }

    private fun drop(assignment: DeliveryAssignmentDto): NavIntent? {
        val drop = assignment.drop ?: return null
        serverLink(assignment.navigation?.dropUrl)?.let { return NavIntent(it, NavIntent.Source.SERVER_LINK) }
        return geo(drop.latitude, drop.longitude, label = null)?.let { NavIntent(it, NavIntent.Source.COORDINATES) }
    }

    private fun serverLink(url: String?): String? = url?.trim()?.takeIf { it.startsWith("https://") }

    /** A pin, or null for a missing, out-of-range or 0,0 coordinate. The drop pin carries no label. */
    private fun geo(lat: Double?, lng: Double?, label: String?): String? {
        if (lat == null || lng == null || (lat == 0.0 && lng == 0.0)) return null
        if (lat !in -MAX_LAT..MAX_LAT || lng !in -MAX_LNG..MAX_LNG) return null
        val suffix = label?.takeIf { it.isNotBlank() }?.let { "(${encode(it)})" }.orEmpty()
        return "geo:$lat,$lng?q=$lat,$lng$suffix"
    }

    private fun encode(text: String): String = URLEncoder.encode(text, "UTF-8")

    private const val MAX_LAT = 90.0
    private const val MAX_LNG = 180.0
}

/** Opens a maps hand-off. False when nothing on the phone can open it; never throws. */
fun Context.openNavigation(intent: NavIntent): Boolean = startSafely(Intent(Intent.ACTION_VIEW, Uri.parse(intent.uri)))

/** Opens the dialler with the number filled in. ACTION_DIAL needs no call permission. */
fun Context.dialNumber(phone: String): Boolean = startSafely(Intent(Intent.ACTION_DIAL, Uri.fromParts("tel", phone, null)))

private fun Context.startSafely(intent: Intent): Boolean = try {
    startActivity(intent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
    true
} catch (e: ActivityNotFoundException) {
    false
} catch (e: SecurityException) {
    false
}
