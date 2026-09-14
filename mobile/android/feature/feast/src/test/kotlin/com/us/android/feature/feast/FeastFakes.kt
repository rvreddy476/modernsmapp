package com.us.android.feature.feast

import android.app.Activity
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.AddCartItemRequest
import com.us.android.core.food.network.CancelOrderRequest
import com.us.android.core.food.network.FeastAddressDto
import com.us.android.core.food.network.FeastAddressRequest
import com.us.android.core.food.network.FeastApi
import com.us.android.core.food.network.FeastCartDto
import com.us.android.core.food.network.FeastCartItemDto
import com.us.android.core.food.network.FeastInvoiceDto
import com.us.android.core.food.network.FeastMenuDto
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.FeastRestaurantDto
import com.us.android.core.food.network.FeastTotalsPaiseDto
import com.us.android.core.food.network.FeastTrackingDto
import com.us.android.core.food.network.ItemsDto
import com.us.android.core.food.network.OrderPaymentDto
import com.us.android.core.food.network.PaymentIntentDto
import com.us.android.core.food.network.PaymentIntentRequest
import com.us.android.core.food.network.PlaceOrderRequest
import com.us.android.core.food.network.StatusDto
import com.us.android.core.food.network.UpdateCartItemRequest
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentLauncher
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentSession
import kotlinx.serialization.KSerializer
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import retrofit2.Response
import java.io.File
import java.io.IOException

val testJson = NetworkModule.provideJson()

fun <T> ok(value: T): Response<ApiEnvelope<T>> = Response.success(ApiEnvelope(data = value))

fun <T> refused(status: Int, code: String, message: String): Response<ApiEnvelope<T>> =
    Response.error(status, """{"error":{"code":"$code","message":"$message"},"meta":{}}""".toResponseBody("application/json".toMediaType()))

/** A golden fixture from :core:food's copies (byte-identical to food-service's), decoded as production does. */
fun <T> fixture(name: String, serializer: KSerializer<T>): T {
    val raw = File("../../core/food/src/test/resources/contracts/$name").readText()
    return checkNotNull(testJson.decodeFromString(ApiEnvelope.serializer(serializer), raw).data)
}

fun restaurant(
    id: String = "r-1",
    isOpen: Boolean = true,
    accepting: Boolean = true,
    status: String = "ACTIVE",
) = FeastRestaurantDto(id = id, name = "Test Kitchen", status = status, isOpen = isOpen, isAcceptingOrders = accepting)

fun pricedCart(restaurantId: String = "r-1") = FeastCartDto(
    id = "cart-1",
    restaurantId = restaurantId,
    restaurant = "Test Kitchen",
    items = listOf(FeastCartItemDto(id = "line-1", menuItemId = "m-1", name = "Paneer Tikka", quantity = 2)),
    totalsPaise = FeastTotalsPaiseDto(itemSubtotalPaise = Paise(50_000), finalAmountPaise = Paise(64_912)),
)

val pinnedAddress = FeastAddressDto(
    id = "addr-1",
    label = "Home",
    addressLine1 = "2 Test Road",
    city = "Bengaluru",
    latitude = 12.97,
    longitude = 77.59,
    isDefault = true,
)

fun payment(status: String, refund: String? = null) =
    ok(OrderPaymentDto(orderId = "order-1", status = status, amountMinor = 64_912, currency = "INR", refundStatus = refund))

/** A food-service that answers from fields the test sets, and records what it was asked. */
@Suppress("TooManyFunctions")
class FakeFeastApi : FeastApi {
    var restaurant: FeastRestaurantDto = restaurant()
    var menu = FeastMenuDto()
    var cart: FeastCartDto = pricedCart()
    var addresses: List<FeastAddressDto> = listOf(pinnedAddress)
    var order = FeastOrderDto(id = "order-1", orderNumber = "FG1", restaurantId = "r-1", status = "PAYMENT_PENDING")

    var addToCartResponse: () -> Response<ApiEnvelope<FeastCartDto>> = { ok(cart) }
    val addToCartRequests = mutableListOf<AddCartItemRequest>()

    var placeOrderResponse: () -> Response<ApiEnvelope<FeastOrderDto>> = { ok(order) }
    val placeOrderKeys = mutableListOf<String>()

    /** Each payment-status read takes the next answer; the last one repeats. */
    val paymentAnswers = ArrayDeque<() -> Response<ApiEnvelope<OrderPaymentDto>>>()
    var paymentReads = 0
        private set

    override suspend fun restaurants(query: String?, city: String?, limit: Int?) = ok(ItemsDto(listOf(restaurant)))

    override suspend fun restaurant(restaurantId: String) = ok(restaurant)

    override suspend fun menu(restaurantId: String) = ok(menu)

    override suspend fun cart() = ok(cart)

    override suspend fun addCartItem(body: AddCartItemRequest): Response<ApiEnvelope<FeastCartDto>> {
        addToCartRequests += body
        return addToCartResponse()
    }

    override suspend fun updateCartItem(cartItemId: String, body: UpdateCartItemRequest) = ok(cart)

    override suspend fun removeCartItem(cartItemId: String) = ok(StatusDto("removed"))

    override suspend fun clearCart() = ok(StatusDto("cleared"))

    override suspend fun addresses() = ok(ItemsDto(addresses))

    override suspend fun createAddress(body: FeastAddressRequest) = ok(pinnedAddress)

    override suspend fun updateAddress(addressId: String, body: FeastAddressRequest) = ok(pinnedAddress)

    override suspend fun deleteAddress(addressId: String) = ok(StatusDto("deleted"))

    override suspend fun placeOrder(idempotencyKey: String, body: PlaceOrderRequest): Response<ApiEnvelope<FeastOrderDto>> {
        placeOrderKeys += idempotencyKey
        return placeOrderResponse()
    }

    override suspend fun orders() = ok(ItemsDto(listOf(order)))

    override suspend fun order(orderId: String) = ok(order)

    override suspend fun tracking(orderId: String) = ok(FeastTrackingDto(orderId = orderId, status = order.status))

    override suspend fun cancelOrder(orderId: String, body: CancelOrderRequest) = ok(order)

    override suspend fun invoice(orderId: String): Response<ApiEnvelope<FeastInvoiceDto>> = refused(404, "FOOD_NOT_FOUND", "not found")

    override suspend fun orderPayment(orderId: String): Response<ApiEnvelope<OrderPaymentDto>> {
        paymentReads++
        val answer = if (paymentAnswers.size > 1) paymentAnswers.removeFirst() else paymentAnswers.firstOrNull()
        return answer?.invoke() ?: payment("confirming")
    }

    override suspend fun createPaymentIntent(
        orderId: String,
        idempotencyKey: String,
        body: PaymentIntentRequest,
    ): Response<ApiEnvelope<PaymentIntentDto>> = error("the checkout ViewModel never opens an intent; the Activity's opener does")

    fun repository() = FeastRepository(this, testJson)
}

/** Status reads that throw like a dropped connection. */
fun <T> offline(): Response<ApiEnvelope<T>> = throw IOException("offline")

/** The coordinator with a launcher that must never be asked to open: checkout only CONFIRMS. */
fun confirmingOnlyCoordinator() = PaymentCoordinator(
    object : PaymentLauncher {
        override fun open(activity: Activity, attempt: PaymentAttempt, session: PaymentSession, onOutcome: (PaymentOutcome) -> Unit) =
            error("the checkout ViewModel must never open a sheet itself")

        override fun abandon(attempt: PaymentAttempt) = Unit
    },
)
