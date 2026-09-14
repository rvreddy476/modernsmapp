package com.us.android.core.payments

/**
 * Which APPLICATION a payment belongs to.
 *
 * Founder requirement (2026-09-14): every payment, refund and transaction
 * belongs to one individual application — MStore and Feast inside the Momentum
 * super-app today — so history and status are scoped per application, never
 * the whole app. Application settings will later come from a server-side
 * registry; until then each product holds its key in ONE constant (commerce:
 * `mstore`, provisional until the founder confirms registry keys).
 *
 * An application id is a lowercase key: `[a-z][a-z0-9._-]*`, at most 64
 * characters. Every type in this module that names a payment carries one, and
 * the coordinator refuses to mix two.
 *
 * ## Client-side only, for now
 *
 * The backend does not store an application id yet, so this module sends it
 * NOWHERE — not to a product endpoint, and not to the provider (it is not in
 * Razorpay's `notes`). It scopes the client: the in-flight record, sheet
 * results, confirmation and history. When the backend supports it, it goes:
 *
 *  1. on intent creation — commerce `POST /v1/commerce/orders/:id/payment/intent`
 *     and Feast's equivalent — so payments-service stamps the intent, the
 *     transaction and every refund derived from it;
 *  2. on each status read — commerce `GET /v1/commerce/orders/:id/payment/status`,
 *     Feast `GET /v1/food/orders/:id/payment` — so the server can refuse a read
 *     for another application's payment;
 *  3. as the required filter of the history endpoint behind
 *     [PaymentHistorySource].
 */
object PaymentApplication {

    private val KEY = Regex("^[a-z][a-z0-9._-]{0,63}$")

    fun isValid(applicationId: String): Boolean = KEY.matches(applicationId)

    /** Returns [applicationId] if it is a valid key; throws otherwise. */
    fun requireValid(applicationId: String): String {
        require(isValid(applicationId)) {
            "applicationId must be a non-blank lowercase key ([a-z][a-z0-9._-]*, " +
                "at most 64 characters); got '$applicationId'"
        }
        return applicationId
    }
}

/**
 * One opening of a payment sheet, for one reference in one application.
 *
 * C3-LB-4. The reference alone (an order id, in every product so far) is not
 * enough: a buyer whose first attempt failed can retry the SAME order, and the
 * first attempt's late callback must not be applied to the second. [id] is
 * minted per opening by the product.
 *
 * [applicationId] is part of the identity too: MStore order `o-1` and Feast
 * order `o-1` are different payments, and an outcome for one can never match
 * the other.
 *
 * @param referenceId the product's own handle on what is being paid for —
 *   the id its status endpoint is keyed by (a commerce order id, a Feast
 *   order id).
 */
data class PaymentAttempt(
    val applicationId: String,
    val referenceId: String,
    val id: String,
) {
    init {
        PaymentApplication.requireValid(applicationId)
    }
}

/**
 * What the provider SDK needs to open its sheet.
 *
 * Every provider field is server-sourced. payments-service creates the
 * provider order and returns `client_session {provider, order_id, key_id}` —
 * public fields only — through each domain's intent endpoint; the amount and
 * currency are the server intent's own figures, relayed. The client never
 * CHOOSES an amount (LB-4): with `order_id` present, Razorpay prices the sheet
 * from the order.
 *
 * Provider fields may be empty. The launcher validates them before presenting
 * anything, so a malformed session becomes [PaymentOutcome.Unavailable] rather
 * than a sheet that cannot work. [applicationId] may not: it is validated here.
 *
 * There is no prefill: no product sends one today.
 */
data class PaymentSession(
    /** The application this payment belongs to. See [PaymentApplication]. */
    val applicationId: String,
    val provider: String,
    val providerOrderId: String,
    val keyId: String,
    val amountMinor: Long,
    val currency: String,
    /** The line under the merchant name on the sheet, e.g. "Order MS-1042". */
    val description: String,
) {
    init {
        PaymentApplication.requireValid(applicationId)
    }

    companion object {
        const val CLIENT_SESSION_PROVIDER = "provider"
        const val CLIENT_SESSION_ORDER_ID = "order_id"
        const val CLIENT_SESSION_KEY_ID = "key_id"

        /**
         * Builds a session from the server's `client_session` map plus the
         * intent's amount and currency.
         *
         * Missing keys become empty strings, which the launcher refuses before
         * presenting anything.
         */
        fun fromClientSession(
            applicationId: String,
            clientSession: Map<String, String>,
            amountMinor: Long,
            currency: String,
            description: String,
        ): PaymentSession = PaymentSession(
            applicationId = applicationId,
            provider = clientSession[CLIENT_SESSION_PROVIDER].orEmpty(),
            providerOrderId = clientSession[CLIENT_SESSION_ORDER_ID].orEmpty(),
            keyId = clientSession[CLIENT_SESSION_KEY_ID].orEmpty(),
            amountMinor = amountMinor,
            currency = currency,
            description = description,
        )
    }
}
