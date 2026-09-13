package com.us.android.feature.kitchen.queue

import com.us.android.core.food.realtime.FoodRealtimeScope
import com.us.android.core.food.realtime.FoodRealtimeTokens
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
 * The scoped restaurant token (backend lane B5, wired in Feast A4):
 *
 *     POST /v1/food/realtime/token   {"scope": "restaurant", "id": "<restaurant id>"}
 *
 * The token lives five minutes, so :core:food's source fetches a fresh one for
 * every (re)connect rather than caching. When the route refuses (404 for a
 * restaurant that is not the caller's, 503 when realtime is not configured) the
 * source throws, [LiveOrderQueue] sees it and polls `GET …/kitchen-queue` every
 * 15 s — the queue is never without a transport.
 */
class ScopedRestaurantRealtimeTokens @Inject constructor(
    private val tokens: FoodRealtimeTokens,
) : RestaurantRealtimeTokens {
    override fun forRestaurant(restaurantId: String): RealtimeTokenSource =
        tokens.forScope(FoodRealtimeScope.Restaurant(restaurantId))
}
