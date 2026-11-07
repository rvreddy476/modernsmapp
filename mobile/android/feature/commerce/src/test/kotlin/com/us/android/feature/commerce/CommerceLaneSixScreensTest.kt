package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.network.AddFavouriteRequest
import com.us.android.core.commerce.network.CancelOrderRequest
import com.us.android.core.commerce.network.CartDto
import com.us.android.core.commerce.network.CartLineDto
import com.us.android.core.commerce.network.FavouriteDto
import com.us.android.core.commerce.network.OrderDto
import com.us.android.core.commerce.network.OrderPaymentDto
import com.us.android.core.commerce.network.ProductListDto
import com.us.android.core.commerce.network.ProductSummaryDto
import com.us.android.core.commerce.network.UpdateCartItemRequest
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.feature.commerce.bag.BagUiState
import com.us.android.feature.commerce.bag.BagViewModel
import com.us.android.feature.commerce.browse.StoreBrowseUiState
import com.us.android.feature.commerce.browse.StoreBrowseViewModel
import com.us.android.feature.commerce.checkout.ORDER_NOT_PAYABLE_ON_RETRY
import com.us.android.feature.commerce.checkout.toSheetAttempt
import com.us.android.feature.commerce.orders.CANCELLED_NOT_REFRESHED
import com.us.android.feature.commerce.orders.OrderDetailUiState
import com.us.android.feature.commerce.orders.OrderDetailViewModel
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response

/**
 * Android lane 6, the buyer screens around checkout: the bag's per-row busy
 * flags (audit L1), search results that cannot be overwritten by an older
 * query (M6), and the order page after a refused payment or a cancel.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CommerceLaneSixScreensTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    // ─── L1: the bag ─────────────────────────────────────────────────

    private class BagApi : FakeCommerceApi() {
        val gates = mutableMapOf<String, CompletableDeferred<Response<ApiEnvelope<CartDto>>>>()

        override suspend fun getCart(): Response<ApiEnvelope<CartDto>> = envelope(cart(1, 1))

        override suspend fun updateCartItem(
            variantId: String,
            body: UpdateCartItemRequest,
        ): Response<ApiEnvelope<CartDto>> = gates.getOrPut(variantId) { CompletableDeferred() }.await()

        fun cart(a: Int, b: Int) = CartDto(
            items = listOf(
                CartLineDto(variantId = "a", productId = "pa", quantity = a),
                CartLineDto(variantId = "b", productId = "pb", quantity = b),
            ),
            itemCount = a + b,
        )
    }

    private fun BagViewModel.content() = state.value as BagUiState.Content

    @Test
    fun `one row finishing does not release another row still in flight`() = runTest(dispatcher) {
        val api = BagApi()
        val vm = BagViewModel(CommerceRepository(api))
        advanceUntilIdle()

        vm.setQuantity("a", 2)
        vm.setQuantity("b", 3)
        advanceUntilIdle()
        assertThat(vm.content().busyVariantIds).containsExactly("a", "b")

        api.gates.getValue("a").complete(envelope(api.cart(2, 1)))
        advanceUntilIdle()
        assertThat(vm.content().busyVariantIds).containsExactly("b")
        // Checkout stays blocked while a row is still changing.
        assertThat(vm.content().canCheckout).isFalse()

        api.gates.getValue("b").complete(envelope(api.cart(2, 3)))
        advanceUntilIdle()
        assertThat(vm.content().busyVariantIds).isEmpty()
        assertThat(vm.content().bag.items.map { it.quantity }).containsExactly(2, 3).inOrder()
    }

    @Test
    fun `a refused row is settled on the latest bag, not the one it started from`() = runTest(dispatcher) {
        val api = BagApi()
        val vm = BagViewModel(CommerceRepository(api))
        advanceUntilIdle()

        vm.setQuantity("a", 2)
        vm.setQuantity("b", 3)
        advanceUntilIdle()
        api.gates.getValue("b").complete(envelope(api.cart(1, 3)))
        advanceUntilIdle()

        api.gates.getValue("a").complete(refused("OUT_OF_STOCK"))
        advanceUntilIdle()

        assertThat(vm.content().busyVariantIds).isEmpty()
        assertThat(vm.content().message).isNotNull()
        // b's accepted change survives a's refusal.
        assertThat(vm.content().bag.items.single { it.variantId == "b" }.quantity).isEqualTo(3)
    }

    // ─── M6: search results ──────────────────────────────────────────

    private class SearchApi : FakeCommerceApi() {
        val answers = mutableMapOf<String, CompletableDeferred<Unit>>()

        override suspend fun listProducts(
            query: String?,
            categoryId: String?,
            cursor: String?,
            limit: Int,
            sort: String?,
        ): Response<ApiEnvelope<ProductListDto>> {
            val key = "${query.orEmpty()}|${cursor.orEmpty()}"
            answers[key]?.await()
            return envelope(
                ProductListDto(
                    items = listOf(ProductSummaryDto(id = "${query.orEmpty()}-${cursor ?: "1"}", title = "x")),
                    nextCursor = if (cursor == null) "c2" else null,
                ),
            )
        }

        override suspend fun getCart(): Response<ApiEnvelope<CartDto>> = envelope(CartDto())
    }

    private fun StoreBrowseViewModel.ids() = (state.value as StoreBrowseUiState.Content).items.map { it.id }

    @Test
    fun `an older search answering last cannot replace the newer one`() = runTest(dispatcher) {
        val api = SearchApi()
        api.answers["shoe|"] = CompletableDeferred()
        val vm = StoreBrowseViewModel(CommerceRepository(api), SavedStateHandle())
        advanceUntilIdle()

        vm.onQueryChange("shoe")
        advanceTimeBy(SEARCH_SETTLE_MS)
        vm.onQueryChange("shirt")
        advanceTimeBy(SEARCH_SETTLE_MS)
        advanceUntilIdle()
        assertThat(vm.ids()).containsExactly("shirt-1")

        api.answers.getValue("shoe|").complete(Unit)
        advanceUntilIdle()
        assertThat(vm.ids()).containsExactly("shirt-1")
    }

    @Test
    fun `a page for the old search is not appended to the new one`() = runTest(dispatcher) {
        val api = SearchApi()
        api.answers["|c2"] = CompletableDeferred()
        val vm = StoreBrowseViewModel(CommerceRepository(api), SavedStateHandle())
        advanceUntilIdle()

        vm.loadMore()
        advanceUntilIdle()
        vm.onQueryChange("shirt")
        advanceTimeBy(SEARCH_SETTLE_MS)
        advanceUntilIdle()
        api.answers.getValue("|c2").complete(Unit)
        advanceUntilIdle()

        assertThat(vm.ids()).containsExactly("shirt-1")
    }

    @Test
    fun `an appended page keeps a heart toggled while it loaded`() = runTest(dispatcher) {
        val api = object : FakeCommerceApi() {
            val gate = CompletableDeferred<Unit>()
            override suspend fun listProducts(
                query: String?,
                categoryId: String?,
                cursor: String?,
                limit: Int,
                sort: String?,
            ): Response<ApiEnvelope<ProductListDto>> {
                if (cursor != null) gate.await()
                val id = if (cursor == null) "p1" else "p2"
                return envelope(
                    ProductListDto(
                        items = listOf(ProductSummaryDto(id = id, title = id)),
                        nextCursor = if (cursor == null) "c2" else null,
                    ),
                )
            }

            override suspend fun getCart(): Response<ApiEnvelope<CartDto>> = envelope(CartDto())

            override suspend fun addFavourite(
                body: AddFavouriteRequest,
            ) = envelope(FavouriteDto(productId = body.productId, isFavourite = true))
        }
        val vm = StoreBrowseViewModel(CommerceRepository(api), SavedStateHandle())
        advanceUntilIdle()

        vm.loadMore()
        vm.toggleFavourite("p1")
        advanceUntilIdle()
        api.gate.complete(Unit)
        advanceUntilIdle()

        val items = (vm.state.value as StoreBrowseUiState.Content).items
        assertThat(items.map { it.id }).containsExactly("p1", "p2").inOrder()
        assertThat(items.first().favourite).isTrue()
    }

    // ─── The order page ──────────────────────────────────────────────

    private class OrderApi : FakeCommerceApi() {
        var paymentStatus = "pending"
        var status = "payment_pending"
        var readsFail = false
        var reads = 0

        override suspend fun getOrder(orderId: String): Response<ApiEnvelope<OrderDto>> {
            reads++
            if (readsFail) return unused()
            return envelope(
                OrderDto(
                    id = orderId,
                    orderNumber = "MS-1",
                    status = status,
                    paymentStatus = paymentStatus,
                    totalMinor = Paise(100),
                ),
            )
        }

        override suspend fun orderPayment(orderId: String): Response<ApiEnvelope<OrderPaymentDto>> = notFound()

        override suspend fun cancelOrder(orderId: String, body: CancelOrderRequest): Response<ApiEnvelope<Unit>> =
            Response.success<ApiEnvelope<Unit>>(204, null)
    }

    private fun orderVm(api: OrderApi, handoff: PaymentHandoff = PaymentHandoff()) = OrderDetailViewModel(
        repo = CommerceRepository(api),
        handoff = handoff,
        payments = pollingOnlyPaymentCoordinator(),
        savedState = SavedStateHandle(mapOf("orderId" to "o-1")),
    )

    private fun OrderDetailViewModel.content() = state.value as OrderDetailUiState.Content

    @Test
    fun `an order that is no longer payable is re-read, with no invitation to pay`() = runTest(dispatcher) {
        val api = OrderApi()
        val handoff = PaymentHandoff()
        val vm = orderVm(api, handoff)
        advanceUntilIdle()
        val attempt = checkNotNull(vm.payNow())
        advanceUntilIdle()
        // A webhook marked it paid before the retry reached the intent route.
        api.status = "confirmed"
        api.paymentStatus = "paid"
        val readsBefore = api.reads

        handoff.publish(PaymentHandoffEvent.Unavailable(attempt.toSheetAttempt(), ORDER_NOT_PAYABLE_ON_RETRY))
        advanceUntilIdle()

        assertThat(api.reads).isGreaterThan(readsBefore)
        assertThat(vm.content().order.paymentStatus).isEqualTo(PaymentStatus.PAID)
        assertThat(vm.content().paying).isFalse()
        assertThat(vm.content().message).isNull()
    }

    @Test
    fun `a cancel shows the server's refund state`() = runTest(dispatcher) {
        val api = OrderApi().apply {
            status = "confirmed"
            paymentStatus = "paid"
        }
        val vm = orderVm(api)
        advanceUntilIdle()

        api.status = "refund_pending"
        api.paymentStatus = "refund_pending"
        vm.cancel("changed my mind")
        advanceUntilIdle()

        assertThat(vm.content().order.paymentStatus).isEqualTo(PaymentStatus.REFUND_PENDING)
        assertThat(vm.content().cancelling).isFalse()
        assertThat(vm.content().message).isNull()
    }

    @Test
    fun `a cancel whose re-read fails is not left spinning`() = runTest(dispatcher) {
        val api = OrderApi().apply {
            status = "confirmed"
            paymentStatus = "paid"
        }
        val vm = orderVm(api)
        advanceUntilIdle()

        api.readsFail = true
        vm.cancel("changed my mind")
        advanceUntilIdle()

        assertThat(vm.content().cancelling).isFalse()
        assertThat(vm.content().message).isEqualTo(CANCELLED_NOT_REFRESHED)
    }

    private companion object {
        /** Past the results page's 300 ms debounce. */
        const val SEARCH_SETTLE_MS = 400L
    }
}
