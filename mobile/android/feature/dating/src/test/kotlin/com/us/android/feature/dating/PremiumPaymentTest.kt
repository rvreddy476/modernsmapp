package com.us.android.feature.dating

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.toHandoffEvent
import com.us.android.core.payments.toSheetResult
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.dating.premium.DATING_PAYMENT_APPLICATION_ID
import com.us.android.feature.dating.premium.DatingPaymentStatusSource
import com.us.android.feature.dating.premium.PremiumState
import com.us.android.feature.dating.premium.PremiumViewModel
import com.us.android.feature.dating.premium.rupees
import com.us.android.feature.dating.premium.toReading
import com.us.android.feature.dating.network.PremiumPaymentDto
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertThrows
import org.junit.Rule
import org.junit.Test

/**
 * Premium's payment state machine: the SDK can never say paid, only the
 * server's status can; a failed read keeps confirming; a failure is retried as
 * a NEW purchase; process death resumes from the server without reopening a
 * sheet; and Dating's payment state never meets Feast's or MStore's.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PremiumPaymentTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val api = FakeDatingApi()
    private val handoff = PaymentHandoff()

    private fun vm(handle: SavedStateHandle = SavedStateHandle()) =
        PremiumViewModel(api.repository(), handoff, confirmingOnlyCoordinator(), handle)

    /** Buys a 30-day pass and returns the attempt the sheet was requested for. */
    private fun TestScope.bought(model: PremiumViewModel, product: String = "pass_30d"): PaymentAttempt {
        advanceUntilIdle()
        runCurrent()
        assertThat(model.state.value).isInstanceOf(PremiumState.Ready::class.java)
        model.buy(product)
        advanceUntilIdle()
        runCurrent()
        return (model.state.value as PremiumState.OpeningPayment).request.attempt
    }

    private fun TestScope.history(model: PremiumViewModel): List<PremiumState> {
        val seen = mutableListOf<PremiumState>()
        backgroundScope.launch(dispatcher) { model.state.collect { seen += it } }
        return seen
    }

    @Test
    fun `the SDK reporting success never shows paid while the server says confirming`() = runTest(dispatcher) {
        api.paymentAnswers += { premiumPayment(status = "confirming") }
        val model = vm()
        val seen = history(model)
        val attempt = bought(model)

        // Exactly what Razorpay's onPaymentSuccess becomes on the bus.
        handoff.publish(PaymentOutcome.Succeeded("pay_123").toSheetResult(attempt).toHandoffEvent())
        runCurrent()
        assertThat(model.state.value).isInstanceOf(PremiumState.Confirming::class.java)

        advanceUntilIdle() // the whole 180 s poll
        runCurrent()

        assertThat(seen.filterIsInstance<PremiumState.Paid>()).isEmpty()
        assertThat(model.state.value).isInstanceOf(PremiumState.StillConfirming::class.java)
        assertThat(api.paymentReads).isGreaterThan(1)
    }

    @Test
    fun `paid is shown only once the status source says paid`() = runTest(dispatcher) {
        api.paymentAnswers += { premiumPayment(status = "confirming") }
        api.paymentAnswers += { premiumPayment(status = "confirming") }
        api.paymentAnswers += { premiumPayment(status = "paid") }
        val model = vm()
        val seen = history(model)
        val attempt = bought(model)

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        advanceTimeBy(3_500) // reads at 1 s and 3 s: both confirming
        runCurrent()
        assertThat(model.state.value).isInstanceOf(PremiumState.Confirming::class.java)

        advanceUntilIdle()
        runCurrent()

        assertThat(model.state.value).isEqualTo(PremiumState.Paid("Premium pass, 30 days"))
        val firstPaid = seen.indexOfFirst { it is PremiumState.Paid }
        assertThat(seen.subList(0, firstPaid).filterIsInstance<PremiumState.Confirming>()).isNotEmpty()
        assertThat(api.paymentReads).isEqualTo(3)
    }

    @Test
    fun `a status read that fails keeps confirming rather than failing`() = runTest(dispatcher) {
        api.paymentAnswers += { refused(500, "PREMIUM_PAYMENT_STATUS_FAILED") }
        api.paymentAnswers += { offline() }
        api.paymentAnswers += { premiumPayment(status = "paid") }
        val model = vm()
        val seen = history(model)
        val attempt = bought(model)

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        advanceTimeBy(3_500)
        runCurrent()
        assertThat(model.state.value).isInstanceOf(PremiumState.Confirming::class.java)

        advanceUntilIdle()
        runCurrent()

        assertThat(seen.filterIsInstance<PremiumState.PaymentFailed>()).isEmpty()
        assertThat(model.state.value).isInstanceOf(PremiumState.Paid::class.java)
    }

    @Test
    fun `a refund outranks paid, and an unknown status keeps confirming`() {
        val paid = PremiumPaymentDto(purchaseId = "p", status = "paid")
        assertThat(paid.toReading()).isEqualTo(PaymentStatusReading.Paid)
        assertThat(paid.copy(refundStatus = "refunded").toReading()).isEqualTo(PaymentStatusReading.Refunded)
        assertThat(paid.copy(refundStatus = "partially_refunded").toReading()).isEqualTo(PaymentStatusReading.RefundPending)
        assertThat(paid.copy(status = "settled_somehow").toReading()).isEqualTo(PaymentStatusReading.Confirming)
        assertThat(paid.copy(status = "failed").toReading()).isInstanceOf(PaymentStatusReading.Failed::class.java)
    }

    @Test
    fun `after the server says failed, a retry is a new purchase under a new key`() = runTest(dispatcher) {
        api.paymentAnswers += { premiumPayment(status = "failed") }
        val model = vm()
        val first = bought(model)

        handoff.publish(PaymentHandoffEvent.SheetClosed(first))
        advanceUntilIdle()
        runCurrent()
        assertThat(model.state.value).isInstanceOf(PremiumState.PaymentFailed::class.java)

        model.retry()
        advanceUntilIdle()
        runCurrent()
        val retry = (model.state.value as PremiumState.OpeningPayment).request.attempt

        assertThat(api.purchaseRequests).hasSize(2)
        assertThat(api.purchaseRequests[1].idempotencyKey).isNotEqualTo(api.purchaseRequests[0].idempotencyKey)
        assertThat(retry.referenceId).isNotEqualTo(first.referenceId)
        assertThat(retry.id).isNotEqualTo(first.id)
        assertThat(retry.applicationId).isEqualTo(DATING_PAYMENT_APPLICATION_ID)
    }

    @Test
    fun `an ending for an earlier attempt, or for the same purchase id in feast or mstore, is not applied`() = runTest(dispatcher) {
        val model = vm()
        val mine = bought(model)
        val before = model.state.value

        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(id = "an-older-attempt")))
        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(applicationId = "feast")))
        handoff.publish(PaymentHandoffEvent.SheetClosed(mine.copy(applicationId = "mstore")))
        advanceUntilIdle()
        runCurrent()

        assertThat(model.state.value).isEqualTo(before)
        assertThat(api.paymentReads).isEqualTo(0)
    }

    @Test
    fun `503 PREMIUM_UNAVAILABLE shows premium isn't available yet`() = runTest(dispatcher) {
        api.purchaseResponse = { refusedWithFixture(503, "premium_purchase_post_503_unavailable.json") }
        val model = vm()
        advanceUntilIdle()
        runCurrent()

        model.buy("boost")
        advanceUntilIdle()
        runCurrent()

        assertThat(model.state.value).isEqualTo(PremiumState.Unavailable)
        assertThat(DatingCopy.PREMIUM_UNAVAILABLE).isEqualTo("Premium isn't available yet")
    }

    @Test
    fun `after process death with a sheet requested, the server is asked and no sheet reopens`() = runTest(dispatcher) {
        val handle = SavedStateHandle()
        bought(vm(handle))

        api.paymentAnswers += { premiumPayment(status = "paid") }
        val restored = SavedStateHandle().also { copy -> handle.keys().forEach { copy[it] = handle.get<Any?>(it) } }
        val second = vm(restored)
        val seen = history(second)
        advanceUntilIdle()
        runCurrent()

        assertThat(second.state.value).isEqualTo(PremiumState.Paid("Premium pass, 30 days"))
        assertThat(seen.filterIsInstance<PremiumState.OpeningPayment>()).isEmpty()
        assertThat(api.purchaseRequests).hasSize(1)
    }

    @Test
    fun `a lost purchase response is resent under the same key`() = runTest(dispatcher) {
        api.purchaseResponse = { offline() }
        val model = vm()
        advanceUntilIdle()
        runCurrent()
        model.buy("pass_90d")
        advanceUntilIdle()
        runCurrent()
        assertThat(model.state.value).isInstanceOf(PremiumState.Ready::class.java)

        api.purchaseResponse = { ok(fixture("premium_purchase_post_200_repeat.json", com.us.android.feature.dating.network.PremiumPurchaseResultDto.serializer())) }
        model.buy("pass_90d")
        advanceUntilIdle()
        runCurrent()

        assertThat(api.purchaseRequests.map { it.idempotencyKey }.distinct()).hasSize(1)
        assertThat(api.purchaseRequests).hasSize(2)
        assertThat(model.state.value).isInstanceOf(PremiumState.OpeningPayment::class.java)
    }

    // ── Isolation: Dating vs Feast vs MStore ────────────────────────────────

    @Test
    fun `dating, feast and mstore payment state never meet`() = runTest(dispatcher) {
        val handle = SavedStateHandle()
        val store = object : PaymentStateStore {
            override fun get(key: String): String? = handle[key]
            override fun set(key: String, value: String?) {
                handle[key] = value
            }
        }
        // Feast and MStore left in-flight attempts in the same saved state, with the SAME reference id.
        val feast = PaymentAttempt("feast", "purchase-1", "feast-attempt")
        val mstore = PaymentAttempt("mstore", "purchase-1", "mstore-attempt")
        InFlightPayment(store, "feast").attempt = feast
        InFlightPayment(store, "mstore").attempt = mstore

        val dating = vm(handle)
        advanceUntilIdle()
        runCurrent()
        assertThat(dating.state.value).isInstanceOf(PremiumState.Ready::class.java)
        assertThat(api.paymentReads).isEqualTo(0)

        val datingAttempt = bought(dating)
        assertThat(datingAttempt.referenceId).isEqualTo("purchase-1")
        assertThat(InFlightPayment(store, "dating").attempt).isEqualTo(datingAttempt)
        assertThat(InFlightPayment(store, "feast").attempt).isEqualTo(feast)
        assertThat(InFlightPayment(store, "mstore").attempt).isEqualTo(mstore)

        // Neither other stream hears Dating's ending, and Dating's never hears theirs.
        val heardElsewhere = mutableListOf<PaymentHandoffEvent>()
        backgroundScope.launch(dispatcher) { handoff.events("feast").collect { heardElsewhere += it } }
        backgroundScope.launch(dispatcher) { handoff.events("mstore").collect { heardElsewhere += it } }
        handoff.publish(PaymentHandoffEvent.SheetClosed(feast))
        handoff.publish(PaymentHandoffEvent.SheetClosed(mstore))
        runCurrent()
        assertThat(dating.state.value).isInstanceOf(PremiumState.OpeningPayment::class.java)
        heardElsewhere.clear()
        handoff.publish(PaymentHandoffEvent.SheetClosed(datingAttempt))
        runCurrent()
        assertThat(heardElsewhere).isEmpty()

        // Dating's status source cannot confirm another application's payment.
        val source = DatingPaymentStatusSource(api.repository())
        assertThrows(IllegalArgumentException::class.java) { confirmingOnlyCoordinator().confirm("feast", "purchase-1", source) }
        assertThrows(IllegalArgumentException::class.java) { confirmingOnlyCoordinator().confirm("mstore", "purchase-1", source) }
    }

    @Test
    fun `prices are the catalogue's, formatted`() {
        assertThat(rupees(39_900)).isEqualTo("₹399")
        assertThat(rupees(4_950)).isEqualTo("₹49.50")
        assertThat(rupees(5)).isEqualTo("₹0.05")
    }
}
