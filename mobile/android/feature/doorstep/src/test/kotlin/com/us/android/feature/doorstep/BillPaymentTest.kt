package com.us.android.feature.doorstep

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.testing.MainDispatcherRule
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.payment.BillPayState
import com.us.android.feature.doorstep.payment.BillPayment
import com.us.android.feature.doorstep.payment.DoorstepReference
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceTimeBy
import kotlinx.coroutines.test.runCurrent
import kotlinx.coroutines.test.runTest
import org.junit.Rule
import org.junit.Test

/**
 * The extras bill (and dues) payment keeps checkout's rules: the reference is
 * the bill, its row is read from the booking's payments, and only the
 * server's `succeeded` on the EXTRAS reference settles it — the booking's own
 * paid row does not.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class BillPaymentTest {

    private val dispatcher = StandardTestDispatcher()

    @get:Rule
    val main = MainDispatcherRule(dispatcher)

    private val repo = FakeDoorstepRepository()
    private val handoff = PaymentHandoff()

    @Test
    fun `a bill is paid only by its own succeeded extras row`() = runTest(dispatcher) {
        var settled: String? = null
        val pay = BillPayment(backgroundScope, repo, handoff, confirmingOnlyCoordinator(), SavedStateHandle(), onSettled = { settled = it })
        runCurrent()
        pay.pay(BOOKING_ID, "bill-1")
        runCurrent()
        val opening = pay.state.value as BillPayState.Opening
        assertThat(opening.request.referenceId).isEqualTo("bill-1")

        val bookingPaid = intent(status = "succeeded")
        val extrasPending = intent(referenceId = "bill-1", referenceType = DoorstepReference.EXTRAS, status = "pending", paymentId = "p-extras")
        val extrasPaid = extrasPending.copy(status = "succeeded")
        repo.paymentsResults.addAll(
            listOf(
                // The booking itself is paid; the bill is not yet.
                DoorstepResult.Success(payments(bookingPaid, extrasPending)),
                DoorstepResult.Success(payments(bookingPaid, extrasPaid)),
            ),
        )
        handoff.publish(PaymentHandoffEvent.SheetClosed(opening.request.attempt))
        runCurrent()
        assertThat(pay.state.value).isEqualTo(BillPayState.Confirming("bill-1"))
        advanceTimeBy(1_100)
        runCurrent()
        assertThat(pay.state.value).isEqualTo(BillPayState.Confirming("bill-1"))
        assertThat(settled).isNull()
        advanceTimeBy(2_100)
        runCurrent()
        assertThat(pay.state.value).isEqualTo(BillPayState.Paid("bill-1"))
        assertThat(settled).isEqualTo("bill-1")
    }

    @Test
    fun `a sheet that never opened is a failure the customer can retry, not a payment`() = runTest(dispatcher) {
        val pay = BillPayment(backgroundScope, repo, handoff, confirmingOnlyCoordinator(), SavedStateHandle())
        runCurrent()
        pay.pay(BOOKING_ID, "bill-1")
        runCurrent()
        val attempt = (pay.state.value as BillPayState.Opening).request.attempt
        handoff.publish(PaymentHandoffEvent.Unavailable(attempt, "no sheet"))
        runCurrent()
        assertThat(pay.state.value).isEqualTo(BillPayState.Failed("bill-1", "no sheet"))
        assertThat(repo.paymentReads).isEqualTo(0)
    }
}
