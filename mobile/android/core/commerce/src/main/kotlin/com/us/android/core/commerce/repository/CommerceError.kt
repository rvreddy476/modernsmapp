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

    /** The older, reasonless `COUPON_UNAVAILABLE`. A current server says why: see [CouponRefused]. */
    data object CouponUnavailable : CommerceError

    /**
     * A coupon was refused, with the server's reason (422 `COUPON_*`).
     *
     * Quote and checkout can answer any of the six codes; the payment retry
     * on a `payment_failed` order re-claims the order's coupon and can answer
     * `COUPON_USED_UP`, `COUPON_INVALID` or `COUPON_EXPIRED`, leaving the
     * order unpaid. [minOrder] is `COUPON_MIN_ORDER`'s `details.min_order_minor`.
     */
    data class CouponRefused(
        val reason: CouponRefusal,
        val minOrder: Paise? = null,
    ) : CommerceError

    data object CartEmpty : CommerceError

    /** A5: prepaid-only launch. */
    data object CodNotSupported : CommerceError

    data object OrderNotFound : CommerceError

    /**
     * The chosen delivery address is gone (`ADDRESS_NOT_FOUND`), deleted on
     * another device, say. Its own case so checkout sends the buyer to pick
     * an address rather than telling them an order could not be found.
     */
    data object AddressNotFound : CommerceError

    /**
     * A 401 that reached the app: the token authenticator already tried to
     * refresh and could not, so the session is over. It is never "not found"
     * and never a failed payment; the buyer has to sign in again.
     */
    data object SessionExpired : CommerceError

    data object CancelNotPermitted : CommerceError

    /** Transient contention. Safe to retry as-is. */
    data object TryAgain : CommerceError

    /**
     * The server does not have this endpoint: a 404 carrying no error code.
     *
     * Its own case rather than [Unexpected] because it is not a fault. The
     * app ships ahead of the services it talks to, and a surface built on an
     * additive endpoint — MStore's `/home`, favourites — must be able to tell
     * "this server is older than this build" from "something went wrong" and
     * hide itself instead of showing the customer an error for a section
     * they never asked for.
     */
    data object NotAvailable : CommerceError

    data class Network(val cause: Throwable?) : CommerceError

    /**
     * Anything unmapped. Renders as a generic failure.
     *
     * [httpStatus] (2026-09-30) is the response status, 0 when built without
     * one, so a poll can tell a 4xx the server will keep giving — a 403
     * NOT_YOUR_ORDER, a 400 — from a 5xx it may not. It is NOT what a screen
     * branches on: branch on [code], as every existing `code == "NO_SELLER"`
     * check does, and the two-argument form keeps compiling for them.
     *
     * [fields] (2026-10-24) is the refusal's per-field `details.fields`, when
     * it sends one: `PRODUCT_INCOMPLETE` names every gap a listing still has
     * before it can be reviewed, and `ATTRIBUTE_VALUES_INVALID` every value
     * refused, in the same envelope. Empty for every other code, so the
     * existing two- and three-argument forms compare as they did.
     */
    data class Unexpected(
        val code: String,
        val message: String,
        val httpStatus: Int = 0,
        val fields: List<FieldProblem> = emptyList(),
    ) : CommerceError
}

/**
 * One field a refusal names: its code (a category attribute code, or a
 * built-in such as `listing.price`, which carries a dot no attribute code may),
 * the label the server gives it, and the server's own reason. The reason is
 * for the log; a seller is shown copy written for them.
 */
data class FieldProblem(
    val code: String,
    val label: String,
    val reason: String,
)

/**
 * Whether the server has answered this request for good, so repeating it is
 * pointless: the order is not there, or not this caller's, or the request
 * itself was refused with a 4xx that will not change. A 5xx, a network
 * failure, a 408 or a 429 are NOT permanent.
 *
 * [CommerceError.NotAvailable] (a bare 404, "no such route") is deliberately
 * not permanent here: the payment source treats it as "older server" and
 * falls back, and a poll that stops on it would strand a real payment.
 * [CommerceError.SessionExpired] is not permanent either: a payment poll that
 * stopped on it would end in "failed" for a payment nobody has read.
 */
fun CommerceError.isPermanentRefusal(): Boolean = when (this) {
    CommerceError.OrderNotFound,
    CommerceError.CancelNotPermitted,
    -> true

    is CommerceError.Unexpected ->
        httpStatus in HTTP_CLIENT_ERRORS && httpStatus !in HTTP_TRANSIENT_CLIENT_ERRORS

    else -> false
}

/**
 * Whether a WRITE that failed this way may still have been applied.
 *
 * True when no definite answer came back: the connection broke or timed out
 * (possibly after the server committed), the server failed with a 5xx or a
 * status-less error, or it asked for the same request again (`TRY_AGAIN`).
 * Only resending the same request under the same idempotency key is safe
 * then; anything else risks doing it twice. A coded 4xx, a 401 and a bare
 * 404 are definite refusals: nothing was applied.
 */
fun CommerceError.outcomeUnknown(): Boolean = when (this) {
    is CommerceError.Network, CommerceError.TryAgain -> true
    is CommerceError.Unexpected -> httpStatus == 0 || httpStatus >= HTTP_FIRST_SERVER_ERROR
    else -> false
}

private const val HTTP_FIRST_SERVER_ERROR = 500
private const val HTTP_BAD_REQUEST = 400
private const val HTTP_LAST_CLIENT_ERROR = 499
private const val HTTP_REQUEST_TIMEOUT = 408
private const val HTTP_TOO_MANY_REQUESTS = 429
private val HTTP_CLIENT_ERRORS = HTTP_BAD_REQUEST..HTTP_LAST_CLIENT_ERROR
private val HTTP_TRANSIENT_CLIENT_ERRORS = setOf(HTTP_REQUEST_TIMEOUT, HTTP_TOO_MANY_REQUESTS)

/** Why a coupon was refused, one per server code (commerce `coupons.go`). */
enum class CouponRefusal(val wire: String) {
    INVALID("COUPON_INVALID"),

    /** `COUPON_INVALID` carrying `details.starts_at`: the coupon has not started. */
    NOT_YET_ACTIVE("COUPON_INVALID"),
    EXPIRED("COUPON_EXPIRED"),
    MIN_ORDER("COUPON_MIN_ORDER"),
    USED_UP("COUPON_USED_UP"),
    NOT_APPLICABLE("COUPON_NOT_APPLICABLE"),

    /** Platform coupons are switched off. */
    NOT_AVAILABLE("COUPON_NOT_AVAILABLE"),
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
