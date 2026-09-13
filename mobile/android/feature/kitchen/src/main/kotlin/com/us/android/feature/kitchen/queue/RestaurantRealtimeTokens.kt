package com.us.android.feature.kitchen.queue

import com.us.android.core.realtime.RealtimeTokenSource
import javax.inject.Inject

/** Issues the topic token for one restaurant's order stream. */
fun interface RestaurantRealtimeTokens {
    fun forRestaurant(restaurantId: String): RealtimeTokenSource
}

/** The notification-service topics a restaurant-scoped token grants (food-service realtime_token.go). */
object KitchenRealtimeTopics {
    fun forRestaurant(restaurantId: String): List<String> =
        listOf("food.restaurant.$restaurantId.orders", "food.restaurant.$restaurantId")
}

/**
 * ============================================================================
 *  WIRE AFTER B5 — the realtime token adapter is deliberately NOT implemented.
 * ============================================================================
 *
 * Backend lane B5 is replacing the token route with a scoped one:
 *
 *     POST /v1/food/realtime/token   {"scope": "restaurant", "id": "<restaurant id>"}
 *
 * Its request and response DTOs are intentionally not declared here, so nothing
 * pins a contract that is still moving. Until B5 commits, every token request
 * throws [RealtimeTokenNotWiredException]; [LiveOrderQueue] sees that and polls
 * `GET …/kitchen-queue` every 15 s, which is a working (if slower) queue.
 *
 * To wire it: implement [RestaurantRealtimeTokens.forRestaurant] with a
 * cached-until-refused source (the shape of :core:food's
 * FoodRealtimeTokenSource, but posting the scoped body — the token lives 5
 * minutes, so honour `forceRefresh`), and swap the binding in
 * `KitchenModule.bindRestaurantRealtimeTokens`. Nothing else changes.
 *
 * NOTE: :core:food's existing `FoodApi.realtimeToken()` posts NO body, and the
 * current food-service handler already answers that with 400 INVALID_BODY, so
 * it must not be used as a stopgap.
 */
class PendingB5RestaurantRealtimeTokens @Inject constructor() : RestaurantRealtimeTokens {
    override fun forRestaurant(restaurantId: String): RealtimeTokenSource =
        RealtimeTokenSource { throw RealtimeTokenNotWiredException() }
}

class RealtimeTokenNotWiredException :
    IllegalStateException("restaurant realtime token is not wired until backend lane B5 lands; polling instead")
