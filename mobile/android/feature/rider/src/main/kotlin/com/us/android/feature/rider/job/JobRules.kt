package com.us.android.feature.rider.job

import com.us.android.core.food.network.DeliveryAssignmentDto
import com.us.android.core.food.network.VerifyDeliveryDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderAssignmentStep
import com.us.android.core.food.repository.code
import java.net.URLEncoder

/** Where the job stands, from the assignment's status (food-service deliveryTransitionAllowed). */
enum class JobPhase {
    /** ASSIGNED: the offer was taken; the rider confirms the job. */
    CONFIRM,

    /** ACCEPTED: riding to the restaurant. The pickup code is visible. */
    TO_RESTAURANT,

    /** ARRIVED_AT_RESTAURANT: the kitchen enters the rider's pickup code. */
    AT_RESTAURANT,

    /** PICKED_UP: riding to the customer. */
    TO_CUSTOMER,

    /** ARRIVED_AT_CUSTOMER: the rider enters the customer's code. */
    AT_CUSTOMER,

    DELIVERED,

    /** Released, cancelled or failed — nothing left to do. */
    ENDED,
    ;

    companion object {
        fun of(status: String): JobPhase = when (status) {
            "ASSIGNED" -> CONFIRM
            "ACCEPTED" -> TO_RESTAURANT
            "ARRIVED_AT_RESTAURANT" -> AT_RESTAURANT
            "PICKED_UP" -> TO_CUSTOMER
            "ARRIVED_AT_CUSTOMER" -> AT_CUSTOMER
            "DELIVERED" -> DELIVERED
            else -> ENDED
        }
    }
}

/** What the active-job screen offers in each phase. Pure. */
data class JobActions(
    val phase: JobPhase,
    /** The rider's own next step button, if any. PICKED_UP and DELIVERED are never here. */
    val step: RiderAssignmentStep?,
    val canRelease: Boolean,
    /** Shown to the kitchen; only while the server returns it (after accept, before pickup). */
    val pickupCode: String?,
    val navigateToRestaurant: Boolean,
    val navigateToCustomer: Boolean,
    val canEnterDeliveryCode: Boolean,
) {
    val isActive: Boolean get() = phase != JobPhase.DELIVERED && phase != JobPhase.ENDED

    companion object {
        fun of(assignment: DeliveryAssignmentDto): JobActions {
            val phase = JobPhase.of(assignment.status)
            return JobActions(
                phase = phase,
                step = when (phase) {
                    JobPhase.CONFIRM -> RiderAssignmentStep.ACCEPT
                    JobPhase.TO_RESTAURANT -> RiderAssignmentStep.ARRIVED_AT_RESTAURANT
                    JobPhase.TO_CUSTOMER -> RiderAssignmentStep.ARRIVED_AT_CUSTOMER
                    else -> null
                },
                canRelease = phase == JobPhase.CONFIRM || phase == JobPhase.TO_RESTAURANT || phase == JobPhase.AT_RESTAURANT,
                pickupCode = assignment.pickupCode?.takeIf {
                    it.isNotBlank() && (phase == JobPhase.TO_RESTAURANT || phase == JobPhase.AT_RESTAURANT)
                },
                navigateToRestaurant = phase == JobPhase.CONFIRM || phase == JobPhase.TO_RESTAURANT,
                navigateToCustomer = phase == JobPhase.TO_CUSTOMER || phase == JobPhase.AT_CUSTOMER,
                canEnterDeliveryCode = phase == JobPhase.TO_CUSTOMER || phase == JobPhase.AT_CUSTOMER,
            )
        }
    }
}

/** The verify-delivery result, as the drop screen shows it. */
sealed interface DeliveryCodeOutcome {
    data object Delivered : DeliveryCodeOutcome

    /** 422 FOOD_DELIVERY_CODE_INVALID: ask the customer to read it again. */
    data object WrongCode : DeliveryCodeOutcome

    /** 429 FOOD_DELIVERY_CODE_ATTEMPTS_EXCEEDED: five wrong codes; support must help. */
    data object Locked : DeliveryCodeOutcome

    /** 409 FOOD_DELIVERY_ASSIGNMENT_NOT_READY: the kitchen has not confirmed pickup yet. */
    data object NotPickedUp : DeliveryCodeOutcome

    /** 404: the job is no longer this rider's. */
    data object NotYourJob : DeliveryCodeOutcome

    /** 403 FOOD_DELIVERY_PARTNER_NOT_ACTIVE. */
    data object NotActive : DeliveryCodeOutcome

    data class Failed(val error: FoodError) : DeliveryCodeOutcome

    companion object {
        fun from(result: FoodResult<VerifyDeliveryDto>): DeliveryCodeOutcome = when (result) {
            is FoodResult.Success -> Delivered
            is FoodResult.Failure -> when {
                result.error == FoodError.NotFound -> NotYourJob
                else -> when (result.error.code) {
                    "FOOD_DELIVERY_CODE_INVALID" -> WrongCode
                    "FOOD_DELIVERY_CODE_ATTEMPTS_EXCEEDED" -> Locked
                    "FOOD_DELIVERY_ASSIGNMENT_NOT_READY" -> NotPickedUp
                    "FOOD_DELIVERY_PARTNER_NOT_ACTIVE" -> NotActive
                    else -> Failed(result.error)
                }
            }
        }
    }
}

/** A place to navigate to. Coordinates are null when the server gave none (see JobLocations). */
data class NavTarget(val label: String, val latitude: Double? = null, val longitude: Double? = null) {
    val hasCoordinates: Boolean get() = latitude != null && longitude != null
}

/**
 * Hand-off to Google Maps. The app draws no map of its own (maps-compose is not
 * available offline); turn-by-turn belongs to Maps.
 */
object NavigationHandoff {
    const val MAPS_PACKAGE = "com.google.android.apps.maps"

    /** Two-wheeler navigation (`mode=l`), straight into turn-by-turn. Null without coordinates. */
    fun twoWheelerUri(target: NavTarget): String? {
        val lat = target.latitude ?: return null
        val lng = target.longitude ?: return null
        return "google.navigation:q=$lat,$lng&mode=l"
    }

    /** The generic fallback any maps app handles: a pin, or a search by name when there are no coordinates. */
    fun geoUri(target: NavTarget): String {
        val label = URLEncoder.encode(target.label, "UTF-8")
        val lat = target.latitude
        val lng = target.longitude
        return if (lat != null && lng != null) "geo:$lat,$lng?q=$lat,$lng($label)" else "geo:0,0?q=$label"
    }
}
