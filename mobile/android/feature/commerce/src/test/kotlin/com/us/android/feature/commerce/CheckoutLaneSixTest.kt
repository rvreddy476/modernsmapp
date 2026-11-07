package com.us.android.feature.commerce

import androidx.lifecycle.SavedStateHandle
import com.google.common.truth.Truth.assertThat
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.network.CheckoutRequest
import com.us.android.core.commerce.network.CheckoutResultDto
import com.us.android.core.commerce.network.OrderPaymentDto
import com.us.android.core.commerce.network.QuoteDto
import com.us.android.core.commerce.network.QuoteRequest
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.network.ApiEnvelope
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.feature.commerce.checkout.CART_EMPTY_COPY
import com.us.android.feature.commerce.checkout.COUPON_GONE_ON_RETRY
import com.us.android.feature.commerce.checkout.CheckoutContinuation
import com.us.android.feature.commerce.checkout.CheckoutUiState
import com.us.android.feature.commerce.checkout.CheckoutViewModel
import com.us.android.feature.commerce.checkout.FailedAction
import com.us.android.feature.commerce.checkout.ORDER_NOT_PAYABLE_ON_RETRY
import com.us.android.feature.commerce.checkout.OUT_OF_STOCK_ON_RETRY
import com.us.android.feature.commerce.checkout.PaymentMethod
import com.us.android.feature.commerce.checkout.PaymentRefusal
import com.us.android.feature.commerce.checkout.UNKNOWN_OUTCOME_COPY
import com.us.android.feature.commerce.checkout.paymentFailedDetail
import com.us.android.feature.commerce.checkout.toSheetAttempt
import com.us.android.feature.commerce.ui.SESSION_EXPIRED_COPY
import kotlinx.coroutines.CompletableDeferred
import kotlinx.coroutines.Dispatchers
import kotlinx.coroutines.ExperimentalCoroutinesApi
import kotlinx.coroutines.test.StandardTestDispatcher
import kotlinx.coroutines.test.advanceUntilIdle
import kotlinx.coroutines.test.resetMain
import kotlinx.coroutines.test.runTest
import kotlinx.coroutines.test.setMain
import okhttp3.MediaType.Companion.toMediaType
import okhttp3.ResponseBody.Companion.toResponseBody
import org.junit.After
import org.junit.Before
import org.junit.Test
import retrofit2.Response
import java.io.IOException

/**
 * Android lane 6, checkout: audit 2026-10-30 H1, M1, M2, L2, L3 and L7, and
 * the coupon and session refusals, through the REAL repository and the REAL
 * ViewModel. The seam is the Retrofit interface, so a "lost response" is a
 * thrown IOException and a refusal is the server's own error body.
 */
@OptIn(ExperimentalCoroutinesApi::class)
class CheckoutLaneSixTest {

    private val dispatcher = StandardTestDispatcher()

    @Before
    fun setUp() = Dispatchers.setMain(dispatcher)

    @After
    fun tearDown() = Dispatchers.resetMain()

    private class Api : FakeCommerceApi() {
        val quotes = mutableListOf<QuoteRequest>()
        val checkouts = mutableListOf<Pair<String, CheckoutRequest>>()

        /** Answers for the next quotes, in order; a plain quote once empty. */
        val quoteAnswers = ArrayDeque<suspend () -> Response<ApiEnvelope<QuoteDto>>>()

        /** Answers for the next checkouts, in order; an order once empty. */
        val checkoutAnswers = ArrayDeque<suspend () -> Response<ApiEnvelope<CheckoutResultDto>>>()

        var payment: OrderPaymentDto? = null

        override suspend fun quote(body: QuoteRequest): Response<ApiEnvelope<QuoteDto>> {
            quotes += body
            return quoteAnswers.removeFirstOrNull()?.invoke() ?: envelope(quote(body.paymentMethod))
        }

        override suspend fun checkout(
            idempotencyKey: String,
            body: CheckoutRequest,
        ): Response<ApiEnvelope<CheckoutResultDto>> {
            checkouts += idempotencyKey to body
            return checkoutAnswers.removeFirstOrNull()?.invoke() ?: envelope(
                CheckoutResultDto(orderId = "o-1", orderNumber = "MS-1", totalMinor = Paise(body.expectedTotalMinor)),
            )
        }

        override suspend fun orderPayment(orderId: String): Response<ApiEnvelope<OrderPaymentDto>> =
            payment?.let { envelope(it) } ?: notFound()

        fun quote(method: String?) = QuoteDto(
            quoteId = "q-${quotes.size}-$method",
            subtotalMinor = Paise(200_000),
            shippingMinor = Paise(4_000),
            taxMinor = Paise(31_119),
            totalMinor = Paise(204_000),
            currency = "INR",
            serviceable = true,
        )
    }

    private fun <T> error(status: Int, code: String, details: String = ""): Response<ApiEnvelope<T>> {
        val extra = if (details.isEmpty()) "" else ""","details":$details"""
        return Response.error(
            status,
            """{"error":{"code":"$code","message":"m"$extra}}""".toResponseBody("application/json".toMediaType()),
        )
    }

    private fun vm(api: Api, handoff: PaymentHandoff = PaymentHandoff()) = CheckoutViewModel(
        CommerceRepository(api),
        handoff,
        SavedStateHandle(),
        pollingOnlyPaymentCoordinator(),
    )

    private fun CheckoutViewModel.ready(): CheckoutUiState.Ready {
        prepare("addr-1", "5 Main St")
        dispatcher.scheduler.advanceUntilIdle()
        return state.value as CheckoutUiState.Ready
    }

    // ─── H1: a checkout with no answer ───────────────────────────────

    @Test
    fun `a checkout that got no answer is resent under the same key and finds its order`() = runTest(dispatcher) {
        val api = Api()
        api.checkoutAnswers += { throw IOException("read timed out") }
        val model = vm(api)
        model.ready()

        model.placeOrder()
        advanceUntilIdle()

        // The order may exist: the phase stays Submitting, and the only button
        // resends this request.
        val failed = model.state.value as CheckoutUiState.Failed
        assertThat(failed.action).isEqualTo(FailedAction.Resubmit)
        assertThat(failed.message).isEqualTo(UNKNOWN_OUTCOME_COPY)
        assertThat(model.savedPhase()).isEqualTo(CheckoutContinuation.Phase.Submitting)

        model.retry()
        assertThat((model.state.value as CheckoutUiState.Failed).retrying).isTrue()
        advanceUntilIdle()

        assertThat(api.checkouts).hasSize(2)
        assertThat(api.checkouts[1].first).isEqualTo(api.checkouts[0].first)
        assertThat(api.checkouts[1].second).isEqualTo(api.checkouts[0].second)
        assertThat(model.state.value).isInstanceOf(CheckoutUiState.OpeningPayment::class.java)
    }

    @Test
    fun `a 5xx at checkout is an unknown outcome too`() = runTest(dispatcher) {
        val api = Api()
        api.checkoutAnswers += { error(502, "") }
        val model = vm(api)
        model.ready()

        model.placeOrder()
        advanceUntilIdle()

        assertThat((model.state.value as CheckoutUiState.Failed).action).isEqualTo(FailedAction.Resubmit)
        assertThat(model.savedPhase()).isEqualTo(CheckoutContinuation.Phase.Submitting)
    }

    @Test
    fun `a definite refusal at checkout means no order and goes back to the bag`() = runTest(dispatcher) {
        val api = Api()
        api.checkoutAnswers += { error(400, "CART_EMPTY") }
        val model = vm(api)
        model.ready()

        model.placeOrder()
        advanceUntilIdle()

        val failed = model.state.value as CheckoutUiState.Failed
        assertThat(failed.action).isEqualTo(FailedAction.BackToBag)
        assertThat(failed.message).isEqualTo(CART_EMPTY_COPY)
        assertThat(model.savedPhase()).isEqualTo(CheckoutContinuation.Phase.Quoted)
    }

    @Test
    fun `a quote lost in transit offers a re-quote that works`() = runTest(dispatcher) {
        val api = Api()
        api.quoteAnswers += { throw IOException("offline") }
        val model = vm(api)

        model.prepare("addr-1", "5 Main St")
        advanceUntilIdle()
        assertThat((model.state.value as CheckoutUiState.Failed).action).isEqualTo(FailedAction.Requote)

        model.retry()
        advanceUntilIdle()

        assertThat(api.quotes).hasSize(2)
        assertThat(api.checkouts).isEmpty()
        assertThat(model.state.value).isInstanceOf(CheckoutUiState.Ready::class.java)
    }

    @Test
    fun `a signed-out checkout is not an unknown outcome and is not retried`() = runTest(dispatcher) {
        val api = Api()
        api.checkoutAnswers += { error(401, "UNAUTHORIZED") }
        val model = vm(api)
        model.ready()

        model.placeOrder()
        advanceUntilIdle()

        val failed = model.state.value as CheckoutUiState.Failed
        assertThat(failed.message).isEqualTo(SESSION_EXPIRED_COPY)
        assertThat(failed.action).isEqualTo(FailedAction.BackToBag)
        assertThat(model.savedPhase()).isEqualTo(CheckoutContinuation.Phase.Quoted)
    }

    // ─── M1: nothing re-quotes over an order being placed ────────────

    @Test
    fun `a method change waits for its quote and Place order waits with it`() = runTest(dispatcher) {
        val api = Api()
        val gate = CompletableDeferred<Unit>()
        val model = vm(api)
        model.ready()
        api.quoteAnswers += {
            gate.await()
            envelope(api.quote("card"))
        }

        model.selectMethod(PaymentMethod.CARD)
        advanceUntilIdle()
        val repricing = model.state.value as CheckoutUiState.Ready
        assertThat(repricing.repricing).isTrue()
        assertThat(repricing.canChange).isFalse()

        // Place order and a second change are both refused until it lands.
        model.placeOrder()
        model.selectMethod(PaymentMethod.UPI)
        advanceUntilIdle()
        assertThat(api.checkouts).isEmpty()
        assertThat(api.quotes).hasSize(2)

        gate.complete(Unit)
        advanceUntilIdle()
        val ready = model.state.value as CheckoutUiState.Ready
        assertThat(ready.repricing).isFalse()
        assertThat(ready.paymentMethod).isEqualTo(PaymentMethod.CARD)

        model.placeOrder()
        advanceUntilIdle()
        assertThat(api.checkouts.single().second.paymentMethod).isEqualTo("card")
    }

    @Test
    fun `a method change while the order is being placed sends no quote`() = runTest(dispatcher) {
        val api = Api()
        val gate = CompletableDeferred<Unit>()
        api.checkoutAnswers += {
            gate.await()
            envelope(CheckoutResultDto(orderId = "o-1", orderNumber = "MS-1", totalMinor = Paise(204_000)))
        }
        val model = vm(api)
        model.ready()

        model.placeOrder()
        advanceUntilIdle()
        model.selectMethod(PaymentMethod.CARD)
        advanceUntilIdle()
        assertThat(api.quotes).hasSize(1)

        gate.complete(Unit)
        advanceUntilIdle()
        // The order wins: it is being paid for, not re-priced.
        assertThat(model.state.value).isInstanceOf(CheckoutUiState.OpeningPayment::class.java)
        assertThat(api.checkouts.single().second.paymentMethod).isEqualTo("upi")
    }

    // ─── L2 and L7: after the order is placed ────────────────────────

    @Test
    fun `a placed order is not a requested sheet until the screen opens one`() = runTest(dispatcher) {
        val api = Api()
        val model = vm(api)
        model.ready()

        model.placeOrder()
        advanceUntilIdle()

        val opening = model.state.value as CheckoutUiState.OpeningPayment
        assertThat(model.savedPhase()).isEqualTo(CheckoutContinuation.Phase.OrderPlaced)

        // Someone else's attempt does not move it.
        model.onPaymentSheetRequested(opening.attempt.copy(id = "another"))
        assertThat(model.savedPhase()).isEqualTo(CheckoutContinuation.Phase.OrderPlaced)

        model.onPaymentSheetRequested(opening.attempt)
        assertThat(model.savedPhase()).isEqualTo(CheckoutContinuation.Phase.SheetRequested)
    }

    @Test
    fun `a process death before the sheet opened recovers to Complete your payment, not a poll`() =
        runTest(dispatcher) {
            val api = object : FakeCommerceApi() {
                override suspend fun getOrder(orderId: String) = envelope(
                    com.us.android.core.commerce.network.OrderDto(
                        id = orderId,
                        orderNumber = "MS-1",
                        status = "payment_pending",
                        paymentStatus = "pending",
                        totalMinor = Paise(204_000),
                    ),
                )
            }
            val saved = SavedStateHandle()
            CheckoutContinuation(saved).apply {
                orderId = "o-1"
                orderNumber = "MS-1"
                phase = CheckoutContinuation.Phase.OrderPlaced
            }
            val model = CheckoutViewModel(
                CommerceRepository(api),
                PaymentHandoff(),
                saved,
                pollingOnlyPaymentCoordinator(),
            )

            model.prepare("addr-1", "5 Main St")
            advanceUntilIdle()

            val waiting = model.state.value as CheckoutUiState.PaymentFailed
            assertThat(waiting.attempted).isFalse()
        }

    // ─── L3 and coupons: refusals that name the fix ──────────────────

    @Test
    fun `a deleted address asks for another address`() = runTest(dispatcher) {
        val api = Api()
        api.quoteAnswers += { error(404, "ADDRESS_NOT_FOUND") }
        val model = vm(api)

        model.prepare("addr-gone", "Old flat")
        advanceUntilIdle()

        val failed = model.state.value as CheckoutUiState.Failed
        assertThat(failed.action).isEqualTo(FailedAction.ChangeAddress)
        assertThat(failed.message).contains("address")
    }

    @Test
    fun `a coupon refused at quote says why and sends the buyer to the bag`() = runTest(dispatcher) {
        val api = Api()
        api.quoteAnswers += { error(422, "COUPON_MIN_ORDER", """{"min_order_minor":49900}""") }
        val model = vm(api)

        model.prepare("addr-1", "5 Main St")
        advanceUntilIdle()

        val failed = model.state.value as CheckoutUiState.Failed
        assertThat(failed.action).isEqualTo(FailedAction.BackToBag)
        assertThat(failed.message).contains("₹499.00")
    }

    @Test
    fun `a coupon refused at checkout is a definite refusal`() = runTest(dispatcher) {
        val api = Api()
        api.checkoutAnswers += { error(422, "COUPON_USED_UP") }
        val model = vm(api)
        model.ready()

        model.placeOrder()
        advanceUntilIdle()

        assertThat((model.state.value as CheckoutUiState.Failed).action).isEqualTo(FailedAction.BackToBag)
        assertThat(model.savedPhase()).isEqualTo(CheckoutContinuation.Phase.Quoted)
    }

    // ─── M2 and the second payment ───────────────────────────────────

    private fun placed(
        api: Api,
        handoff: PaymentHandoff,
    ): Pair<CheckoutViewModel, CheckoutUiState.OpeningPayment> {
        val model = vm(api, handoff)
        model.observePaymentHandoff()
        model.ready()
        model.placeOrder()
        dispatcher.scheduler.advanceUntilIdle()
        return model to model.state.value as CheckoutUiState.OpeningPayment
    }

    @Test
    fun `a refused payment says why, and a gone coupon offers no retry`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val (model, opening) = placed(Api(), handoff)

        handoff.publish(PaymentHandoffEvent.Unavailable(opening.attempt.toSheetAttempt(), COUPON_GONE_ON_RETRY))
        advanceUntilIdle()

        val failed = model.state.value as CheckoutUiState.PaymentFailed
        assertThat(failed.reason).isEqualTo(COUPON_GONE_ON_RETRY)
        assertThat(failed.canRetry).isFalse()
        assertThat(failed.orderNumber).isEqualTo("MS-1")
    }

    @Test
    fun `an order that is no longer payable is read, never offered a second payment`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val api = Api()
        val (model, opening) = placed(api, handoff)
        // A webhook already captured it.
        api.payment = OrderPaymentDto(orderId = "o-1", status = "paid", amountMinor = Paise(204_000))

        handoff.publish(
            PaymentHandoffEvent.Unavailable(opening.attempt.toSheetAttempt(), ORDER_NOT_PAYABLE_ON_RETRY),
        )
        advanceUntilIdle()

        assertThat(model.state.value).isEqualTo(CheckoutUiState.Paid("o-1", "MS-1"))
    }

    @Test
    fun `a retryable refusal keeps the retry`() = runTest(dispatcher) {
        val handoff = PaymentHandoff()
        val (model, opening) = placed(Api(), handoff)

        handoff.publish(PaymentHandoffEvent.Unavailable(opening.attempt.toSheetAttempt(), "No session"))
        advanceUntilIdle()

        val failed = model.state.value as CheckoutUiState.PaymentFailed
        assertThat(failed.canRetry).isTrue()
        assertThat(failed.reason).isEqualTo("No session")
    }

    @Test
    fun `refusals sort into final, read the order, and retry`() {
        assertThat(PaymentRefusal.of(OUT_OF_STOCK_ON_RETRY)).isEqualTo(PaymentRefusal.Final)
        assertThat(PaymentRefusal.of(COUPON_GONE_ON_RETRY)).isEqualTo(PaymentRefusal.Final)
        assertThat(PaymentRefusal.of(ORDER_NOT_PAYABLE_ON_RETRY)).isEqualTo(PaymentRefusal.ReadOrder)
        assertThat(PaymentRefusal.of("anything else")).isEqualTo(PaymentRefusal.Retryable)
    }

    @Test
    fun `the payment-failed copy uses the reason and never says failed before an attempt`() {
        val waiting = CheckoutUiState.PaymentFailed("o-1", "MS-1", attempted = false)
        assertThat(paymentFailedDetail(waiting)).contains("Pay to confirm it")
        assertThat(paymentFailedDetail(waiting)).doesNotContain("try paying again")

        val gone = CheckoutUiState.PaymentFailed("o-1", "MS-1", reason = COUPON_GONE_ON_RETRY, canRetry = false)
        assertThat(paymentFailedDetail(gone)).isEqualTo(COUPON_GONE_ON_RETRY)

        val retry = CheckoutUiState.PaymentFailed("o-1", "MS-1", reason = "No session")
        assertThat(paymentFailedDetail(retry)).startsWith("No session")
    }
}
