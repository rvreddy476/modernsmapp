package com.us.android.feature.rider.gate

import com.us.android.core.food.model.FoodCapabilities
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult

/**
 * Who may use Feast Rider, decided from `GET /v1/food/me/capabilities`.
 *
 * FAILS CLOSED. Only an explicit `is_delivery_partner: true` (a delivery
 * partner profile exists) opens the rider screens. Admin, moderator and
 * restaurant owner do not, and neither does a failed call.
 *
 * A signed-in non-partner is not a dead end: `POST /v1/food/delivery/profile`
 * exists, so the gate offers "become a rider".
 */
sealed interface RiderGate {

    data object Checking : RiderGate

    data class DeliveryPartner(val userId: String) : RiderGate

    /** Signed in without a partner profile: the friendly screen with "become a rider". */
    data class NotDeliveryPartner(val userId: String) : RiderGate

    data object SessionExpired : RiderGate

    data class Unavailable(val error: FoodError) : RiderGate

    companion object {
        fun from(result: FoodResult<FoodCapabilities>): RiderGate = when (result) {
            is FoodResult.Success -> if (result.value.isDeliveryPartner) {
                DeliveryPartner(result.value.userId)
            } else {
                NotDeliveryPartner(result.value.userId)
            }
            is FoodResult.Failure -> when (result.error) {
                FoodError.Unauthorized -> SessionExpired
                else -> Unavailable(result.error)
            }
        }
    }
}
