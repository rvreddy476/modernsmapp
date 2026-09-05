package com.us.android.feature.commerce.ui

import com.us.android.core.commerce.repository.CommerceError

/**
 * Customer-facing copy for every typed commerce failure.
 *
 * One place, not per screen. The server went to real trouble to stop
 * returning `INTERNAL_ERROR` for everything — an out-of-stock race, a price
 * change, a permission failure and a database outage used to be
 * indistinguishable — and that work is wasted if each screen invents its own
 * wording and quietly falls back to "something went wrong" for the codes it
 * forgot.
 *
 * Two rules the strings below follow:
 *
 *  * Never claim more than the server said. "Confirming your payment" is
 *    honest while a webhook is outstanding; "Payment successful" is not.
 *  * Say what the customer can DO. "Out of stock" is a fact; "remove it or
 *    reduce the quantity" is an instruction they can act on.
 *
 * The exhaustive `when` is deliberate: adding a case to [CommerceError]
 * without giving it copy is then a compile error rather than a silent
 * fallback to the generic branch.
 */
@Suppress("CyclomaticComplexMethod")
fun CommerceError.describe(): String = when (this) {
    is CommerceError.OutOfStock ->
        if (lines.size == 1) {
            "${lines.first().title} is out of stock."
        } else {
            "${lines.size} items in your bag are out of stock."
        }

    is CommerceError.PriceChanged ->
        "The price changed while you were shopping. Please review the new total."

    CommerceError.QuoteStale ->
        "Your bag or address changed after we calculated delivery. We'll work it out again."

    CommerceError.QuoteExpired ->
        "The delivery estimate expired. We'll calculate it again."

    is CommerceError.NotServiceable ->
        reason?.takeIf { it.isNotBlank() }
            ?: "We can't deliver to this address yet."

    CommerceError.IdempotencyConflict ->
        "Something changed since you started. We'll begin a fresh attempt so nothing is ordered twice."

    CommerceError.ProductUnavailable ->
        "An item in your bag is no longer available."

    CommerceError.MultipleSellers ->
        "Your bag has items from more than one seller. Please order from one seller at a time."

    CommerceError.CouponUnavailable ->
        "That coupon can't be used on this order."

    CommerceError.CartEmpty ->
        "Your bag is empty."

    CommerceError.CodNotSupported ->
        "Cash on delivery isn't available. Please pay by UPI or card."

    CommerceError.OrderNotFound ->
        "We couldn't find that order."

    CommerceError.CancelNotPermitted ->
        "This order can no longer be cancelled."

    CommerceError.TryAgain ->
        "That didn't go through. Please try again."

    // Only reached when a surface chose to RENDER this rather than hide
    // itself, which is the whole point of the case existing — the sections
    // that can degrade never get this far.
    CommerceError.NotAvailable ->
        "That isn't available in this version yet."

    is CommerceError.Network ->
        "We couldn't reach the network. Check your connection and try again."

    is CommerceError.Unexpected ->
        // The code is deliberately NOT shown to the customer; it is for the
        // log. A raw server code on screen is noise they cannot act on.
        "Something went wrong. Please try again."
}

/**
 * Whether retrying the SAME request unchanged is sensible.
 *
 * This is not "did it fail" — most commerce failures need the customer to
 * change something first (re-quote, edit the cart, acknowledge a price), and
 * offering a bare Retry for those just reproduces the failure.
 */
fun CommerceError.isRetryable(): Boolean = when (this) {
    is CommerceError.Network, CommerceError.TryAgain -> true
    else -> false
}
