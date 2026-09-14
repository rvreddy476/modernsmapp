package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.us.android.core.commerce.model.PaymentHandle
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.network.PaymentHandleDto
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.commerce.payment.PaymentHandoffEvent
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.network.di.NetworkModule
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentOutcome
import com.us.android.core.payments.PaymentSheetResult
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.toSheetResult
import com.us.android.feature.commerce.checkout.CheckoutContinuation
import com.us.android.feature.commerce.checkout.CommercePaymentStatusSource
import com.us.android.feature.commerce.checkout.MSTORE_PAYMENT_APPLICATION_ID
import com.us.android.feature.commerce.checkout.toHandoffEvent
import com.us.android.feature.commerce.checkout.toReading
import com.us.android.feature.commerce.checkout.toPaymentSession
import com.us.android.feature.commerce.checkout.toSheetAttempt
import kotlinx.coroutines.test.runTest
import org.junit.Assert.assertEquals
import org.junit.Assert.assertNull
import org.junit.Test
import retrofit2.Response
import com.us.android.core.payments.PaymentAttempt as SheetAttempt

/**
 * Commerce's hops onto `:core:payments`, pinned so the move changed nothing a
 * buyer can see — and so every commerce payment is MStore's.
 *
 *  * Out: an SDK outcome reaches the checkout ViewModel as the SAME handoff
 *    event `:app`'s mapper produced — every ending is SheetClosed, only a
 *    sheet that never opened is Unavailable — stamped with the commerce attempt.
 *  * Back: commerce's payment status becomes the reading the checkout poll
 *    used to act on.
 *  * Saved state: checkout's in-flight attempt is MStore's record, and another
 *    application's pending payment is never resumed here.
 */
class CheckoutPaymentOpenerTest {

    private val attempt = PaymentAttempt(orderId = "order-1", id = "attempt-1")

    @Test
    fun `every sheet ending reaches checkout as the event it always did`() {
        val sheet = attempt.toSheetAttempt()
        val expected = mapOf(
            PaymentOutcome.Succeeded("pay_1") to PaymentHandoffEvent.SheetClosed(attempt),
            PaymentOutcome.Failed(2, "network") to PaymentHandoffEvent.SheetClosed(attempt),
            PaymentOutcome.Cancelled to PaymentHandoffEvent.SheetClosed(attempt),
            PaymentOutcome.Unavailable("no session") to PaymentHandoffEvent.Unavailable(attempt, "no session"),
        )
        expected.forEach { (outcome, event) ->
            assertEquals("$outcome", event, outcome.toSheetResult(sheet).toHandoffEvent(attempt))
        }
    }

    @Test
    fun `a commerce attempt is an MStore payment attempt, so abandon matches open`() {
        assertEquals("mstore", MSTORE_PAYMENT_APPLICATION_ID)
        assertEquals(SheetAttempt("mstore", "order-1", "attempt-1"), attempt.toSheetAttempt())
        assertEquals(attempt.toSheetAttempt(), attempt.toSheetAttempt())
        assertEquals(
            PaymentHandoffEvent.Unavailable(attempt, "r"),
            PaymentSheetResult.Unavailable(attempt.toSheetAttempt(), "r").toHandoffEvent(attempt),
        )
        assertEquals(
            "checkout reads status as MStore",
            "mstore",
            CommercePaymentStatusSource(CommerceRepository(FakeCommerceApi())).applicationId,
        )
    }

    @Test
    fun `the server's merchant_display_name reaches the payment session`() = runTest {
        val handle = openPaymentReturning(
            """{"provider":"razorpay","order_id":"order_x","key_id":"rzp_test_x","merchant_display_name":"Momentum Merchant"}""",
        )

        val session = handle.toPaymentSession(orderNumber = "MS-1")

        assertEquals("Momentum Merchant", session.merchantDisplayName)
        assertEquals("mstore", session.applicationId)
        assertEquals("order_x", session.providerOrderId)
        assertEquals("rzp_test_x", session.keyId)
        assertEquals(204000L, session.amountMinor)
        assertEquals("Order MS-1", session.description)
    }

    @Test
    fun `an older server with no merchant_display_name still opens a session, with no name`() = runTest {
        val handle = openPaymentReturning(
            """{"provider":"razorpay","order_id":"order_x","key_id":"rzp_test_x"}""",
        )

        val session = handle.toPaymentSession(orderNumber = "MS-1")

        assertNull(session.merchantDisplayName)
        assertEquals("rzp_test_x", session.keyId)
        assertEquals("order_x", session.providerOrderId)
    }

    /** The real repository over a wire body decoded with the app's real Json. */
    private suspend fun openPaymentReturning(clientSession: String): PaymentHandle {
        val body = NetworkModule.provideJson().decodeFromString(
            PaymentHandleDto.serializer(),
            """{"payment_intent_id":"pi_1","amount_minor":204000,"currency":"INR","status":"pending",
                "client_session":$clientSession}""",
        )
        val api = object : FakeCommerceApi() {
            override suspend fun openPayment(orderId: String): Response<ApiEnvelope<PaymentHandleDto>> =
                Response.success(ApiEnvelope(data = body))
        }
        val result = CommerceRepository(api).openPayment("order-1")
        return (result as CommerceResult.Success).value
    }

    @Test
    fun `every commerce payment status maps to the reading checkout acted on`() {
        val expected = mapOf(
            PaymentStatus.PAID to PaymentStatusReading.Paid,
            PaymentStatus.FAILED to PaymentStatusReading.Failed(reason = null, retryable = true),
            PaymentStatus.REFUND_PENDING to PaymentStatusReading.RefundPending,
            PaymentStatus.REFUNDED to PaymentStatusReading.Refunded,
            PaymentStatus.PENDING to PaymentStatusReading.Confirming,
            PaymentStatus.AWAITING_CONFIRMATION to PaymentStatusReading.Confirming,
            PaymentStatus.UNKNOWN to PaymentStatusReading.Confirming,
        )
        assertEquals("every status is covered", PaymentStatus.entries.toSet(), expected.keys)
        expected.forEach { (status, reading) -> assertEquals("$status", reading, status.toReading()) }
    }

    @Test
    fun `a pending Feast payment in saved state is never resumed by MStore checkout`() {
        val handle = SavedStateHandle()
        val store = object : PaymentStateStore {
            override fun get(key: String): String? = handle[key]
            override fun set(key: String, value: String?) {
                handle[key] = value
            }
        }
        val feastPending = SheetAttempt("feast", "order-1", "attempt-1")
        InFlightPayment(store, "feast").attempt = feastPending

        val checkout = CheckoutContinuation(handle)
        assertNull("MStore checkout must not resume Feast's payment", checkout.attempt)

        checkout.attempt = attempt
        assertEquals(attempt, checkout.attempt)
        assertEquals("and must not overwrite it", feastPending, InFlightPayment(store, "feast").attempt)

        checkout.clearAttempt()
        assertNull(checkout.attempt)
        assertEquals("nor clear it", feastPending, InFlightPayment(store, "feast").attempt)
    }
}
