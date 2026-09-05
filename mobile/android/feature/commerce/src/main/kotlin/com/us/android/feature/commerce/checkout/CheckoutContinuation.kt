package com.us.android.feature.commerce.checkout

import androidx.lifecycle.SavedStateHandle
import com.us.android.core.commerce.model.Paise
import com.us.android.core.commerce.model.PriceBreakdown
import com.us.android.core.commerce.payment.PaymentAttempt

/**
 * The checkout state that must outlive the process.
 *
 * ## Why this exists
 *
 * Android will kill a backgrounded process at any time, and a buyer inside a
 * payment sheet is backgrounded by definition. Review 4 (C3-LB-4 criterion 5)
 * found the whole checkout living in ViewModel fields: quote, accepted total,
 * idempotency key, order id and payment attempt all vanished together.
 *
 * The money consequences of losing each one are different, and all bad:
 *
 *  * the IDEMPOTENCY KEY is what makes a retried checkout the same order. Lose
 *    it after the request left but before the response arrived, and the retry
 *    mints a new key — which is a second order, a second stock hold, and a
 *    second payment intent for one customer decision.
 *  * the ACCEPTED TOTAL is what the buyer approved. Lose it and the app has to
 *    re-quote, which is fine, but it must not silently submit a different
 *    number than the one on the screen they left.
 *  * the ORDER ID is the only handle on money that may already have moved.
 *    Lose it and the app cannot poll, so it cannot tell the buyer whether they
 *    paid.
 *  * the PAYMENT ATTEMPT is what keeps a late callback from settling a newer
 *    attempt (C3-LB-4). An attempt id that resets on recreation defeats it.
 *
 * ## What is deliberately NOT here
 *
 * The provider `client_session` — the publishable key and the PSP order
 * handle — and anything else the SDK was handed. `SavedStateHandle` is
 * persisted to disk by the system, and a payment session is short-lived
 * server-issued material: it should be re-fetched from the server on
 * recreation, not restored from a bundle that outlived it.
 *
 * The address SUMMARY is stored because it is display text the buyer already
 * chose to see on this screen; the address itself is a server-side record
 * referenced by id.
 */
internal class CheckoutContinuation(private val handle: SavedStateHandle) {

    /**
     * How far the buyer got, from the SERVER's point of view.
     *
     * The distinction that matters is [Phase.OrderPlaced]: before it, nothing
     * durable exists and a fresh attempt is free; after it, an order exists,
     * may hold stock, and may already be paid — so recovery must ASK the
     * server rather than assume anything.
     */
    enum class Phase {
        /** Nothing yet. Quote and go. */
        Fresh,

        /** A server quote is held and was shown to the buyer. */
        Quoted,

        /**
         * A checkout request was SENT. Whether it created an order is unknown
         * — this is precisely the state a lost HTTP response leaves behind,
         * and the reason the idempotency key must survive.
         */
        Submitting,

        /** An order exists. Recovery polls it; it never re-submits. */
        OrderPlaced,

        /** A payment sheet was requested for the active attempt. */
        SheetRequested,
    }

    var phase: Phase
        get() = handle.get<String>(KEY_PHASE)?.let {
            runCatching { Phase.valueOf(it) }.getOrNull()
        } ?: Phase.Fresh
        set(value) {
            handle[KEY_PHASE] = value.name
        }

    var addressId: String?
        get() = handle[KEY_ADDRESS_ID]
        set(v) {
            handle[KEY_ADDRESS_ID] = v
        }

    var addressSummary: String
        get() = handle[KEY_ADDRESS_SUMMARY] ?: ""
        set(v) {
            handle[KEY_ADDRESS_SUMMARY] = v
        }

    var quoteId: String?
        get() = handle[KEY_QUOTE_ID]
        set(v) {
            handle[KEY_QUOTE_ID] = v
        }

    var couponCode: String?
        get() = handle[KEY_COUPON]
        set(v) {
            handle[KEY_COUPON] = v
        }

    var paymentMethod: PaymentMethod
        get() = handle.get<String>(KEY_METHOD)?.let { wire ->
            PaymentMethod.entries.firstOrNull { it.wire == wire }
        } ?: PaymentMethod.UPI
        set(v) {
            handle[KEY_METHOD] = v.wire
        }

    /**
     * The attempt key. Read-through-and-mint: the very first read on a fresh
     * ViewModel creates one and persists it immediately, so it is stable from
     * before the first request rather than from the first save.
     */
    fun attemptKey(mint: () -> String): String {
        handle.get<String>(KEY_ATTEMPT_KEY)?.let { return it }
        val k = mint()
        handle[KEY_ATTEMPT_KEY] = k
        return k
    }

    fun newAttemptKey(mint: () -> String): String {
        val k = mint()
        handle[KEY_ATTEMPT_KEY] = k
        return k
    }

    var orderId: String?
        get() = handle[KEY_ORDER_ID]
        set(v) {
            handle[KEY_ORDER_ID] = v
        }

    var orderNumber: String
        get() = handle[KEY_ORDER_NUMBER] ?: ""
        set(v) {
            handle[KEY_ORDER_NUMBER] = v
        }

    /**
     * The active payment attempt, rebuilt from its two persisted halves.
     *
     * Both are needed: the order id alone cannot distinguish a retry of the
     * same order from the attempt that preceded it, which is the whole point
     * of C3-LB-4.
     */
    var attempt: PaymentAttempt?
        get() {
            val order = handle.get<String>(KEY_ATTEMPT_ORDER) ?: return null
            val id = handle.get<String>(KEY_ATTEMPT_ID) ?: return null
            return PaymentAttempt(orderId = order, id = id)
        }
        set(v) {
            handle[KEY_ATTEMPT_ORDER] = v?.orderId
            handle[KEY_ATTEMPT_ID] = v?.id
        }

    /**
     * The exact breakdown the buyer accepted.
     *
     * Stored as its component minor units rather than a serialized object:
     * `Paise` is a value class over Long, and five longs plus a currency
     * string is a shape that cannot break when the model gains a field.
     */
    var breakdown: PriceBreakdown?
        get() {
            val total = handle.get<Long>(KEY_TOTAL) ?: return null
            return PriceBreakdown(
                subtotal = Paise(handle.get<Long>(KEY_SUBTOTAL) ?: 0L),
                discount = Paise(handle.get<Long>(KEY_DISCOUNT) ?: 0L),
                shipping = Paise(handle.get<Long>(KEY_SHIPPING) ?: 0L),
                tax = Paise(handle.get<Long>(KEY_TAX) ?: 0L),
                total = Paise(total),
                currency = handle.get<String>(KEY_CURRENCY) ?: "INR",
            )
        }
        set(v) {
            handle[KEY_SUBTOTAL] = v?.subtotal?.value
            handle[KEY_DISCOUNT] = v?.discount?.value
            handle[KEY_SHIPPING] = v?.shipping?.value
            handle[KEY_TAX] = v?.tax?.value
            handle[KEY_TOTAL] = v?.total?.value
            handle[KEY_CURRENCY] = v?.currency
        }

    /** Clears everything tied to one checkout attempt, keeping the address. */
    fun clearAttempt() {
        handle[KEY_ORDER_ID] = null
        handle[KEY_ORDER_NUMBER] = null
        handle[KEY_ATTEMPT_ORDER] = null
        handle[KEY_ATTEMPT_ID] = null
    }

    private companion object {
        const val KEY_PHASE = "checkout.phase"
        const val KEY_ADDRESS_ID = "checkout.addressId"
        const val KEY_ADDRESS_SUMMARY = "checkout.addressSummary"
        const val KEY_QUOTE_ID = "checkout.quoteId"
        const val KEY_COUPON = "checkout.coupon"
        const val KEY_METHOD = "checkout.method"
        const val KEY_ATTEMPT_KEY = "checkout.attemptKey"
        const val KEY_ORDER_ID = "checkout.orderId"
        const val KEY_ORDER_NUMBER = "checkout.orderNumber"
        const val KEY_ATTEMPT_ORDER = "checkout.attempt.orderId"
        const val KEY_ATTEMPT_ID = "checkout.attempt.id"
        const val KEY_SUBTOTAL = "checkout.breakdown.subtotal"
        const val KEY_DISCOUNT = "checkout.breakdown.discount"
        const val KEY_SHIPPING = "checkout.breakdown.shipping"
        const val KEY_TAX = "checkout.breakdown.tax"
        const val KEY_TOTAL = "checkout.breakdown.total"
        const val KEY_CURRENCY = "checkout.breakdown.currency"
    }
}
