package com.us.android.feature.mopedu.rider

import com.google.common.truth.Truth.assertThat
import com.us.android.core.network.ApiEnvelope
import com.us.android.feature.mopedu.rider.data.CouponValidationDto
import com.us.android.feature.mopedu.rider.data.MopeduError
import com.us.android.feature.mopedu.rider.data.MopeduErrorEnvelopeDto
import com.us.android.feature.mopedu.rider.data.OutstandingChargeDto
import com.us.android.feature.mopedu.rider.data.PaymentIntentDto
import com.us.android.feature.mopedu.rider.data.QuoteSnapshotDto
import com.us.android.feature.mopedu.rider.data.RideDto
import com.us.android.feature.mopedu.rider.data.RidePaymentDto
import com.us.android.feature.mopedu.rider.data.RideReceiptDto
import com.us.android.feature.mopedu.rider.data.toOption
import kotlinx.serialization.KSerializer
import kotlinx.serialization.json.Json
import org.junit.Assume.assumeTrue
import org.junit.Test
import java.io.File

/**
 * Every golden contract fixture from rider-service's pricing/payments lane
 * decodes into its DTO — STRICTLY, so a renamed key fails here rather than
 * defaulting silently in production.
 *
 * The fixtures are not written yet (the backend lanes are building the
 * contract as this ships): until they are dropped into
 * `src/test/resources/contracts`, every test here is ASSUMED away rather than
 * failed. Names follow food-service's goldens: `<route>_<verb>_<status>[_case].json`.
 * [parsers] names the fixtures this build expects; a fixture present without a
 * parser fails the coverage test, so the DTO grows with the contract.
 */
class MopeduContractFixtureTest {

    private val strict = Json { ignoreUnknownKeys = false }

    private val contractsDir = File("src/test/resources/contracts")

    private val parsers: Map<String, (name: String, raw: String) -> Unit> = mapOf(
        "estimate_post_200.json" to data(QuoteSnapshotDto.serializer()) { dto ->
            assertThat(dto.quoteId).isNotEmpty()
            assertThat(dto.options).isNotEmpty()
            val options = dto.options.map { it.toOption() }
            options.forEach { assertThat(it.totalFare.paise).isGreaterThan(0L) }
        },
        "estimate_post_200_surge.json" to data(QuoteSnapshotDto.serializer()) { dto ->
            val option = dto.options.first().toOption()
            assertThat(option.showsSurgeChip).isTrue()
            assertThat(option.surgeBasisPoints).isGreaterThan(0L)
        },
        "estimate_post_200_coupon.json" to data(QuoteSnapshotDto.serializer()) { dto ->
            val option = dto.options.first().toOption()
            assertThat(option.hasDiscount).isTrue()
            assertThat(option.couponCode).isNotNull()
        },
        "estimate_post_200_outstanding.json" to data(QuoteSnapshotDto.serializer()) { dto ->
            assertThat(dto.options.first().toOption().includesOutstanding).isTrue()
        },
        "coupons_validate_get_200.json" to data(CouponValidationDto.serializer()) { assertThat(it.valid).isTrue() },
        "coupons_validate_get_422.json" to error { assertThat(it).isInstanceOf(MopeduError.Refused::class.java) },
        "rides_post_201.json" to data(RideDto.serializer()) { assertThat(it.paymentMethod).isIn(listOf("cash", "upi", "card")) },
        "rides_active_get_200.json" to data(RideDto.serializer()) { assertThat(it.id).isNotEmpty() },
        "rides_payment_intent_post_200.json" to data(PaymentIntentDto.serializer()) {
            assertThat(it.clientSession).containsKey("provider")
            assertThat(it.clientSession).containsKey("order_id")
            assertThat(it.clientSession).containsKey("key_id")
        },
        "rides_payment_get_200.json" to data(RidePaymentDto.serializer()) { assertThat(it.status).isNotEmpty() },
        "rides_payment_switch_to_cash_post_200.json" to data(RidePaymentDto.serializer()) { assertThat(it.method).isEqualTo("cash") },
        "rides_receipt_get_200.json" to data(RideReceiptDto.serializer()) { assertThat(it.rideId).isNotEmpty() },
        "me_outstanding_get_200.json" to data(kotlinx.serialization.builtins.ListSerializer(OutstandingChargeDto.serializer())) { list ->
            list.forEach { assertThat(it.amountPaise).isGreaterThan(0L) }
        },
        "me_outstanding_payment_intent_post_200.json" to data(PaymentIntentDto.serializer()) {
            assertThat(it.clientSession).containsKey("order_id")
        },
    )

    private fun fixtures(): Set<String> = contractsDir.listFiles { f -> f.name.endsWith(".json") }.orEmpty().map { it.name }.toSet()

    @Test
    fun `every fixture has a parser`() {
        assumeTrue("no rider-service fixtures yet", fixtures().isNotEmpty())
        assertThat(fixtures() - parsers.keys).isEmpty()
    }

    @Test
    fun `every fixture decodes strictly into its DTO`() {
        assumeTrue("no rider-service fixtures yet", fixtures().isNotEmpty())
        for (name in fixtures()) {
            val parse = parsers.getValue(name)
            val raw = File(contractsDir, name).readText()
            try {
                parse(name, raw)
            } catch (e: Throwable) {
                throw AssertionError("fixture $name failed to parse: ${e.message}", e)
            }
        }
    }

    private fun <T> data(serializer: KSerializer<T>, check: (T) -> Unit): (String, String) -> Unit = { _, raw ->
        val envelope = strict.decodeFromString(ApiEnvelope.serializer(serializer), raw)
        assertThat(envelope.error).isNull()
        check(checkNotNull(envelope.data))
    }

    private fun error(check: (MopeduError) -> Unit): (String, String) -> Unit = { name, raw ->
        val status = checkNotNull(STATUS.find(name)) { "fixture name $name carries no HTTP status" }.groupValues[1].toInt()
        strict.decodeFromString(MopeduErrorEnvelopeDto.serializer(), raw)
        check(MopeduError.from(status, raw, strict))
    }

    private companion object {
        val STATUS = Regex("""_(\d{3})(?:_|\.json)""")
    }
}
