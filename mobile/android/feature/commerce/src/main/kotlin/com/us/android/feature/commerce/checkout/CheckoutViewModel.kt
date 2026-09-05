package com.us.android.feature.commerce.checkout

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.PaymentStatus
import com.us.android.core.commerce.model.PriceBreakdown
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.commerce.payment.PaymentHandoff
import com.us.android.core.commerce.payment.PaymentHandoffEvent
import com.us.android.core.commerce.repository.CommerceError
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

/**
 * Drives the checkout and payment screens.
 *
 * Four rules this class exists to keep, all of which an earlier version broke:
 *
 *  1. ONE idempotency key per customer attempt, reused across every retry
 *     (LB-15). A key minted per HTTP call turns each retry into a new order.
 *     A new key is minted only when the customer starts a genuinely new
 *     attempt — after a price acknowledgement, or after a conflict.
 *
 *  2. The app never computes a total. C3-LB-2: every figure rendered comes
 *     from the SERVER's quote breakdown. The previous version was handed a
 *     zero subtotal, displayed `0 + shipping` as the total, and submitted
 *     that as `expected_total_minor` — so the backend recomputed the real
 *     total, disagreed, and returned PRICE_CHANGED on every non-empty cart.
 *     The buyer could not complete an ordinary purchase at all.
 *
 *  3. A PSP redirect is not payment (A1). Returning from the sheet moves to
 *     [CheckoutUiState.AwaitingConfirmation] and polls the server, which
 *     only marks an order paid on a signature-verified provider webhook.
 *
 *  4. C3-LB-4: an outcome is applied only if it belongs to THIS checkout's
 *     active attempt. A replayed or delayed event for another order — or for
 *     an earlier attempt at this one — is ignored, not applied.
 */
@HiltViewModel
class CheckoutViewModel @Inject constructor(
    private val repo: CommerceRepository,
    private val handoff: PaymentHandoff,
    savedState: SavedStateHandle,
) : ViewModel() {

    /**
     * Scope C — the checkout state that survives process death.
     *
     * Android kills a backgrounded process at any time, and a buyer inside a
     * payment sheet is backgrounded by definition. Everything money-relevant
     * lives here rather than in fields: see [CheckoutContinuation] for what
     * each one costs if it is lost.
     */
    private val saved = CheckoutContinuation(savedState)

    /** Guards against subscribing twice across recomposition. */
    private var observingHandoff = false

    private val _state = MutableStateFlow<CheckoutUiState>(CheckoutUiState.Loading)
    val state: StateFlow<CheckoutUiState> = _state.asStateFlow()

    /**
     * The current attempt's key. Held across retries on purpose — see rule 1,
     * and across PROCESS DEATH, which is what makes rule 1 true in practice.
     *
     * Minted on first read and persisted immediately, so it is stable from
     * before the first request rather than from the first save. That ordering
     * is the whole point: the state a lost HTTP response leaves behind is one
     * where the request went out and the key must not have changed.
     */
    private var attemptKey: String
        get() = saved.attemptKey { repo.newCheckoutKey() }
        set(value) {
            // Only ever assigned via newAttempt(); this keeps the two in sync.
            saved.newAttemptKey { value }
        }

    private var addressId: String?
        get() = saved.addressId
        set(v) {
            saved.addressId = v
        }

    private var addressSummary: String
        get() = saved.addressSummary
        set(v) {
            saved.addressSummary = v
        }

    private var quoteId: String?
        get() = saved.quoteId
        set(v) {
            saved.quoteId = v
        }

    /**
     * The last breakdown the SERVER stated. Never computed here.
     *
     * It is what the screen renders and what `placeOrder` submits, so there
     * is exactly one number in play and it is the server's — and it survives
     * recreation so the number the buyer approved is the number resubmitted.
     */
    private var serverBreakdown: PriceBreakdown?
        get() = saved.breakdown
        set(v) {
            saved.breakdown = v
        }

    private var couponCode: String?
        get() = saved.couponCode
        set(v) {
            saved.couponCode = v
        }

    private var method: PaymentMethod
        get() = saved.paymentMethod
        set(v) {
            saved.paymentMethod = v
        }

    /**
     * The payment attempt this screen is waiting on, if any.
     *
     * C3-LB-4: the guard that stops another order's outcome landing here —
     * and it has to survive recreation, or a rotation mid-sheet would reset
     * the attempt id and defeat the guard exactly when it matters.
     */
    private var activeAttempt: PaymentAttempt?
        get() = saved.attempt
        set(v) {
            saved.attempt = v
        }

    /** Test seam. */
    internal fun currentAttemptKey(): String = attemptKey

    /** Test seam. */
    internal fun activePaymentAttempt(): PaymentAttempt? = activeAttempt

    /** Test seam: how far the buyer got, from the server's point of view. */
    internal fun savedPhase(): CheckoutContinuation.Phase = saved.phase

    /**
     * Subscribes to the PSP handoff bus.
     *
     * The SDK reports to the Activity, not to the code that opened the sheet,
     * so the outcome arrives here rather than as a return value.
     *
     * Every ENDING is treated identically: poll the server. A1/R-3 — a
     * client-reported success is not proof of payment, and a client-reported
     * failure is not proof of its absence. A dropped callback or a killed
     * process can sit on top of a capture that completed, and concluding
     * "failed" locally is how an app tells someone their payment failed while
     * their money is gone.
     *
     * The one genuinely different case is [PaymentHandoffEvent.Unavailable]:
     * no sheet was ever presented, so no payment can have been taken and the
     * app says so instead of polling for something that will never arrive.
     *
     * C3-LB-4 — every event is checked against [activeAttempt] FIRST. The
     * previous version checked only that this screen was in a payment-ish
     * state, so a delayed or replayed event for order A, arriving while this
     * screen was opening payment for order B, made B's screen poll A and
     * render A's terminal state as B's result.
     */
    fun observePaymentHandoff() {
        if (observingHandoff) return
        observingHandoff = true
        viewModelScope.launch {
            handoff.events.collect { event ->
                val mine = activeAttempt
                if (mine == null || event.attempt != mine) {
                    // Not ours. Someone else's order, or an earlier attempt at
                    // this one. Dropping it is the whole point: the server
                    // remains the record for whatever order it did belong to.
                    return@collect
                }
                if (handoff.isConsumed(mine)) {
                    // Already acted on. A replayed event after a rotation
                    // must not restart the flow.
                    return@collect
                }
                handoff.consume(mine)

                val orderNumber = when (val current = _state.value) {
                    is CheckoutUiState.OpeningPayment -> current.orderNumber
                    is CheckoutUiState.AwaitingConfirmation -> current.orderNumber
                    else -> return@collect
                }
                when (event) {
                    is PaymentHandoffEvent.SheetClosed ->
                        onPaymentSheetReturned(event.orderId, orderNumber)

                    is PaymentHandoffEvent.Unavailable ->
                        _state.value = CheckoutUiState.PaymentFailed(
                            orderId = event.orderId,
                            orderNumber = orderNumber,
                        )
                }
            }
        }
    }

    /**
     * Step 1 (A4): ask the server to price the cart.
     *
     * C3-LB-2. This takes no subtotal, because there is no client-side
     * subtotal to take. The server returns the complete breakdown and that is
     * what the screen shows.
     */
    fun prepare(addressId: String, addressSummary: String) {
        // Scope C — RECOVERY FIRST.
        //
        // The screen calls prepare() on every entry, including the one that
        // follows a process death. If a checkout was already in flight, the
        // worst possible response is to quote again and offer a fresh Place
        // Order button: that is how a buyer who already has an order — and may
        // already have paid — gets a second one.
        //
        // So the saved phase decides, and anything at or past "a request was
        // sent" is resolved against the SERVER before this screen offers to do
        // anything.
        if (recoverIfInFlight(addressId, addressSummary)) return

        this.addressId = addressId
        this.addressSummary = addressSummary
        saved.phase = CheckoutContinuation.Phase.Fresh
        _state.value = CheckoutUiState.Loading
        viewModelScope.launch { requote() }
    }

    /**
     * Resumes a checkout that survived process death.
     *
     * Returns true when it took over; the caller must then do nothing else.
     *
     * The rules, in the order they matter:
     *
     *  1. **After a request was sent, ask the server.** [Phase.Submitting] is
     *     the state a lost HTTP response leaves: the order may or may not
     *     exist. Resubmitting under the SAME key is safe and is exactly what
     *     the key is for — the server returns the original order rather than
     *     creating a second one.
     *  2. **After an order exists, never submit again.** Fetch it and render
     *     what it actually says.
     *  3. **Never auto-open a second sheet.** A sheet was requested before the
     *     process died; whether it opened, and whether money moved, is not
     *     knowable from here. If the server says paid, failed or expired, that
     *     is the answer. If it says pending, the buyer is offered an explicit
     *     Try payment again — which mints a NEW attempt — rather than having a
     *     sheet appear underneath them.
     */
    private fun recoverIfInFlight(addressId: String, addressSummary: String): Boolean {
        val phase = saved.phase
        if (phase == CheckoutContinuation.Phase.Fresh || phase == CheckoutContinuation.Phase.Quoted) {
            return false
        }
        // Keep the address the buyer chose; a recreation must not change it.
        saved.addressId = saved.addressId ?: addressId
        if (saved.addressSummary.isEmpty()) saved.addressSummary = addressSummary

        _state.value = CheckoutUiState.Loading
        viewModelScope.launch { resumeFromServer(phase) }
        return true
    }

    private suspend fun resumeFromServer(phase: CheckoutContinuation.Phase) {
        val orderId = saved.orderId
        if (orderId == null) {
            // Phase.Submitting with no order id: the response never arrived.
            // Re-send under the SAME key. This is the one case where
            // resubmitting is correct, and it is correct precisely because the
            // key did not change.
            resubmitUnderTheSameKey()
            return
        }

        when (val res = repo.order(orderId)) {
            is CommerceResult.Failure -> {
                // We know an order exists and cannot currently read it. Say
                // that, and do NOT offer to place another one.
                _state.value = CheckoutUiState.AwaitingConfirmation(
                    orderId, saved.orderNumber, elapsedSeconds = 0,
                )
            }

            is CommerceResult.Success -> {
                val order = res.value
                saved.orderNumber = order.orderNumber
                serverBreakdown = order.breakdown
                _state.value = when (order.paymentStatus) {
                    PaymentStatus.PAID -> CheckoutUiState.Paid(order.id, order.orderNumber)
                    PaymentStatus.FAILED -> CheckoutUiState.PaymentFailed(order.id, order.orderNumber)
                    PaymentStatus.REFUND_PENDING, PaymentStatus.REFUNDED ->
                        CheckoutUiState.Expired(order.id)

                    else -> {
                        // Pending or awaiting confirmation. If a sheet had been
                        // requested, keep polling — the capture may still land.
                        // Never reopen it automatically.
                        if (phase == CheckoutContinuation.Phase.SheetRequested) {
                            pollPaymentStatus(order.id, order.orderNumber)
                            CheckoutUiState.AwaitingConfirmation(order.id, order.orderNumber)
                        } else {
                            // The order exists but no sheet was ever requested,
                            // so payment has not been attempted. Offer it as an
                            // explicit action, which mints a new attempt.
                            CheckoutUiState.PaymentFailed(order.id, order.orderNumber)
                        }
                    }
                }
            }
        }
    }

    /**
     * Re-sends a checkout whose response was lost, under the SAME key.
     *
     * LB-15's entire purpose. A new key here would be a second order, a second
     * stock hold and a second payment intent for one customer decision.
     */
    private suspend fun resubmitUnderTheSameKey() {
        val addr = saved.addressId
        val quote = saved.quoteId
        val approved = serverBreakdown
        if (addr == null || quote == null || approved == null) {
            // Not enough survived to resubmit safely. Re-quote rather than
            // guess: no order is known to exist, so starting over is safe.
            saved.phase = CheckoutContinuation.Phase.Fresh
            requote()
            return
        }
        val result = repo.checkout(
            idempotencyKey = attemptKey,
            addressId = addr,
            quoteId = quote,
            paymentMethod = method.wire,
            expectedTotal = approved.total,
        )
        when (result) {
            is CommerceResult.Failure -> _state.value = mapFailure(result.error)
            is CommerceResult.Success -> adoptPlacedOrder(
                result.value.orderId,
                result.value.orderNumber,
                result.value.breakdown,
            )
        }
    }

    /** Re-prices the cart and moves to [CheckoutUiState.Ready]. */
    private suspend fun requote() {
        val addr = addressId ?: return
        when (val q = repo.quote(addr, couponCode, method.wire)) {
            is CommerceResult.Failure -> _state.value = mapFailure(q.error)
            is CommerceResult.Success -> {
                quoteId = q.value.quoteId
                serverBreakdown = q.value.breakdown
                _state.value = CheckoutUiState.Ready(
                    breakdown = q.value.breakdown,
                    addressId = addr,
                    addressSummary = addressSummary,
                    quoteId = q.value.quoteId,
                    paymentMethod = method,
                )
            }
        }
    }

    /**
     * The payment method is bound into the quote server-side, so changing it
     * re-quotes rather than editing the displayed price locally.
     */
    fun selectMethod(m: PaymentMethod) {
        if (m == method) return
        method = m
        viewModelScope.launch { requote() }
    }

    /** Step 2 (LB-14/LB-15): place the order at exactly the quoted total. */
    fun placeOrder() {
        val ready = _state.value as? CheckoutUiState.Ready ?: return
        val addr = addressId ?: return
        val quote = quoteId ?: return
        // The number submitted is the SERVER's, echoed back for checking. If
        // there is no server breakdown there is nothing to approve, and
        // placing an order would mean inventing a total.
        val approved = serverBreakdown ?: return

        _state.value = ready.copy(placing = true)
        // Scope C: recorded BEFORE the request leaves. If the process dies
        // mid-flight, recovery has to know a request was sent — the state
        // where an order may or may not exist is exactly the one that needs
        // the same key resubmitted rather than a fresh checkout offered.
        saved.phase = CheckoutContinuation.Phase.Submitting

        viewModelScope.launch {
            val result = repo.checkout(
                idempotencyKey = attemptKey,
                addressId = addr,
                quoteId = quote,
                paymentMethod = method.wire,
                expectedTotal = approved.total,
            )
            when (result) {
                is CommerceResult.Failure -> {
                    // No order was created, so this is not an in-flight
                    // checkout any more.
                    saved.phase = CheckoutContinuation.Phase.Quoted
                    _state.value = mapFailure(result.error)
                }

                is CommerceResult.Success ->
                    adoptPlacedOrder(result.value.orderId, result.value.orderNumber, result.value.breakdown)
            }
        }
    }

    /**
     * Records a newly created order and moves to opening payment.
     *
     * Everything durable is persisted BEFORE the state changes, so a process
     * death between the two recovers to "an order exists" rather than to
     * "nothing happened".
     */
    private fun adoptPlacedOrder(orderId: String, orderNumber: String, breakdown: PriceBreakdown) {
        serverBreakdown = breakdown
        saved.orderId = orderId
        saved.orderNumber = orderNumber
        // C3-LB-4: a fresh attempt for this order. Minted here, before the
        // sheet can open, so an outcome can be matched to it.
        val attempt = PaymentAttempt(orderId = orderId, id = repo.newCheckoutKey())
        activeAttempt = attempt
        saved.phase = CheckoutContinuation.Phase.SheetRequested
        _state.value = CheckoutUiState.OpeningPayment(attempt = attempt, orderNumber = orderNumber)
    }

    /**
     * The customer accepted the replacement price.
     *
     * C3-LB-2. Two things happen, in this order:
     *
     *  1. a NEW idempotency key, because accepting a different total is a new
     *     customer decision — reusing the old key would earn an
     *     IDEMPOTENCY_CONFLICT, correctly, since the request differs from the
     *     one that key already stands for;
     *  2. a re-quote, so the total submitted next is one the SERVER just
     *     stated.
     *
     * The previous version re-prepared using the stale client-side subtotal —
     * which was zero — so acknowledging a price change produced the same
     * wrong total and the same PRICE_CHANGED, forever. There is no local
     * figure left to carry forward, so the loop cannot re-form.
     */
    fun acknowledgePriceChange() {
        attemptKey = repo.newCheckoutKey()
        // Scope C: a new decision starts a genuinely new checkout. Clearing
        // the order and attempt is what stops a later recreation resuming the
        // abandoned one and polling an order this buyer never placed.
        saved.clearAttempt()
        saved.phase = CheckoutContinuation.Phase.Fresh
        _state.value = CheckoutUiState.Loading
        viewModelScope.launch { requote() }
    }

    /**
     * Retries payment for an order that already exists.
     *
     * C3-LB-4: a NEW attempt id. The previous attempt may still have a
     * callback in flight, and it must not be able to settle this one.
     */
    fun retryPayment() {
        val failed = _state.value as? CheckoutUiState.PaymentFailed ?: return
        val attempt = PaymentAttempt(orderId = failed.orderId, id = repo.newCheckoutKey())
        activeAttempt = attempt
        _state.value = CheckoutUiState.OpeningPayment(attempt, failed.orderNumber)
    }

    /** The quote went stale (cart or address changed). Re-quote, do not retry. */
    fun requoteAfterStaleQuote() {
        _state.value = CheckoutUiState.Loading
        viewModelScope.launch { requote() }
    }

    /**
     * Step 3 (A1): the PSP sheet returned.
     *
     * `succeeded` here means only that the SDK reported a completed flow. It
     * is not a payment fact, so this does not set [CheckoutUiState.Paid] —
     * it starts polling the server, which is the only party that can know.
     */
    fun onPaymentSheetReturned(orderId: String, orderNumber: String) {
        _state.value = CheckoutUiState.AwaitingConfirmation(orderId, orderNumber)
        pollPaymentStatus(orderId, orderNumber)
    }

    @Suppress("MagicNumber")
    private fun pollPaymentStatus(orderId: String, orderNumber: String) {
        viewModelScope.launch {
            var elapsed = 0
            // Back off from 1s toward 5s: most captures land in the first few
            // seconds, and a slow one should not be hammered.
            var interval = 1
            while (elapsed < PAYMENT_CONFIRMATION_TIMEOUT_SECONDS) {
                delay(interval * 1000L)
                elapsed += interval
                interval = minOf(interval + 1, 5)

                when (val s = repo.paymentStatus(orderId)) {
                    is CommerceResult.Failure -> {
                        // A transient failure must not be reported as a
                        // payment failure: we simply do not know yet.
                        _state.value = CheckoutUiState.AwaitingConfirmation(
                            orderId, orderNumber, elapsed,
                        )
                    }

                    is CommerceResult.Success -> when (s.value) {
                        PaymentStatus.PAID -> {
                            _state.value = CheckoutUiState.Paid(orderId, orderNumber)
                            return@launch
                        }

                        PaymentStatus.FAILED -> {
                            _state.value = CheckoutUiState.PaymentFailed(orderId, orderNumber)
                            return@launch
                        }

                        PaymentStatus.REFUND_PENDING, PaymentStatus.REFUNDED -> {
                            // LB-22: the hold expired and the capture landed
                            // late, so the money is being returned. Never
                            // show this as a successful order.
                            _state.value = CheckoutUiState.Expired(orderId)
                            return@launch
                        }

                        else -> _state.value = CheckoutUiState.AwaitingConfirmation(
                            orderId, orderNumber, elapsed,
                        )
                    }
                }
            }
            // Still unconfirmed. The order EXISTS and may yet be paid, so the
            // copy on this state must send the customer to their orders
            // rather than implying failure.
            _state.value = CheckoutUiState.AwaitingConfirmation(
                orderId, orderNumber, PAYMENT_CONFIRMATION_TIMEOUT_SECONDS,
            )
        }
    }

    @Suppress("CyclomaticComplexMethod")
    private fun mapFailure(error: CommerceError): CheckoutUiState = when (error) {
        is CommerceError.PriceChanged -> CheckoutUiState.PriceChanged(
            lines = error.lines,
            newTotal = error.newTotal,
            previousTotal = serverBreakdown?.total ?: Paise.ZERO,
        )

        is CommerceError.OutOfStock -> CheckoutUiState.OutOfStock(error.lines)
        is CommerceError.NotServiceable -> CheckoutUiState.NotServiceable(error.reason)
        CommerceError.QuoteStale, CommerceError.QuoteExpired -> CheckoutUiState.QuoteStale

        CommerceError.IdempotencyConflict -> {
            // Start a genuinely new attempt rather than accepting an order
            // built from a different request (M-7).
            attemptKey = repo.newCheckoutKey()
            saved.clearAttempt()
            saved.phase = CheckoutContinuation.Phase.Fresh
            CheckoutUiState.RetryWithNewAttempt
        }

        CommerceError.ProductUnavailable ->
            CheckoutUiState.Failed("An item in your cart is no longer available.", retryable = false)

        CommerceError.MultipleSellers ->
            CheckoutUiState.Failed("Your cart has items from more than one seller.", retryable = false)

        CommerceError.CouponUnavailable ->
            CheckoutUiState.Failed("That coupon is no longer available.", retryable = true)

        CommerceError.CartEmpty ->
            CheckoutUiState.Failed("Your cart is empty.", retryable = false)

        CommerceError.CodNotSupported ->
            CheckoutUiState.Failed("Cash on delivery isn't available yet.", retryable = false)

        CommerceError.CancelNotPermitted ->
            CheckoutUiState.Failed("This order can no longer be cancelled.", retryable = false)

        CommerceError.OrderNotFound ->
            CheckoutUiState.Failed("We couldn't find that order.", retryable = false)

        CommerceError.TryAgain ->
            CheckoutUiState.Failed("Please try again.", retryable = true)

        is CommerceError.Network ->
            CheckoutUiState.Failed("Check your connection and try again.", retryable = true)

        is CommerceError.Unexpected ->
            CheckoutUiState.Failed("Something went wrong.", retryable = true)
    }
}
