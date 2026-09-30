package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Order
import com.us.android.core.commerce.model.OrderStatus
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.OrderDto
import com.us.android.core.commerce.network.OrderPaymentDto
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.UnavailableLine
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.feature.commerce.checkout.OUT_OF_STOCK_ON_RETRY
import com.us.android.feature.commerce.checkout.paymentOpenRefusal
import com.us.android.feature.commerce.checkout.toSheetAttempt
import com.us.android.feature.commerce.orders.OrderDetailUiState
import com.us.android.feature.commerce.orders.OrderDetailViewModel
import com.us.android.feature.commerce.orders.previewOrder
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response

/**
 * "Pay now" on an order (2026-09-30, contract §4.7).
 *
 * A failed payment keeps its order; the intent route re-reserves the stock
 * and re-opens it, or refuses with OUT_OF_STOCK. The order's ViewModel
 * mints the attempt, listens for the sheet's ending on the handoff, shows a
 * refusal as one line, and polls the server after a closed sheet — before
 * this the screen opened the sheet and listened for nothing.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class OrderDetailPayNowTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private class OrderApi(var status: String, var paymentStatus: String) : FakeCommerceApi() {
        var reads = 0
        var payment: OrderPaymentDto? = null

        override suspend fun getOrder(orderId: String): Response<ApiEnvelope<OrderDto>> {
            reads++
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

        override suspend fun orderPayment(orderId: String): Response<ApiEnvelope<OrderPaymentDto>> =
            payment?.let { envelope(it) } ?: notFound()
    }

    private fun viewModel(api: OrderApi, handoff: PaymentHandoff) = OrderDetailViewModel(
        repo = CommerceRepository(api),
        handoff = handoff,
        payments = pollingOnlyPaymentCoordinator(),
        savedState = SavedStateHandle(mapOf("orderId" to "o-1")),
    )

    private fun content(vm: OrderDetailViewModel) = vm.state.value as OrderDetailUiState.Content

    @Test
    fun `pay now mints a new attempt for this order and holds the button`() = runTest(dispatcher) {
        val vm = viewModel(OrderApi("payment_failed", "failed"), PaymentHandoff())
        dispatcher.scheduler.advanceUntilIdle()

        val first = checkNotNull(vm.payNow())
        assertThat(first.orderId).isEqualTo("o-1")
        assertThat(content(vm).paying).isTrue()

        // Held while one is under way.
        assertThat(vm.payNow()).isNull()
    }

    @Test
    fun `a refused intent is one line, and the button is released`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val vm = viewModel(OrderApi("payment_failed", "failed"), handoff)
        dispatcher.scheduler.advanceUntilIdle()
        val attempt = checkNotNull(vm.payNow())
        dispatcher.scheduler.advanceUntilIdle()

        // What the opener publishes for a 409 OUT_OF_STOCK from the intent route.
        handoff.publish(PaymentHandoffEvent.Unavailable(attempt.toSheetAttempt(), OUT_OF_STOCK_ON_RETRY))
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(content(vm).message).isEqualTo(OUT_OF_STOCK_ON_RETRY)
        assertThat(content(vm).paying).isFalse()
    }

    @Test
    fun `a closed sheet polls the server and re-reads the order`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val api = OrderApi("payment_failed", "failed")
        val vm = viewModel(api, handoff)
        dispatcher.scheduler.advanceUntilIdle()
        val attempt = checkNotNull(vm.payNow())
        dispatcher.scheduler.advanceUntilIdle()
        val readsBefore = api.reads

        // The server settles it while the sheet is closing.
        api.payment = OrderPaymentDto(orderId = "o-1", status = "paid", amountMinor = Paise(100))
        api.status = "confirmed"
        api.paymentStatus = "paid"
        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt.toSheetAttempt()))
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(api.reads).isGreaterThan(readsBefore)
        assertThat(content(vm).order.status).isEqualTo(OrderStatus.CONFIRMED)
        assertThat(content(vm).paying).isFalse()
        assertThat(content(vm).message).isNull()
    }

    @Test
    fun `an ending for another attempt is ignored`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val vm = viewModel(OrderApi("payment_failed", "failed"), handoff)
        dispatcher.scheduler.advanceUntilIdle()
        checkNotNull(vm.payNow())
        dispatcher.scheduler.advanceUntilIdle()

        val someoneElse = com.us.android.core.commerce.payment.PaymentAttempt(orderId = "o-1", id = "stale")
        handoff.publish(PaymentHandoffEvent.Unavailable(someoneElse.toSheetAttempt(), "stale"))
        dispatcher.scheduler.advanceUntilIdle()

        assertThat(content(vm).paying).isTrue()
        assertThat(content(vm).message).isNull()
    }

    // ─── The pure rules ──────────────────────────────────────────────

    @Test
    fun `pay now is offered while awaiting payment and after a retryable failure`() {
        fun order(status: OrderStatus, canRetry: Boolean?): Order =
            previewOrder(status = status).copy(canRetryPayment = canRetry)

        assertThat(order(OrderStatus.PAYMENT_PENDING, null).canPayNow).isTrue()
        assertThat(order(OrderStatus.PAYMENT_FAILED, null).canPayNow).isTrue()
        assertThat(order(OrderStatus.PAYMENT_FAILED, true).canPayNow).isTrue()
        assertThat(order(OrderStatus.PAYMENT_FAILED, false).canPayNow).isFalse()
        assertThat(order(OrderStatus.CONFIRMED, true).canPayNow).isFalse()
        assertThat(order(OrderStatus.EXPIRED, null).canPayNow).isFalse()
    }

    @Test
    fun `the OUT_OF_STOCK refusal has its own line, everything else is a retry`() {
        val outOfStock = CommerceError.OutOfStock(listOf(UnavailableLine("v-1", "p-1", "Kettle", 1, 0)))
        assertThat(paymentOpenRefusal(outOfStock)).isEqualTo(OUT_OF_STOCK_ON_RETRY)
        assertThat(paymentOpenRefusal(CommerceError.Network(null))).contains("try again")
        assertThat(paymentOpenRefusal(CommerceError.OrderNotFound)).contains("try again")
    }
}
