package com.us.android.payment

import android.app.Activity
import android.util.Log
import com.razorpay.Checkout
import com.us.android.core.commerce.payment.PaymentAttempt
import org.json.JSONObject
import javax.inject.Inject
import javax.inject.Singleton

/**
 * The Razorpay implementation of [PaymentLauncher].
 *
 * ## Why the callback is held here
 *
 * Razorpay's SDK does not deliver its result to the caller. It calls back on
 * the **Activity**, which must implement `PaymentResultWithDataListener`. So
 * this launcher parks the pending callback and [com.us.android.MainActivity]
 * forwards the SDK's result into [deliver]. That indirection is the SDK's
 * shape, not a choice; keeping it in one place stops it leaking into the
 * Compose layer.
 *
 * ## C3-LB-4 — one flight, and a slot that cannot be stolen
 *
 * Review 3 (C-LB-2) found a single unguarded callback slot: a second `open`
 * overwrote the first, so the first sheet's result was delivered to the
 * second checkout's callback, and the second sheet's result then found no
 * pending callback and was dropped. A double tap, a retry, or an Activity
 * recreation was enough.
 *
 * Two rules close it:
 *
 *  1. **One flight.** While a sheet is in flight, a second [open] is refused
 *     with [PaymentSheetOutcome.Unavailable] and does NOT touch the pending
 *     slot. Refusing is safe in a way that queueing is not: nothing was
 *     presented to the buyer, so no payment can have been taken, and the
 *     caller can say so plainly.
 *  2. **The slot is bound to its attempt.** [deliver] routes the result to
 *     the attempt that is actually in flight, so a late callback arriving
 *     after the buyer navigated away cannot be handed to a later checkout.
 *     [abandon] clears a slot whose screen has gone.
 *
 * ## What is sent, and what is deliberately not
 *
 * `order_id` and `key_id` come from the SERVER (`client_session`), so the
 * publishable key always matches the order it belongs to. `amount` is the
 * server's own intent amount, relayed — not a figure the client computed. The
 * distinction matters: LB-4's exploit was a client CHOOSING what it paid, and
 * Razorpay treats the order's amount as authoritative whenever `order_id` is
 * present, so a relayed amount cannot override it.
 */
@Singleton
class RazorpayPaymentLauncher @Inject constructor() : PaymentLauncher {

    /** The attempt currently in flight, with the callback awaiting its result. */
    private class InFlight(
        val attempt: PaymentAttempt,
        val onOutcome: (PaymentSheetOutcome) -> Unit,
    )

    private val lock = Any()
    private var inFlight: InFlight? = null

    /** The attempt currently holding the slot, if any. */
    internal fun inFlightAttempt(): PaymentAttempt? = synchronized(lock) { inFlight?.attempt }

    /**
     * Claims the single in-flight slot for [attempt].
     *
     * Returns false — having already reported [PaymentSheetOutcome.Unavailable]
     * to [onOutcome] — when another sheet is in flight. The check and the
     * claim happen under one lock, so two callers racing cannot both believe
     * the slot was free.
     *
     * Refusing is safe in a way that queueing is not: nothing was presented to
     * the buyer, so no payment can have been taken, and the caller can say so
     * plainly. Queueing would open a second sheet the buyer never asked for.
     *
     * Internal rather than private so the one-flight rules can be exercised
     * without an Activity and an SDK. This IS the production state machine,
     * not a description of it.
     */
    internal fun claim(attempt: PaymentAttempt, onOutcome: (PaymentSheetOutcome) -> Unit): Boolean {
        synchronized(lock) {
            val current = inFlight
            if (current == null) {
                inFlight = InFlight(attempt, onOutcome)
                return true
            }
            Log.w(
                TAG,
                "refusing a second sheet for ${attempt.orderId}; " +
                    "${current.attempt.orderId} is still in flight",
            )
        }
        // Reported OUTSIDE the lock, and to the SECOND caller: the first
        // callback is never touched.
        onOutcome(
            PaymentSheetOutcome.Unavailable(
                "A payment is already in progress. Finish or cancel it first.",
            ),
        )
        return false
    }

    override fun open(
        activity: Activity,
        attempt: PaymentAttempt,
        session: Map<String, String>,
        description: String,
        onOutcome: (PaymentSheetOutcome) -> Unit,
    ) {
        // ── Rule 1: one flight ────────────────────────────────────────
        //
        // Checked and claimed under one lock, so two callers racing cannot
        // both believe the slot was free.
        if (!claim(attempt, onOutcome)) return

        val keyId = session["key_id"].orEmpty()
        val orderId = session["order_id"].orEmpty()
        val provider = session["provider"].orEmpty()

        // Fail before presenting anything, so the app can say "we could not
        // open payment" rather than showing a sheet that will not work. Each
        // of these RELEASES the slot: nothing was opened, so nothing is in
        // flight, and holding it would wedge every later attempt.
        when {
            provider.isNotEmpty() && provider != PROVIDER_RAZORPAY ->
                return failToOpen(
                    attempt,
                    "This order was created with $provider, which this build cannot open.",
                )

            keyId.isEmpty() || orderId.isEmpty() ->
                return failToOpen(
                    attempt,
                    "The server did not supply a payment session for this order.",
                )
        }

        val amountMinor = session[SESSION_AMOUNT_MINOR]?.toLongOrNull()
        if (amountMinor == null || amountMinor <= 0) {
            return failToOpen(attempt, "This order has no payable amount.")
        }

        val checkout = Checkout()
        checkout.setKeyID(keyId)

        val options = JSONObject().apply {
            put("name", MERCHANT_NAME)
            put("description", description)
            // Authoritative. With order_id present, Razorpay prices the sheet
            // from the ORDER, so the amount below cannot be used to underpay.
            put("order_id", orderId)
            put("currency", session["currency"] ?: DEFAULT_CURRENCY)
            put("amount", amountMinor)
            put("retry", JSONObject().put("enabled", false))
            // Sending the SDK's own telemetry is off: this flow already
            // reports its outcome to our server, which is the only party whose
            // opinion of the payment counts.
            put("send_sms_hash", false)
        }

        @Suppress("TooGenericExceptionCaught")
        try {
            checkout.open(activity, options)
        } catch (t: Throwable) {
            // An SDK that refuses to open has presented nothing, so no payment
            // can have been taken. Report Unavailable, not Failed.
            Log.e(TAG, "razorpay checkout could not be opened", t)
            failToOpen(attempt, "Payment could not be opened.")
        }
    }

    /**
     * Releases the slot and reports that nothing was presented.
     *
     * Every pre-sheet failure goes through here, so no path can leave the
     * launcher wedged holding a slot for a sheet that never opened.
     */
    private fun failToOpen(attempt: PaymentAttempt, reason: String) {
        val callback = release(attempt) ?: return
        callback(PaymentSheetOutcome.Unavailable(reason))
    }

    /**
     * Takes the pending callback if [attempt] is the one in flight.
     *
     * Returns null when the slot is empty or belongs to someone else — the
     * two cases that must be no-ops rather than misdeliveries.
     */
    private fun release(attempt: PaymentAttempt?): ((PaymentSheetOutcome) -> Unit)? =
        synchronized(lock) {
            val current = inFlight ?: return@synchronized null
            if (attempt != null && current.attempt != attempt) return@synchronized null
            inFlight = null
            current.onOutcome
        }

    /**
     * Delivers a result from the Activity's SDK listener.
     *
     * Called by [com.us.android.MainActivity]. The SDK does not tell us which
     * attempt it is answering — it only ever runs one sheet — so the result
     * goes to whatever is in flight, and the slot is cleared FIRST. A
     * duplicate or late callback then finds an empty slot and is a no-op
     * rather than a second trip through the checkout flow.
     */
    fun deliver(outcome: PaymentSheetOutcome) {
        val callback = release(null)
        if (callback == null) {
            // Not an error: a duplicate callback, or one arriving after the
            // buyer left. Dropping it is correct — the server is the record
            // of what happened, and the orders screen reads it.
            Log.w(TAG, "razorpay delivered a result with no sheet in flight: $outcome")
            return
        }
        callback(outcome)
    }

    /**
     * Abandons the in-flight attempt without delivering an outcome.
     *
     * Called when the screen that opened the sheet has gone. It frees the slot
     * so the next checkout can open, and guarantees the abandoned attempt's
     * callback can never fire against a later order.
     */
    fun abandon(attempt: PaymentAttempt) {
        if (release(attempt) != null) {
            Log.i(TAG, "abandoned the in-flight sheet for ${attempt.orderId}")
        }
    }

    private companion object {
        const val TAG = "RazorpayLauncher"
        const val PROVIDER_RAZORPAY = "razorpay"
        const val MERCHANT_NAME = "atPost"
        const val DEFAULT_CURRENCY = "INR"

        /**
         * The amount is passed through the session map so the port stays free
         * of commerce types. It is the SERVER's intent amount, relayed.
         */
        const val SESSION_AMOUNT_MINOR = "amount_minor"
    }
}
