package com.us.android.core.food.repository

import com.us.android.core.food.network.AddCartItemRequest
import com.us.android.core.food.network.CancelOrderRequest
import com.us.android.core.food.network.FeastAddressDto
import com.us.android.core.food.network.FeastAddressRequest
import com.us.android.core.food.network.FeastApi
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.network.FeastInvoiceDto
import com.us.android.core.food.network.FeastMenuDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.FeastRestaurantDto
import com.us.android.core.food.network.FeastTrackingDto
import com.us.android.core.food.network.OrderPaymentDto
import com.us.android.core.food.network.PaymentIntentDto
import com.us.android.core.food.network.PaymentIntentRequest
import com.us.android.core.food.network.PlaceOrderRequest
import com.us.android.core.food.network.UpdateCartItemRequest
import com.us.android.core.network.ApiEnvelope
import kotlinx.serialization.json.Json
import retrofit2.Response
import java.util.UUID
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The customer's food data layer (Feast in Momentum, A5).
 *
 * Every expected 4xx/5xx is a [FoodResult.Failure]; nothing throws except
 * cancellation. Branch on [FoodError.code]; show [FoodError.serverMessage] when
 * the server wrote the words (serviceability and pricing refusals do).
 *
 * Deliberately NOT a payments type: `:core:food` ships in the partner apps too,
 * which must never carry `:core:payments`. The payment status source that
 * adapts [orderPayment] lives in `:feature:feast`.
 */
@Suppress("TooManyFunctions")
@Singleton
class FeastRepository @Inject constructor(
    private val api: FeastApi,
    private val json: Json,
) {

    suspend fun restaurants(query: String? = null, city: String? = null): FoodResult<List<FeastRestaurantDto>> =
        call { api.restaurants(query?.takeIf { it.isNotBlank() }, city?.takeIf { it.isNotBlank() }, LIST_LIMIT) }
            .map { it.items }

    suspend fun restaurant(restaurantId: String): FoodResult<FeastRestaurantDto> = call { api.restaurant(restaurantId) }

    suspend fun menu(restaurantId: String): FoodResult<FeastMenuDto> = call { api.menu(restaurantId) }

    suspend fun cart(): FoodResult<FeastCartDto> = call { api.cart() }

    suspend fun addToCart(request: AddCartItemRequest): FoodResult<FeastCartDto> = call { api.addCartItem(request) }

    suspend fun updateCartItem(cartItemId: String, quantity: Int): FoodResult<FeastCartDto> =
        call { api.updateCartItem(cartItemId, UpdateCartItemRequest(quantity = quantity)) }

    /** The route answers `{status}`, so the fresh cart is read back. */
    suspend fun removeCartItem(cartItemId: String): FoodResult<FeastCartDto> =
        when (val removed = call { api.removeCartItem(cartItemId) }) {
            is FoodResult.Failure -> removed
            is FoodResult.Success -> cart()
        }

    suspend fun addresses(): FoodResult<List<FeastAddressDto>> = call { api.addresses() }.map { it.items }

    suspend fun createAddress(request: FeastAddressRequest): FoodResult<FeastAddressDto> =
        call { api.createAddress(request) }

    suspend fun deleteAddress(addressId: String): FoodResult<Unit> = call { api.deleteAddress(addressId) }.map { }

    /** [idempotencyKey] is minted ONCE per customer decision and reused on every resend. */
    suspend fun placeOrder(idempotencyKey: String, request: PlaceOrderRequest): FoodResult<FeastOrderDto> =
        call { api.placeOrder(idempotencyKey, request) }

    suspend fun orders(): FoodResult<List<FeastOrderDto>> = call { api.orders() }.map { it.items }

    suspend fun order(orderId: String): FoodResult<FeastOrderDto> = call { api.order(orderId) }

    suspend fun tracking(orderId: String): FoodResult<FeastTrackingDto> = call { api.tracking(orderId) }

    suspend fun cancelOrder(orderId: String, reason: String): FoodResult<FeastOrderDto> =
        call { api.cancelOrder(orderId, CancelOrderRequest(reason)) }

    suspend fun invoice(orderId: String): FoodResult<FeastInvoiceDto> = call { api.invoice(orderId) }

    suspend fun orderPayment(orderId: String): FoodResult<OrderPaymentDto> = call { api.orderPayment(orderId) }

    /** [method] is `upi` or `card`. One [idempotencyKey] per payment attempt. */
    suspend fun createPaymentIntent(orderId: String, idempotencyKey: String, method: String): FoodResult<PaymentIntentDto> =
        call { api.createPaymentIntent(orderId, idempotencyKey, PaymentIntentRequest(method)) }

    fun newIdempotencyKey(): String = UUID.randomUUID().toString()

    private suspend fun <T> call(block: suspend () -> Response<ApiEnvelope<T>>): FoodResult<T> = foodCall(json, block)

    private companion object {
        const val LIST_LIMIT = 50
    }
}

/** The words the server wrote for a refusal, when it wrote any. */
val FoodError.serverMessage: String?
    get() = when (this) {
        is FoodError.InvalidField -> message
        is FoodError.Unexpected -> message
        else -> null
    }?.takeIf { it.isNotBlank() }
