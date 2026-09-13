package com.us.android.feature.kitchen.gate

import com.us.android.core.food.model.FoodCapabilities
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult

/**
 * Who may use Feast Kitchen, decided from `GET /v1/food/me/capabilities`.
 *
 * FAILS CLOSED. Only an explicit `is_restaurant_owner: true` opens the app.
 * Being an admin, a moderator or a delivery partner does not, and neither does
 * a failed call — an error is [Unavailable] with a retry, never a guess that
 * lets a non-partner into partner screens whose every route would 404.
 */
sealed interface RoleGate {

    data object Checking : RoleGate

    data class RestaurantPartner(val userId: String) : RoleGate

    /** Signed in, but owns no restaurant: the friendly "this app is for restaurant partners" screen. */
    data object NotRestaurantPartner : RoleGate

    /** The session is gone; the shell returns to sign-in. */
    data object SessionExpired : RoleGate

    data class Unavailable(val error: FoodError) : RoleGate

    companion object {
        fun from(result: FoodResult<FoodCapabilities>): RoleGate = when (result) {
            is FoodResult.Success -> if (result.value.isRestaurantOwner) {
                RestaurantPartner(result.value.userId)
            } else {
                NotRestaurantPartner
            }
            is FoodResult.Failure -> when (result.error) {
                FoodError.Unauthorized -> SessionExpired
                else -> Unavailable(result.error)
            }
        }
    }
}
