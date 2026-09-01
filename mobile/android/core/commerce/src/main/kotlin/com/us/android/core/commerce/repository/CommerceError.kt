package com.us.android.core.commerce.repository

import com.us.android.core.commerce.model.Paise

/**
 * The checkout failures the UI must render distinctly.
 *
 * This type exists because of a specific server-side defect. The old handler
 * mapped EVERY store error to `INTERNAL_ERROR` with a 500, so an out-of-stock
 * race, a price change, a permission failure and a database outage were
 * indistinguishable to the client — which meant the app could not render any
 * of them properly and fell back to "something went wrong".
 *
 * The server now returns a stable `code` per §7's state list. Branch on the
 * code, never on the message: messages are human-facing and get reworded.
 */
sealed interface CommerceError {

    /**
     * One or more lines cannot be fulfilled.
     *
     * Carries per-line detail so the cart can grey exactly the wrong rows
     * rather than failing the whole basket with a generic banner.
     */
    data class OutOfStock(val lines: List<UnavailableLine>) : CommerceError

    /**
     * The price moved since the customer last saw it.
     *
     * §7-J5: this is a BLOCKING state. The app shows old → new per line and
     * requires an explicit acknowledgement before retrying — charging a
     * different number than the one on screen is not something to resolve
     * silently, however small the delta.
     */
    data class PriceChanged(
        val lines: List<ChangedLine>,
        val newTotal: Paise?,
    ) : CommerceError

    /**
     * The delivery quote no longer matches the cart or address.
     *
     * A4/R-4: the quote is bound to the cart version, address content,
     * seller and item set. Anything the customer changed after quoting
     * invalidates it, and the app must re-quote rather than spend a stale
     * price — the alternative is charging ₹70 for a delivery that now costs
     * ₹170.
     */
    data object QuoteStale : CommerceError

    data object QuoteExpired : CommerceError

    /** We cannot deliver to this PIN code. A first-class state, not an error. */
    data class NotServiceable(val reason: String?) : CommerceError

    /**
     * The same Idempotency-Key was reused with a different request.
     *
     * M-7: returning the original order here is how a client that retried
     * after changing its address silently shipped somewhere else. The app
     * must start a fresh attempt with a new key.
     */
    data object IdempotencyConflict : CommerceError

    /** A product in the cart is no longer purchasable (LB-17). */
    data object ProductUnavailable : CommerceError

    /** D2: P0 carts hold one seller's items. */
    data object MultipleSellers : CommerceError

    data object CouponUnavailable : CommerceError

    data object CartEmpty : CommerceError

    /** A5: prepaid-only launch. */
    data object CodNotSupported : CommerceError

    data object OrderNotFound : CommerceError

    data object CancelNotPermitted : CommerceError

    /** Transient contention. Safe to retry as-is. */
    data object TryAgain : CommerceError

    data class Network(val cause: Throwable?) : CommerceError

    /** Anything unmapped. Renders as a generic failure. */
    data class Unexpected(val code: String, val message: String) : CommerceError
}

data class UnavailableLine(
    val variantId: String,
    val productId: String,
    val title: String,
    val requested: Int,
    val available: Int,
)

data class ChangedLine(
    val variantId: String,
    val was: Paise,
    val now: Paise,
)

/** A result that carries a typed failure rather than a bare exception. */
sealed interface CommerceResult<out T> {
    data class Success<T>(val value: T) : CommerceResult<T>
    data class Failure(val error: CommerceError) : CommerceResult<Nothing>
}

inline fun <T, R> CommerceResult<T>.map(transform: (T) -> R): CommerceResult<R> = when (this) {
    is CommerceResult.Success -> CommerceResult.Success(transform(value))
    is CommerceResult.Failure -> this
}
