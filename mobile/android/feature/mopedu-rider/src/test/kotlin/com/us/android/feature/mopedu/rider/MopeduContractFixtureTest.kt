package com.us.android.feature.mopedu.rider

import com.google.common.truth.Truth.assertThat
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.ReceiptLine
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.SurgeReason
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.mopedu.rider.data.ActiveRideResponseDto
import com.us.android.feature.mopedu.rider.data.CouponValidationDto
import com.us.android.feature.mopedu.rider.data.MopeduError
import com.us.android.feature.mopedu.rider.data.MopeduErrorEnvelopeDto
import com.us.android.feature.mopedu.rider.data.OutstandingChargeDto
import com.us.android.feature.mopedu.rider.data.PaymentIntentDto
import com.us.android.feature.mopedu.rider.data.QuoteSnapshotDto
import com.us.android.feature.mopedu.rider.data.RefundListDto
import com.us.android.feature.mopedu.rider.data.RideDto
import com.us.android.feature.mopedu.rider.data.RidePaymentDto
import com.us.android.feature.mopedu.rider.data.RideReceiptDto
import com.us.android.feature.mopedu.rider.data.code
import com.us.android.feature.mopedu.rider.data.toIntent
import com.us.android.feature.mopedu.rider.data.toOption
import com.us.android.feature.mopedu.rider.data.toPayment
import com.us.android.feature.mopedu.rider.data.toReceipt
import kotlinx.serialization.KSerializer
import kotlinx.serialization.builtins.ListSerializer
import kotlinx.serialization.json.Json
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Every golden contract fixture from rider-service's pricing/payments lane
 * decodes into its DTO and maps into the domain.
 *
 * The fixtures are rider-service's handler-test goldens
 * (internal/http/testdata/contracts, written by internal/http/contracts_test.go),
 * copied byte for byte into src/test/resources/contracts and keyed here on the
 * backend's file names. Decoding is STRICT — unknown keys fail — so a key
 * renamed on either side, or a field the server added that the DTO does not
 * declare, fails here rather than defaulting silently in production (where the
 * platform Json ignores unknown keys).
 *
 * [parsers] must name every fixture in the directory and every parser must
 * have its fixture; [pending] names the routes the app calls whose fixtures
 * the backend has not published yet.
 */
class MopeduContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }

    private val contractsDir = File("src/test/resources/contracts")

    /** The fixtures rider-service publishes today, keyed on the backend file name. */
    private val parsers: Map<String, (raw: String) -> Unit> = mapOf(
        // TestContract_EstimateWithPeakWindowAndCoupon — POST /v1/rider/estimate 200:
        // one auto option in the "Morning peak" window with MOPEDU20 applied.
        "estimate_peak_window_coupon.json" to data(QuoteSnapshotDto.serializer()) { dto ->
            assertThat(dto.quoteId).isEqualTo("7f6d3b1e-9c2a-4e0f-8b5d-2a1c3e4f5a6b")
            assertThat(dto.options).hasSize(1)
            assertThat(dto.options.single().breakdown?.taxLines).hasSize(2)
            val option = dto.options.single().toOption()
            assertThat(option.totalFare.paise).isEqualTo(11978L)
            assertThat(option.showsSurgeChip).isTrue()
            assertThat(option.surgeReason).isEqualTo(SurgeReason.PEAK_HOURS)
            assertThat(option.surgeBasisPoints).isEqualTo(2500L)
            assertThat(option.windowName).isEqualTo("Morning peak")
            assertThat(option.hasDiscount).isTrue()
            assertThat(option.discount.paise).isEqualTo(2000L)
            assertThat(option.couponCode).isEqualTo("MOPEDU20")
            assertThat(option.includesOutstanding).isFalse()
            assertThat(option.breakdown.surgePaise).isEqualTo(2569L)
            assertThat(option.breakdown.platformFeePaise).isEqualTo(500L)
            assertThat(option.breakdown.taxPaise).isEqualTo(632L)
            assertThat(option.taxNote).isEqualTo("adviser confirmation pending")
        },
        // TestContract_CouponValidateError — GET /v1/rider/coupons/validate 422, a full handler round trip.
        "coupon_validate_error.json" to error(status = 422) { err ->
            assertThat(err).isInstanceOf(MopeduError.Refused::class.java)
            assertThat(err.code).isEqualTo("COUPONS_DISABLED")
        },
        // TestContract_PaymentIntent — POST /v1/rider/rides/{id}/payment/intent 200:
        // the client_session carries the PUBLISHABLE key id only.
        "payment_intent.json" to data(PaymentIntentDto.serializer()) { dto ->
            assertThat(dto.clientSession).containsExactly(
                "provider", "razorpay",
                "order_id", "order_R1x2y3z4",
                "key_id", "rzp_test_publishable",
                "merchant_display_name", "Mopedu",
            )
            val intent = dto.toIntent()
            assertThat(intent.intentId).isEqualTo("6f0e2c9a-1b3d-4e5f-8a7b-9c0d1e2f3a4b")
            assertThat(intent.amount.paise).isEqualTo(12451L)
            assertThat(intent.currency).isEqualTo("INR")
            assertThat(intent.status).isEqualTo("pending")
        },
        // TestContract_PaymentStatusPaid — GET /v1/rider/rides/{id}/payment 200 after the signed capture.
        "payment_status_paid.json" to data(RidePaymentDto.serializer()) { dto ->
            assertThat(dto.intentId).isEqualTo("6f0e2c9a-1b3d-4e5f-8a7b-9c0d1e2f3a4b")
            assertThat(dto.updatedAt).isEqualTo("2026-09-18T04:05:30Z")
            val payment = dto.toPayment()
            assertThat(payment.method).isEqualTo(PaymentMethod.UPI)
            assertThat(payment.status).isEqualTo(RidePaymentStatus.PAID)
            assertThat(payment.status.isSettled).isTrue()
            assertThat(payment.amount.paise).isEqualTo(12451L)
            assertThat(payment.refunded.paise).isEqualTo(0L)
        },
        // TestContract_ReceiptWithWaitingCharge — GET /v1/rider/rides/{id}/receipt 200:
        // cash, three waiting minutes, the captain has not confirmed yet.
        "receipt_waiting_charge.json" to data(RideReceiptDto.serializer()) { dto ->
            // The flat column is the legacy string; the app reads the payment block.
            assertThat(dto.paymentStatus).isEqualTo("pending_cash_confirmation")
            assertThat(dto.trackedDistanceM).isEqualTo(6702)
            val receipt = dto.toReceipt()
            assertThat(receipt.paymentMethod).isEqualTo(PaymentMethod.CASH)
            assertThat(receipt.paymentStatus).isEqualTo(RidePaymentStatus.CASH_PENDING)
            assertThat(receipt.totalFare.paise).isEqualTo(12451L)
            assertThat(receipt.breakdown.waitingChargePaise).isEqualTo(450L)
            assertThat(receipt.lines).contains(ReceiptLine("Waiting", MoneyPaise(450L)))
            assertThat(receipt.lines).contains(ReceiptLine("Discount", MoneyPaise(-2000L)))
            assertThat(receipt.lines.map { it.label }).doesNotContain("Toll")
            assertThat(receipt.refunds).isEmpty()
            assertThat(receipt.totalRefunded.paise).isEqualTo(0L)
            assertThat(receipt.taxNote).isEqualTo("adviser confirmation pending")
            assertThat(receipt.completedAtEpochMs).isNotNull()
        },
        // TestContract_ReceiptWithPartialRefund — the receipt of an online payment after one partial refund.
        "receipt_partial_refund.json" to data(RideReceiptDto.serializer()) { dto ->
            val receipt = dto.toReceipt()
            assertThat(receipt.paymentMethod).isEqualTo(PaymentMethod.UPI)
            assertThat(receipt.paymentStatus).isEqualTo(RidePaymentStatus.PARTIALLY_REFUNDED)
            assertThat(receipt.paymentStatus.isRefund).isTrue()
            assertThat(receipt.totalRefunded.paise).isEqualTo(5000L)
            val refund = receipt.refunds.single()
            assertThat(refund.id).isEqualTo("8d1c2b3a-4f5e-4a6b-9c8d-7e6f5a4b3c2d")
            assertThat(refund.amount.paise).isEqualTo(5000L)
            assertThat(refund.status).isEqualTo("refunded")
            assertThat(refund.reason).isEqualTo("captain ended the ride early")
            assertThat(refund.createdAtEpochMs).isNotNull()
        },
        // TestContract_RefundListRow — one row of GET /v1/rider/internal/admin/refunds 200.
        // The admin console's route, not the app's; pinned so the refund row and the
        // receipt's refunds[] stay one shape.
        "refund_list_row.json" to data(RefundListDto.serializer()) { dto ->
            val row = dto.items.single()
            assertThat(row.rideId).isEqualTo("9a8b7c6d-5e4f-4a3b-9c2d-1e0f9a8b7c6d")
            assertThat(row.amountPaise).isEqualTo(5000L)
            assertThat(row.status).isEqualTo("refunded")
            assertThat(row.providerReference).isEqualTo("cmd_7b2e4d6f")
        },
    )

    /**
     * Routes the app calls whose fixtures rider-service has NOT published yet
     * (no TestContract_* for them in contracts_test.go as of 2026-09-18):
     *
     *  - GET  /v1/rider/coupons/validate 200          (coupon_validate_ok.json)
     *  - POST /v1/rider/rides 201                     (ride_created.json)
     *  - GET  /v1/rider/rides/active 200              (ride_active.json)
     *  - GET  /v1/rider/rides/me 200                  (rides_me.json)
     *  - POST /v1/rider/rides/{id}/payment/switch-to-cash 200 (payment_switch_to_cash.json)
     *  - GET  /v1/rider/me/outstanding 200            (outstanding_list.json)
     *  - POST /v1/rider/me/outstanding/{id}/payment/intent 200 (outstanding_payment_intent.json)
     *
     * The names are what we expect; rename here when the backend publishes.
     * Each is ASSUMED away until its file appears, then the parser runs.
     */
    private val pending: Map<String, (raw: String) -> Unit> = mapOf(
        "coupon_validate_ok.json" to data(CouponValidationDto.serializer()) { assertThat(it.valid).isTrue() },
        "ride_created.json" to data(RideDto.serializer()) { assertThat(it.paymentMethod).isIn(listOf("cash", "upi", "card")) },
        "ride_active.json" to data(ActiveRideResponseDto.serializer()) { assertThat(it.active).isEqualTo(it.ride != null) },
        "rides_me.json" to data(ListSerializer(RideDto.serializer())) { list -> list.forEach { assertThat(it.id).isNotEmpty() } },
        "payment_switch_to_cash.json" to data(RidePaymentDto.serializer()) { assertThat(it.method).isEqualTo("cash") },
        "outstanding_list.json" to data(ListSerializer(OutstandingChargeDto.serializer())) { list ->
            list.forEach { assertThat(it.amountPaise).isGreaterThan(0L) }
        },
        "outstanding_payment_intent.json" to data(PaymentIntentDto.serializer()) { assertThat(it.clientSession).containsKey("order_id") },
    )

    private fun fixtures(): Set<String> = contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()

    @Test
    fun `every fixture has a parser and every parser a fixture`() {
        assertThat(fixtures() - parsers.keys - pending.keys).isEmpty()
        assertThat(parsers.keys - fixtures()).isEmpty()
        assertThat(parsers.keys.intersect(pending.keys)).isEmpty()
    }

    @Test
    fun `every fixture decodes strictly into its DTO`() {
        for ((name, parse) in parsers) {
            val raw = File(contractsDir, name).readText()
            try {
                parse(raw)
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed to parse: ${e.message}", e)
            }
        }
    }

    @Test
    fun `pending fixtures decode once the backend publishes them`() {
        val published = pending.keys.filter { File(contractsDir, it).exists() }
        assumeTrue("none of the pending fixtures is published yet: ${pending.keys}", published.isNotEmpty())
        for (name in published) {
            val raw = File(contractsDir, name).readText()
            try {
                pending.getValue(name)(raw)
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed to parse: ${e.message}", e)
            }
        }
    }

    private fun <T> data(serializer: KSerializer<T>, check: (T) -> Unit): (String) -> Unit = { raw ->
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), raw)
        assertThat(envelope.error).isNull()
        check(checkNotNull(envelope.data))
    }

    /** [status] is the HTTP status the Go test asserts for the route; the fixture body carries the envelope only. */
    private fun error(status: Int, check: (MopeduError) -> Unit): (String) -> Unit = { raw ->
        val envelope = strict.decodeFromString(MopeduErrorEnvelopeDto.serializer(), raw)
        assertThat(envelope.error).isNotNull()
        check(MopeduError.from(status, raw, strict))
    }
}
