package com.us.android.core.payments

/**
 * The port a product implements so [PaymentCoordinator] can ask the SERVER
 * whether a payment happened.
 *
 * A1/R-3: a payment is confirmed only by payments-service's signature-verified
 * provider webhook. The client never marks anything paid; it reads the
 * product's own status endpoint, and this port is that read, mapped into a
 * vocabulary the coordinator understands.
 *
 * A source belongs to ONE application ([applicationId]), and the coordinator
 * will not use it to confirm another application's payment: MStore's source
 * can never be asked about a Feast order.
 *
 * ## Rules for an implementation
 *
 *  * A read that could not be made — network, 5xx, an undecodable body — is
 *    [PaymentStatusReading.Unreachable], never [PaymentStatusReading.Failed].
 *    "We do not know yet" and "the payment failed" are different facts, and
 *    telling a buyer their payment failed while their money is gone is the
 *    worst outcome this flow can produce. The coordinator also treats a THROWN
 *    exception as unreachable, but an implementation should not rely on that.
 *  * A value the app does not recognise is [PaymentStatusReading.Confirming].
 *    The server's vocabulary can grow ahead of a released app.
 *  * A refund outranks paid. A capture that landed after the order lapsed is
 *    being returned, and must never render as a successful order.
 *  * Do not send [applicationId] to the endpoint yet — the backend does not
 *    store it. See [PaymentApplication] for where it goes once it does.
 *
 * ## Example: Feast
 *
 * Feast checkout is not built yet. When it is, its status endpoint is
 * `GET /v1/food/orders/:id/payment`, which answers `confirming | paid | failed`
 * plus a `refund_status`. The whole integration on this side is a class like
 * this one, in `:core:food` beside the food-service DTOs (the DTO, API and
 * `refund_status` values below are illustrative — pin the real ones with a
 * golden contract fixture first):
 *
 * ```kotlin
 * /** Feast's application id for :core:payments. ONE constant, provisional. */
 * const val FEAST_PAYMENT_APPLICATION_ID = "feast"
 *
 * class FeastPaymentStatusSource @Inject constructor(
 *     private val api: FoodApi,
 * ) : PaymentStatusSource {
 *
 *     override val applicationId: String = FEAST_PAYMENT_APPLICATION_ID
 *
 *     override suspend fun status(referenceId: String): PaymentStatusReading {
 *         val dto = try {
 *             api.orderPayment(orderId = referenceId).body()?.data
 *         } catch (e: IOException) {
 *             return PaymentStatusReading.Unreachable(e.message)
 *         } ?: return PaymentStatusReading.Unreachable("empty body")
 *
 *         return when {
 *             dto.refundStatus == "pending" -> PaymentStatusReading.RefundPending
 *             dto.refundStatus == "refunded" -> PaymentStatusReading.Refunded
 *             dto.status == "paid" -> PaymentStatusReading.Paid
 *             // The server keeps a failed attempt pending and retryable for
 *             // 15 minutes, so the buyer is offered a retry on the SAME order.
 *             dto.status == "failed" -> PaymentStatusReading.Failed(retryable = true)
 *             else -> PaymentStatusReading.Confirming // "confirming", or newer
 *         }
 *     }
 * }
 * ```
 *
 * The Feast checkout ViewModel then persists its attempt in an
 * `InFlightPayment(store, FEAST_PAYMENT_APPLICATION_ID)`, opens the sheet with
 * [PaymentCoordinator.launch] using a session and attempt stamped `feast`, and
 * on [PaymentSheetResult.Closed] (or after a process death with a sheet already
 * requested) collects
 * `coordinator.confirm(FEAST_PAYMENT_APPLICATION_ID, orderId, feastStatusSource)`.
 */
interface PaymentStatusSource {

    /** The one application whose payments this source reads. */
    val applicationId: String

    /**
     * Reads the server's payment state for [referenceId] within
     * [applicationId]. One request, no retry.
     */
    suspend fun status(referenceId: String): PaymentStatusReading
}

/** What the server said about a payment, in the coordinator's vocabulary. */
sealed interface PaymentStatusReading {

    /** Not settled yet. Keep polling. */
    data object Confirming : PaymentStatusReading

    /** The server has a verified capture. The only source of "paid". */
    data object Paid : PaymentStatusReading

    /**
     * The server says the payment failed.
     *
     * @param retryable whether the product lets the buyer try again on the
     *   same reference. Commerce and Feast both do; a product that does not
     *   says so here, and its UI stops offering the retry.
     */
    data class Failed(
        val reason: String? = null,
        val retryable: Boolean = true,
    ) : PaymentStatusReading

    /** Money moved and is being returned. Never a successful order. */
    data object RefundPending : PaymentStatusReading

    /** Money moved and has been returned. Never a successful order. */
    data object Refunded : PaymentStatusReading

    /**
     * The read could not be made. NOT a failure: the coordinator keeps
     * confirming, because the capture may well have completed.
     */
    data class Unreachable(val cause: String? = null) : PaymentStatusReading
}
