package com.us.android.feature.rider.offers

import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.core.food.repository.code
import com.us.android.core.food.repository.outcomeUnknown
import com.us.android.feature.rider.job.JobActions
import com.us.android.feature.rider.ui.userMessage

sealed interface AcceptOutcome {
    data object Accepted : AcceptOutcome

    data class Refused(val error: FoodError) : AcceptOutcome
}

/**
 * Accepting a job offer when the answer can be lost (food audit M3).
 *
 * food-service answers a replayed accept by the rider who already holds the
 * job with 200 (since 3c39a152d), so an accept whose outcome is unknown is
 * sent once more. If it still fails, the rider's current work is read: when it
 * holds this offer's order, the accept DID land and the rider has the job,
 * whatever the failed response said.
 */
class OfferAcceptance(private val rider: RiderRepository) {

    suspend fun accept(offerId: String, orderId: String): AcceptOutcome {
        var result = rider.acceptOffer(offerId)
        if (result is FoodResult.Failure && result.error.outcomeUnknown) result = rider.acceptOffer(offerId)
        return when (result) {
            is FoodResult.Success -> AcceptOutcome.Accepted
            is FoodResult.Failure ->
                if (holdsJobFor(orderId)) AcceptOutcome.Accepted else AcceptOutcome.Refused(result.error)
        }
    }

    private suspend fun holdsJobFor(orderId: String): Boolean {
        if (orderId.isBlank()) return false
        val current = rider.currentAssignment()
        if (current is FoodResult.Success && current.value?.orderId == orderId) return true
        // A batch's current job may be another stop; any active assignment for the order counts.
        val all = rider.assignments()
        return all is FoodResult.Success && all.value.any { it.orderId == orderId && JobActions.of(it).isActive }
    }

    companion object {
        const val GONE = "Another rider took this job, or its time ran out."

        /** What the rider is told when the server refused the accept (food-service 3c39a152d codes). */
        fun refusalMessage(error: FoodError): String = when (error.code) {
            "WORK_BUSY" -> "Finish your current delivery before taking another."
            "WORK_OFF_DUTY", "FOOD_DELIVERY_PARTNER_NOT_ACTIVE" -> "You're offline. Go online to take jobs."
            "WORK_EXPIRED", "WORK_STALE", "WORK_CONFLICT" ->
                "This job changed while you were answering. Check Home for the latest."
            "DELIVERY_ACCEPT_FAILED", "FOOD_ORDER_STATUS_CONFLICT", "FOOD_ORDER_TRANSITION_NOT_ALLOWED" -> GONE
            else -> if (error is FoodError.NotFound) GONE else error.userMessage()
        }
    }
}
