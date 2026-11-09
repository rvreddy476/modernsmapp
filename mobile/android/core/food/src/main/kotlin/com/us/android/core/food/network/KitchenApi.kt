package com.us.android.core.food.network

import com.us.android.core.network.ApiEnvelope
import retrofit2.Response
import retrofit2.http.Body
import retrofit2.http.DELETE
import retrofit2.http.GET
import retrofit2.http.Header
import retrofit2.http.PATCH
import retrofit2.http.POST
import retrofit2.http.Path
import retrofit2.http.Query

/**
 * food-service's restaurant partner routes for the Kitchen app: restaurants,
 * the menu, the order queue and transitions, pickup handover and earnings
 * (handler.go, handler_kitchen.go, handler_delivery_proof.go).
 *
 * Onboarding (compliance, location, hours, FSSAI, payout, submit, accepting)
 * stays on [FoodApi]. Every partner route acts only on the caller's own
 * restaurant; anything else is a 404.
 *
 * Order transitions REQUIRE an `Idempotency-Key` (400 IDEMPOTENCY_KEY_REQUIRED
 * without one). A retry of the same user action must reuse its key.
 */
@Suppress("TooManyFunctions")
interface KitchenApi {
    @POST("v1/food/partner/restaurants")
    suspend fun createRestaurant(@Body body: CreateKitchenRequest): Response<ApiEnvelope<PartnerRestaurantDto>>

    @GET("v1/food/partner/restaurants")
    suspend fun listRestaurants(): Response<ApiEnvelope<ItemsDto<PartnerRestaurantDto>>>

    @GET("v1/food/partner/restaurants/{restaurantId}")
    suspend fun getRestaurant(@Path("restaurantId") restaurantId: String): Response<ApiEnvelope<PartnerRestaurantDto>>

    // Menu

    @GET("v1/food/partner/restaurants/{restaurantId}/menu/categories")
    suspend fun listMenuCategories(
        @Path("restaurantId") restaurantId: String,
    ): Response<ApiEnvelope<ItemsDto<MenuCategoryDto>>>

    @POST("v1/food/partner/restaurants/{restaurantId}/menu/categories")
    suspend fun createMenuCategory(
        @Path("restaurantId") restaurantId: String,
        @Body body: MenuCategoryRequest,
    ): Response<ApiEnvelope<MenuCategoryDto>>

    @PATCH("v1/food/partner/menu/categories/{categoryId}")
    suspend fun updateMenuCategory(
        @Path("categoryId") categoryId: String,
        @Body body: MenuCategoryRequest,
    ): Response<ApiEnvelope<MenuCategoryDto>>

    @DELETE("v1/food/partner/menu/categories/{categoryId}")
    suspend fun deleteMenuCategory(@Path("categoryId") categoryId: String): Response<ApiEnvelope<StatusDto>>

    @POST("v1/food/partner/restaurants/{restaurantId}/menu/items")
    suspend fun createMenuItem(
        @Path("restaurantId") restaurantId: String,
        @Body body: MenuItemRequest,
    ): Response<ApiEnvelope<MenuItemDto>>

    @PATCH("v1/food/partner/menu/items/{itemId}")
    suspend fun updateMenuItem(
        @Path("itemId") itemId: String,
        @Body body: MenuItemRequest,
    ): Response<ApiEnvelope<MenuItemDto>>

    @DELETE("v1/food/partner/menu/items/{itemId}")
    suspend fun deleteMenuItem(@Path("itemId") itemId: String): Response<ApiEnvelope<StatusDto>>

    @PATCH("v1/food/partner/menu/items/{itemId}/availability")
    suspend fun setMenuItemAvailability(
        @Path("itemId") itemId: String,
        @Body body: AvailabilityRequest,
    ): Response<ApiEnvelope<StatusDto>>

    // Orders

    /**
     * The restaurant's orders, newest first (handler_partner_orders.go;
     * golden partner_orders_get_200_page.json). [statuses] narrows the list
     * (repeated `status=`; an unknown status is a 400, never ignored), [limit]
     * is 1..100 (100 by default), and [cursor] is `meta.next_cursor` from the
     * previous page, sent back untouched. `meta.next_cursor` is absent on the
     * last page. With no query the answer is the latest 100 orders of any status.
     */
    @GET("v1/food/partner/restaurants/{restaurantId}/orders")
    suspend fun listOrders(
        @Path("restaurantId") restaurantId: String,
        @Query("status") statuses: List<String>? = null,
        @Query("limit") limit: Int? = null,
        @Query("cursor") cursor: String? = null,
    ): Response<ApiEnvelope<ItemsDto<PartnerOrderDto>>>

    @GET("v1/food/partner/restaurants/{restaurantId}/kitchen-queue")
    suspend fun kitchenQueue(@Path("restaurantId") restaurantId: String): Response<ApiEnvelope<KitchenQueueDto>>

    /**
     * One order WITH its items (handler_b8.go GetPartnerOrder; golden
     * partner_order_get_200.json). 404 FOOD_NOT_FOUND unless the order is at
     * one of the caller's restaurants. The restaurant list route never carries
     * items, so the order detail reads this.
     */
    @GET("v1/food/partner/orders/{orderId}")
    suspend fun getOrder(@Path("orderId") orderId: String): Response<ApiEnvelope<PartnerOrderDto>>

    /** CONFIRMED → PREPARING. */
    @POST("v1/food/partner/orders/{orderId}/accept")
    suspend fun acceptOrder(
        @Path("orderId") orderId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: OrderStatusRequest,
    ): Response<ApiEnvelope<PartnerOrderDto>>

    /** CONFIRMED → RESTAURANT_REJECTED; a paid order's refund is requested in the same transaction. */
    @POST("v1/food/partner/orders/{orderId}/reject")
    suspend fun rejectOrder(
        @Path("orderId") orderId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: OrderStatusRequest,
    ): Response<ApiEnvelope<PartnerOrderDto>>

    /** CONFIRMED → PREPARING (the same edge as accept). */
    @POST("v1/food/partner/orders/{orderId}/mark-preparing")
    suspend fun markPreparing(
        @Path("orderId") orderId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: OrderStatusRequest,
    ): Response<ApiEnvelope<PartnerOrderDto>>

    /** PREPARING → READY_FOR_PICKUP, which chains to DELIVERY_ASSIGNING server-side. */
    @POST("v1/food/partner/orders/{orderId}/mark-ready")
    suspend fun markReady(
        @Path("orderId") orderId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: OrderStatusRequest,
    ): Response<ApiEnvelope<PartnerOrderDto>>

    /** The rider's pickup code; moves the order to PICKED_UP. */
    @POST("v1/food/partner/orders/{orderId}/verify-pickup")
    suspend fun verifyPickup(
        @Path("orderId") orderId: String,
        @Body body: VerifyPickupRequest,
    ): Response<ApiEnvelope<VerifyPickupDto>>

    // Earnings

    @GET("v1/food/partner/restaurants/{restaurantId}/reports/summary")
    suspend fun summary(@Path("restaurantId") restaurantId: String): Response<ApiEnvelope<RestaurantSummaryDto>>

    @GET("v1/food/partner/restaurants/{restaurantId}/settlements")
    suspend fun settlements(@Path("restaurantId") restaurantId: String): Response<ApiEnvelope<ItemsDto<SettlementDto>>>
}
