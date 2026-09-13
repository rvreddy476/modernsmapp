package com.us.android.core.food.repository

import com.us.android.core.food.network.AvailabilityRequest
import com.us.android.core.food.network.KitchenApi
import com.us.android.core.food.network.KitchenOrderDto
import com.us.android.core.food.network.MenuCategoryDto
import com.us.android.core.food.network.MenuCategoryRequest
import com.us.android.core.food.network.MenuItemDto
import com.us.android.core.food.network.MenuItemRequest
import com.us.android.core.food.network.OrderStatusRequest
import com.us.android.core.food.network.PartnerOrderDto
import com.us.android.core.food.network.PartnerRestaurantDto
import com.us.android.core.food.network.RestaurantSummaryDto
import com.us.android.core.food.network.SettlementDto
import com.us.android.core.food.network.VerifyPickupDto
import com.us.android.core.food.network.VerifyPickupRequest
import kotlinx.serialization.json.Json
import javax.inject.Inject
import javax.inject.Singleton

/** The restaurant's four order transitions. Each maps to one route and one edge. */
enum class KitchenOrderAction {
    /** CONFIRMED → PREPARING. */
    ACCEPT,

    /** CONFIRMED → RESTAURANT_REJECTED. */
    REJECT,

    /** CONFIRMED → PREPARING. */
    MARK_PREPARING,

    /** PREPARING → READY_FOR_PICKUP. */
    MARK_READY,
}

/**
 * The restaurant partner's data layer beyond onboarding: restaurants, menu,
 * orders, pickup handover and earnings. Typed failures, never exceptions —
 * see [foodCall].
 */
@Suppress("TooManyFunctions")
@Singleton
class KitchenRepository @Inject constructor(
    private val api: KitchenApi,
    private val json: Json,
) {

    suspend fun restaurants(): FoodResult<List<PartnerRestaurantDto>> =
        foodCall(json) { api.listRestaurants() }.map { it.items }

    suspend fun restaurant(restaurantId: String): FoodResult<PartnerRestaurantDto> =
        foodCall(json) { api.getRestaurant(restaurantId) }

    suspend fun menuCategories(restaurantId: String): FoodResult<List<MenuCategoryDto>> =
        foodCall(json) { api.listMenuCategories(restaurantId) }.map { it.items }

    suspend fun createMenuCategory(restaurantId: String, request: MenuCategoryRequest): FoodResult<MenuCategoryDto> =
        foodCall(json) { api.createMenuCategory(restaurantId, request) }

    suspend fun updateMenuCategory(categoryId: String, request: MenuCategoryRequest): FoodResult<MenuCategoryDto> =
        foodCall(json) { api.updateMenuCategory(categoryId, request) }

    suspend fun deleteMenuCategory(categoryId: String): FoodResult<Unit> =
        foodCall(json) { api.deleteMenuCategory(categoryId) }.map { }

    suspend fun createMenuItem(restaurantId: String, request: MenuItemRequest): FoodResult<MenuItemDto> =
        foodCall(json) { api.createMenuItem(restaurantId, request) }

    suspend fun updateMenuItem(itemId: String, request: MenuItemRequest): FoodResult<MenuItemDto> =
        foodCall(json) { api.updateMenuItem(itemId, request) }

    suspend fun deleteMenuItem(itemId: String): FoodResult<Unit> =
        foodCall(json) { api.deleteMenuItem(itemId) }.map { }

    suspend fun setMenuItemAvailability(itemId: String, available: Boolean): FoodResult<Unit> =
        foodCall(json) { api.setMenuItemAvailability(itemId, AvailabilityRequest(available)) }.map { }

    suspend fun orders(restaurantId: String): FoodResult<List<PartnerOrderDto>> =
        foodCall(json) { api.listOrders(restaurantId) }.map { it.items }

    suspend fun kitchenQueue(restaurantId: String): FoodResult<List<KitchenOrderDto>> =
        foodCall(json) { api.kitchenQueue(restaurantId) }.map { it.orders }

    /** [idempotencyKey] must be reused when the SAME user action is retried. */
    suspend fun transition(
        orderId: String,
        action: KitchenOrderAction,
        idempotencyKey: String,
        reason: String? = null,
    ): FoodResult<PartnerOrderDto> {
        val body = OrderStatusRequest(reason?.takeIf { it.isNotBlank() })
        return foodCall(json) {
            when (action) {
                KitchenOrderAction.ACCEPT -> api.acceptOrder(orderId, idempotencyKey, body)
                KitchenOrderAction.REJECT -> api.rejectOrder(orderId, idempotencyKey, body)
                KitchenOrderAction.MARK_PREPARING -> api.markPreparing(orderId, idempotencyKey, body)
                KitchenOrderAction.MARK_READY -> api.markReady(orderId, idempotencyKey, body)
            }
        }
    }

    suspend fun verifyPickup(orderId: String, code: String): FoodResult<VerifyPickupDto> =
        foodCall(json) { api.verifyPickup(orderId, VerifyPickupRequest(code.trim())) }

    suspend fun summary(restaurantId: String): FoodResult<RestaurantSummaryDto> =
        foodCall(json) { api.summary(restaurantId) }

    suspend fun settlements(restaurantId: String): FoodResult<List<SettlementDto>> =
        foodCall(json) { api.settlements(restaurantId) }.map { it.items }
}
