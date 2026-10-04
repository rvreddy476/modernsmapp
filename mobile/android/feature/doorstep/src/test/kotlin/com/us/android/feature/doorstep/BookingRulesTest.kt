package com.us.android.feature.doorstep

import com.google.common.truth.Truth.assertThat
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.feature.doorstep.data.OutstandingDto
import com.us.android.feature.doorstep.data.RefundDto
import com.us.android.feature.doorstep.domain.BillRules
import com.us.android.feature.doorstep.domain.BookingRules
import com.us.android.feature.doorstep.domain.BookingStatus
import com.us.android.feature.doorstep.domain.ExtrasSummary
import com.us.android.feature.doorstep.domain.OutstandingRules
import com.us.android.feature.doorstep.domain.StepState
import com.us.android.feature.doorstep.model.Paise
import com.us.android.feature.doorstep.payment.DOORSTEP_PAYMENT_APPLICATION_ID
import com.us.android.feature.doorstep.payment.DoorstepReference
import com.us.android.feature.doorstep.payment.readingFor
import com.us.android.feature.doorstep.payment.toPaymentSession
import kotlinx.serialization.json.JsonObject
import org.junit.Test
import java.time.Duration
import java.time.Instant

/** The booking screen's pure rules: the OTP window, extras totals, dues, the timeline, and "paid" from the server only. */
class BookingRulesTest {

    // ── OTP visibility ──

    @Test
    fun `the start OTP shows only from assignment until the job starts`() {
        val shown = setOf("assigned", "en_route", "arrived")
        BookingStatus.entries.filter { it != BookingStatus.UNKNOWN }.forEach { status ->
            val otp = BookingRules.visibleStartOtp(booking(status = status.wire, startOtp = "4821"))
            if (status.wire in shown) {
                assertThat(otp).isEqualTo("4821")
            } else {
                assertThat(otp).isNull()
            }
        }
    }

    @Test
    fun `a leaked OTP on an unknown status, or a blank one, is never shown`() {
        assertThat(BookingRules.visibleStartOtp(booking(status = "something_new", startOtp = "4821"))).isNull()
        assertThat(BookingRules.visibleStartOtp(booking(status = "arrived", startOtp = "  "))).isNull()
        assertThat(BookingRules.visibleStartOtp(booking(status = "arrived", startOtp = null))).isNull()
    }

    // ── Extras ──

    @Test
    fun `extras totals add the server's per-extra totals by state`() {
        val summary = ExtrasSummary.of(
            listOf(
                extra("a", "approved", 30000),
                extra("b", "billed", 45000, quantity = 3),
                extra("c", "proposed", 12000),
                extra("d", "proposed", 8000, quantity = 2),
                extra("e", "declined", 99900),
                extra("f", "withdrawn", 50000),
            ),
        )
        assertThat(summary.agreedTotal).isEqualTo(Paise(75000))
        assertThat(summary.proposedTotal).isEqualTo(Paise(20000))
        assertThat(summary.proposed.map { it.id }).containsExactly("c", "d").inOrder()
    }

    @Test
    fun `only an open, pending or outstanding bill with money on it is payable`() {
        assertThat(BillRules.payable(bill("open"))).isTrue()
        assertThat(BillRules.payable(bill("payment_pending"))).isTrue()
        assertThat(BillRules.payable(bill("outstanding"))).isTrue()
        listOf("paid", "waived", "refunded").forEach { assertThat(BillRules.payable(bill(it))).isFalse() }
        assertThat(BillRules.payable(bill("open", amountPaise = 0))).isFalse()
        assertThat(BillRules.payable(null)).isFalse()
    }

    @Test
    fun `unpaid dues block a new booking, settled ones do not`() {
        assertThat(OutstandingRules.blocksBooking(OutstandingDto(50000, listOf(bill("outstanding"))))).isTrue()
        assertThat(OutstandingRules.blocksBooking(OutstandingDto(0, listOf(bill("outstanding"))))).isTrue()
        assertThat(OutstandingRules.blocksBooking(OutstandingDto(0, listOf(bill("paid"))))).isFalse()
        assertThat(OutstandingRules.blocksBooking(OutstandingDto(0, emptyList()))).isFalse()
        assertThat(OutstandingRules.blocksBooking(null)).isFalse()
    }

    // ── Timeline and countdown ──

    @Test
    fun `the timeline marks done, current and upcoming, and off-ramps have none`() {
        val enRoute = BookingRules.timeline(BookingStatus.EN_ROUTE)!!
        assertThat(enRoute.map { it.state }).containsExactly(
            StepState.DONE, StepState.DONE, StepState.CURRENT, StepState.UPCOMING, StepState.UPCOMING, StepState.UPCOMING,
        ).inOrder()
        assertThat(BookingRules.timeline(BookingStatus.COMPLETED)!!.all { it.state == StepState.DONE }).isTrue()
        assertThat(BookingRules.timeline(BookingStatus.AWAITING_EXTRAS_PAYMENT)!!.first { it.state == StepState.CURRENT }.label)
            .isEqualTo("Job started")
        assertThat(BookingRules.timeline(BookingStatus.PENDING_PAYMENT)!!.all { it.state == StepState.UPCOMING }).isTrue()
        assertThat(BookingRules.timeline(BookingStatus.CANCELLED)).isNull()
        assertThat(BookingRules.isLive(BookingStatus.CANCELLED)).isFalse()
        assertThat(BookingRules.isLive(BookingStatus.UNKNOWN)).isTrue()
    }

    @Test
    fun `the hold countdown never goes negative`() {
        val now = Instant.parse("2026-10-04T06:30:00Z")
        assertThat(BookingRules.holdRemaining(Instant.parse("2026-10-04T06:39:05Z"), now)).isEqualTo(Duration.ofSeconds(545))
        assertThat(BookingRules.countdownText(Duration.ofSeconds(545))).isEqualTo("9:05")
        assertThat(BookingRules.holdRemaining(Instant.parse("2026-10-04T06:29:00Z"), now)).isEqualTo(Duration.ZERO)
        assertThat(BookingRules.holdRemaining(null, now)).isNull()
    }

    // ── Paid only from the server's rows ──

    @Test
    fun `only a succeeded row for this reference is paid`() {
        val paid = payments(intent(status = "succeeded"))
        assertThat(paid.readingFor(DoorstepReference.BOOKING, BOOKING_ID)).isEqualTo(PaymentStatusReading.Paid)
        // The same id as an extras reference is not this payment.
        assertThat(paid.readingFor(DoorstepReference.EXTRAS, BOOKING_ID)).isEqualTo(PaymentStatusReading.Confirming)
        // Another booking's row is not this payment.
        assertThat(paid.readingFor(DoorstepReference.BOOKING, "other")).isEqualTo(PaymentStatusReading.Confirming)
        listOf("created", "pending", "authorised_soon").forEach { status ->
            assertThat(payments(intent(status = status)).readingFor(DoorstepReference.BOOKING, BOOKING_ID))
                .isEqualTo(PaymentStatusReading.Confirming)
        }
        assertThat(payments(intent(status = "failed")).readingFor(DoorstepReference.BOOKING, BOOKING_ID))
            .isEqualTo(PaymentStatusReading.Failed(retryable = true))
    }

    @Test
    fun `a refund outranks paid`() {
        val refund = RefundDto("r1", PAYMENT_ID, "late_capture", 224800, "pending", "2026-10-04T06:50:00Z")
        assertThat(payments(intent(status = "succeeded"), refunds = listOf(refund)).readingFor(DoorstepReference.BOOKING, BOOKING_ID))
            .isEqualTo(PaymentStatusReading.RefundPending)
        assertThat(payments(intent(status = "partially_refunded")).readingFor(DoorstepReference.BOOKING, BOOKING_ID))
            .isEqualTo(PaymentStatusReading.RefundPending)
        assertThat(payments(intent(status = "refunded")).readingFor(DoorstepReference.BOOKING, BOOKING_ID))
            .isEqualTo(PaymentStatusReading.Refunded)
        // A finished refund on ANOTHER payment does not touch this one.
        val other = refund.copy(paymentId = "other-payment")
        assertThat(payments(intent(status = "succeeded"), refunds = listOf(other)).readingFor(DoorstepReference.BOOKING, BOOKING_ID))
            .isEqualTo(PaymentStatusReading.Paid)
    }

    @Test
    fun `the sheet session is the server's checkout, stamped doorstep, or nothing`() {
        val session = intent().toPaymentSession("Doorstep booking")!!
        assertThat(session.applicationId).isEqualTo(DOORSTEP_PAYMENT_APPLICATION_ID)
        assertThat(session.providerOrderId).isEqualTo("order_RZP1")
        assertThat(session.keyId).isEqualTo("rzp_test_publishable")
        assertThat(session.amountMinor).isEqualTo(224800)
        assertThat(session.currency).isEqualTo("INR")
        assertThat(session.merchantDisplayName).isEqualTo("Doorstep")
        assertThat(intent(checkout = JsonObject(emptyMap())).toPaymentSession("x")).isNull()
    }
}
