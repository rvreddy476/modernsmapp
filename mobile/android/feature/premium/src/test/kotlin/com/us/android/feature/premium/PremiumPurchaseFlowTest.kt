package com.us.android.feature.premium

import android.app.Activity
import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.ErrorMapper
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentStateStore
import com.us.android.feature.premium.data.CheckoutDto
import com.us.android.feature.premium.data.CheckoutRequest
import com.us.android.feature.premium.data.PremiumRepository
import com.us.android.feature.premium.data.PremiumWords
import com.us.android.feature.premium.payment.PREMIUM_PAYMENT_APPLICATION_ID
import com.us.android.feature.premium.payment.PremiumPaymentOpener
import com.us.android.feature.premium.payment.PremiumPurchaseFlow
import com.us.android.feature.premium.payment.PurchaseState
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.Job
import kotlinx.coroutines.launch
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.TestScope
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.After
import org.junit.Test

/**
 * A Premium purchase on `:core:payments`, application "premium". What it protects:
 *
 *  - the checkout sends the plan code and nothing else;
 *  - Razorpay gets the SERVER's session and GST-inclusive total, and a
 *    checkout whose amount is not its GST total never opens a sheet;
 *  - the SDK can never say paid: only the pass poll can, a failed read keeps
 *    confirming, and a refund is never Premium;
 *  - the poll is 1, 2, 3, 4 s then every 5 s, and gives up at exactly 120 s
 *    as "still confirming" — never as a failure;
 *  - an ending for another attempt, or one already acted on, changes nothing;
 *  - after process death the server is asked and no sheet reopens.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class PremiumPurchaseFlowTest {

    private val dispatcher = StandardTestDispatcher()
    private val api = FakePremiumApi()
    private val repository = PremiumRepository(api, ErrorMapper(testJson))
    private val handoff = PaymentHandoff()
    private val launcher = RecordingLauncher()
    private val payments = coordinator(launcher)
    private val opener = PremiumPaymentOpener(payments, handoff)
    private val store = MapStore()
    private val job = Job()
    private val work = CoroutineScope(dispatcher + job)

    @After
    fun tearDown() = job.cancel()

    private class MapStore : PaymentStateStore {
        val values = mutableMapOf<String, String?>()
        override fun get(key: String): String? = values[key]
        override fun set(key: String, value: String?) {
            values[key] = value
        }
    }

    private fun flow(onStore: PaymentStateStore = store) = PremiumPurchaseFlow(
        scope = work,
        store = onStore,
        repository = repository,
        handoff = handoff,
        payments = payments,
    )

    private fun history(flow: PremiumPurchaseFlow): List<PurchaseState> {
        val seen = mutableListOf<PurchaseState>()
        work.launch { flow.state.collect { seen += it } }
        return seen
    }

    /** Buy → confirm step → Pay → the sheet opens through the real opener; the attempt it opened. */
    private fun TestScope.inSheet(flow: PremiumPurchaseFlow): PaymentAttempt {
        flow.buy("premium_30d")
        runCurrent()
        assertThat(flow.state.value).isInstanceOf(PurchaseState.Review::class.java)
        flow.pay()
        val opening = flow.state.value as PurchaseState.OpeningPayment
        opener.start(Activity(), opening.request)
        flow.onSheetOpened(opening.request)
        assertThat(flow.state.value).isEqualTo(PurchaseState.InSheet)
        return opening.request.attempt
    }

    /** The sheet ends the way Razorpay reports it, through the production mapping and bus. */
    private fun TestScope.sheetEnds(outcome: PaymentOutcome) {
        launcher.pending!!.invoke(outcome)
        runCurrent()
    }

    @Test
    fun `the checkout names the plan only, and Razorpay gets the server's session and total`() = runTest(dispatcher) {
        val flow = flow()
        val attempt = inSheet(flow)

        assertThat(api.checkoutBodies).containsExactly(CheckoutRequest(plan = "premium_30d"))
        val (opened, session) = launcher.opened.single()
        assertThat(opened).isEqualTo(attempt)
        assertThat(attempt.applicationId).isEqualTo(PREMIUM_PAYMENT_APPLICATION_ID)
        assertThat(attempt.referenceId).isEqualTo("b2222222-2222-4222-8222-222222222222")
        assertThat(session.applicationId).isEqualTo(PREMIUM_PAYMENT_APPLICATION_ID)
        assertThat(session.amountMinor).isEqualTo(17_582) // amount_paise = total_paise, GST included
        assertThat(session.currency).isEqualTo("INR")
        assertThat(session.providerOrderId).isEqualTo("order_b2222222")
        assertThat(session.keyId).isEqualTo("rzp_test_key")
        assertThat(session.provider).isEqualTo("razorpay")
    }

    @Test
    fun `the confirm step shows the checkout and nothing opens until Pay`() = runTest(dispatcher) {
        val flow = flow()
        flow.buy("premium_30d")
        runCurrent()

        val review = flow.state.value as PurchaseState.Review
        assertThat(review.checkout.amountPaise).isEqualTo(17_582)
        assertThat(launcher.opened).isEmpty()

        flow.reset()
        assertThat(flow.state.value).isEqualTo(PurchaseState.Idle)
        assertThat(launcher.opened).isEmpty()
    }

    @Test
    fun `a checkout whose amount is not its GST total never opens a sheet`() = runTest(dispatcher) {
        api.checkoutAnswers += {
            val good = envelope("checkout.json", CheckoutDto.serializer()).data!!
            ApiEnvelope(data = good.copy(amountPaise = 14_900)) // the pre-tax price, not the total
        }
        val flow = flow()
        flow.buy("premium_30d")
        runCurrent()
        flow.pay()

        assertThat(flow.state.value).isEqualTo(PurchaseState.Failed(PremiumWords.AMOUNT_MISMATCH, "premium_30d"))
        assertThat(launcher.opened).isEmpty()
    }

    @Test
    fun `a checkout without a usable session never opens a sheet`() = runTest(dispatcher) {
        api.checkoutAnswers += {
            val good = envelope("checkout.json", CheckoutDto.serializer()).data!!
            ApiEnvelope(data = good.copy(clientSession = null))
        }
        val flow = flow()
        flow.buy("premium_30d")
        runCurrent()
        flow.pay()

        assertThat(flow.state.value).isEqualTo(PurchaseState.Failed(PremiumWords.NO_ONLINE_PAYMENT, "premium_30d"))
        assertThat(launcher.opened).isEmpty()
    }

    @Test
    fun `the SDK's success is never Premium while the server says confirming, and the poll stops at 120 s`() =
        runTest(dispatcher) {
            val flow = flow()
            val seen = history(flow)
            inSheet(flow)

            sheetEnds(PaymentOutcome.Succeeded("pay_rzp_1"))
            assertThat(flow.state.value).isEqualTo(PurchaseState.Confirming(0))

            advanceTimeBy(1_000)
            runCurrent()
            assertThat(api.passReads).isEqualTo(1) // the first read at 1 s
            advanceTimeBy(9_000)
            runCurrent()
            assertThat(api.passReads).isEqualTo(4) // 1, 3, 6, 10 s
            advanceTimeBy(5_000)
            runCurrent()
            assertThat(api.passReads).isEqualTo(5) // then every 5 s

            advanceTimeBy(104_999)
            runCurrent()
            assertThat(flow.state.value).isInstanceOf(PurchaseState.Confirming::class.java)
            advanceTimeBy(1)
            runCurrent()
            assertThat(flow.state.value).isEqualTo(PurchaseState.StillConfirming)
            assertThat(api.passReads).isEqualTo(26) // 1, 3, 6, 10, 15 … 120
            advanceUntilIdle()
            assertThat(api.passReads).isEqualTo(26)
            assertThat(seen.filterIsInstance<PurchaseState.Paid>()).isEmpty()
            assertThat(seen.filterIsInstance<PurchaseState.Failed>()).isEmpty()
        }

    @Test
    fun `Premium is shown only once the pass reads paid, failed reads keep confirming`() = runTest(dispatcher) {
        api.passAnswers += { passAnswer("confirming") }
        api.passAnswers += { offline() }
        api.passAnswers += { refused(500, "INTERNAL_ERROR") }
        api.passAnswers += { passAnswer("paid", invoiceId = "d4444444-4444-4444-8444-444444444444") }
        val flow = flow()
        val seen = history(flow)
        inSheet(flow)

        // Razorpay reports a cancel as an error; the money may still have moved, so the pass is asked.
        sheetEnds(PaymentOutcome.Failed(code = 0, message = "cancelled"))
        advanceTimeBy(6_500)
        runCurrent()
        assertThat(flow.state.value).isInstanceOf(PurchaseState.Confirming::class.java)
        advanceUntilIdle()

        val paid = flow.state.value as PurchaseState.Paid
        assertThat(paid.endsAt).isEqualTo("2026-12-13T10:00:00Z")
        assertThat(paid.invoiceId).isEqualTo("d4444444-4444-4444-8444-444444444444")
        assertThat(paid.receipt!!.amountPaise).isEqualTo(17_582)
        assertThat(seen.filterIsInstance<PurchaseState.Failed>()).isEmpty()
        assertThat(api.passReads).isEqualTo(4)
    }

    @Test
    fun `a failed pass is a failure and a refunded one is never Premium`() = runTest(dispatcher) {
        api.passAnswers += { passAnswer("failed") }
        val flow = flow()
        inSheet(flow)
        sheetEnds(PaymentOutcome.Cancelled)
        advanceUntilIdle()
        assertThat(flow.state.value).isEqualTo(PurchaseState.Failed(PremiumWords.FAILED, "premium_30d"))

        api.passAnswers += { passAnswer("refunded") }
        flow.retry()
        runCurrent()
        flow.pay()
        val opening = flow.state.value as PurchaseState.OpeningPayment
        opener.start(Activity(), opening.request)
        flow.onSheetOpened(opening.request)
        sheetEnds(PaymentOutcome.Succeeded(null))
        advanceUntilIdle()
        assertThat(flow.state.value).isEqualTo(PurchaseState.Failed(PremiumWords.REFUNDED, "premium_30d"))
    }

    @Test
    fun `a paid pass without its invoice yet reads the pass again until the invoice is issued`() = runTest(dispatcher) {
        api.passAnswers += { passAnswer("paid") }
        api.passAnswers += { passAnswer("paid") }
        api.passAnswers += { passAnswer("paid", invoiceId = "inv-1") }
        val flow = flow()
        inSheet(flow)
        sheetEnds(PaymentOutcome.Succeeded(null))
        advanceTimeBy(1_000)
        runCurrent()
        assertThat((flow.state.value as PurchaseState.Paid).invoiceId).isNull()

        advanceUntilIdle()
        val paid = flow.state.value as PurchaseState.Paid
        assertThat(paid.invoiceId).isEqualTo("inv-1")
        assertThat(paid.invoiceGaveUp).isFalse()
        assertThat(api.passReads).isEqualTo(3)
    }

    @Test
    fun `the invoice wait gives up after eight reads and points to Invoices`() = runTest(dispatcher) {
        api.lastPass = { passAnswer("paid") }
        val flow = flow()
        inSheet(flow)
        sheetEnds(PaymentOutcome.Succeeded(null))
        advanceUntilIdle()

        assertThat((flow.state.value as PurchaseState.Paid).invoiceGaveUp).isTrue()
        assertThat(api.passReads).isEqualTo(1 + PremiumPurchaseFlow.INVOICE_WAIT_READS)
    }

    @Test
    fun `Check again after still confirming asks about the same pass`() = runTest(dispatcher) {
        val flow = flow()
        inSheet(flow)
        sheetEnds(PaymentOutcome.Succeeded(null))
        advanceUntilIdle()
        assertThat(flow.state.value).isEqualTo(PurchaseState.StillConfirming)

        api.lastPass = { passAnswer("paid", invoiceId = "inv-1") }
        flow.checkAgain()
        advanceUntilIdle()
        assertThat(flow.state.value).isInstanceOf(PurchaseState.Paid::class.java)
        assertThat(api.checkoutBodies).hasSize(1)
    }

    @Test
    fun `PREMIUM_NOT_ENABLED at checkout is not available, other refusals are words`() = runTest(dispatcher) {
        api.checkoutAnswers += { refused(503, "PREMIUM_NOT_ENABLED") }
        api.checkoutAnswers += { refused(503, "PREMIUM_TAX_UNAVAILABLE") }
        val flow = flow()

        flow.buy("premium_30d")
        runCurrent()
        assertThat(flow.state.value).isEqualTo(PurchaseState.Unavailable)

        flow.reset()
        flow.buy("premium_30d")
        runCurrent()
        assertThat(flow.state.value).isEqualTo(PurchaseState.Failed(PremiumWords.TAX_UNAVAILABLE, "premium_30d"))
        assertThat(launcher.opened).isEmpty()
    }

    @Test
    fun `an ending for another attempt, or one already acted on, changes nothing`() = runTest(dispatcher) {
        val flow = flow()
        val attempt = inSheet(flow)

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt.copy(id = "someone-else")))
        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt.copy(applicationId = "memberships")))
        runCurrent()
        assertThat(flow.state.value).isEqualTo(PurchaseState.InSheet)

        handoff.consume(attempt)
        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        runCurrent()
        assertThat(flow.state.value).isEqualTo(PurchaseState.InSheet)
        assertThat(api.passReads).isEqualTo(0)
    }

    @Test
    fun `a sheet that never opened is a failure with nothing charged, and no poll`() = runTest(dispatcher) {
        val flow = flow()
        inSheet(flow)
        sheetEnds(PaymentOutcome.Unavailable("A payment is already in progress. Finish or cancel it first."))
        advanceUntilIdle()

        assertThat(flow.state.value).isInstanceOf(PurchaseState.Failed::class.java)
        assertThat(api.passReads).isEqualTo(0)
    }

    @Test
    fun `after process death the server is asked and no sheet reopens`() = runTest(dispatcher) {
        val first = flow()
        inSheet(first)
        job.children.forEach { it.cancel() } // the process dies with the sheet open

        api.lastPass = { passAnswer("paid", invoiceId = "inv-1") }
        val opensBefore = launcher.opened.size
        val second = flow(store)
        second.resume()
        advanceUntilIdle()

        assertThat(second.state.value).isInstanceOf(PurchaseState.Paid::class.java)
        assertThat(launcher.opened).hasSize(opensBefore)
        assertThat(api.checkoutBodies).hasSize(1)
    }
}
