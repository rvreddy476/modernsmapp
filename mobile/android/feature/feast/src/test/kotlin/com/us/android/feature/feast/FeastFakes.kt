package com.us.android.feature.feast

import android.app.Activity
import com.us.android.core.food.model.Paise
import com.us.android.core.food.network.AddCartItemRequest
import com.us.android.core.food.network.CancelOrderRequest
import com.us.android.core.food.network.CreateRefundRequest
import com.us.android.core.food.network.CreateSupportTicketRequest
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
import com.us.android.core.food.network.RefundRequestDto
import com.us.android.core.food.network.StatusDto
import com.us.android.core.food.network.SupportTicketDto
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

private fun fixtureText(name: String): String = File("../../core/food/src/test/resources/contracts/$name").readText()

/** A golden fixture from :core:food's copies (byte-identical to food-service's), decoded as production does. */
fun <T> fixture(name: String, serializer: KSerializer<T>): T =
    checkNotNull(testJson.decodeFromString(ApiEnvelope.serializer(serializer), fixtureText(name)).data)

/** A golden error fixture served with its HTTP [status], exactly as the server wrote it. */
fun <T> refusedWithFixture(status: Int, name: String): Response<ApiEnvelope<T>> =
    Response.error(status, fixtureText(name).toResponseBody("application/json".toMediaType()))

/** What the home screen asked the restaurant list for. */
data class RestaurantListRequest(val query: String?, val city: String?, val lat: Double?, val lng: Double?)

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
    var cartReads = 0
        private set
    var addresses: List<FeastAddressDto> = listOf(pinnedAddress)
    var order = FeastOrderDto(id = "order-1", orderNumber = "FG1", restaurantId = "r-1", status = "PAYMENT_PENDING")

    var addToCartResponse: () -> Response<ApiEnvelope<FeastCartDto>> = { ok(cart) }
    val addToCartRequests = mutableListOf<AddCartItemRequest>()

    var placeOrderResponse: () -> Response<ApiEnvelope<FeastOrderDto>> = { ok(order) }
    val placeOrderKeys = mutableListOf<String>()
    val placeOrderBodies = mutableListOf<PlaceOrderRequest>()

    /** Each payment-status read takes the next answer; the last one repeats. */
    val paymentAnswers = ArrayDeque<() -> Response<ApiEnvelope<OrderPaymentDto>>>()
    var paymentReads = 0
        private set

    /** The list the restaurant route serves; null serves [restaurant] alone. */
    var restaurants: List<FeastRestaurantDto>? = null
    val restaurantListRequests = mutableListOf<RestaurantListRequest>()
    val restaurantDetailPoints = mutableListOf<Pair<Double?, Double?>>()

    override suspend fun restaurants(query: String?, city: String?, lat: Double?, lng: Double?, limit: Int?):
        Response<ApiEnvelope<ItemsDto<FeastRestaurantDto>>> {
        restaurantListRequests += RestaurantListRequest(query, city, lat, lng)
        return ok(ItemsDto(restaurants ?: listOf(restaurant)))
    }

    override suspend fun restaurant(restaurantId: String, lat: Double?, lng: Double?): Response<ApiEnvelope<FeastRestaurantDto>> {
        restaurantDetailPoints += lat to lng
        return ok(restaurant)
    }

    override suspend fun menu(restaurantId: String) = ok(menu)

    override suspend fun cart(): Response<ApiEnvelope<FeastCartDto>> {
        cartReads++
        return ok(cart)
    }

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
        placeOrderBodies += body
        return placeOrderResponse()
    }

    override suspend fun orders() = ok(ItemsDto(listOf(order)))

    var orderReads = 0
        private set
    var trackingReads = 0
        private set

    override suspend fun order(orderId: String): Response<ApiEnvelope<FeastOrderDto>> {
        orderReads++
        return ok(order)
    }

    override suspend fun tracking(orderId: String): Response<ApiEnvelope<FeastTrackingDto>> {
        trackingReads++
        return ok(FeastTrackingDto(orderId = orderId, status = order.status))
    }

    var cancelResponse: () -> Response<ApiEnvelope<FeastOrderDto>> = { ok(order) }
    var cancelCalls = 0
        private set

    override suspend fun cancelOrder(orderId: String, body: CancelOrderRequest): Response<ApiEnvelope<FeastOrderDto>> {
        cancelCalls++
        return cancelResponse()
    }

    var ticketResponse: () -> Response<ApiEnvelope<SupportTicketDto>> =
        { ok(SupportTicketDto(id = "ticket-1", orderId = order.id)) }
    val ticketRequests = mutableListOf<CreateSupportTicketRequest>()
    val ticketKeys = mutableListOf<String>()

    override suspend fun createSupportTicket(
        idempotencyKey: String,
        body: CreateSupportTicketRequest,
    ): Response<ApiEnvelope<SupportTicketDto>> {
        ticketKeys += idempotencyKey
        ticketRequests += body
        return ticketResponse()
    }

    var refundResponse: () -> Response<ApiEnvelope<RefundRequestDto>> =
        { ok(RefundRequestDto(id = "refund-1", status = "requested")) }
    val refundRequests = mutableListOf<CreateRefundRequest>()
    val refundKeys = mutableListOf<String>()

    override suspend fun requestRefund(
        idempotencyKey: String,
        body: CreateRefundRequest,
    ): Response<ApiEnvelope<RefundRequestDto>> {
        refundKeys += idempotencyKey
        refundRequests += body
        return refundResponse()
    }

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
