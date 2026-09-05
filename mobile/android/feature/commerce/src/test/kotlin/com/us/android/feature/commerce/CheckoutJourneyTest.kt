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
import org.junit.Assert.assertNotEquals
import org.junit.Assert.assertTrue
import org.junit.Before
import org.junit.Test
import retrofit2.Response

/**
 * C3-LB-2 and C3-LB-4 — the buyer journey, from a real cart to a payment
 * opening, through the REAL repository and the REAL ViewModel.
 *
 * ## Why a fake API rather than a fake repository
 *
 * Review 3's central proof complaint was that fakes were standing in for the
 * layer whose behaviour was in question, so the tests could not see the
 * defect. B-LB-1 lived partly in the DTO→model mapping and partly in the
 * ViewModel's arithmetic, so a fake repository would have skipped exactly the
 * code under suspicion.
 *
 * The seam here is therefore [CommerceApi] — the Retrofit interface, the last
 * thing before the wire. Everything above it is production: envelope
 * unwrapping, DTO decoding, the breakdown mapping, the ViewModel. The
 * requests the fake RECORDS are the bytes that would have been sent, which is
 * what "submits exactly the quoted total" has to mean.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CheckoutJourneyTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    // ─── The fake wire ───────────────────────────────────────────────

    /**
     * A CommerceApi that answers with server-shaped bodies and records what it
     * was asked.
     *
     * The quote body is the thing that matters: a REAL server states all five
     * figures, and the old client ignored them because they did not exist.
     */
    private class FakeApi : FakeCommerceApi() {
        var quoteBody = QuoteDto(
            quoteId = "quote-1",
            subtotalMinor = Paise(200000),
            discountMinor = Paise(0),
            shippingMinor = Paise(4000),
            taxMinor = Paise(31119),
            totalMinor = Paise(204000),
            currency = "INR",
            courierCode = "test",
            serviceable = true,
        )

        /** Set to make the next checkout answer PRICE_CHANGED. */
        var priceChangedTo: Long? = null

        val quoteRequests = mutableListOf<QuoteRequest>()
        val checkoutRequests = mutableListOf<Pair<String, CheckoutRequest>>()

        override suspend fun quote(body: QuoteRequest): Response<ApiEnvelope<QuoteDto>> {
            quoteRequests += body
            return ok(quoteBody)
        }

        override suspend fun checkout(
            idempotencyKey: String,
            body: CheckoutRequest,
        ): Response<ApiEnvelope<CheckoutResultDto>> {
            checkoutRequests += idempotencyKey to body
            priceChangedTo?.let { newTotal ->
                priceChangedTo = null
                val payload = """{"error":{"code":"PRICE_CHANGED","message":"price moved",""" +
                    """"details":{"new_total_minor":$newTotal,"lines":[]}}}"""
                return Response.error(
                    409,
                    payload.toResponseBody("application/json".toMediaType()),
                )
            }
            return ok(
                CheckoutResultDto(
                    orderId = "order-${checkoutRequests.size}",
                    orderNumber = "ORD-${checkoutRequests.size}",
                    totalMinor = Paise(body.expectedTotalMinor),
                    taxMinor = quoteBody.taxMinor,
                    shippingMinor = quoteBody.shippingMinor,
                ),
            )
        }

        // Nothing below is exercised by these cases.
        override suspend fun listProducts(
            query: String?,
            categoryId: String?,
            cursor: String?,
            limit: Int,
            sort: String?,
        ): Response<ApiEnvelope<ProductListDto>> = notUsed()
        override suspend fun getProduct(productId: String): Response<ApiEnvelope<ProductDetailDto>> = notUsed()
        override suspend fun getCart(): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun addToCart(body: AddToCartRequest): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun updateCartItem(
            itemId: String,
            body: UpdateCartItemRequest,
        ): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun removeCartItem(itemId: String): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun listAddresses(): Response<ApiEnvelope<List<AddressDto>>> = notUsed()
        override suspend fun addAddress(body: AddressDto): Response<ApiEnvelope<AddressDto>> = notUsed()
        override suspend fun openPayment(orderId: String): Response<ApiEnvelope<PaymentHandleDto>> = notUsed()
        override suspend fun paymentStatus(orderId: String): Response<ApiEnvelope<PaymentStatusDto>> = notUsed()
        override suspend fun listOrders(
            cursor: String?,
            limit: Int,
        ): Response<ApiEnvelope<OrderListDto>> = notUsed()
        override suspend fun getOrder(orderId: String): Response<ApiEnvelope<OrderDto>> = notUsed()
        override suspend fun cancelOrder(
            orderId: String,
            body: CancelOrderRequest,
        ): Response<ApiEnvelope<Unit>> = notUsed()

        private fun <T> ok(value: T) = Response.success(ApiEnvelope(data = value))

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

        private fun <T> notUsed(): T = error("not part of this journey")
    }

    private fun vm(api: FakeApi, handoff: PaymentHandoff = PaymentHandoff()) =
        CheckoutViewModel(CommerceRepository(api), handoff, SavedStateHandle())

    // ─── C3-LB-2 ─────────────────────────────────────────────────────

    /**
     * THE journey B-LB-1 broke: a positive-value cart reaches payment opening.
     *
     * Before the fix this could not happen at all. The screen was handed a
     * zero subtotal, showed shipping as the total, submitted 4000, and the
     * server answered PRICE_CHANGED — forever.
     */
    @Test
    fun `a positive cart reaches payment opening without a price-changed loop`() = runTest(dispatcher) {
        val api = FakeApi()
        val model = vm(api)

        model.prepare(addressId = "addr-1", addressSummary = "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()

        val ready = model.state.value as CheckoutUiState.Ready
        assertEquals("the screen must show the SERVER's subtotal", Paise(200000), ready.breakdown.subtotal)
        assertEquals(Paise(204000), ready.breakdown.total)

        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        assertTrue(
            "an ordinary cart must reach the sheet, not a PRICE_CHANGED state; got ${model.state.value}",
            model.state.value is CheckoutUiState.OpeningPayment,
        )
    }

    /** The wire assertion: the exact quoted total, and nothing derived. */
    @Test
    fun `checkout submits exactly the server's quoted total`() = runTest(dispatcher) {
        val api = FakeApi()
        val model = vm(api)

        model.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        val (_, request) = api.checkoutRequests.single()
        assertEquals(
            "expected_total_minor must be the server's total_minor, byte for byte",
            204000L,
            request.expectedTotalMinor,
        )
        assertNotEquals(
            "submitting shipping alone is the old defect",
            4000L,
            request.expectedTotalMinor,
        )
    }

    /**
     * NC-2B's target: the client must not add GST to a GST-inclusive total.
     *
     * Catalogue prices already include GST (D1), so `total + tax` would
     * overcharge by the tax amount on every order.
     */
    @Test
    fun `the client never adds tax to the total`() = runTest(dispatcher) {
        val api = FakeApi()
        val model = vm(api)

        model.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        val submitted = api.checkoutRequests.single().second.expectedTotalMinor
        val taxAdded = 204000L + 31119L
        assertNotEquals("GST is inside the total, never added to it", taxAdded, submitted)
        assertEquals(204000L, submitted)
    }

    /** The quote carries what the price depends on, so the server can bind it. */
    @Test
    fun `the quote request names the payment method`() = runTest(dispatcher) {
        val api = FakeApi()
        vm(api).prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals("upi", api.quoteRequests.single().paymentMethod)
    }

    /**
     * The price-change loop, resolved.
     *
     * The server rejects the first attempt, the buyer accepts the replacement,
     * and the SECOND attempt carries the re-quoted total under a NEW key. The
     * old code re-prepared from a stale zero subtotal and resubmitted the same
     * wrong number indefinitely.
     */
    @Test
    fun `accepting a price change re-quotes, mints a new key, and completes`() = runTest(dispatcher) {
        val api = FakeApi()
        val model = vm(api)

        model.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        val firstKey = model.currentAttemptKey()

        api.priceChangedTo = 250000L
        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()
        assertTrue(
            "a moved price must BLOCK, not resubmit; got ${model.state.value}",
            model.state.value is CheckoutUiState.PriceChanged,
        )

        // The server now states the new price.
        api.quoteBody = api.quoteBody.copy(totalMinor = Paise(250000), subtotalMinor = Paise(246000))

        model.acknowledgePriceChange()
        dispatcher.scheduler.advanceUntilIdle()

        assertNotEquals(
            "accepting a different total is a NEW customer decision and needs a new key",
            firstKey,
            model.currentAttemptKey(),
        )
        val ready = model.state.value as CheckoutUiState.Ready
        assertEquals(Paise(250000), ready.breakdown.total)

        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        assertTrue(
            "the accepted replacement must complete; got ${model.state.value}",
            model.state.value is CheckoutUiState.OpeningPayment,
        )
        assertEquals(
            "the second attempt must carry the ACCEPTED total",
            250000L,
            api.checkoutRequests.last().second.expectedTotalMinor,
        )
        assertEquals(
            "exactly two attempts — one blocked, one accepted; no loop",
            2,
            api.checkoutRequests.size,
        )
    }

    // ─── C3-LB-4 ─────────────────────────────────────────────────────

    /**
     * THE C-LB-1 defect: an event for order A, arriving while this screen is
     * mid-payment for order B, used to be applied — B's screen would poll A
     * and render A's state as B's result.
     */
    @Test
    fun `an event for another order cannot change this checkout`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val api = FakeApi()
        val model = vm(api, handoff)

        model.observePaymentHandoff()
        model.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        val mine = model.activePaymentAttempt()!!
        val before = model.state.value

        // Order A's outcome, delayed, arriving now.
        handoff.publish(
            PaymentHandoffEvent.Unavailable(
                PaymentAttempt("order-A", "attempt-A"),
                "someone else's failure",
            ),
        )
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            "another order's outcome must not touch this state",
            before,
            model.state.value,
        )
        assertEquals("and must not disturb the active attempt", mine, model.activePaymentAttempt())
    }

    /** A replayed event for an EARLIER attempt at the same order is ignored too. */
    @Test
    fun `an event for an earlier attempt at the same order is ignored`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val api = FakeApi()
        val model = vm(api, handoff)

        model.observePaymentHandoff()
        model.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        val mine = model.activePaymentAttempt()!!
        val before = model.state.value

        handoff.publish(
            PaymentHandoffEvent.Unavailable(
                PaymentAttempt(mine.orderId, "an-older-attempt"),
                "stale",
            ),
        )
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            "the order id alone is not identity: a retry produces a second attempt " +
                "and the first one's late callback must not settle it",
            before,
            model.state.value,
        )
    }

    /** This checkout's OWN outcome is applied. */
    @Test
    fun `this checkout's own unavailable outcome is applied`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val api = FakeApi()
        val model = vm(api, handoff)

        model.observePaymentHandoff()
        model.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        val mine = model.activePaymentAttempt()!!
        handoff.publish(PaymentHandoffEvent.Unavailable(mine, "no session"))
        dispatcher.scheduler.advanceUntilIdle()

        assertTrue(
            "without this, the guard above could be passing by ignoring everything",
            model.state.value is CheckoutUiState.PaymentFailed,
        )
    }

    /** A replayed event, already acted on, must not restart the flow. */
    @Test
    fun `a replayed event is consumed exactly once`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val api = FakeApi()
        val model = vm(api, handoff)

        model.observePaymentHandoff()
        model.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        val mine = model.activePaymentAttempt()!!
        handoff.publish(PaymentHandoffEvent.Unavailable(mine, "no session"))
        dispatcher.scheduler.advanceUntilIdle()
        val afterFirst = model.state.value

        // The same event replayed — what a rotation would deliver.
        handoff.publish(PaymentHandoffEvent.SheetClosed(mine))
        dispatcher.scheduler.advanceUntilIdle()

        assertEquals(
            "a consumed attempt must not be re-delivered; a rotation would otherwise " +
                "restart a flow the buyer has already moved past",
            afterFirst,
            model.state.value,
        )
    }

    /** A retry of a failed payment gets a genuinely new attempt. */
    @Test
    fun `retrying payment mints a new attempt`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val api = FakeApi()
        val model = vm(api, handoff)

        model.observePaymentHandoff()
        model.prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()

        val first = model.activePaymentAttempt()!!
        handoff.publish(PaymentHandoffEvent.Unavailable(first, "no session"))
        dispatcher.scheduler.advanceUntilIdle()

        model.retryPayment()
        val second = model.activePaymentAttempt()!!

        assertEquals("the same order", first.orderId, second.orderId)
        assertNotEquals("but a new attempt", first.id, second.id)
    }
}

/**
 * The typed-error contract, pinned at the layer that decodes it.
 *
 * C3-LB-2 exposed this: `call` read the error code from `response.body()`,
 * which Retrofit populates only for a 2xx. On every 4xx it was null, so EVERY
 * typed server error collapsed to "Something went wrong" — and the checkout
 * states built on those codes, PRICE_CHANGED among them, were unreachable.
 *
 * These run through the real repository against real Retrofit error responses.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class ServerErrorCodeTest {

    private class ErrorApi(private val status: Int, private val body: String) : FakeCommerceApi() {
        override suspend fun quote(body: QuoteRequest): Response<ApiEnvelope<QuoteDto>> =
            Response.error(status, this.body.toResponseBody("application/json".toMediaType()))

        override suspend fun checkout(
            idempotencyKey: String,
            body: CheckoutRequest,
        ): Response<ApiEnvelope<CheckoutResultDto>> =
            Response.error(status, this.body.toResponseBody("application/json".toMediaType()))

        override suspend fun listProducts(
            query: String?,
            categoryId: String?,
            cursor: String?,
            limit: Int,
            sort: String?,
        ): Response<ApiEnvelope<ProductListDto>> = notUsed()
        override suspend fun getProduct(productId: String): Response<ApiEnvelope<ProductDetailDto>> = notUsed()
        override suspend fun getCart(): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun addToCart(body: AddToCartRequest): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun updateCartItem(
            itemId: String,
            body: UpdateCartItemRequest,
        ): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun removeCartItem(itemId: String): Response<ApiEnvelope<CartDto>> = notUsed()
        override suspend fun listAddresses(): Response<ApiEnvelope<List<AddressDto>>> = notUsed()
        override suspend fun addAddress(body: AddressDto): Response<ApiEnvelope<AddressDto>> = notUsed()
        override suspend fun openPayment(orderId: String): Response<ApiEnvelope<PaymentHandleDto>> = notUsed()
        override suspend fun paymentStatus(orderId: String): Response<ApiEnvelope<PaymentStatusDto>> = notUsed()
        override suspend fun listOrders(
            cursor: String?,
            limit: Int,
        ): Response<ApiEnvelope<OrderListDto>> = notUsed()
        override suspend fun getOrder(orderId: String): Response<ApiEnvelope<OrderDto>> = notUsed()
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

    private fun errorFor(code: String, details: String = "{}") = ErrorApi(
        409,
        """{"error":{"code":"$code","message":"m","details":$details}}""",
    )

    @Test
    fun `a price-changed response is decoded, not swallowed`() = kotlinx.coroutines.test.runTest {
        val repo = CommerceRepository(
            errorFor("PRICE_CHANGED", """{"new_total_minor":"250000","lines":[]}"""),
        )
        val result = repo.checkout("k", "a", "q", "upi", Paise(1))
        val failure = result as com.us.android.core.commerce.repository.CommerceResult.Failure
        assertTrue(
            "a 4xx carries its code in errorBody, not body; reading the wrong one made " +
                "every typed error generic",
            failure.error is com.us.android.core.commerce.repository.CommerceError.PriceChanged,
        )
        assertEquals(
            Paise(250000),
            (failure.error as com.us.android.core.commerce.repository.CommerceError.PriceChanged).newTotal,
        )
    }

    @Test
    fun `every checkout error code the UI renders survives the wire`() = kotlinx.coroutines.test.runTest {
        // Each of these drives a distinct CheckoutUiState. If any decodes to
        // Unexpected, that state is unreachable in production.
        val codes = mapOf(
            "OUT_OF_STOCK" to com.us.android.core.commerce.repository.CommerceError.OutOfStock::class,
            "QUOTE_STALE" to com.us.android.core.commerce.repository.CommerceError.QuoteStale::class,
            "QUOTE_EXPIRED" to com.us.android.core.commerce.repository.CommerceError.QuoteExpired::class,
            "IDEMPOTENCY_CONFLICT" to
                com.us.android.core.commerce.repository.CommerceError.IdempotencyConflict::class,
            "PRODUCT_UNAVAILABLE" to
                com.us.android.core.commerce.repository.CommerceError.ProductUnavailable::class,
            "MULTIPLE_SELLERS" to com.us.android.core.commerce.repository.CommerceError.MultipleSellers::class,
            "COUPON_UNAVAILABLE" to
                com.us.android.core.commerce.repository.CommerceError.CouponUnavailable::class,
            "CART_EMPTY" to com.us.android.core.commerce.repository.CommerceError.CartEmpty::class,
            "COD_NOT_SUPPORTED" to
                com.us.android.core.commerce.repository.CommerceError.CodNotSupported::class,
        )
        codes.forEach { (code, expected) ->
            val repo = CommerceRepository(errorFor(code, """{"lines":[]}"""))
            val failure = repo.checkout("k", "a", "q", "upi", Paise(1))
                as com.us.android.core.commerce.repository.CommerceResult.Failure
            assertEquals(
                "$code must decode to its own error, not a generic one",
                expected,
                failure.error::class,
            )
        }
    }

    @Test
    fun `a body with no code still fails safely`() = kotlinx.coroutines.test.runTest {
        val repo = CommerceRepository(ErrorApi(502, "<html>gateway timeout</html>"))
        val failure = repo.checkout("k", "a", "q", "upi", Paise(1))
            as com.us.android.core.commerce.repository.CommerceResult.Failure
        assertTrue(
            "an unparseable error body must not crash or be read as success",
            failure.error is com.us.android.core.commerce.repository.CommerceError.Unexpected,
        )
    }
}
