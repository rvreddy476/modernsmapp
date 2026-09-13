package com.us.android.feature.rider.job

import android.content.ActivityNotFoundException
import android.content.Context
import android.content.Intent
import android.net.Uri
import com.us.android.core.food.network.DeliveryAssignmentDto
import javax.inject.Inject

/**
 * Where the rider navigates to for a job.
 *
 * ROUTE GAP (reported with A4): `GET /v1/food/delivery/assignments/current`
 * returns the restaurant's NAME but no coordinates, and nothing about the
 * customer's drop-off; no rider route exposes either. This interface is the
 * seam for when the assignment gains `pickup` / `drop` locations.
 */
interface JobLocations {
    /** The restaurant. Never null: the name at least supports a Maps search. */
    suspend fun restaurant(assignment: DeliveryAssignmentDto): NavTarget

    /** The customer's drop-off, or null when the server gives none. */
    suspend fun customer(assignment: DeliveryAssignmentDto): NavTarget?
}

/** Today's binding: names only, so the pickup hand-off is a Maps search and the drop has no target. */
class AssignmentOnlyJobLocations @Inject constructor() : JobLocations {
    override suspend fun restaurant(assignment: DeliveryAssignmentDto): NavTarget = NavTarget(label = assignment.restaurantName)

    override suspend fun customer(assignment: DeliveryAssignmentDto): NavTarget? = null
}

/**
 * Hands navigation to Google Maps: two-wheeler turn-by-turn when there are
 * coordinates, a `geo:` pin or search any maps app handles otherwise. False
 * when nothing on the phone can open either.
 */
fun Context.handOffNavigation(target: NavTarget): Boolean {
    NavigationHandoff.twoWheelerUri(target)?.let { uri ->
        val maps = Intent(Intent.ACTION_VIEW, Uri.parse(uri))
            .setPackage(NavigationHandoff.MAPS_PACKAGE)
            .addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
        try {
            startActivity(maps)
            return true
        } catch (e: ActivityNotFoundException) {
            // Google Maps is not installed; fall through to geo:.
        }
    }
    return try {
        startActivity(Intent(Intent.ACTION_VIEW, Uri.parse(NavigationHandoff.geoUri(target))).addFlags(Intent.FLAG_ACTIVITY_NEW_TASK))
        true
    } catch (e: ActivityNotFoundException) {
        false
    }
}
