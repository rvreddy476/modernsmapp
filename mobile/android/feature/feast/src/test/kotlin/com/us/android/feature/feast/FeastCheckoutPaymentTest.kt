package com.us.android.feature.feast

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.payments.toHandoffEvent
import com.us.android.core.payments.toSheetResult
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.feast.checkout.FEAST_PAYMENT_APPLICATION_ID
import com.us.android.feature.feast.checkout.FeastCheckoutState
import com.us.android.feature.feast.checkout.FeastCheckoutViewModel
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * Feast's payment state machine: the SDK can never say paid, only the server
 * can; a read that fails keeps confirming; a failure is retried on the same
 * order; and process death resumes from the server without reopening a sheet.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class FeastCheckoutPaymentTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val api = FakeFeastApi()
    private val handoff = PaymentHandoff()
    private val session = FeastSession()

    private fun vm(handle: SavedStateHandle = SavedStateHandle()) =
        FeastCheckoutViewModel(api.repository(), session, handoff, confirmingOnlyCoordinator(), handle)

    /** Places the order and returns the attempt the sheet was requested for. */
    private fun TestScope.placed(model: FeastCheckoutViewModel): PaymentAttempt {
        advanceUntilIdle()
        runCurrent()
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.Ready::class.java)
        model.placeOrder()
        advanceUntilIdle()
        runCurrent()
        val opening = model.state.value as FeastCheckoutState.OpeningPayment
        return opening.request.attempt
    }

    private fun TestScope.history(model: FeastCheckoutViewModel): List<FeastCheckoutState> {
        val seen = mutableListOf<FeastCheckoutState>()
        backgroundScope.launch(dispatcher) { model.state.collect { seen += it } }
        return seen
    }

    @Test
    fun `the SDK reporting success never shows paid while the server says confirming`() = runTest(dispatcher) {
        api.paymentAnswers += { payment("confirming") }
        val model = vm()
        val seen = history(model)
        val attempt = placed(model)

        // Exactly what Razorpay's onPaymentSuccess becomes on the bus.
        handoff.publish(PaymentOutcome.Succeeded("pay_123").toSheetResult(attempt).toHandoffEvent())
        runCurrent()
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.Confirming::class.java)

        advanceUntilIdle() // the whole 180 s poll
        runCurrent()

        assertThat(seen.filterIsInstance<FeastCheckoutState.Paid>()).isEmpty()
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.StillConfirming::class.java)
        assertThat(api.paymentReads).isGreaterThan(1)
    }

    @Test
    fun `paid is shown only once the status source says paid`() = runTest(dispatcher) {
        api.paymentAnswers += { payment("confirming") }
        api.paymentAnswers += { payment("confirming") }
        api.paymentAnswers += { payment("paid") }
        val model = vm()
        val seen = history(model)
        val attempt = placed(model)

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        advanceTimeBy(3_500) // reads at 1 s and 3 s: both confirming
        runCurrent()
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.Confirming::class.java)

        advanceUntilIdle() // the read at 6 s says paid
        runCurrent()

        assertThat(model.state.value).isEqualTo(FeastCheckoutState.Paid("order-1", "FG1"))
        val firstPaid = seen.indexOfFirst { it is FeastCheckoutState.Paid }
        assertThat(seen.subList(0, firstPaid).filterIsInstance<FeastCheckoutState.Confirming>()).isNotEmpty()
        assertThat(api.paymentReads).isEqualTo(3)
    }

    @Test
    fun `a status read that fails keeps confirming rather than failing`() = runTest(dispatcher) {
        api.paymentAnswers += { refused(500, "FOOD_PAYMENT_STATUS_FAILED", "payment status is unavailable") }
        api.paymentAnswers += { offline() }
        api.paymentAnswers += { payment("paid") }
        val model = vm()
        val seen = history(model)
        val attempt = placed(model)

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        advanceTimeBy(1_500)
        runCurrent()
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.Confirming::class.java)
        advanceTimeBy(2_000)
        runCurrent()
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.Confirming::class.java)

        advanceUntilIdle()
        runCurrent()

        assertThat(seen.filterIsInstance<FeastCheckoutState.PaymentFailed>()).isEmpty()
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.Paid::class.java)
    }

    @Test
    fun `retrying after the server says failed pays for the same order with a new attempt`() = runTest(dispatcher) {
        api.paymentAnswers += { payment("failed") }
        val model = vm()
        val first = placed(model)

        handoff.publish(PaymentHandoffEvent.SheetClosed(first))
        advanceUntilIdle()
        runCurrent()
        assertThat(model.state.value).isInstanceOf(FeastCheckoutState.PaymentFailed::class.java)

        model.retryPayment()
        val retry = (model.state.value as FeastCheckoutState.OpeningPayment).request.attempt

        assertThat(retry.referenceId).isEqualTo(first.referenceId)
        assertThat(retry.id).isNotEqualTo(first.id)
        assertThat(retry.applicationId).isEqualTo(FEAST_PAYMENT_APPLICATION_ID)
        assertThat(api.placeOrderKeys).hasSize(1)
    }

    @Test
    fun `an ending for an earlier attempt, or for the same order in mstore, is not applied`() = runTest(dispatcher) {
        val model = vm()
        val mine = placed(model)
        val before = model.state.value

        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(id = "an-older-attempt")))
        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(applicationId = "mstore")))
        advanceUntilIdle()
        runCurrent()

        assertThat(model.state.value).isEqualTo(before)
        assertThat(api.paymentReads).isEqualTo(0)
    }

    @Test
    fun `a failed sheet that never opened offers a retry on the same order`() = runTest(dispatcher) {
        val model = vm()
        val mine = placed(model)

        handoff.publish(PaymentHandoffEvent.Unavailable(mine, "Online payment isn't available right now."))
        advanceUntilIdle()
        runCurrent()

        val failed = model.state.value as FeastCheckoutState.PaymentFailed
        assertThat(failed.orderId).isEqualTo("order-1")
        assertThat(failed.reason).isEqualTo("Online payment isn't available right now.")
    }

    @Test
    fun `after process death with a sheet requested, the server is asked and no sheet reopens`() = runTest(dispatcher) {
        val handle = SavedStateHandle()
        val first = vm(handle)
        placed(first)

        // The process dies inside the sheet; the capture lands meanwhile.
        api.paymentAnswers += { payment("paid") }
        val restored = SavedStateHandle().also { copy -> handle.keys().forEach { copy[it] = handle.get<Any?>(it) } }
        val second = vm(restored)
        val seen = history(second)
        advanceUntilIdle()
        runCurrent()

        assertThat(second.state.value).isEqualTo(FeastCheckoutState.Paid("order-1", "FG1"))
        assertThat(seen.filterIsInstance<FeastCheckoutState.OpeningPayment>()).isEmpty()
        assertThat(api.placeOrderKeys).hasSize(1)
    }

    @Test
    fun `a lost place-order response is resent under the same key`() = runTest(dispatcher) {
        val handle = SavedStateHandle()
        api.placeOrderResponse = { offline() }
        val first = vm(handle)
        advanceUntilIdle()
        runCurrent()
        first.placeOrder()
        advanceUntilIdle()
        runCurrent()
        val key = api.placeOrderKeys.single()

        api.placeOrderResponse = { ok(api.order) }
        first.placeOrder()
        advanceUntilIdle()
        runCurrent()

        assertThat(api.placeOrderKeys).containsExactly(key, key).inOrder()
        assertThat(first.state.value).isInstanceOf(FeastCheckoutState.OpeningPayment::class.java)
    }

    // ── Isolation ────────────────────────────────────────────────────────

    @Test
    fun `a pending feast payment is never mstore's, and a pending mstore payment is never resumed by feast`() =
        runTest(dispatcher) {
            val handle = SavedStateHandle()
            val store = object : PaymentStateStore {
                override fun get(key: String): String? = handle[key]
                override fun set(key: String, value: String?) {
                    handle[key] = value
                }
            }

            // An MStore checkout left its in-flight attempt in the same saved state.
            InFlightPayment(store, "mstore").attempt = PaymentAttempt("mstore", "order-1", "mstore-attempt")

            val feast = vm(handle)
            advanceUntilIdle()
            runCurrent()
            assertThat(feast.state.value).isInstanceOf(FeastCheckoutState.Ready::class.java)
            assertThat(api.paymentReads).isEqualTo(0)

            val feastAttempt = placed(feast)

            assertThat(InFlightPayment(store, "feast").attempt).isEqualTo(feastAttempt)
            assertThat(InFlightPayment(store, "mstore").attempt).isEqualTo(PaymentAttempt("mstore", "order-1", "mstore-attempt"))

            // MStore's stream never hears Feast's ending.
            handoff.publish(PaymentHandoffEvent.SheetClosed(feastAttempt))
            val heardByMStore = mutableListOf<PaymentHandoffEvent>()
            backgroundScope.launch(dispatcher) { handoff.events("mstore").collect { heardByMStore += it } }
            runCurrent()
            assertThat(heardByMStore).isEmpty()
        }
}
