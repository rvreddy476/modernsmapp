package com.us.android.core.payments

import android.app.Activity
import android.util.Log
import com.razorpay.Checkout
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
 * this launcher parks the pending callback and the Activity — through
 * [ActivityPaymentHost] — forwards the SDK's result into [deliver]. That
 * indirection is the SDK's shape, not a choice; keeping it in one place stops
 * it leaking into the Compose layer.
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
 *     with [PaymentOutcome.Unavailable] and does NOT touch the pending
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
class RazorpayPaymentLauncher @Inject constructor() : PaymentLauncher, PaymentResultSink {

    /** The attempt currently in flight, with the callback awaiting its result. */
    private class InFlight(
        val attempt: PaymentAttempt,
        val onOutcome: (PaymentOutcome) -> Unit,
    )

    private val lock = Any()
    private var inFlight: InFlight? = null

    /** The attempt currently holding the slot, if any. */
    internal fun inFlightAttempt(): PaymentAttempt? = synchronized(lock) { inFlight?.attempt }

    /**
     * Claims the single in-flight slot for [attempt].
     *
     * Returns false — having already reported [PaymentOutcome.Unavailable]
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
    internal fun claim(attempt: PaymentAttempt, onOutcome: (PaymentOutcome) -> Unit): Boolean {
        synchronized(lock) {
            val current = inFlight
            if (current == null) {
                inFlight = InFlight(attempt, onOutcome)
                return true
            }
            Log.w(
                TAG,
                "refusing a second sheet for ${attempt.referenceId}; " +
                    "${current.attempt.referenceId} is still in flight",
            )
        }
        // Reported OUTSIDE the lock, and to the SECOND caller: the first
        // callback is never touched.
        onOutcome(
            PaymentOutcome.Unavailable(
                "A payment is already in progress. Finish or cancel it first.",
            ),
        )
        return false
    }

    override fun open(
        activity: Activity,
        attempt: PaymentAttempt,
        session: PaymentSession,
        onOutcome: (PaymentOutcome) -> Unit,
    ) {
        // ── Rule 1: one flight ────────────────────────────────────────
        //
        // Checked and claimed under one lock, so two callers racing cannot
        // both believe the slot was free.
        if (!claim(attempt, onOutcome)) return

        val keyId = session.keyId
        val orderId = session.providerOrderId
        val provider = session.provider

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

        val amountMinor = session.amountMinor
        if (amountMinor <= 0) {
            return failToOpen(attempt, "This order has no payable amount.")
        }

        val checkout = Checkout()
        checkout.setKeyID(keyId)

        val options = checkoutOptions(session).toJsonObject()

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
        callback(PaymentOutcome.Unavailable(reason))
    }

    /**
     * Takes the pending callback if [attempt] is the one in flight.
     *
     * Returns null when the slot is empty or belongs to someone else — the
     * two cases that must be no-ops rather than misdeliveries.
     */
    private fun release(attempt: PaymentAttempt?): ((PaymentOutcome) -> Unit)? =
        synchronized(lock) {
            val current = inFlight ?: return@synchronized null
            if (attempt != null && current.attempt != attempt) return@synchronized null
            inFlight = null
            current.onOutcome
        }

    /**
     * Delivers a result from the Activity's SDK listener.
     *
     * Called by [ActivityPaymentHost]. The SDK does not tell us which attempt
     * it is answering — it only ever runs one sheet — so the result goes to
     * whatever is in flight, and the slot is cleared FIRST. A duplicate or late
     * callback then finds an empty slot and is a no-op rather than a second
     * trip through the checkout flow.
     */
    override fun deliver(outcome: PaymentOutcome) {
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
    override fun abandon(attempt: PaymentAttempt) {
        if (release(attempt) != null) {
            Log.i(TAG, "abandoned the in-flight sheet for ${attempt.referenceId}")
        }
    }

    internal companion object {
        const val TAG = "RazorpayLauncher"
        const val PROVIDER_RAZORPAY = "razorpay"

        /**
         * The name on the Razorpay sheet's header when the server names none.
         *
         * "Momentum Merchant" is the business name registered on the founder's
         * Razorpay account (decision, 12 Sep 2026). The sheet has to show the
         * name the buyer's bank statement will carry: the old value, "atPost",
         * outlived the product rename, and a sheet naming a merchant the
         * statement does not is exactly the mismatch a buyer reads as fraud.
         * Internal so a test can pin it.
         *
         * Since 2026-09-14 the server's per-application registry supplies the
         * name ([PaymentSession.merchantDisplayName]); this is the fallback for
         * a server that omits it. See [merchantNameFor].
         */
        const val DEFAULT_MERCHANT_NAME = "Momentum Merchant"

        /** The longest merchant name shown. Matches the server's own cap. */
        const val MERCHANT_NAME_MAX_LENGTH = 64

        /**
         * The name the sheet shows for [session].
         *
         * The server already trims and caps, but this string is rendered on a
         * payment sheet, so the client cleans it again: control characters are
         * removed, the rest is trimmed and capped at [MERCHANT_NAME_MAX_LENGTH]
         * (never splitting a surrogate pair), and anything blank afterwards —
         * including an absent name — becomes [DEFAULT_MERCHANT_NAME].
         *
         * Never log this alongside the key id.
         */
        fun merchantNameFor(session: PaymentSession): String {
            val cleaned = session.merchantDisplayName
                ?.filterNot { it.isISOControl() }
                ?.trim()
                ?.capAt(MERCHANT_NAME_MAX_LENGTH)
                ?.trim()
            return if (cleaned.isNullOrBlank()) DEFAULT_MERCHANT_NAME else cleaned
        }

        private fun String.capAt(max: Int): String {
            if (length <= max) return this
            val end = if (Character.isHighSurrogate(this[max - 1])) max - 1 else max
            return substring(0, end)
        }

        /**
         * The options handed to `Checkout.open`, as plain values so they can be
         * checked without the Android `org.json` runtime. The key id is not
         * here: it is set on the Checkout itself.
         */
        fun checkoutOptions(session: PaymentSession): Map<String, Any> = linkedMapOf(
            "name" to merchantNameFor(session),
            "description" to session.description,
            // Authoritative. With order_id present, Razorpay prices the sheet
            // from the ORDER, so the amount below cannot be used to underpay.
            "order_id" to session.providerOrderId,
            "currency" to session.currency,
            "amount" to session.amountMinor,
            "retry" to mapOf("enabled" to false),
            // Sending the SDK's own telemetry is off: this flow already
            // reports its outcome to our server, which is the only party whose
            // opinion of the payment counts.
            "send_sms_hash" to false,
        )

        private fun Map<*, *>.toJsonObject(): JSONObject = JSONObject().also { json ->
            forEach { (key, value) ->
                json.put(key as String, if (value is Map<*, *>) value.toJsonObject() else value)
            }
        }
    }
}
