package com.us.android.core.memberships.payment

import android.app.Activity
import com.us.android.core.common.result.AppResult
import com.us.android.core.memberships.data.MembershipCheckout
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.PaymentStatusDto
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentSession
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.core.payments.PaymentStatusSource
import com.us.android.core.payments.toHandoffEvent
import javax.inject.Inject
import javax.inject.Singleton

/**
 * Memberships' application id in `:core:payments` — the ONE place it is
 * named. It matches the payments-service application `memberships`
 * (contract, 23 Oct 2026). Every attempt, session, confirmation and in-flight
 * record is stamped with it, so a pending membership payment can never be
 * resumed or shown in MStore, Feast or Dating, and theirs never reach here.
 */
const val MEMBERSHIPS_PAYMENT_APPLICATION_ID = "memberships"

/**
 * What a membership screen hands the Activity to open the sheet: the attempt
 * (always memberships') and the session built from the server's
 * `client_session`. In memory only; the saved state holds identities alone.
 */
data class MembershipPaymentRequest(
    val attempt: PaymentAttempt,
    val session: PaymentSession,
) {
    init {
        require(attempt.applicationId == MEMBERSHIPS_PAYMENT_APPLICATION_ID) {
            "a membership payment request cannot carry a ${attempt.applicationId} attempt"
        }
        require(session.applicationId == MEMBERSHIPS_PAYMENT_APPLICATION_ID) {
            "a membership payment request cannot carry a ${session.applicationId} session"
        }
    }

    /** The membership payment row's id — what the status poll is keyed by. */
    val paymentId: String get() = attempt.referenceId
}

/**
 * `GET /v1/monetization/memberships/payments/:paymentId`, read for the
 * coordinator. A read that could not be made is [PaymentStatusReading.Unreachable]
 * — never Failed. A refund outranks paid. An unknown status keeps confirming.
 */
class MembershipPaymentStatusSource(
    private val repository: MembershipsRepository,
) : PaymentStatusSource {

    override val applicationId: String = MEMBERSHIPS_PAYMENT_APPLICATION_ID

    override suspend fun status(referenceId: String): PaymentStatusReading =
        when (val result = repository.paymentStatus(referenceId)) {
            is AppResult.Failure -> PaymentStatusReading.Unreachable(result.error.toString())
            is AppResult.Success -> result.data.toReading()
        }
}

/**
 * The server's membership payment status in the coordinator's vocabulary
 * (service/membership_payments.go `paymentView`):
 *
 *  - `refund_status` `refunded` is [PaymentStatusReading.Refunded]; any other
 *    refund state (`pending`, or `failed` — support is on it, the money is
 *    not settled) is [PaymentStatusReading.RefundPending]: never "paid";
 *  - `paid` is [PaymentStatusReading.Paid] — the signed event was applied;
 *  - `failed` is [PaymentStatusReading.Failed], retryable: the SAME join key
 *    reopens a failed payment (store/postgres/membership_payments.go replay);
 *  - anything else (`confirming`, or newer) keeps confirming.
 */
fun PaymentStatusDto.toReading(): PaymentStatusReading {
    val refund = refundStatus?.trim()?.lowercase().orEmpty()
    return when {
        refund == REFUNDED -> PaymentStatusReading.Refunded
        refund.isNotEmpty() -> PaymentStatusReading.RefundPending
        status.trim().lowercase() == PAID -> PaymentStatusReading.Paid
        status.trim().lowercase() == FAILED -> PaymentStatusReading.Failed(retryable = true)
        else -> PaymentStatusReading.Confirming
    }
}

private const val REFUNDED = "refunded"
private const val PAID = "paid"
private const val FAILED = "failed"

/**
 * The sheet session from a checkout, stamped memberships'; null when the
 * server attached no `client_session`. The amount and currency are the
 * server's, priced from the level — the client never chooses one.
 */
fun MembershipCheckout.toPaymentSession(description: String): PaymentSession? {
    if (clientSession.isEmpty()) return null
    return PaymentSession.fromClientSession(
        applicationId = MEMBERSHIPS_PAYMENT_APPLICATION_ID,
        clientSession = clientSession,
        amountMinor = amountPaise,
        currency = currency,
        description = description,
    )
}

/**
 * Opens the sheet for a membership payment onto the Activity (the provider
 * SDK opens onto an Activity and calls back on it — `MainActivity`, which is
 * the Activity hosting every Compose screen), then publishes exactly one
 * ending onto memberships' stream of [PaymentHandoff]. The checkout (and so
 * the server session) already exists.
 */
@Singleton
class MembershipPaymentOpener @Inject constructor(
    private val payments: PaymentCoordinator,
    private val handoff: PaymentHandoff,
) {

    fun start(activity: Activity, request: MembershipPaymentRequest) {
        payments.launch(activity, request.attempt, request.session) { sheet ->
            handoff.publish(sheet.toHandoffEvent())
        }
    }

    /** Releases the launcher's one in-flight slot when the screen that asked goes away. */
    fun abandon(request: MembershipPaymentRequest) {
        payments.abandon(request.attempt)
    }
}
