package com.us.android.feature.feast

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModelStore
import com.google.common.truth.Truth.assertThat
import com.us.android.core.food.network.FeastOrderDto
import com.us.android.core.food.network.RefundRequestDto
import com.us.android.core.food.network.SupportTicketDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.feast.checkout.logCode
import com.us.android.feature.feast.orders.OrderActions
import com.us.android.feature.feast.orders.RefundOutcome
import com.us.android.feature.feast.orders.RefundRequestFlow
import com.us.android.feature.feast.tracking.FeastClock
import com.us.android.feature.feast.tracking.OrderEventStream
import com.us.android.feature.feast.tracking.OrderTrackingViewModel
import com.us.android.feature.feast.ui.serverTotal
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.awaitCancellation
import kotlinx.coroutines.flow.flow
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test
import java.io.IOException
import java.time.Instant

/**
 * Cancel only before cooking, a refund request after (founder decision 31
 * Oct), Pay now for an unpaid order (food audit M1), and tracking that stops
 * polling when nobody is looking (L5).
 */
@OptIn(ExperimentalCoroutinesApi::class)
class FeastOrderActionsTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val api = FakeFeastApi()

    private val confirmed = fixture("order_get_200.json", FeastOrderDto.serializer())
    private val outForDelivery = fixture("order_get_200_out_for_delivery.json", FeastOrderDto.serializer())
    private val placed = fixture("order_place_201.json", FeastOrderDto.serializer())

    private fun tracking(store: ViewModelStore): OrderTrackingViewModel =
        OrderTrackingViewModel(
            SavedStateHandle(mapOf("orderId" to api.order.id)),
            api.repository(),
            OrderEventStream { flow { awaitCancellation() } },
            FeastClock { Instant.parse("2026-09-13T06:40:00Z") },
        ).also { store.put("tracking", it) }

    private fun TestScope.watch(vm: OrderTrackingViewModel): Job = backgroundScope.launch { vm.state.collect {} }

    // ── The rules, from the server's own responses ──────────────────────

    @Test
    fun `cancel follows can_cancel from the golden responses`() {
        assertThat(OrderActions.canCancel(confirmed)).isTrue()
        assertThat(OrderActions.canCancel(outForDelivery)).isFalse()
        assertThat(OrderActions.canCancel(placed)).isFalse()
    }

    @Test
    fun `the server's can_cancel wins over the status`() {
        assertThat(OrderActions.canCancel(FeastOrderDto(id = "o", status = "CONFIRMED", canCancel = false))).isFalse()
        assertThat(OrderActions.canCancel(FeastOrderDto(id = "o", status = "PREPARING", canCancel = true))).isTrue()
    }

    @Test
    fun `a payment failure is logged by kind and code, never with the server's words`() {
        val logged = FoodError.Unexpected(409, "FOOD_ORDER_NOT_PAYABLE", "order FG1 for asha@example.com").logCode()

        assertThat(logged).isEqualTo("Unexpected(409, FOOD_ORDER_NOT_PAYABLE)")
        assertThat(FoodError.Network(IOException("10.0.0.7 unreachable")).logCode()).isEqualTo("Network")
    }

    @Test
    fun `without can_cancel only PLACED and CONFIRMED may be cancelled`() {
        val cancellable = listOf("PLACED", "CONFIRMED", "PAYMENT_PENDING", "PREPARING", "DELIVERED")
            .filter { OrderActions.canCancel(FeastOrderDto(id = "o", status = it)) }

        assertThat(cancellable).containsExactly("PLACED", "CONFIRMED")
    }

    @Test
    fun `a paid order the kitchen started offers a refund, and a cancellable one does not`() {
        assertThat(OrderActions.canRequestRefund(outForDelivery)).isTrue()
        assertThat(OrderActions.canRequestRefund(confirmed)).isFalse()
        assertThat(OrderActions.canRequestRefund(outForDelivery.copy(paymentStatus = "PENDING"))).isFalse()
    }

    @Test
    fun `pay now is offered only for an online order still awaiting payment`() {
        assertThat(OrderActions.canPayNow(placed)).isTrue()
        assertThat(OrderActions.canPayNow(placed.copy(status = "PAYMENT_FAILED", paymentStatus = "FAILED"))).isTrue()
        assertThat(OrderActions.canPayNow(placed.copy(paymentStatus = "CAPTURED"))).isFalse()
        assertThat(OrderActions.canPayNow(confirmed)).isFalse()
        assertThat(OrderActions.canPayNow(placed.copy(paymentMethod = "COD"))).isFalse()
    }

    // ── Cancel and refund on the order screen ───────────────────────────

    @Test
    fun `an order that can still be cancelled is not sent a refund request`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            api.order = confirmed
            val vm = tracking(store)
            runCurrent()

            vm.requestRefund("changed my mind")
            runCurrent()

            assertThat(api.ticketRequests).isEmpty()
            assertThat(api.refundRequests).isEmpty()
        } finally { store.clear() }
    }

    @Test
    fun `an order the kitchen started is not sent a cancel`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            api.order = outForDelivery
            val vm = tracking(store)
            runCurrent()

            vm.cancel()
            runCurrent()

            assertThat(api.cancelCalls).isEqualTo(0)
            assertThat(vm.state.value.canCancel).isFalse()
            assertThat(vm.state.value.canRequestRefund).isTrue()
        } finally { store.clear() }
    }

    @Test
    fun `a cancel refused after preparing says so, and offers a refund instead`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            api.order = confirmed
            val vm = tracking(store)
            runCurrent()
            assertThat(vm.state.value.canCancel).isTrue()

            // The kitchen starts between the read and the tap.
            api.cancelResponse = {
                api.order = confirmed.copy(status = "PREPARING", canCancel = false)
                refusedWithFixture(409, "order_cancel_post_409_after_preparing.json")
            }
            vm.cancel()
            runCurrent()

            assertThat(vm.state.value.message?.text).isEqualTo(OrderTrackingViewModel.KITCHEN_STARTED)
            assertThat(vm.state.value.canCancel).isFalse()
            assertThat(vm.state.value.canRequestRefund).isTrue()
        } finally { store.clear() }
    }

    @Test
    fun `a refund request opens a ticket, then asks for the server total under it`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            api.order = outForDelivery
            val vm = tracking(store)
            runCurrent()

            vm.requestRefund("  half the order was missing ")
            runCurrent()

            val ticket = api.ticketRequests.single()
            assertThat(ticket.orderId).isEqualTo(outForDelivery.id)
            assertThat(ticket.category).isEqualTo("refund")
            assertThat(ticket.detail).isEqualTo("half the order was missing")
            val refund = api.refundRequests.single()
            assertThat(refund.orderId).isEqualTo(outForDelivery.id)
            assertThat(refund.ticketId).isEqualTo("ticket-1")
            assertThat(refund.amount).isEqualTo(outForDelivery.serverTotal())
            assertThat(refund.reason).isEqualTo("half the order was missing")
            assertThat(vm.state.value.refundRequested).isTrue()
            assertThat(vm.state.value.canRequestRefund).isFalse()
        } finally { store.clear() }
    }

    @Test
    fun `a refund retried after a failure reuses the ticket already opened`() = runTest(dispatcher) {
        api.order = outForDelivery
        val flow = RefundRequestFlow(api.repository())
        api.refundResponse = { offline() }
        flow.submit(outForDelivery, null)

        api.refundResponse = { ok(RefundRequestDto(id = "refund-1", status = "requested")) }
        flow.submit(outForDelivery, null)

        assertThat(api.ticketRequests).hasSize(1)
        assertThat(api.refundRequests.map { it.ticketId }).containsExactly("ticket-1", "ticket-1")
        assertThat(api.refundRequests.last().reason).isEqualTo(RefundRequestFlow.DEFAULT_REASON)
    }

    // ── Idempotency-Key per decision (food server lane, 9 Nov 2026) ─────

    @Test
    fun `a refund whose answer was lost is resent under the same key, and says so`() = runTest(dispatcher) {
        val flow = RefundRequestFlow(api.repository())
        api.refundResponse = { refused(504, "GATEWAY_TIMEOUT", "upstream timed out") }

        val first = flow.submit(outForDelivery, null)
        api.refundResponse = { ok(RefundRequestDto(id = "refund-1", status = "requested")) }
        flow.submit(outForDelivery, null)

        assertThat(first).isEqualTo(refusedWith(RefundRequestFlow.OUTCOME_UNKNOWN))
        assertThat(api.refundKeys).hasSize(2)
        assertThat(api.refundKeys.distinct()).hasSize(1)
        // The ticket and the refund are separate requests: separate keys.
        assertThat(api.ticketKeys.single()).isNotEqualTo(api.refundKeys.first())
    }

    @Test
    fun `a ticket whose answer was lost is resent under the same key, never a second ticket`() = runTest(dispatcher) {
        val flow = RefundRequestFlow(api.repository())
        api.ticketResponse = { offline() }

        val first = flow.submit(outForDelivery, null)
        api.ticketResponse = { ok(SupportTicketDto(id = "ticket-1", orderId = outForDelivery.id)) }
        flow.submit(outForDelivery, null)

        assertThat(first).isEqualTo(refusedWith(RefundRequestFlow.OUTCOME_UNKNOWN))
        assertThat(api.ticketKeys).hasSize(2)
        assertThat(api.ticketKeys.distinct()).hasSize(1)
        assertThat(api.refundRequests).hasSize(1)
    }

    @Test
    fun `a definite refusal frees the refund key, so asking again is a new request`() = runTest(dispatcher) {
        val flow = RefundRequestFlow(api.repository())
        api.refundResponse = { refused(409, "FOOD_REFUND_NOT_ELIGIBLE", "order has no captured payment to refund") }

        flow.submit(outForDelivery, null)
        flow.submit(outForDelivery, null)

        assertThat(api.refundKeys.distinct()).hasSize(2)
        // The ticket was opened: it is reused, not re-opened.
        assertThat(api.ticketKeys).hasSize(1)
    }

    @Test
    fun `a definite ticket refusal frees the ticket key`() = runTest(dispatcher) {
        val flow = RefundRequestFlow(api.repository())
        api.ticketResponse = { refused(403, "FOOD_SUPPORT_ORDER_FORBIDDEN", "order does not belong to customer") }

        val first = flow.submit(outForDelivery, null)
        flow.submit(outForDelivery, null)

        assertThat(first).isEqualTo(refusedWith(RefundRequestFlow.NOT_YOURS))
        assertThat(api.ticketKeys.distinct()).hasSize(2)
        assertThat(api.refundRequests).isEmpty()
    }

    @Test
    fun `the refund golden is read as requested`() = runTest(dispatcher) {
        api.refundResponse = { ok(fixture("refund_post_201.json", RefundRequestDto.serializer())) }

        val outcome = RefundRequestFlow(api.repository()).submit(outForDelivery, "Cold food")

        assertThat((outcome as RefundOutcome.Requested).refund.status).isEqualTo("requested")
    }

    @Test
    fun `each refund refusal has its own words`() = runTest(dispatcher) {
        val flow = RefundRequestFlow(api.repository())

        api.refundResponse = { refused(422, "FOOD_REFUND_AMOUNT_INVALID", "refund amount exceeds remaining") }
        val amount = flow.submit(outForDelivery, null)
        api.refundResponse = { refused(409, "FOOD_REFUND_NOT_ELIGIBLE", "order has no captured payment to refund") }
        val ineligible = flow.submit(outForDelivery, null)
        api.refundResponse = { refused(403, "FOOD_SUPPORT_ORDER_FORBIDDEN", "order does not belong to customer") }
        val forbidden = flow.submit(outForDelivery, null)

        assertThat(amount).isEqualTo(refusedWith(RefundRequestFlow.ALREADY_REQUESTED))
        assertThat(ineligible).isEqualTo(refusedWith(RefundRequestFlow.NOT_ELIGIBLE))
        assertThat(forbidden).isEqualTo(refusedWith(RefundRequestFlow.NOT_YOURS))
    }

    private fun refusedWith(message: String) = RefundOutcome.Refused(message)

    // ── Polling only while shown (L5) ────────────────────────────────────

    @Test
    fun `tracking reads the order once, then polls only while the screen collects it`() = runTest(dispatcher) {
        val store = ViewModelStore()
        try {
            api.order = outForDelivery
            val vm = tracking(store)
            runCurrent()
            advanceTimeBy(60_000)
            runCurrent()
            assertThat(api.orderReads).isEqualTo(1)

            val shown = watch(vm)
            runCurrent()
            advanceTimeBy(10_001)
            runCurrent()
            val whileShown = api.orderReads
            // The first show follows the read init made; the 10 s poll reads again.
            assertThat(whileShown).isEqualTo(2)

            shown.cancel()
            runCurrent()
            advanceTimeBy(120_000)
            runCurrent()
            assertThat(api.orderReads).isEqualTo(whileShown)
        } finally { store.clear() }
    }
}
