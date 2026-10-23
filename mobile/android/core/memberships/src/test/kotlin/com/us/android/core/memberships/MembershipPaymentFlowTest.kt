package com.us.android.core.memberships

import com.google.common.truth.Truth.assertThat
import com.us.android.core.common.result.AppResult
import com.us.android.core.memberships.data.MembershipCheckout
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.payment.MEMBERSHIPS_PAYMENT_APPLICATION_ID
import com.us.android.core.memberships.payment.MembershipPayState
import com.us.android.core.memberships.payment.MembershipPaymentFlow
import com.us.android.core.network.ErrorMapper
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.payments.toHandoffEvent
import com.us.android.core.payments.toSheetResult
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
 * A membership payment's state machine (paid memberships, 2026-10-23) on
 * `:core:payments`, application "memberships". What it protects:
 *
 *  - the SDK can never say paid; only the server's payment status can, and a
 *    read that fails keeps confirming;
 *  - ONE Idempotency-Key per decision, reused on every resend of it (a lost
 *    response, a failed payment), new for another level, for a definite
 *    refusal, and for `CHECKOUT_EXPIRED`;
 *  - a refund outranks paid;
 *  - after process death the server is asked first and no sheet reopens;
 *  - an ending for another attempt, or one already acted on, changes nothing.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class MembershipPaymentFlowTest {

    private val dispatcher = StandardTestDispatcher()
    private val api = FakeMembershipsApi()
    private val repository = MembershipsRepository(api, ErrorMapper(NetworkModule.provideJson()))
    private val handoff = PaymentHandoff()
    private val store = MapStore()

    /**
     * Where the flows run: the test dispatcher, in the FOREGROUND (advanceUntilIdle
     * does not run backgroundScope work), cancelled after each test.
     */
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

    private fun flow(scope: CoroutineScope, onStore: PaymentStateStore = store) = MembershipPaymentFlow(
        scope = scope,
        store = onStore,
        repository = repository,
        handoff = handoff,
        payments = confirmingOnlyCoordinator(),
        prefix = "test.join",
    )

    private val join: suspend (String) -> AppResult<MembershipCheckout?> = { key ->
        repository.join("creator-1", "tier-1", key)
    }

    private fun TestScope.history(flow: MembershipPaymentFlow): List<MembershipPayState> {
        val seen = mutableListOf<MembershipPayState>()
        work.launch { flow.state.collect { seen += it } }
        return seen
    }

    /** Joins and returns the attempt the sheet was requested for. */
    private fun TestScope.opened(flow: MembershipPaymentFlow): PaymentAttempt {
        flow.pay("join:creator-1:tier-1", "Supporter membership", join)
        runCurrent()
        val opening = flow.state.value as MembershipPayState.OpeningPayment
        flow.onSheetOpened(opening.request)
        assertThat(flow.state.value).isEqualTo(MembershipPayState.InSheet)
        return opening.request.attempt
    }

    @Test
    fun `the SDK reporting success never shows paid while the server says confirming`() = runTest(dispatcher) {
        api.subscribeAnswers += { checkout() }
        val flow = flow(work)
        val seen = history(flow)
        val attempt = opened(flow)
        assertThat(attempt.applicationId).isEqualTo(MEMBERSHIPS_PAYMENT_APPLICATION_ID)
        assertThat(attempt.referenceId).isEqualTo("pay-1")

        handoff.publish(PaymentOutcome.Succeeded("pay_rzp_1").toSheetResult(attempt).toHandoffEvent())
        runCurrent()
        assertThat(flow.state.value).isInstanceOf(MembershipPayState.Confirming::class.java)
        advanceUntilIdle()

        assertThat(seen.filterIsInstance<MembershipPayState.Paid>()).isEmpty()
        assertThat(flow.state.value).isEqualTo(MembershipPayState.StillConfirming)
        assertThat(api.statusReads).isGreaterThan(1)
    }

    @Test
    fun `access is shown as started only once the server says paid`() = runTest(dispatcher) {
        api.subscribeAnswers += { checkout() }
        api.statusAnswers += { payment("confirming") }
        api.statusAnswers += { offline() }
        api.statusAnswers += { refused(500, "INTERNAL_ERROR") }
        api.statusAnswers += { payment("paid") }
        val flow = flow(work)
        val seen = history(flow)
        val attempt = opened(flow)

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        advanceTimeBy(6_500) // reads at 1, 3 and 6 s: confirming, unreachable, a 500
        runCurrent()
        assertThat(flow.state.value).isInstanceOf(MembershipPayState.Confirming::class.java)
        advanceUntilIdle()

        assertThat(flow.state.value).isEqualTo(MembershipPayState.Paid)
        assertThat(seen.filterIsInstance<MembershipPayState.Failed>()).isEmpty()
        assertThat(api.statusReads).isEqualTo(4)
    }

    @Test
    fun `a dropped join is resent under the SAME key, so it is never a second join`() = runTest(dispatcher) {
        api.subscribeAnswers += { offline() }
        api.subscribeAnswers += { checkout() }
        val flow = flow(work)

        flow.pay("join:creator-1:tier-1", "Supporter membership", join)
        runCurrent()
        val failed = flow.state.value as MembershipPayState.Failed
        assertThat(failed.retryable).isTrue()

        flow.retry()
        runCurrent()
        assertThat(flow.state.value).isInstanceOf(MembershipPayState.OpeningPayment::class.java)
        val (first, second) = api.subscribeCalls
        assertThat(second.second).isEqualTo(first.second)
        assertThat(first.second).isNotEmpty()
    }

    @Test
    fun `the same decision pressed again after a drop reuses the key, another level mints a new one`() = runTest(
        dispatcher
    ) {
        api.subscribeAnswers += { offline() }
        api.subscribeAnswers += { offline() }
        api.subscribeAnswers += { offline() }
        val flow = flow(work)

        flow.pay("join:creator-1:tier-1", "a", join)
        runCurrent()
        flow.pay("join:creator-1:tier-1", "a", join)
        runCurrent()
        flow.pay("join:creator-1:tier-2", "b") { key -> repository.join("creator-1", "tier-2", key) }
        runCurrent()

        val keys = api.subscribeCalls.map { it.second }
        assertThat(keys[1]).isEqualTo(keys[0])
        assertThat(keys[2]).isNotEqualTo(keys[0])
    }

    @Test
    fun `a definite refusal opens nothing and the next try is a new key`() = runTest(dispatcher) {
        api.subscribeAnswers += { refused(409, "ALREADY_SUBSCRIBED") }
        api.subscribeAnswers += { checkout() }
        val flow = flow(work)

        flow.pay("join:creator-1:tier-1", "a", join)
        runCurrent()
        val refused = flow.state.value as MembershipPayState.Refused
        assertThat(refused.code).isEqualTo("ALREADY_SUBSCRIBED")
        assertThat(refused.message).isEqualTo("You're already a member.")
        assertThat(flow.currentKey).isNull()

        flow.pay("join:creator-1:tier-1", "a", join)
        runCurrent()
        assertThat(api.subscribeCalls[1].second).isNotEqualTo(api.subscribeCalls[0].second)
    }

    @Test
    fun `an expired checkout starts again at once under a new key`() = runTest(dispatcher) {
        api.subscribeAnswers += { refused(409, "CHECKOUT_EXPIRED") }
        api.subscribeAnswers += { checkout() }
        val flow = flow(work)

        flow.pay("join:creator-1:tier-1", "a", join)
        runCurrent()

        assertThat(flow.state.value).isInstanceOf(MembershipPayState.OpeningPayment::class.java)
        assertThat(api.subscribeCalls).hasSize(2)
        assertThat(api.subscribeCalls[1].second).isNotEqualTo(api.subscribeCalls[0].second)
    }

    @Test
    fun `a failed payment is paid again under the SAME key, which reopens it`() = runTest(dispatcher) {
        api.subscribeAnswers += { checkout() }
        api.subscribeAnswers += { checkout() }
        api.statusAnswers += { payment("failed") }
        val flow = flow(work)
        val attempt = opened(flow)

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        advanceUntilIdle()
        assertThat((flow.state.value as MembershipPayState.Failed).retryable).isTrue()

        flow.retry()
        runCurrent()
        val retried = flow.state.value as MembershipPayState.OpeningPayment
        assertThat(api.subscribeCalls[1].second).isEqualTo(api.subscribeCalls[0].second)
        // A new opening of the sheet is a new attempt for the same payment.
        assertThat(retried.request.attempt).isNotEqualTo(attempt)
        assertThat(retried.request.paymentId).isEqualTo(attempt.referenceId)
    }

    @Test
    fun `a refund outranks paid and is never access`() = runTest(dispatcher) {
        api.subscribeAnswers += { checkout() }
        api.statusAnswers += { payment("paid", refund = "pending") }
        val flow = flow(work)
        val seen = history(flow)
        val attempt = opened(flow)

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        advanceUntilIdle()

        assertThat(flow.state.value).isEqualTo(MembershipPayState.Refunding)
        assertThat(seen).doesNotContain(MembershipPayState.Paid)
    }

    @Test
    fun `a sheet that never opened charged nothing and offers Pay again`() = runTest(dispatcher) {
        api.subscribeAnswers += { checkout() }
        val flow = flow(work)
        val attempt = opened(flow)

        handoff.publish(PaymentHandoffEvent.Unavailable(attempt, "Razorpay is not available"))
        runCurrent()

        val failed = flow.state.value as MembershipPayState.Failed
        assertThat(failed.message).isEqualTo("Razorpay is not available")
        assertThat(api.statusReads).isEqualTo(0)
    }

    @Test
    fun `an ending for another attempt, or one already acted on, changes nothing`() = runTest(dispatcher) {
        api.subscribeAnswers += { checkout() }
        val flow = flow(work)
        val attempt = opened(flow)

        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt.copy(id = "someone-else")))
        handoff.publish(PaymentHandoffEvent.SheetClosed(PaymentAttempt("dating", attempt.referenceId, attempt.id)))
        runCurrent()
        assertThat(flow.state.value).isEqualTo(MembershipPayState.InSheet)

        handoff.consume(attempt)
        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt))
        runCurrent()
        assertThat(flow.state.value).isEqualTo(MembershipPayState.InSheet)
        assertThat(api.statusReads).isEqualTo(0)
    }

    @Test
    fun `after process death the server is asked first and the sheet is never reopened`() = runTest(dispatcher) {
        api.subscribeAnswers += { checkout() }
        val first = flow(work)
        opened(first)
        // The process dies with the sheet requested; the saved state survives.
        api.statusAnswers += { payment("paid") }
        val revived = flow(work, onStore = store)
        val seen = history(revived)

        assertThat(revived.hasSomethingToResume).isTrue()
        revived.resume(open = join, label = "Supporter membership")
        advanceUntilIdle()

        assertThat(revived.state.value).isEqualTo(MembershipPayState.Paid)
        assertThat(seen.filterIsInstance<MembershipPayState.OpeningPayment>()).isEmpty()
        assertThat(api.subscribeCalls).hasSize(1)
    }

    @Test
    fun `a request lost in a process death is resent under the key it left with`() = runTest(dispatcher) {
        api.subscribeAnswers += { offline() }
        val first = flow(work)
        first.pay("join:creator-1:tier-1", "a", join)
        runCurrent()
        // Simulate death mid-request: the phase is saved as submitting.
        store.values.keys.first { it.endsWith(".phase") }.let { store.values[it] = "SUBMITTING" }

        api.subscribeAnswers += { checkout() }
        val revived = flow(work, onStore = store)
        revived.resume(open = join, label = "a")
        runCurrent()

        assertThat(revived.state.value).isInstanceOf(MembershipPayState.OpeningPayment::class.java)
        assertThat(api.subscribeCalls[1].second).isEqualTo(api.subscribeCalls[0].second)
    }

    @Test
    fun `a checkout with no session cannot be paid now, and a paid replay is confirmed, not reopened`() = runTest(
        dispatcher
    ) {
        api.subscribeAnswers += { checkout(session = false) }
        val flow = flow(work)
        flow.pay("join:creator-1:tier-1", "a", join)
        runCurrent()
        assertThat(
            (flow.state.value as MembershipPayState.Failed).message
        ).isEqualTo("Online payment isn't available right now.")

        api.subscribeAnswers += { checkout(status = "active", session = false) }
        api.statusAnswers += { payment("paid") }
        flow.retry()
        advanceUntilIdle()
        assertThat(flow.state.value).isEqualTo(MembershipPayState.Paid)
    }

    @Test
    fun `nothing to pay is applied at once`() = runTest(dispatcher) {
        val flow = flow(work)
        flow.pay("change:sub-1:tier-0", "a") { AppResult.Success(null) }
        runCurrent()
        assertThat(flow.state.value).isInstanceOf(MembershipPayState.Applied::class.java)
        assertThat(flow.hasSomethingToResume).isFalse()
    }
}
