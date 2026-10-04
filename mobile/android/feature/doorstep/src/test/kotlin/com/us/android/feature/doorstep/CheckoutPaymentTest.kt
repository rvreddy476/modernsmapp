package com.us.android.feature.doorstep

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.toHandoffEvent
import com.us.android.core.payments.toSheetResult
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.doorstep.checkout.CheckoutContinuation
import com.us.android.feature.doorstep.checkout.CheckoutState
import com.us.android.feature.doorstep.checkout.CheckoutViewModel
import com.us.android.feature.doorstep.data.BookingCreatedDto
import com.us.android.feature.doorstep.data.DoorstepCodes
import com.us.android.feature.doorstep.data.DoorstepError
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.payment.DOORSTEP_PAYMENT_APPLICATION_ID
import com.us.android.feature.doorstep.payment.DoorstepPaymentConfig
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
import java.io.IOException

/**
 * Doorstep checkout's money rules: the Idempotency-Key is in saved state
 * before `POST /bookings` leaves; the SDK can never say paid, only the
 * server's payment rows can; a read that fails keeps confirming; a failure is
 * retried on the same booking; process death resumes from the server and
 * never books twice or reopens a sheet.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CheckoutPaymentTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val repo = FakeDoorstepRepository()
    private val handoff = PaymentHandoff()

    private fun handle() = SavedStateHandle(
        mapOf(
            "quoteId" to "3f6b8a52-0c1d-4e7f-9a2b-5c6d7e8f9a01",
            "addressId" to ADDRESS_ID,
            "slotStart" to "2026-10-04T10:00:00Z",
            "requireFemalePro" to true,
        ),
    )

    private fun vm(handle: SavedStateHandle = handle(), environment: String = "prod") =
        CheckoutViewModel(handle, repo, handoff, confirmingOnlyCoordinator(), DoorstepPaymentConfig.forEnvironment(environment))

    /** A booking whose intent carries payments-service's stub session (a development stack). */
    private fun stubBooking() {
        repo.createBookingResult = { _, _ -> DoorstepResult.Success(BookingCreatedDto(booking(), intent(status = "pending", checkout = stubCheckout()))) }
    }

    private fun TestScope.ready(model: CheckoutViewModel) {
        advanceUntilIdle()
        assertThat(model.state.value).isInstanceOf(CheckoutState.Ready::class.java)
    }

    @Test
    fun `the idempotency key is in saved state before the booking request leaves`() = runTest(dispatcher) {
        val handle = handle()
        var savedAtCall: String? = null
        repo.createBookingResult = { key, _ ->
            // Read from saved state AT THE MOMENT of the call: process death now must not lose it.
            savedAtCall = handle.get<String>(CheckoutContinuation.KEY_BOOKING_KEY)
            assertThat(handle.get<String>(CheckoutContinuation.KEY_PHASE)).isEqualTo("SUBMITTING")
            DoorstepResult.Success(BookingCreatedDto(booking(), intent()))
        }
        val model = vm(handle)
        ready(model)
        model.book()
        advanceUntilIdle()

        assertThat(repo.bookingKeys).hasSize(1)
        assertThat(savedAtCall).isEqualTo(repo.bookingKeys.single())
        assertThat(repo.bookingRequests.single().requireFemalePro).isTrue()
        val opening = model.state.value as CheckoutState.OpeningPayment
        assertThat(opening.request.attempt.applicationId).isEqualTo(DOORSTEP_PAYMENT_APPLICATION_ID)
        assertThat(opening.request.referenceId).isEqualTo(BOOKING_ID)
    }

    @Test
    fun `a lost response is resent under the same key after process death, never a new one`() = runTest(dispatcher) {
        val handle = handle()
        repo.createBookingResult = { _, _ -> DoorstepResult.Failure(DoorstepError.Network(IOException("lost"))) }
        val first = vm(handle)
        ready(first)
        first.book()
        advanceUntilIdle()
        val firstKey = repo.bookingKeys.single()
        // A network failure keeps the key: the booking may exist.
        assertThat(handle.get<String>(CheckoutContinuation.KEY_BOOKING_KEY)).isEqualTo(firstKey)

        // Process death mid-request: the phase says SUBMITTING with no booking id.
        handle[CheckoutContinuation.KEY_PHASE] = "SUBMITTING"
        repo.createBookingResult = { _, _ -> DoorstepResult.Success(BookingCreatedDto(booking(), intent())) }
        vm(handle)
        advanceUntilIdle()
        assertThat(repo.bookingKeys).containsExactly(firstKey, firstKey)
    }

    @Test
    fun `a definite refusal clears the key, so the next decision is a new booking`() = runTest(dispatcher) {
        val handle = handle()
        repo.createBookingResult = { _, _ ->
            DoorstepResult.Failure(DoorstepError.Refused(409, DoorstepCodes.SLOT_TAKEN, "taken"))
        }
        val model = vm(handle)
        ready(model)
        model.book()
        advanceUntilIdle()
        val ready = model.state.value as CheckoutState.Ready
        assertThat(ready.pickAnotherSlot).isTrue()
        assertThat(handle.get<String>(CheckoutContinuation.KEY_BOOKING_KEY)).isNull()

        repo.createBookingResult = { _, _ -> DoorstepResult.Failure(DoorstepError.Refused(409, DoorstepCodes.OUTSTANDING_DUE, "dues")) }
        model.book()
        advanceUntilIdle()
        assertThat(model.state.value).isEqualTo(CheckoutState.BlockedByDues)
        assertThat(repo.bookingKeys.distinct()).hasSize(2)
    }

    @Test
    fun `the SDK reporting success is not paid — only the server's succeeded row is`() = runTest(dispatcher) {
        val model = vm()
        ready(model)
        model.book()
        advanceUntilIdle()
        val attempt = (model.state.value as CheckoutState.OpeningPayment).request.attempt

        // The server still says pending, then the read fails, then succeeded.
        repo.paymentsResults.addAll(
            listOf(
                DoorstepResult.Success(payments(intent(status = "pending"))),
                DoorstepResult.Failure(DoorstepError.Network(IOException("flaky"))),
                DoorstepResult.Success(payments(intent(status = "succeeded"))),
            ),
        )
        handoff.publish(PaymentOutcome.Succeeded(providerPaymentId = "pay_sdk_says_yes").toSheetResult(attempt).toHandoffEvent())
        runCurrent()
        assertThat(model.state.value).isInstanceOf(CheckoutState.Confirming::class.java)

        advanceTimeBy(1_100) // first read: pending
        runCurrent()
        assertThat(model.state.value).isInstanceOf(CheckoutState.Confirming::class.java)
        advanceTimeBy(2_100) // second read: unreachable — still confirming, never failed
        runCurrent()
        assertThat(model.state.value).isInstanceOf(CheckoutState.Confirming::class.java)
        advanceTimeBy(3_100) // third read: succeeded
        runCurrent()
        assertThat(model.state.value).isEqualTo(CheckoutState.Paid(BOOKING_ID))
        assertThat(repo.paymentReads).isEqualTo(3)
    }

    @Test
    fun `an ending for another attempt is ignored`() = runTest(dispatcher) {
        val model = vm()
        ready(model)
        model.book()
        advanceUntilIdle()
        val attempt = (model.state.value as CheckoutState.OpeningPayment).request.attempt
        handoff.publish(PaymentHandoffEvent.SheetClosed(attempt.copy(id = "someone-else")))
        runCurrent()
        assertThat(model.state.value).isInstanceOf(CheckoutState.OpeningPayment::class.java)
        assertThat(repo.paymentReads).isEqualTo(0)
    }

    @Test
    fun `a failed payment is retried on the same booking with a new attempt, never re-booked`() = runTest(dispatcher) {
        val model = vm()
        ready(model)
        model.book()
        advanceUntilIdle()
        val first = (model.state.value as CheckoutState.OpeningPayment).request.attempt
        repo.paymentsFallback = DoorstepResult.Success(payments(intent(status = "failed")))
        handoff.publish(PaymentHandoffEvent.SheetClosed(first))
        advanceTimeBy(1_100)
        runCurrent()
        assertThat(model.state.value).isInstanceOf(CheckoutState.PaymentFailed::class.java)

        model.retryPayment()
        advanceUntilIdle()
        val second = (model.state.value as CheckoutState.OpeningPayment).request.attempt
        assertThat(second.referenceId).isEqualTo(first.referenceId)
        assertThat(second.id).isNotEqualTo(first.id)
        assertThat(repo.paymentIntentCalls).isEqualTo(1)
        assertThat(repo.bookingKeys).hasSize(1)
    }

    @Test
    fun `an expired hold on retry sends the customer back to the slots`() = runTest(dispatcher) {
        val model = vm()
        ready(model)
        model.book()
        advanceUntilIdle()
        val first = (model.state.value as CheckoutState.OpeningPayment).request.attempt
        handoff.publish(PaymentHandoffEvent.Unavailable(first, "no sheet"))
        runCurrent()
        repo.paymentIntentResult = DoorstepResult.Failure(DoorstepError.Refused(410, DoorstepCodes.HOLD_EXPIRED, "expired"))
        model.retryPayment()
        advanceUntilIdle()
        assertThat(model.state.value).isEqualTo(CheckoutState.HoldExpired)
    }

    @Test
    fun `after process death with a sheet requested the server is asked and nothing is reopened or re-booked`() = runTest(dispatcher) {
        val handle = handle()
        val first = vm(handle)
        ready(first)
        first.book()
        advanceUntilIdle()
        assertThat(first.state.value).isInstanceOf(CheckoutState.OpeningPayment::class.java)

        repo.paymentsFallback = DoorstepResult.Success(payments(intent(status = "succeeded")))
        val revived = vm(handle)
        advanceUntilIdle()
        assertThat(revived.state.value).isEqualTo(CheckoutState.Paid(BOOKING_ID))
        assertThat(repo.bookingKeys).hasSize(1)
        assertThat(repo.paymentIntentCalls).isEqualTo(0)
    }

    // ── The dev stub gateway ──

    @Test
    fun `a dev build settles a stub checkout through stub-confirm and is paid only when GET payment says so`() = runTest(dispatcher) {
        stubBooking()
        repo.paymentsResults.addAll(
            listOf(
                DoorstepResult.Success(payments(intent(status = "pending", checkout = stubCheckout()))),
                DoorstepResult.Success(payments(intent(status = "succeeded", checkout = stubCheckout()))),
            ),
        )
        val model = vm(environment = "dev")
        ready(model)
        model.book()
        runCurrent()
        // No sheet: the stub is asked to settle, and the customer sees "confirming".
        assertThat(repo.stubConfirmCalls).isEqualTo(1)
        assertThat(model.state.value).isInstanceOf(CheckoutState.Confirming::class.java)
        assertThat(repo.paymentReads).isEqualTo(0)

        advanceTimeBy(1_100) // first read: still pending — the stub-confirm success alone is NOT paid
        runCurrent()
        assertThat(model.state.value).isInstanceOf(CheckoutState.Confirming::class.java)
        advanceTimeBy(2_100) // second read: succeeded (the signed event landed)
        runCurrent()
        assertThat(model.state.value).isEqualTo(CheckoutState.Paid(BOOKING_ID))
        assertThat(repo.paymentReads).isEqualTo(2)
        assertThat(repo.stubConfirmCalls).isEqualTo(1)
    }

    @Test
    fun `a stub-confirm that succeeds while the server never says succeeded is never paid`() = runTest(dispatcher) {
        stubBooking()
        repo.paymentsFallback = DoorstepResult.Success(payments(intent(status = "pending", checkout = stubCheckout())))
        val model = vm(environment = "dev")
        ready(model)
        model.book()
        val seen = mutableListOf<CheckoutState>()
        val watcher = backgroundScope.launch { model.state.collect { seen += it } }
        advanceUntilIdle()
        watcher.cancel()
        assertThat(repo.stubConfirmCalls).isEqualTo(1)
        assertThat(repo.paymentReads).isGreaterThan(0)
        assertThat(seen.none { it is CheckoutState.Paid }).isTrue()
        assertThat(model.state.value).isEqualTo(CheckoutState.StillConfirming(BOOKING_ID))
    }

    @Test
    fun `a stub checkout outside a dev build is unavailable and never calls stub-confirm`() = runTest(dispatcher) {
        stubBooking()
        val model = vm(environment = "prod")
        ready(model)
        model.book()
        advanceUntilIdle()
        assertThat(model.state.value).isInstanceOf(CheckoutState.PaymentFailed::class.java)
        assertThat(repo.stubConfirmCalls).isEqualTo(0)
        assertThat(repo.paymentReads).isEqualTo(0)
    }

    @Test
    fun `a server with a real provider refuses the stub and nothing is polled or paid`() = runTest(dispatcher) {
        stubBooking()
        repo.stubConfirmResult = DoorstepResult.Failure(DoorstepError.Refused(409, DoorstepCodes.STUB_UNAVAILABLE, "real provider"))
        val model = vm(environment = "dev")
        ready(model)
        model.book()
        advanceUntilIdle()
        val failed = model.state.value as CheckoutState.PaymentFailed
        assertThat(failed.bookingId).isEqualTo(BOOKING_ID)
        assertThat(repo.paymentReads).isEqualTo(0)

        // Off a development stack the route is a 404: the same refusal.
        repo.stubConfirmResult = DoorstepResult.Failure(DoorstepError.Refused(404, DoorstepCodes.NOT_FOUND, "not here"))
        repo.paymentIntentResult = DoorstepResult.Success(intent(status = "pending", checkout = stubCheckout()))
        model.retryPayment()
        advanceUntilIdle()
        assertThat(model.state.value).isInstanceOf(CheckoutState.PaymentFailed::class.java)
        assertThat(repo.stubConfirmCalls).isEqualTo(2)
        assertThat(repo.bookingKeys).hasSize(1)
    }

    @Test
    fun `a lost stub-confirm response still asks the server, which alone decides`() = runTest(dispatcher) {
        stubBooking()
        repo.stubConfirmResult = DoorstepResult.Failure(DoorstepError.Network(IOException("lost")))
        repo.paymentsFallback = DoorstepResult.Success(payments(intent(status = "succeeded", checkout = stubCheckout())))
        val model = vm(environment = "dev")
        ready(model)
        model.book()
        advanceUntilIdle()
        assertThat(model.state.value).isEqualTo(CheckoutState.Paid(BOOKING_ID))
        assertThat(repo.paymentReads).isGreaterThan(0)
    }

    @Test
    fun `a late capture being refunded is never shown as booked`() = runTest(dispatcher) {
        val handle = handle()
        val first = vm(handle)
        ready(first)
        first.book()
        advanceUntilIdle()
        repo.paymentsFallback = DoorstepResult.Success(payments(intent(status = "refunded")))
        val revived = vm(handle)
        advanceUntilIdle()
        assertThat(revived.state.value).isEqualTo(CheckoutState.Refunding(BOOKING_ID))
    }
}
