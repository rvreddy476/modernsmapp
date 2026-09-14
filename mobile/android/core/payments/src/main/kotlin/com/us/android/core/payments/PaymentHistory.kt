package com.us.android.core.payments

/**
 * A future read of payment and refund history — INTERFACE ONLY.
 *
 * No product implements this yet and no endpoint exists. It is declared now so
 * the shape is settled before anyone builds a history screen: history is always
 * read for ONE application. There is deliberately no "all applications" query;
 * MStore's orders screen must never list a Feast payment, and a super-app wide
 * view, if the founder ever wants one, is a separate decision rather than a
 * missing filter.
 *
 * When the backend supports it, [PaymentHistoryQuery.applicationId] is the
 * required filter on the request (see [PaymentApplication]).
 */
interface PaymentHistorySource {

    /** The one application whose history this source reads. */
    val applicationId: String

    /**
     * One page of [PaymentHistoryQuery.applicationId]'s history, newest first.
     * An implementation must refuse a query for any application but its own.
     */
    suspend fun history(query: PaymentHistoryQuery): PaymentHistoryPage
}

/** A history request. [applicationId] is required and validated. */
data class PaymentHistoryQuery(
    val applicationId: String,
    val cursor: String? = null,
    val limit: Int = DEFAULT_LIMIT,
) {
    init {
        PaymentApplication.requireValid(applicationId)
        require(limit in 1..MAX_LIMIT) { "limit must be in 1..$MAX_LIMIT" }
    }

    companion object {
        const val DEFAULT_LIMIT = 20
        const val MAX_LIMIT = 100
    }
}

/** One page of one application's history. */
data class PaymentHistoryPage(
    val applicationId: String,
    val entries: List<PaymentHistoryEntry>,
    val nextCursor: String?,
)

/** A payment or a refund, always belonging to one application. */
data class PaymentHistoryEntry(
    val applicationId: String,
    val referenceId: String,
    val kind: Kind,
    val status: PaymentStatusReading,
    val amountMinor: Long,
    val currency: String,
    val occurredAtEpochMillis: Long,
) {
    init {
        PaymentApplication.requireValid(applicationId)
    }

    enum class Kind { PAYMENT, REFUND }
}
