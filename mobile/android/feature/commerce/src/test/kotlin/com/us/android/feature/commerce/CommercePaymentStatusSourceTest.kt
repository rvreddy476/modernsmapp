package com.us.android.feature.commerce

import com.google.common.truth.Truth.assertThat
import com.google.common.truth.Truth.assertWithMessage
import com.us.android.core.commerce.model.OrderPayment
import com.us.android.core.commerce.model.OrderPaymentState
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.OrderPaymentDto
import com.us.android.core.commerce.network.PaymentStatusDto
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.feature.commerce.checkout.CommercePaymentStatusSource
import com.us.android.feature.commerce.checkout.toReading
import kotlinx.coroutines.test.runTest
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.Test
import retrofit2.Response
import java.io.File

/**
 * MStore's payment status source (2026-09-30): the three-state
 * `GET /orders/:id/payment`, the fallback to the older route, and which
 * failures stop the poll.
 *
 * Before this every failed read was Unreachable, so a poll against an order
 * the server said did not exist ran for the full 180 s. Now a permanent
 * refusal is Failed and the coordinator stops; a 5xx or a dropped
 * connection is still Unreachable, because "we do not know yet" and "the
 * payment failed" are different facts.
 *
 * The mapping table is pinned twice: against inline bodies in the
 * contract §4.5 shape (including states no golden shows yet, such as
 * `refunded`), and against commerce-service's own
 * `payment/order_payment_get_*.json` goldens, which `:core:commerce` copies
 * byte for byte and [com.us.android.core.commerce.CommerceContractFixtureTest]
 * keeps identical to the server's.
 */
class CommercePaymentStatusSourceTest {

    private val json = NetworkModule.provideJson()

    private fun body(status: String, refund: String?) =
        """{"order_id":"0b8f3c52-8d0a-4c55-9a55-3f3f0e1a0030","status":"$status","amount_minor":25000,""" +
            """"currency":"INR","refund_status":${refund?.let { "\"$it\"" } ?: "null"},""" +
            """"updated_at":"2026-09-13T06:31:05Z"}"""

    private fun decoded(status: String, refund: String?): OrderPaymentDto =
        json.decodeFromString(OrderPaymentDto.serializer(), body(status, refund))

    // ─── The mapping table ───────────────────────────────────────────

    @Test
    fun `the three-state fixtures map to the readings checkout acts on`() {
        val expected = mapOf(
            ("confirming" to null) to PaymentStatusReading.Confirming,
            ("paid" to null) to PaymentStatusReading.Paid,
            ("failed" to null) to PaymentStatusReading.Failed(retryable = true),
            ("paid" to "pending") to PaymentStatusReading.RefundPending,
            ("paid" to "partially_refunded") to PaymentStatusReading.Refunded,
            ("paid" to "refunded") to PaymentStatusReading.Refunded,
            // The server's vocabulary can grow ahead of the app.
            ("settling" to null) to PaymentStatusReading.Confirming,
        )
        for ((case, reading) in expected) {
            val (status, refund) = case
            val dto = decoded(status, refund)
            val payment = OrderPayment(
                orderId = dto.orderId,
                state = OrderPaymentState.from(dto.status),
                refundStatus = dto.refundStatus,
                amount = dto.amountMinor,
                currency = dto.currency,
            )
            assertWithMessage("$status / $refund").that(payment.toReading()).isEqualTo(reading)
            assertThat(payment.amount).isEqualTo(Paise(25_000))
        }
    }

    // ─── Over the wire ───────────────────────────────────────────────

    private class StatusApi : FakeCommerceApi() {
        var threeState: () -> Response<ApiEnvelope<OrderPaymentDto>> = { unused() }
        var legacy: () -> Response<ApiEnvelope<PaymentStatusDto>> = { unused() }
        var threeStateReads = 0
        var legacyReads = 0

        override suspend fun orderPayment(orderId: String): Response<ApiEnvelope<OrderPaymentDto>> {
            threeStateReads++
            return threeState()
        }

        override suspend fun paymentStatus(orderId: String): Response<ApiEnvelope<PaymentStatusDto>> {
            legacyReads++
            return legacy()
        }
    }

    private fun source(api: StatusApi) = CommercePaymentStatusSource(CommerceRepository(api))

    private fun <T> errorResponse(code: Int, body: String): Response<ApiEnvelope<T>> =
        Response.error(code, body.toResponseBody("application/json".toMediaType()))

    @Test
    fun `the three-state route is read first and its answer is the reading`() = runTest {
        val api = StatusApi().apply { threeState = { envelope(decoded("paid", null)) } }

        assertThat(source(api).status("o-1")).isEqualTo(PaymentStatusReading.Paid)
        assertThat(api.legacyReads).isEqualTo(0)
    }

    @Test
    fun `a server without the route falls back ONCE to the older status read, and stays there`() = runTest {
        val api = StatusApi().apply {
            threeState = { notFound() }
            legacy = { envelope(PaymentStatusDto(orderId = "o-1", orderStatus = "paid", paymentStatus = "paid")) }
        }
        val source = source(api)

        assertThat(source.status("o-1")).isEqualTo(PaymentStatusReading.Paid)
        assertThat(source.status("o-1")).isEqualTo(PaymentStatusReading.Paid)

        assertThat(api.threeStateReads).isEqualTo(1)
        assertThat(api.legacyReads).isEqualTo(2)
    }

    @Test
    fun `ORDER_NOT_FOUND stops the poll as a non-retryable failure`() = runTest {
        val api = StatusApi().apply {
            threeState = { errorResponse(404, """{"error":{"code":"ORDER_NOT_FOUND","message":"order not found"}}""") }
        }

        val reading = source(api).status("o-1")

        assertThat(reading).isInstanceOf(PaymentStatusReading.Failed::class.java)
        assertThat((reading as PaymentStatusReading.Failed).retryable).isFalse()
        assertThat(api.legacyReads).isEqualTo(0)
    }

    @Test
    fun `a 403 stops the poll too`() = runTest {
        val api = StatusApi().apply {
            threeState = { errorResponse(403, """{"error":{"code":"NOT_YOUR_ORDER","message":"no"}}""") }
        }

        assertThat(source(api).status("o-1")).isInstanceOf(PaymentStatusReading.Failed::class.java)
    }

    @Test
    fun `a 5xx and a network failure keep confirming as Unreachable`() = runTest {
        val broken = StatusApi().apply {
            threeState = { errorResponse(503, """{"error":{"code":"UPSTREAM","message":"down"}}""") }
        }
        val offline = StatusApi().apply { threeState = { throw java.io.IOException("no route to host") } }

        assertThat(source(broken).status("o-1")).isInstanceOf(PaymentStatusReading.Unreachable::class.java)
        assertThat(source(offline).status("o-1")).isInstanceOf(PaymentStatusReading.Unreachable::class.java)
    }

    @Test
    fun `a permanent refusal on the older route stops the poll as well`() = runTest {
        val api = StatusApi().apply {
            threeState = { notFound() }
            legacy = { errorResponse(404, """{"error":{"code":"ORDER_NOT_FOUND","message":"order not found"}}""") }
        }

        assertThat(source(api).status("o-1")).isInstanceOf(PaymentStatusReading.Failed::class.java)
    }

    // ─── Against commerce-service's own goldens ───────────────────────

    /**
     * The payment goldens, as `:core:commerce` copied them byte for byte from
     * commerce-service (`payment/order_payment_get_*.json`), each served
     * through the repository at the status in its name and read by the
     * source. Every such golden must be in the table, so a new server state
     * cannot land without a decision about what checkout does with it.
     */
    @Test
    fun `every commerce order-payment golden maps to the reading checkout acts on`() = runTest {
        val goldens = File("../../core/commerce/src/test/resources/contracts/payment")
        assertThat(goldens.isDirectory).isTrue()
        val expected = mapOf(
            "order_payment_get_200_confirming.json" to PaymentStatusReading.Confirming,
            "order_payment_get_200_failed.json" to PaymentStatusReading.Failed(retryable = true),
            "order_payment_get_200_paid.json" to PaymentStatusReading.Paid,
            "order_payment_get_200_paid_after_stub_confirm.json" to PaymentStatusReading.Paid,
            "order_payment_get_200_paid_refund_pending.json" to PaymentStatusReading.RefundPending,
        )
        val present = goldens.listFiles { f -> f.name.startsWith("order_payment_get_") }.orEmpty().map { it.name }
        assertThat(present).containsExactlyElementsIn(expected.keys + FORBIDDEN_GOLDEN)

        for ((name, reading) in expected) {
            val raw = File(goldens, name).readText()
            val body = json.decodeFromString(ApiEnvelope.serializer(OrderPaymentDto.serializer()), raw)
            val api = StatusApi().apply { threeState = { Response.success(body) } }
            assertWithMessage(name).that(source(api).status("o-1")).isEqualTo(reading)
        }

        // The refusal golden: a 403 is permanent, so the poll stops.
        val forbidden = StatusApi().apply {
            threeState = { errorResponse(403, File(goldens, FORBIDDEN_GOLDEN).readText()) }
        }
        val reading = source(forbidden).status("o-1")
        assertThat(reading).isInstanceOf(PaymentStatusReading.Failed::class.java)
        assertThat((reading as PaymentStatusReading.Failed).retryable).isFalse()
        assertThat(forbidden.legacyReads).isEqualTo(0)
    }

    @Test
    fun `the failure-to-reading rule, as a table`() {
        assertThat(CommerceError.OrderNotFound.toReading()).isInstanceOf(PaymentStatusReading.Failed::class.java)
        assertThat(CommerceError.Unexpected("X", "y", httpStatus = 400).toReading())
            .isInstanceOf(PaymentStatusReading.Failed::class.java)
        assertThat(CommerceError.Unexpected("X", "y", httpStatus = 429).toReading())
            .isInstanceOf(PaymentStatusReading.Unreachable::class.java)
        assertThat(CommerceError.Unexpected("X", "y", httpStatus = 500).toReading())
            .isInstanceOf(PaymentStatusReading.Unreachable::class.java)
        assertThat(CommerceError.Network(null).toReading()).isInstanceOf(PaymentStatusReading.Unreachable::class.java)
        assertThat(CommerceError.NotAvailable.toReading()).isInstanceOf(PaymentStatusReading.Unreachable::class.java)
    }
}

private const val FORBIDDEN_GOLDEN = "order_payment_get_403_not_customer.json"
