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
     * true when the server wrote it (a refusal it returned, or the restaurant
     * routes' `unserviceable_message`), false when it is the app's wording for a
     * flag the server set (see [ServiceabilityRules]).
     */
    data class Blocked(val message: String, val code: String?, val fromServer: Boolean) : Serviceability
}

/**
 * The serviceability rules the customer app applies.
 *
 *  * With a delivery point, `GET /v1/food/restaurants[/:id]?lat=&lng=` judges
 *    each restaurant by the rule `POST /orders` applies and answers in
 *    `serviceable`, `unserviceable_reason_code` and `unserviceable_message`.
 *    Those fields ARE the answer whenever present ([fromServer]).
 *  * Without a point (no address picked, or one with no pin) they are absent,
 *    and only then does the app judge from the restaurant's flags: its status,
 *    its own open switch, the schedule (`is_open_now`) and
 *    `is_accepting_orders`.
 *  * A refusal the server returned at add-to-cart or `POST /orders`
 *    (422 FOOD_ADDRESS_OUT_OF_RANGE, FOOD_RESTAURANT_TAX_CATEGORY_MISSING, …) is
 *    remembered per address by [com.us.android.feature.feast.FeastSession] and
 *    blocks further adds with the SERVER'S OWN message ([resolve]).
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

    /**
     * The refusals the restaurant routes judge for a point. A fresh server
     * answer supersedes a remembered refusal with one of these codes; a pricing
     * or tax refusal is not judged there, so it stands.
     */
    val JUDGED_BY_RESTAURANT_ROUTES: Set<String> = setOf(
        "FOOD_RESTAURANT_NOT_ACCEPTING",
        "FOOD_RESTAURANT_OUTSIDE_HOURS",
        "FOOD_ADDRESS_OUT_OF_RANGE",
        "FOOD_ADDRESS_LOCATION_REQUIRED",
        "FOOD_RESTAURANT_LOCATION_MISSING",
    )

    const val OUTSIDE_HOURS = "FOOD_RESTAURANT_OUTSIDE_HOURS"
    const val NOT_ACCEPTING = "FOOD_RESTAURANT_NOT_ACCEPTING"

    /** The server's answer when it gave one, else the app's reading of the restaurant's flags. */
    fun of(restaurant: FeastRestaurantDto): Serviceability = fromServer(restaurant) ?: fromFlags(restaurant)

    /** [of], with a refusal the server returned earlier for this address ([remembered]) taken into account. */
    fun resolve(restaurant: FeastRestaurantDto, remembered: Serviceability.Blocked?): Serviceability {
        val server = fromServer(restaurant)
        return when {
            remembered == null -> server ?: fromFlags(restaurant)
            server != null && remembered.code in JUDGED_BY_RESTAURANT_ROUTES -> server
            else -> remembered
        }
    }

    /** The restaurant routes' own serviceability for the point sent, or null when no point was sent. */
    fun fromServer(restaurant: FeastRestaurantDto): Serviceability? {
        val serviceable = restaurant.serviceable ?: return null
        if (serviceable) return Serviceability.Open
        val message = restaurant.unserviceableMessage?.takeIf { it.isNotBlank() }
        return Serviceability.Blocked(
            message = message ?: "${displayName(restaurant)} can't take your order right now.",
            code = restaurant.unserviceableReasonCode?.takeIf { it.isNotBlank() },
            fromServer = message != null,
        )
    }

    /** The fallback when the server was not asked about a point. */
    private fun fromFlags(restaurant: FeastRestaurantDto): Serviceability = when {
        restaurant.status.isNotBlank() && restaurant.status != ACTIVE ->
            Serviceability.Blocked("${displayName(restaurant)} isn't taking orders.", null, fromServer = false)
        !restaurant.isOpen || restaurant.isOpenNow == false ->
            Serviceability.Blocked("${displayName(restaurant)} is closed right now.", null, fromServer = false)
        !restaurant.isAcceptingOrders ->
            Serviceability.Blocked("${displayName(restaurant)} isn't accepting orders right now.", null, fromServer = false)
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

    private fun displayName(restaurant: FeastRestaurantDto) = restaurant.name.ifBlank { "This restaurant" }

    private const val ACTIVE = "ACTIVE"
}
