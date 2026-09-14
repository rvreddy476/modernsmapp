package com.us.android.feature.feast.restaurant

import com.us.android.core.food.network.FeastRestaurantDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.code
import com.us.android.core.food.repository.serverMessage

/** Whether a restaurant can take this customer's order right now. */
sealed interface Serviceability {
    data object Open : Serviceability

    /**
     * Adding to the cart is blocked. [message] is shown as is; [fromServer] is
     * true when the server wrote it (a refusal it returned), false when it is
     * the app's wording for a flag the server set (see [ServiceabilityRules]).
     */
    data class Blocked(val message: String, val code: String?, val fromServer: Boolean) : Serviceability
}

/**
 * The serviceability rules the customer app applies.
 *
 * ROUTE GAP (A5): `GET /v1/food/restaurants[/:id]` carries `is_open`,
 * `is_accepting_orders` and `status` but no distance, no in-range flag and no
 * message, and takes no lat/lng. So:
 *
 *  * a restaurant the server flags closed or not accepting is blocked with the
 *    app's wording for that flag;
 *  * range, opening hours and pricing are decided at `POST /orders`, and a
 *    refusal there (422 FOOD_ADDRESS_OUT_OF_RANGE, FOOD_RESTAURANT_OUTSIDE_HOURS,
 *    …) is remembered by [com.us.android.feature.feast.FeastSession] and blocks
 *    further adds with the SERVER'S OWN message.
 */
object ServiceabilityRules {

    /** The 422/503 codes by which the server refuses an order for a reason adding more food cannot fix. */
    val REFUSAL_CODES: Set<String> = setOf(
        "FOOD_RESTAURANT_NOT_ACCEPTING",
        "FOOD_RESTAURANT_OUTSIDE_HOURS",
        "FOOD_ADDRESS_OUT_OF_RANGE",
        "FOOD_ADDRESS_LOCATION_REQUIRED",
        "FOOD_RESTAURANT_LOCATION_MISSING",
        "FOOD_RESTAURANT_TAX_CATEGORY_MISSING",
        "FOOD_RESTAURANT_STATE_UNKNOWN",
        "FOOD_RESTAURANT_GSTIN_MISSING",
        "FOOD_PLATFORM_GSTIN_NOT_CONFIGURED",
    )

    /** Codes whose refusal depends on the delivery address, so a different address clears them. */
    val ADDRESS_CODES: Set<String> = setOf("FOOD_ADDRESS_OUT_OF_RANGE", "FOOD_ADDRESS_LOCATION_REQUIRED")

    fun of(restaurant: FeastRestaurantDto): Serviceability = when {
        restaurant.status.isNotBlank() && restaurant.status != ACTIVE ->
            Serviceability.Blocked("${restaurant.name.ifBlank { "This restaurant" }} isn't taking orders.", null, fromServer = false)
        !restaurant.isOpen ->
            Serviceability.Blocked("${restaurant.name.ifBlank { "This restaurant" }} is closed right now.", null, fromServer = false)
        !restaurant.isAcceptingOrders ->
            Serviceability.Blocked(
                "${restaurant.name.ifBlank { "This restaurant" }} isn't accepting orders right now.",
                null,
                fromServer = false,
            )
        else -> Serviceability.Open
    }

    /** A server refusal as a block carrying the server's words, or null when [error] is not one. */
    fun fromRefusal(error: FoodError): Serviceability.Blocked? {
        val code = error.code ?: return null
        if (code !in REFUSAL_CODES) return null
        return Serviceability.Blocked(
            message = error.serverMessage ?: "This restaurant can't take your order right now.",
            code = code,
            fromServer = true,
        )
    }

    private const val ACTIVE = "ACTIVE"
}
