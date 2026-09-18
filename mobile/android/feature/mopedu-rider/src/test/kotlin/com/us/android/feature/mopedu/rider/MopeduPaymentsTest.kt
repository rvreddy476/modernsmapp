package com.us.android.feature.mopedu.rider

import com.google.common.truth.Truth.assertThat
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.OutstandingCharge
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentSession
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.feature.mopedu.rider.data.MopeduError
import com.us.android.feature.mopedu.rider.data.MopeduResult
import com.us.android.feature.mopedu.rider.payment.MOPEDU_PAYMENT_APPLICATION_ID
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentRequest
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentStatusSource
import com.us.android.feature.mopedu.rider.payment.OutstandingPaymentStatusSource
import com.us.android.feature.mopedu.rider.payment.toPaymentSession
import com.us.android.feature.mopedu.rider.payment.toReading
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertThrows
import org.junit.Test

class MopeduPaymentsTest {

    @Test
    fun `the application id is mopedu and a request refuses another application's attempt or session`() {
        assertThat(MOPEDU_PAYMENT_APPLICATION_ID).isEqualTo("mopedu")
        val mine = PaymentAttempt("mopedu", "ride-1", "a-1")
        val session = PaymentSession("mopedu", "razorpay", "order_1", "key", 6500, "INR", "Mopedu ride")
        MopeduPaymentRequest(mine, session)

        assertThrows(IllegalArgumentException::class.java) { MopeduPaymentRequest(mine.copy(applicationId = "feast"), session) }
        assertThrows(IllegalArgumentException::class.java) { MopeduPaymentRequest(mine, session.copy(applicationId = "dating")) }
    }

    @Test
    fun `the server's ride payment status maps into the coordinator's vocabulary`() {
        fun reading(status: RidePaymentStatus, method: PaymentMethod = PaymentMethod.UPI) = payment(method, status).toReading()

        assertThat(reading(RidePaymentStatus.PAID)).isEqualTo(PaymentStatusReading.Paid)
        assertThat(reading(RidePaymentStatus.CASH_CONFIRMED, PaymentMethod.CASH)).isEqualTo(PaymentStatusReading.Paid)
        assertThat(reading(RidePaymentStatus.FAILED)).isEqualTo(PaymentStatusReading.Failed(retryable = true))
        assertThat(reading(RidePaymentStatus.REFUNDED)).isEqualTo(PaymentStatusReading.Refunded)
        assertThat(reading(RidePaymentStatus.PARTIALLY_REFUNDED)).isEqualTo(PaymentStatusReading.RefundPending)
        assertThat(reading(RidePaymentStatus.CONFIRMING)).isEqualTo(PaymentStatusReading.Confirming)
        assertThat(reading(RidePaymentStatus.CASH_PENDING, PaymentMethod.CASH)).isEqualTo(PaymentStatusReading.Confirming)
        // A status newer than this build keeps confirming; it is never paid.
        assertThat(reading(RidePaymentStatus.UNKNOWN)).isEqualTo(PaymentStatusReading.Confirming)
    }

    @Test
    fun `a read that cannot be made is unreachable, never failed`() = runTest {
        val repo = FakeRiderRepository()
        repo.lastPaymentAnswer = MopeduResult.Failure(MopeduError.Network(null))
        val source = MopeduPaymentStatusSource(repo)
        assertThat(source.applicationId).isEqualTo("mopedu")
        assertThat(source.status("ride-1")).isInstanceOf(PaymentStatusReading.Unreachable::class.java)

        repo.lastPaymentAnswer = MopeduResult.Success(payment(PaymentMethod.UPI, RidePaymentStatus.PAID))
        assertThat(source.status("ride-1")).isEqualTo(PaymentStatusReading.Paid)
    }

    @Test
    fun `an outstanding charge is paid once it has left the list or is listed paid`() = runTest {
        val repo = FakeRiderRepository()
        val source = OutstandingPaymentStatusSource(repo)
        fun charge(status: String) = OutstandingCharge("charge-1", "ride-9", "Cancellation fee", MoneyPaise(2000L), null, status)

        repo.outstandingAnswer = MopeduResult.Success(listOf(charge("pending")))
        assertThat(source.status("charge-1")).isEqualTo(PaymentStatusReading.Confirming)

        repo.outstandingAnswer = MopeduResult.Success(listOf(charge("failed")))
        assertThat(source.status("charge-1")).isEqualTo(PaymentStatusReading.Failed(retryable = true))

        repo.outstandingAnswer = MopeduResult.Success(listOf(charge("paid")))
        assertThat(source.status("charge-1")).isEqualTo(PaymentStatusReading.Paid)

        repo.outstandingAnswer = MopeduResult.Success(emptyList())
        assertThat(source.status("charge-1")).isEqualTo(PaymentStatusReading.Paid)

        repo.outstandingAnswer = MopeduResult.Failure(MopeduError.Unexpected(503, null))
        assertThat(source.status("charge-1")).isInstanceOf(PaymentStatusReading.Unreachable::class.java)
    }

    @Test
    fun `the sheet session is the server's client_session stamped mopedu, or null without one`() {
        val session = intent().toPaymentSession("Mopedu ride")!!
        assertThat(session.applicationId).isEqualTo("mopedu")
        assertThat(session.provider).isEqualTo("razorpay")
        assertThat(session.providerOrderId).isEqualTo("order_rzp_1")
        assertThat(session.keyId).isEqualTo("rzp_test_key")
        assertThat(session.amountMinor).isEqualTo(6500L)
        assertThat(session.currency).isEqualTo("INR")
        assertThat(intent(withSession = false).toPaymentSession("x")).isNull()
    }
}
