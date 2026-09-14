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
 * food-service's CUSTOMER routes — Feast inside Momentum (A5).
 *
 * Every route here exists in handler.go / handler_invoice.go at c1b45af4. The
 * shapes of the ones with golden fixtures are pinned by FoodContractFixtureTest;
 * the rest (restaurants, menu, addresses, order list, cancel, tracking) have NO
 * golden fixture yet, a backend gap reported with A5, so their DTOs decode
 * leniently like every other production read.
 *
 * Idempotency: `POST /orders` and `POST /orders/:id/payments/intents` REQUIRE an
 * `Idempotency-Key` header (400 IDEMPOTENCY_KEY_REQUIRED otherwise).
 */
@Suppress("TooManyFunctions")
interface FeastApi {

    // Discovery

    /** `q` and `city` are the only filters the route takes — there is no lat/lng. */
    @GET("v1/food/restaurants")
    suspend fun restaurants(
        @Query("q") query: String? = null,
        @Query("city") city: String? = null,
        @Query("limit") limit: Int? = null,
    ): Response<ApiEnvelope<ItemsDto<FeastRestaurantDto>>>

    @GET("v1/food/restaurants/{restaurantId}")
    suspend fun restaurant(@Path("restaurantId") restaurantId: String): Response<ApiEnvelope<FeastRestaurantDto>>

    @GET("v1/food/restaurants/{restaurantId}/menu")
    suspend fun menu(@Path("restaurantId") restaurantId: String): Response<ApiEnvelope<FeastMenuDto>>

    // Cart

    @GET("v1/food/cart")
    suspend fun cart(): Response<ApiEnvelope<FeastCartDto>>

    /** 201 with the cart; 409 FOOD_CART_RESTAURANT_CONFLICT unless `clear_existing`. */
    @POST("v1/food/cart/items")
    suspend fun addCartItem(@Body body: AddCartItemRequest): Response<ApiEnvelope<FeastCartDto>>

    @PATCH("v1/food/cart/items/{cartItemId}")
    suspend fun updateCartItem(
        @Path("cartItemId") cartItemId: String,
        @Body body: UpdateCartItemRequest,
    ): Response<ApiEnvelope<FeastCartDto>>

    @DELETE("v1/food/cart/items/{cartItemId}")
    suspend fun removeCartItem(@Path("cartItemId") cartItemId: String): Response<ApiEnvelope<StatusDto>>

    @DELETE("v1/food/cart")
    suspend fun clearCart(): Response<ApiEnvelope<StatusDto>>

    // Addresses

    @GET("v1/food/addresses")
    suspend fun addresses(): Response<ApiEnvelope<ItemsDto<FeastAddressDto>>>

    @POST("v1/food/addresses")
    suspend fun createAddress(@Body body: FeastAddressRequest): Response<ApiEnvelope<FeastAddressDto>>

    @PATCH("v1/food/addresses/{addressId}")
    suspend fun updateAddress(
        @Path("addressId") addressId: String,
        @Body body: FeastAddressRequest,
    ): Response<ApiEnvelope<FeastAddressDto>>

    @DELETE("v1/food/addresses/{addressId}")
    suspend fun deleteAddress(@Path("addressId") addressId: String): Response<ApiEnvelope<StatusDto>>

    // Orders

    @POST("v1/food/orders")
    suspend fun placeOrder(
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: PlaceOrderRequest,
    ): Response<ApiEnvelope<FeastOrderDto>>

    @GET("v1/food/orders")
    suspend fun orders(): Response<ApiEnvelope<ItemsDto<FeastOrderDto>>>

    @GET("v1/food/orders/{orderId}")
    suspend fun order(@Path("orderId") orderId: String): Response<ApiEnvelope<FeastOrderDto>>

    @GET("v1/food/orders/{orderId}/tracking")
    suspend fun tracking(@Path("orderId") orderId: String): Response<ApiEnvelope<FeastTrackingDto>>

    @POST("v1/food/orders/{orderId}/cancel")
    suspend fun cancelOrder(
        @Path("orderId") orderId: String,
        @Body body: CancelOrderRequest,
    ): Response<ApiEnvelope<FeastOrderDto>>

    @GET("v1/food/orders/{orderId}/invoice")
    suspend fun invoice(@Path("orderId") orderId: String): Response<ApiEnvelope<FeastInvoiceDto>>

    // Payment

    /** The poll: `confirming | paid | failed` plus `refund_status`. Paid only after the signed webhook. */
    @GET("v1/food/orders/{orderId}/payment")
    suspend fun orderPayment(@Path("orderId") orderId: String): Response<ApiEnvelope<OrderPaymentDto>>

    @POST("v1/food/orders/{orderId}/payments/intents")
    suspend fun createPaymentIntent(
        @Path("orderId") orderId: String,
        @Header("Idempotency-Key") idempotencyKey: String,
        @Body body: PaymentIntentRequest,
    ): Response<ApiEnvelope<PaymentIntentDto>>
}
