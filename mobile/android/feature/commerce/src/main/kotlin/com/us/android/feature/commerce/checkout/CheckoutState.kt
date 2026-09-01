package com.us.android.feature.commerce.checkout

import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.PriceBreakdown
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.commerce.repository.ChangedLine
import com.us.android.core.commerce.repository.UnavailableLine

/**
 * Checkout and payment UI state.
 *
 * §7-J5 and §7-J6. The states here are not a rendering convenience — several
 * of them exist specifically because the old flow had no way to express them
 * and therefore did the wrong thing:
 *
 *  * [PriceChanged] — the server used to reprice silently, so a customer
 *    could be charged a different number from the one on screen.
 *  * [OutOfStock] — every store failure came back as a 500, so the cart
 *    could only say "something went wrong" and could not point at a line.
 *  * [QuoteStale] — the delivery quote is bound to the cart and address; a
 *    change after quoting must re-quote rather than spend a stale price.
 *  * [AwaitingConfirmation] — the single most important one. A1: the PSP
 *    redirect is NOT proof of payment. Treating a callback as success is how
 *    an order gets fulfilled against a payment that was never captured.
 */
sealed interface CheckoutUiState {

    data object Loading : CheckoutUiState

    /** Ready to place the order. Every figure comes from the server. */
    data class Ready(
        val breakdown: PriceBreakdown,
        val addressId: String,
        val addressSummary: String,
        val quoteId: String,
        val paymentMethod: PaymentMethod,
        val placing: Boolean = false,
    ) : CheckoutUiState

    /**
     * Blocking. The customer must see old → new and acknowledge before the
     * attempt is retried; the acknowledgement is what makes the new price a
     * decision rather than a surprise.
     */
    data class PriceChanged(
        val lines: List<ChangedLine>,
        val newTotal: Paise?,
        val previousTotal: Paise,
    ) : CheckoutUiState

    /** Blocking, with per-line detail so the cart can grey the right rows. */
    data class OutOfStock(val lines: List<UnavailableLine>) : CheckoutUiState

    /** The cart or address moved after the quote. Re-quote, do not retry. */
    data object QuoteStale : CheckoutUiState

    /** We cannot deliver here. A state, not an error banner. */
    data class NotServiceable(val reason: String?) : CheckoutUiState

    /**
     * Placed, and now waiting on the PSP sheet to open.
     *
     * C3-LB-4: it carries the [PaymentAttempt] the sheet will be opened for.
     * The attempt — not the order id alone — is what an incoming outcome is
     * matched against, because a buyer may retry the same order and the first
     * attempt's late callback must not settle the second.
     */
    data class OpeningPayment(
        val attempt: PaymentAttempt,
        val orderNumber: String,
    ) : CheckoutUiState {
        val orderId: String get() = attempt.orderId
    }

    /**
     * The PSP returned and the callback looked genuine — and that is ALL it
     * means.
     *
     * A1/R-3: the server marks an order paid only when a signature-verified
     * provider webhook arrives, so the app polls
     * `GET /orders/:id/payment/status` from here. Showing "Payment
     * successful" at this point would be claiming something nobody has
     * verified, and a provider can still fail to capture after the redirect.
     */
    data class AwaitingConfirmation(
        val orderId: String,
        val orderNumber: String,
        val elapsedSeconds: Int = 0,
    ) : CheckoutUiState

    data class Paid(val orderId: String, val orderNumber: String) : CheckoutUiState

    /** The payment failed. The order survives and can be retried. */
    data class PaymentFailed(val orderId: String, val orderNumber: String) : CheckoutUiState

    /**
     * LB-22: the reservation lapsed before payment, so the order was
     * terminated and the stock released to other shoppers. A late capture
     * for this order is refunded automatically rather than fulfilled — the
     * copy must not promise delivery.
     */
    data class Expired(val orderId: String) : CheckoutUiState

    /**
     * M-7: the same Idempotency-Key was reused with a different request.
     * The app starts a fresh attempt with a new key rather than accepting
     * an order built from the earlier one.
     */
    data object RetryWithNewAttempt : CheckoutUiState

    data class Failed(val message: String, val retryable: Boolean) : CheckoutUiState
}

/**
 * A5 — prepaid only.
 *
 * COD is absent from this enum, not merely hidden in the UI. The review was
 * explicit that hiding a control in Android is not a fence: the server
 * rejects `cod` at the handler, in the checkout transaction, and by a CHECK
 * constraint in migration 012. This enum simply has nothing to send.
 */
enum class PaymentMethod(val wire: String, val label: String) {
    UPI("upi", "UPI"),
    CARD("card", "Card"),
}

/*
 * WHY ONLY TWO.
 *
 * This enum listed NET_BANKING and WALLET as well. Both would have produced
 * an order the customer could never pay for, because the three layers
 * disagreed about the vocabulary:
 *
 *   Android enum                     upi, card, net_banking, wallet
 *   commerce CHECK (gated 998)       upi, card, net_banking
 *   payments-service (B6)            upi, card
 *
 * commerce forwards the order's `payment_method` verbatim to payments when it
 * opens the intent (internal/payments/p0client.go), so `net_banking` passes
 * checkout, the order commits and holds stock — and then intent creation is
 * refused. The buyer is left with a `payment_pending` order they cannot
 * complete, and the stock stays reserved until the expiry sweeper releases it.
 *
 * `wallet` was worse: before B6 payments accepted it and SKIPPED provider
 * order creation, producing an intent with a blank provider reference that can
 * never be captured, refunded or reconciled.
 *
 * The client offers what the whole chain accepts. Widening this enum requires
 * widening payments-service first, not the other way round.
 */

/** How long to keep polling before offering a manual check. */
const val PAYMENT_CONFIRMATION_TIMEOUT_SECONDS = 180
