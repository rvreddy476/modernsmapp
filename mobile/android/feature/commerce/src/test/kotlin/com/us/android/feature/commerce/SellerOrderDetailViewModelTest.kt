package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.SellerAction
import com.us.android.core.commerce.network.OrderStatusHistoryDto
import com.us.android.core.commerce.network.SellerCancelOrderRequest
import com.us.android.core.commerce.network.SellerFulfilmentResultDto
import com.us.android.core.commerce.network.SellerOrderCardDto
import com.us.android.core.commerce.network.SellerOrderDto
import com.us.android.core.commerce.network.SellerOrderHistoryDto
import com.us.android.core.commerce.network.SellerOrderItemDto
import com.us.android.core.commerce.network.ShipOrderRequest
import com.us.android.core.commerce.network.ShipmentDto
import com.us.android.core.commerce.network.ShipmentsDto
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.commerce.seller.SellerOrderDetailUiState
import com.us.android.feature.commerce.seller.SellerOrderDetailViewModel
import com.us.android.feature.commerce.seller.SellerOrderSheet
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response

/**
 * The seller's order actions, through the REAL repository and ViewModel.
 *
 * The seam is the Retrofit interface, so what a tap actually sends is what
 * is asserted: the ship action carries the courier and tracking number the
 * seller typed, and a refusal from the server lands as an inline message
 * with the latch released and the seller's typing intact.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class SellerOrderDetailViewModelTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private class OrderApi(initialStatus: String) : FakeCommerceApi() {
        var status = initialStatus
        var refuseWith: String? = null
        val ships = mutableListOf<ShipOrderRequest>()
        val packs = mutableListOf<String>()
        val cancels = mutableListOf<SellerCancelOrderRequest>()
        var reads = 0

        override suspend fun sellerOrder(orderId: String): Response<ApiEnvelope<SellerOrderCardDto>> {
            reads += 1
            return envelope(
                SellerOrderCardDto(
                    order = SellerOrderDto(id = orderId, orderNumber = "MS-1", status = status, paymentStatus = "paid"),
                    items = listOf(SellerOrderItemDto(id = "i-1", productTitle = "Kettle", finalPriceMinor = Paise(1))),
                    sellerSubtotalMinor = Paise(1),
                ),
            )
        }

        /** What the history route answers; null keeps the base fake's unstubbed 500. */
        var history: List<OrderStatusHistoryDto>? = null
        var historyMissing = false

        override suspend fun packOrder(orderId: String): Response<ApiEnvelope<SellerFulfilmentResultDto>> {
            packs += orderId
            return refuseWith?.let { refused(it) }
                ?: envelope(SellerFulfilmentResultDto(orderId, "packed", applied = true)).also { status = "packed" }
        }

        override suspend fun shipOrder(orderId: String, body: ShipOrderRequest): Response<ApiEnvelope<ShipmentsDto>> {
            ships += body
            return refuseWith?.let { refused(it) }
                ?: envelope(ShipmentsDto(listOf(ShipmentDto(id = "s-1", orderId = orderId, courier = body.courier))))
                    .also { status = "shipped" }
        }

        override suspend fun sellerCancelOrder(
            orderId: String,
            body: SellerCancelOrderRequest,
        ): Response<ApiEnvelope<SellerFulfilmentResultDto>> {
            cancels += body
            return refuseWith?.let { refused(it) }
                ?: envelope(SellerFulfilmentResultDto(orderId, "cancelled", applied = true)).also { status = "cancelled" }
        }

        override suspend fun sellerOrderHistory(orderId: String): Response<ApiEnvelope<SellerOrderHistoryDto>> =
            when {
                historyMissing -> notFound()
                history != null -> envelope(SellerOrderHistoryDto(orderId, history!!))
                else -> unused()
            }
    }

    private fun viewModel(api: OrderApi) = SellerOrderDetailViewModel(
        repo = CommerceRepository(api),
        savedState = SavedStateHandle(mapOf("orderId" to "o-1")),
    )

    private fun SellerOrderDetailViewModel.content() = state.value as SellerOrderDetailUiState.Content

    @Test
    fun `ship sends the courier and tracking number and re-reads the order`() = runTest(dispatcher) {
        val api = OrderApi("packed")
        val vm = viewModel(api)
        advanceUntilIdle()
        assertThat(vm.content().actions).containsExactly(SellerAction.SHIP, SellerAction.CANCEL)

        vm.openShip()
        vm.updateShipForm { it.copy(courier = " Delhivery ", trackingNumber = "DL1234567") }
        vm.ship()
        advanceUntilIdle()

        // Trimmed on the way out; the server binds the raw strings.
        assertThat(api.ships).containsExactly(ShipOrderRequest(courier = "Delhivery", trackingNumber = "DL1234567"))
        // The status is READ BACK, not assumed: the server decides where a
        // transition lands.
        assertThat(api.reads).isEqualTo(2)
        val after = vm.content()
        assertThat(after.order.status).isEqualTo(OrderStatus.SHIPPED)
        assertThat(after.actions).isEmpty()
        assertThat(after.sheet).isNull()
        assertThat(after.busy).isNull()
        assertThat(after.error).isNull()
    }

    @Test
    fun `an invalid ship form never reaches the wire`() = runTest(dispatcher) {
        val api = OrderApi("packed")
        val vm = viewModel(api)
        advanceUntilIdle()

        vm.openShip()
        vm.updateShipForm { it.copy(courier = "Delhivery", trackingNumber = "DL 12") }
        vm.ship()
        advanceUntilIdle()

        assertThat(api.ships).isEmpty()
        assertThat(vm.content().canSubmitShip).isFalse()
        assertThat(vm.content().sheet).isEqualTo(SellerOrderSheet.SHIP)
    }

    @Test
    fun `a refused action surfaces the error, releases the latch and keeps the typing`() = runTest(dispatcher) {
        val api = OrderApi("packed").apply { refuseWith = "CANCEL_NOT_PERMITTED" }
        val vm = viewModel(api)
        advanceUntilIdle()

        vm.openCancel()
        vm.updateCancelReason("Out of stock")
        vm.cancel()
        advanceUntilIdle()

        assertThat(api.cancels).containsExactly(SellerCancelOrderRequest("Out of stock"))
        val after = vm.content()
        assertThat(after.error).isEqualTo("This order can no longer be cancelled.")
        assertThat(after.busy).isNull()
        // The sheet stays up with the reason intact: a refusal is something
        // to read, not a reason to retype.
        assertThat(after.sheet).isEqualTo(SellerOrderSheet.CANCEL)
        assertThat(after.cancelReason).isEqualTo("Out of stock")
        // No re-read on a refusal; the screen still shows the order it had.
        assertThat(api.reads).isEqualTo(1)
        assertThat(after.order.status).isEqualTo(OrderStatus.PACKED)
    }

    @Test
    fun `an action the table does not allow from this status is not sent`() = runTest(dispatcher) {
        // Shipped: nothing left for the seller to do. A stale button must not
        // turn into a request the server will refuse anyway.
        val api = OrderApi("shipped")
        val vm = viewModel(api)
        advanceUntilIdle()
        assertThat(vm.content().actions).isEmpty()

        vm.pack()
        vm.openShip()
        vm.updateShipForm { it.copy(courier = "Delhivery", trackingNumber = "DL1234567") }
        vm.ship()
        advanceUntilIdle()

        assertThat(api.packs).isEmpty()
        assertThat(api.ships).isEmpty()
    }

    @Test
    fun `pack from confirmed sends the bare post and moves the order on`() = runTest(dispatcher) {
        val api = OrderApi("confirmed")
        val vm = viewModel(api)
        advanceUntilIdle()
        assertThat(vm.content().actions).containsExactly(SellerAction.PACK, SellerAction.CANCEL)

        vm.pack()
        // Busy while in flight: a second tap is a no-op.
        assertThat(vm.content().busy).isEqualTo(SellerAction.PACK)
        vm.pack()
        advanceUntilIdle()

        assertThat(api.packs).containsExactly("o-1")
        assertThat(vm.content().order.status).isEqualTo(OrderStatus.PACKED)
        assertThat(vm.content().actions).containsExactly(SellerAction.SHIP, SellerAction.CANCEL)
    }

    @Test
    fun `the timeline is the server's recorded history, with what is left after it`() = runTest(dispatcher) {
        val api = OrderApi("packed").apply {
            history = listOf(
                OrderStatusHistoryDto(toStatus = "payment_pending", actorType = "customer", createdAt = "2026-09-12T10:15:00Z"),
                OrderStatusHistoryDto(fromStatus = "payment_pending", toStatus = "confirmed", actorType = "system", createdAt = "2026-09-12T10:16:00Z"),
                OrderStatusHistoryDto(fromStatus = "confirmed", toStatus = "packed", actorType = "seller", createdAt = "2026-09-12T12:00:00Z"),
            )
        }
        val vm = viewModel(api)
        advanceUntilIdle()

        val timeline = vm.content().timeline
        // Every recorded move, done, at the moment the server wrote it.
        assertThat(timeline.map { it.label }).containsExactly("Placed", "Confirmed", "Packed", "Shipped", "Delivered").inOrder()
        assertThat(timeline.take(3).map { it.done }).containsExactly(true, true, true)
        assertThat(timeline.take(3).map { it.at })
            .containsExactly("2026-09-12T10:15:00Z", "2026-09-12T10:16:00Z", "2026-09-12T12:00:00Z").inOrder()
        // What is left is shown as not done, with no guessed time.
        assertThat(timeline.drop(3).map { it.done }).containsExactly(false, false)
        assertThat(timeline.drop(3).map { it.at }).containsExactly(null, null)
    }

    @Test
    fun `a server without the history route gets the derived timeline, and the order still shows`() = runTest(dispatcher) {
        val api = OrderApi("packed").apply { historyMissing = true }
        val vm = viewModel(api)
        advanceUntilIdle()

        val content = vm.content()
        assertThat(content.order.status).isEqualTo(OrderStatus.PACKED)
        // The pre-route shape: derived from the order's stamps and status.
        assertThat(content.timeline.map { it.label }).containsExactly("Placed", "Paid", "Packed", "Shipped", "Delivered").inOrder()
        assertThat(content.timeline.map { it.done }).containsExactly(true, true, true, false, false).inOrder()
    }

    @Test
    fun `a cancel by the seller reads as such from the history`() = runTest(dispatcher) {
        val api = OrderApi("cancelled").apply {
            history = listOf(
                OrderStatusHistoryDto(toStatus = "payment_pending", actorType = "customer", createdAt = "2026-09-12T10:15:00Z"),
                OrderStatusHistoryDto(fromStatus = "payment_pending", toStatus = "confirmed", actorType = "system", createdAt = "2026-09-12T10:16:00Z"),
                OrderStatusHistoryDto(fromStatus = "confirmed", toStatus = "cancelled", actorType = "seller", notes = "Out of stock", createdAt = "2026-09-12T11:00:00Z"),
            )
        }
        val vm = viewModel(api)
        advanceUntilIdle()

        // A closed order ends on its closing step: nothing is left to do.
        assertThat(vm.content().timeline.map { it.label }).containsExactly("Placed", "Confirmed", "Cancelled by you").inOrder()
        assertThat(vm.content().timeline.all { it.done }).isTrue()
    }
}
