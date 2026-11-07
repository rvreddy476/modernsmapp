package com.us.android.core.commerce

import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.model.StockReason
import com.us.android.core.commerce.network.CommerceApi
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.commerce.repository.CouponRefusal
import com.us.android.core.commerce.repository.isPermanentRefusal
import com.us.android.core.commerce.repository.outcomeUnknown
import com.us.android.core.network.di.NetworkModule
import kotlinx.coroutines.runBlocking
import mockwebserver3.MockResponse
import mockwebserver3.MockWebServer
import okhttp3.MediaType.Companion.toMediaType
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Retrofit
import retrofit2.converter.kotlinx.serialization.asConverterFactory
import java.io.IOException

/**
 * The commerce wire changes of Android lane 6 (audit 2026-10-30 and the
 * server work since), through the REAL repository over a real socket:
 * session expiry, the address refusal, the coupon codes, stock-adjust
 * idempotency, the checkout response's money and the part-refund status.
 */
class CommerceLaneSixWireTest {

    private val json = NetworkModule.provideJson()
    private lateinit var server: MockWebServer
    private lateinit var repo: CommerceRepository

    @Before
    fun setUp() {
        server = MockWebServer()
        server.start()
        val api = Retrofit.Builder()
            .baseUrl(server.url("/"))
            .addConverterFactory(json.asConverterFactory("application/json".toMediaType()))
            .build()
            .create(CommerceApi::class.java)
        repo = CommerceRepository(api)
    }

    @After
    fun tearDown() = server.close()

    private fun enqueue(body: String, code: Int = 200, header: Pair<String, String>? = null) {
        val builder = MockResponse.Builder().code(code).body(body)
        header?.let { builder.addHeader(it.first, it.second) }
        server.enqueue(builder.build())
    }

    private fun error(result: CommerceResult<*>): CommerceError = (result as CommerceResult.Failure).error

    // ─── 401 is the session ──────────────────────────────────────────

    @Test
    fun `a 401 from any commerce route is a session problem, not a missing order`() = runBlocking {
        enqueue("""{"error":{"code":"UNAUTHORIZED","message":"missing or invalid X-User-Id header"}}""", 401)
        val read = error(repo.order("o-1"))
        assertThat(read).isEqualTo(CommerceError.SessionExpired)

        // Even with no body at all: the status decides.
        enqueue("", 401)
        assertThat(error(repo.orderPayment("o-1"))).isEqualTo(CommerceError.SessionExpired)
    }

    @Test
    fun `a 401 never stops a payment poll as a permanent refusal`() {
        // A poll that stopped on it would render "payment failed" for a
        // payment nobody has read; it keeps confirming instead.
        assertThat(CommerceError.SessionExpired.isPermanentRefusal()).isFalse()
        assertThat(CommerceError.SessionExpired.outcomeUnknown()).isFalse()
    }

    // ─── Address ─────────────────────────────────────────────────────

    @Test
    fun `ADDRESS_NOT_FOUND is an address refusal, not an order one`() = runBlocking {
        enqueue("""{"error":{"code":"ADDRESS_NOT_FOUND","message":"address not found"}}""", 404)
        assertThat(error(repo.quote("a-gone"))).isEqualTo(CommerceError.AddressNotFound)
    }

    // ─── Coupons ─────────────────────────────────────────────────────

    @Test
    fun `every coupon refusal the server sends at quote decodes to its own reason`() = runBlocking {
        val cases = listOf(
            """{"code":"COUPON_INVALID","message":"m"}""" to CouponRefusal.INVALID,
            """{"code":"COUPON_INVALID","message":"that coupon is not active yet",""" +
                """"details":{"starts_at":"2026-12-01T00:00:00Z"}}""" to CouponRefusal.NOT_YET_ACTIVE,
            """{"code":"COUPON_EXPIRED","message":"m"}""" to CouponRefusal.EXPIRED,
            """{"code":"COUPON_MIN_ORDER","message":"m","details":{"min_order_minor":49900}}""" to
                CouponRefusal.MIN_ORDER,
            """{"code":"COUPON_USED_UP","message":"m"}""" to CouponRefusal.USED_UP,
            """{"code":"COUPON_NOT_APPLICABLE","message":"m"}""" to CouponRefusal.NOT_APPLICABLE,
            """{"code":"COUPON_NOT_AVAILABLE","message":"m"}""" to CouponRefusal.NOT_AVAILABLE,
        )
        cases.forEach { (body, reason) ->
            enqueue("""{"error":$body}""", 422)
            val refused = error(repo.quote("a-1", couponCode = "SAVE10"))
            assertThat(refused).isInstanceOf(CommerceError.CouponRefused::class.java)
            assertThat((refused as CommerceError.CouponRefused).reason).isEqualTo(reason)
        }
    }

    @Test
    fun `the minimum-order refusal carries the server's minimum in paise`() = runBlocking {
        enqueue(
            """{"error":{"code":"COUPON_MIN_ORDER","message":"m","details":{"min_order_minor":49900}}}""",
            422,
        )
        val refused = error(repo.checkout("k", "a", "q", "upi", Paise(1))) as CommerceError.CouponRefused
        assertThat(refused.minOrder).isEqualTo(Paise(49_900))
    }

    @Test
    fun `a payment retry whose coupon is used up is a coupon refusal`() = runBlocking {
        enqueue("""{"error":{"code":"COUPON_USED_UP","message":"coupon used up"}}""", 422)
        val refused = error(repo.openPayment("o-1"))
        assertThat(refused).isEqualTo(CommerceError.CouponRefused(CouponRefusal.USED_UP))
    }

    // ─── Ambiguous write outcomes (H1) ───────────────────────────────

    @Test
    fun `only a missing answer leaves a write's outcome unknown`() {
        assertThat(CommerceError.Network(IOException("timeout")).outcomeUnknown()).isTrue()
        assertThat(CommerceError.TryAgain.outcomeUnknown()).isTrue()
        assertThat(CommerceError.Unexpected("INTERNAL_ERROR", "m", httpStatus = 500).outcomeUnknown()).isTrue()
        assertThat(CommerceError.Unexpected("", "", httpStatus = 504).outcomeUnknown()).isTrue()

        assertThat(CommerceError.Unexpected("BAD", "m", httpStatus = 400).outcomeUnknown()).isFalse()
        assertThat(CommerceError.Unexpected("BAD", "m", httpStatus = 499).outcomeUnknown()).isFalse()
        assertThat(CommerceError.CartEmpty.outcomeUnknown()).isFalse()
        assertThat(CommerceError.NotAvailable.outcomeUnknown()).isFalse()
        assertThat(CommerceError.IdempotencyConflict.outcomeUnknown()).isFalse()
    }

    @Test
    fun `a 502 with an HTML body is an unknown outcome at checkout`() = runBlocking {
        enqueue("<html>bad gateway</html>", 502)
        assertThat(error(repo.checkout("k", "a", "q", "upi", Paise(1))).outcomeUnknown()).isTrue()
    }

    // ─── Checkout money (L2) ─────────────────────────────────────────

    @Test
    fun `the checkout outcome relays the committed total and derives no breakdown`() = runBlocking {
        enqueue(
            """{"data":{"order_id":"o-1","order_number":"MS-1","total_minor":204000,""" +
                """"tax_minor":31119,"shipping_minor":4000,"currency":"INR"}}""",
            201,
        )
        val outcome = (repo.checkout("k", "a", "q", "upi", Paise(204_000)) as CommerceResult.Success).value
        assertThat(outcome.total).isEqualTo(Paise(204_000))
        assertThat(outcome.currency).isEqualTo("INR")
    }

    // ─── Stock idempotency (M3) ──────────────────────────────────────

    @Test
    fun `a stock adjustment sends the edit's key and reads a replay as a replay`() = runBlocking {
        val body = """{"data":{"variant_id":"v-1","total_qty":60,"reserved_qty":0,"available":60}}"""
        enqueue(body)
        enqueue(body, header = "Idempotent-Replayed" to "true")

        val first = repo.adjustStock("v-1", 50, StockReason.PURCHASE, idempotencyKey = "edit-1")
        val again = repo.adjustStock("v-1", 50, StockReason.PURCHASE, idempotencyKey = "edit-1")

        assertThat(server.takeRequest().headers["Idempotency-Key"]).isEqualTo("edit-1")
        assertThat(server.takeRequest().headers["Idempotency-Key"]).isEqualTo("edit-1")
        assertThat((first as CommerceResult.Success).value.replayed).isFalse()
        assertThat((again as CommerceResult.Success).value.replayed).isTrue()
        assertThat(again.value.level.total).isEqualTo(60)
    }

    @Test
    fun `a reused stock key on a different edit is an idempotency conflict`() = runBlocking {
        enqueue(
            """{"error":{"code":"IDEMPOTENCY_CONFLICT",""" +
                """"message":"this Idempotency-Key was used for a different request"}}""",
            409,
        )
        val refused = error(repo.adjustStock("v-1", 5, StockReason.PURCHASE, idempotencyKey = "edit-1"))
        assertThat(refused).isEqualTo(CommerceError.IdempotencyConflict)
    }

    @Test
    fun `stock keys are fresh per edit and within the server's 128 characters`() {
        val a = repo.newStockAdjustmentKey()
        val b = repo.newStockAdjustmentKey()
        assertThat(a).isNotEqualTo(b)
        assertThat(a.length).isAtMost(128)
    }

    // ─── Part refunds ────────────────────────────────────────────────

    @Test
    fun `a part-refunded order is not a refunded one`() {
        assertThat(PaymentStatus.from("partially_refunded")).isEqualTo(PaymentStatus.PARTIALLY_REFUNDED)
        assertThat(PaymentStatus.from("refunded")).isEqualTo(PaymentStatus.REFUNDED)
        assertThat(PaymentStatus.from("refund_pending")).isEqualTo(PaymentStatus.REFUND_PENDING)
    }

    // ─── Currency (L8) ───────────────────────────────────────────────

    @Test
    fun `an amount prints in the currency the server stated`() {
        assertThat(Paise(123_456).formatIn("INR")).isEqualTo("₹1,234.56")
        assertThat(Paise(123_456).formatIn("")).isEqualTo("₹1,234.56")
        assertThat(Paise(1_200).formatIn("usd")).isEqualTo("USD 12.00")
    }
}
