package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.AddToCartRequest
import com.us.android.core.commerce.network.AddressDto
import com.us.android.core.commerce.network.AdjustStockRequest
import com.us.android.core.commerce.network.CancelOrderRequest
import com.us.android.core.commerce.network.CartDto
import com.us.android.core.commerce.network.CheckoutRequest
import com.us.android.core.commerce.network.CheckoutResultDto
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.commerce.network.CreateProductRequest
import com.us.android.core.commerce.network.OrderDto
import com.us.android.core.commerce.network.OrderListDto
import com.us.android.core.commerce.network.PaymentHandleDto
import com.us.android.core.commerce.network.PaymentStatusDto
import com.us.android.core.commerce.network.PayoutRequest
import com.us.android.core.commerce.network.ProductDetailDto
import com.us.android.core.commerce.network.ProductListDto
import com.us.android.core.commerce.network.QuoteDto
import com.us.android.core.commerce.network.QuoteRequest
import com.us.android.core.commerce.network.ReadinessDto
import com.us.android.core.commerce.network.SaveDocumentsRequest
import com.us.android.core.commerce.network.SellerAddressRequest
import com.us.android.core.commerce.network.SellerProductDto
import com.us.android.core.commerce.network.SellerProductsDto
import com.us.android.core.commerce.network.SellerProfileDto
import com.us.android.core.commerce.network.SellerVariantDto
import com.us.android.core.commerce.network.StartSellingRequest
import com.us.android.core.commerce.network.StockDto
import com.us.android.core.commerce.network.TaxClassListDto
import com.us.android.core.commerce.network.UpdateCartItemRequest
import com.us.android.core.commerce.network.UpdateVariantRequest
import com.us.android.core.commerce.network.VariantDto
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.commerce.payment.PaymentHandoff
import com.us.android.core.commerce.payment.PaymentHandoffEvent
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.checkout.CheckoutUiState
import com.us.android.feature.commerce.checkout.CheckoutViewModel
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import retrofit2.Response

/**
 * Scope C — process-death recovery.
 *
 * Android kills a backgrounded process at any time, and a buyer inside a
 * payment sheet is backgrounded by definition. Review 4 found the entire
 * checkout living in ViewModel fields: quote, accepted total, idempotency key,
 * order id and payment attempt all vanished together, and nothing restored
 * initiated the poll the design assumed.
 *
 * Death is simulated the way the framework behaves: the ViewModel is
 * discarded and a NEW one is built from a COPY of the saved bundle. Nothing
 * else carries over. Anything these tests could reach that a new process could
 * not would make them prove less than they claim.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CheckoutProcessDeathTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    // ─── A server that can lose a response and change its mind ───────

    private class RecoveryApi : CommerceApi {
        var quoteBody = QuoteDto(
            quoteId = "quote-1",
            subtotalMinor = Paise(200000),
            shippingMinor = Paise(4000),
            taxMinor = Paise(31119),
            totalMinor = Paise(204000),
            currency = "INR",
            serviceable = true,
        )

        /**
         * Simulates the state that makes the idempotency key matter: the
         * server CREATES the order and the client never sees the response.
         */
        var dropNextCheckoutResponse = false

        /** What the server says about the order when asked later. */
        var orderPaymentStatus = "pending"

        val checkoutRequests = mutableListOf<Pair<String, CheckoutRequest>>()
        val ordersFetched = mutableListOf<String>()

        /** Server-side order book, keyed by idempotency key — like the real one. */
        private val ordersByKey = linkedMapOf<String, String>()

        fun distinctOrdersCreated(): Int = ordersByKey.size

        override suspend fun quote(body: QuoteRequest): Response<ApiEnvelope<QuoteDto>> =
            Response.success(ApiEnvelope(data = quoteBody))

        override suspend fun checkout(
            idempotencyKey: String,
            body: CheckoutRequest,
        ): Response<ApiEnvelope<CheckoutResultDto>> {
            checkoutRequests += idempotencyKey to body
            // Idempotent on the key, exactly as the server is.
            val orderId = ordersByKey.getOrPut(idempotencyKey) { "order-${ordersByKey.size + 1}" }

            if (dropNextCheckoutResponse) {
                dropNextCheckoutResponse = false
                // The order EXISTS; the client just never learns of it.
                return Response.error(
                    504,
                    "gateway timeout".toResponseBody("text/plain".toMediaType()),
                )
            }
            return Response.success(
                ApiEnvelope(
                    data = CheckoutResultDto(
                        orderId = orderId,
                        orderNumber = "ORD-$orderId",
                        totalMinor = Paise(body.expectedTotalMinor),
                        taxMinor = quoteBody.taxMinor,
                        shippingMinor = quoteBody.shippingMinor,
                    ),
                ),
            )
        }

        override suspend fun getOrder(orderId: String): Response<ApiEnvelope<OrderDto>> {
            ordersFetched += orderId
            val json = """
                {"data":{"id":"$orderId","order_number":"ORD-$orderId",
                 "status":"payment_pending","payment_status":"$orderPaymentStatus",
                 "total_minor":204000,"tax_minor":31119,"shipping_minor":4000,
                 "subtotal_minor":200000,"discount_minor":0,"items":[]}}
            """.trimIndent()
            @Suppress("UNCHECKED_CAST")
            return kotlinx.serialization.json.Json { ignoreUnknownKeys = true }
                .decodeFromString<ApiEnvelope<OrderDto>>(json)
                .let { Response.success(it) }
        }

        override suspend fun paymentStatus(orderId: String): Response<ApiEnvelope<PaymentStatusDto>> =
            Response.success(
                ApiEnvelope(
                    data = PaymentStatusDto(
                        orderId = orderId,
                        orderStatus = "payment_pending",
                        paymentStatus = orderPaymentStatus,
                    ),
                ),
            )

        override suspend fun listProducts(
            query: String?,
            categoryId: String?,
            cursor: String?,
            limit: Int,
        ): Response<ApiEnvelope<ProductListDto>> = notUsed()

        override suspend fun getProduct(productId: String): Response<ApiEnvelope<ProductDetailDto>> = notUsed()
        override suspend fun getCart(): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun addToCart(body: AddToCartRequest): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun updateCartItem(
            variantId: String,
            body: UpdateCartItemRequest,
        ): Response<ApiEnvelope<CartDto>> = notUsed()

        override suspend fun removeCartItem(variantId: String): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun listAddresses(): Response<ApiEnvelope<List<AddressDto>>> = notUsed()
        override suspend fun addAddress(body: AddressDto): Response<ApiEnvelope<AddressDto>> = notUsed()
        override suspend fun openPayment(orderId: String): Response<ApiEnvelope<PaymentHandleDto>> = notUsed()
        override suspend fun listOrders(
            cursor: String?,
            limit: Int,
        ): Response<ApiEnvelope<OrderListDto>> = notUsed()

        override suspend fun cancelOrder(
            orderId: String,
            body: CancelOrderRequest,
        ): Response<ApiEnvelope<Unit>> = notUsed()

        // The seller surface. Not exercised by this test — a call landing here
        // is a wiring mistake, so it fails loudly rather than returning an
        // empty success that would look like a passing assertion.
        override suspend fun sellerProfile(): Response<ApiEnvelope<SellerProfileDto>> = notUsed()

        override suspend fun taxClasses(): Response<ApiEnvelope<TaxClassListDto>> = notUsed()

        override suspend fun createProduct(
            body: CreateProductRequest,
        ): Response<ApiEnvelope<SellerProductDto>> = notUsed()

        override suspend fun submitProduct(productId: String): Response<ApiEnvelope<Unit>> = notUsed()

        override suspend fun sellerVariant(
            variantId: String,
        ): Response<ApiEnvelope<SellerVariantDto>> = notUsed()

        override suspend fun updateVariant(
            variantId: String,
            body: UpdateVariantRequest,
        ): Response<ApiEnvelope<VariantDto>> = notUsed()

        override suspend fun sellerProducts(
            status: String?,
            limit: Int,
            offset: Int,
        ): Response<ApiEnvelope<SellerProductsDto>> = notUsed()

        override suspend fun stock(variantId: String): Response<ApiEnvelope<StockDto>> = notUsed()

        override suspend fun adjustStock(
            variantId: String,
            body: AdjustStockRequest,
        ): Response<ApiEnvelope<StockDto>> = notUsed()

        override suspend fun startSelling(
            body: StartSellingRequest,
        ): Response<ApiEnvelope<SellerProfileDto>> = notUsed()

        override suspend fun submitSellerApplication(): Response<ApiEnvelope<Unit>> = notUsed()

        override suspend fun sellerReadiness(): Response<ApiEnvelope<ReadinessDto>> = notUsed()

        override suspend fun savePayout(
            body: PayoutRequest,
        ): Response<ApiEnvelope<Unit>> = notUsed()

        override suspend fun saveDocuments(
            body: SaveDocumentsRequest,
        ): Response<ApiEnvelope<Unit>> = notUsed()

        override suspend fun saveSellerAddress(
            body: SellerAddressRequest,
        ): Response<ApiEnvelope<Unit>> = notUsed()

        private fun <T> notUsed(): T = error("not part of this test")
    }

    /** Copies the saved bundle, the way the system restores one into a new process. */
    private fun survive(handle: SavedStateHandle): SavedStateHandle {
        val copy = SavedStateHandle()
        handle.keys().forEach { k -> copy[k] = handle.get<Any?>(k) }
        return copy
    }

    private fun vm(api: RecoveryApi, handle: SavedStateHandle, handoff: PaymentHandoff) =
        CheckoutViewModel(CommerceRepository(api), handoff, handle)

    // ─── 1. Before order creation ────────────────────────────────────

    @Test
    fun `recreation before checkout preserves the quote, the accepted total and the key`() =
        runTest(dispatcher) {
            val api = RecoveryApi()
            val handoff = PaymentHandoff()
            val h1 = SavedStateHandle()

            val first = vm(api, h1, handoff)
            first.prepare("addr-1", "5 Main St")
            dispatcher.scheduler.advanceUntilIdle()

            val key = first.currentAttemptKey()
            assertTrue(first.state.value is CheckoutUiState.Ready)

            // The process dies.
            val second = vm(api, survive(h1), handoff)

            assertEquals(
                "a new key would make the retry a SECOND order for one customer decision",
                key,
                second.currentAttemptKey(),
            )
        }

    // ─── 2. The lost response ────────────────────────────────────────

    @Test
    fun `a lost response followed by recreation resubmits the same key and yields one order`() =
        runTest(dispatcher) {
            val api = RecoveryApi()
            val handoff = PaymentHandoff()
            val h1 = SavedStateHandle()

            val first = vm(api, h1, handoff)
            first.prepare("addr-1", "5 Main St")
            dispatcher.scheduler.advanceUntilIdle()
            val key = first.currentAttemptKey()

            // The request goes out, the server creates the order, the response
            // is lost. This is the state the key exists for.
            api.dropNextCheckoutResponse = true
            first.placeOrder()
            dispatcher.scheduler.advanceUntilIdle()

            val second = vm(api, survive(h1), handoff)
            second.prepare("addr-1", "5 Main St")
            dispatcher.scheduler.advanceUntilIdle()

            assertEquals(
                "recovery must resubmit under the SAME key",
                key,
                api.checkoutRequests.last().first,
            )
            assertEquals(
                "one customer decision must produce exactly one order, one stock hold and " +
                    "one payment intent",
                1,
                api.distinctOrdersCreated(),
            )
        }

    // ─── 3. After the order exists ───────────────────────────────────

    @Test
    fun `recreation after order creation fetches the order and never checks out again`() =
        runTest(dispatcher) {
            val api = RecoveryApi()
            val handoff = PaymentHandoff()
            val h1 = SavedStateHandle()

            val first = vm(api, h1, handoff)
            first.prepare("addr-1", "5 Main St")
            dispatcher.scheduler.advanceUntilIdle()
            first.placeOrder()
            dispatcher.scheduler.advanceUntilIdle()
            val submissions = api.checkoutRequests.size

            val second = vm(api, survive(h1), handoff)
            second.prepare("addr-1", "5 Main St")
            dispatcher.scheduler.advanceUntilIdle()

            assertEquals(
                "an order already exists; submitting again would create a second one",
                submissions,
                api.checkoutRequests.size,
            )
            assertTrue(
                "recovery must ASK the server about the order it already has",
                api.ordersFetched.isNotEmpty(),
            )
        }

    @Test
    fun `a server that reports paid renders paid and does not reopen payment`() =
        runTest(dispatcher) {
            val api = RecoveryApi()
            val handoff = PaymentHandoff()
            val h1 = SavedStateHandle()

            val first = vm(api, h1, handoff)
            first.prepare("addr-1", "5 Main St")
            dispatcher.scheduler.advanceUntilIdle()
            first.placeOrder()
            dispatcher.scheduler.advanceUntilIdle()

            // While the process was dead, the webhook landed.
            api.orderPaymentStatus = "paid"

            val second = vm(api, survive(h1), handoff)
            second.prepare("addr-1", "5 Main St")
            dispatcher.scheduler.advanceUntilIdle()

            assertTrue(
                "the buyer paid; the app must say so rather than opening a second sheet. " +
                    "Got ${second.state.value}",
                second.state.value is CheckoutUiState.Paid,
            )
        }

    @Test
    fun `a server that reports refunded renders expired truthfully`() = runTest(dispatcher) {
        val api = RecoveryApi()
        val handoff = PaymentHandoff()
        val h1 = SavedStateHandle()

        val first = vm(api, h1, handoff)
        first.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        first.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        api.orderPaymentStatus = "refunded"

        val second = vm(api, survive(h1), handoff)
        second.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()

        assertTrue(
            "a late capture that was refunded must never render as a successful order. " +
                "Got ${second.state.value}",
            second.state.value is CheckoutUiState.Expired,
        )
    }

    // ─── 4. The attempt survives, so the C3-LB-4 guard still holds ───

    @Test
    fun `the payment attempt survives recreation and still rejects another order's event`() =
        runTest(dispatcher) {
            val api = RecoveryApi()
            val handoff = PaymentHandoff()
            val h1 = SavedStateHandle()

            val first = vm(api, h1, handoff)
            first.prepare("addr-1", "5 Main St")
            dispatcher.scheduler.advanceUntilIdle()
            first.placeOrder()
            dispatcher.scheduler.advanceUntilIdle()
            val originalAttempt = first.activePaymentAttempt()!!

            val second = vm(api, survive(h1), handoff)
            second.observePaymentHandoff()
            second.prepare("addr-1", "5 Main St")
            dispatcher.scheduler.advanceUntilIdle()

            assertEquals(
                "the attempt must survive recreation, or a rotation mid-sheet resets the " +
                    "guard exactly when it matters",
                originalAttempt,
                second.activePaymentAttempt(),
            )

            val before = second.state.value
            handoff.publish(
                PaymentHandoffEvent.Unavailable(
                    PaymentAttempt("some-other-order", "some-other-attempt"),
                    "not ours",
                ),
            )
            dispatcher.scheduler.advanceUntilIdle()
            assertEquals(
                "another order's event must not apply after recreation",
                before,
                second.state.value,
            )
        }

    @Test
    fun `retrying payment after recovery mints a new attempt`() = runTest(dispatcher) {
        val api = RecoveryApi()
        val handoff = PaymentHandoff()
        val h1 = SavedStateHandle()

        val first = vm(api, h1, handoff)
        first.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        first.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()
        val originalAttempt = first.activePaymentAttempt()!!

        api.orderPaymentStatus = "failed"
        val second = vm(api, survive(h1), handoff)
        second.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        assertTrue(second.state.value is CheckoutUiState.PaymentFailed)

        second.retryPayment()
        val retried = second.activePaymentAttempt()!!
        assertEquals("the same order", originalAttempt.orderId, retried.orderId)
        assertNotEquals(
            "a retry must be a NEW attempt, or the failed one's late callback could settle it",
            originalAttempt.id,
            retried.id,
        )
    }

    // ─── 5. Nothing sensitive is persisted ───────────────────────────

    @Test
    fun `no provider session material reaches saved state`() = runTest(dispatcher) {
        val api = RecoveryApi()
        val h1 = SavedStateHandle()
        val first = vm(api, h1, PaymentHandoff())
        first.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        first.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        // SavedStateHandle is written to disk by the system. A publishable key
        // is not a secret, but a payment session is short-lived server-issued
        // material and must be re-fetched, never restored from a stale bundle.
        val forbidden = listOf("key_id", "client_session", "razorpay", "rzp_", "secret")
        h1.keys().forEach { k ->
            val v = h1.get<Any?>(k)?.toString().orEmpty().lowercase()
            forbidden.forEach { bad ->
                assertFalse(
                    "saved key $k must not carry $bad",
                    k.lowercase().contains(bad) || v.contains(bad),
                )
            }
        }
    }
}
