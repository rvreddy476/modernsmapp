package com.us.android.core.payments

import android.app.Activity
import android.util.Log
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.flow
import javax.inject.Inject
import javax.inject.Singleton
import kotlin.coroutines.cancellation.CancellationException

/**
 * The generic payment flow: open a sheet, then believe only the server.
 *
 * Everything here operates per (applicationId, referenceId). A sheet opens
 * only for a session and an attempt of the same application, and a
 * confirmation reads only through that application's [PaymentStatusSource].
 *
 * ## Two halves, on purpose
 *
 * The flow is [launch] followed by [confirm], and the two are separate calls
 * rather than one suspend function because they run in different places with
 * different lifetimes:
 *
 *  * [launch] needs the ACTIVITY (the provider SDK opens onto it and calls
 *    back on it), so it runs where the Activity is.
 *  * [confirm] belongs to the screen's state holder, whose inputs — the
 *    attempt and how far the buyer got — are persisted in its saved state
 *    ([InFlightPayment], keyed by application).
 *
 * A product joins the two with the attempt-scoped, consume-once, per-application
 * event bus in this module, [PaymentHandoff] (shared by MStore and Feast). That split is what survives process death
 * and app kill, exactly as Momentum commerce did before this module existed:
 * nothing here is in-memory state that has to outlive the process. After a
 * recreation the product reads its saved attempt and phase, asks its server,
 * and — if a sheet had been requested and the server still says pending —
 * calls [confirm] again. It never reopens a sheet by itself.
 *
 * ## The rule this class exists to keep
 *
 * A1/R-3: a payment is paid only when payments-service's signature-verified
 * webhook says so. Nothing the SDK reports is a payment fact, so:
 *
 *  * [launch] reports every ENDING of the sheet — success, failure,
 *    cancellation — as [PaymentSheetResult.Closed], and "paid" is not even
 *    representable there;
 *  * [confirm] emits [PaymentConfirmation.Paid] only when the
 *    [PaymentStatusSource] reads [PaymentStatusReading.Paid];
 *  * a status read that fails keeps [PaymentConfirmation.Confirming]. Only the
 *    server saying "failed" produces [PaymentConfirmation.Failed].
 */
@Singleton
class PaymentCoordinator @Inject constructor(
    private val launcher: PaymentLauncher,
) {

    /**
     * Opens the provider sheet for [attempt] and reports how it ended.
     *
     * [onResult] is called exactly once, on the main thread. A second opening
     * while one is in flight is refused by the launcher and reported as
     * [PaymentSheetResult.Unavailable] to the SECOND caller only.
     *
     * Retrying after a failure is a new [attempt] (a new id) for the same
     * reference, usually with the same server session: the launcher's slot is
     * free again once the first sheet ended.
     *
     * @throws IllegalArgumentException if [session] and [attempt] belong to
     *   different applications — a wiring mistake, caught before anything is
     *   presented, so nothing can have been charged.
     */
    fun launch(
        activity: Activity,
        attempt: PaymentAttempt,
        session: PaymentSession,
        onResult: (PaymentSheetResult) -> Unit,
    ) {
        require(session.applicationId == attempt.applicationId) {
            "a ${session.applicationId} session cannot be opened for a ${attempt.applicationId} attempt"
        }
        launcher.open(activity, attempt, session) { outcome ->
            // The production mapper, which PaymentOutcomeMappingTest calls.
            onResult(outcome.toSheetResult(attempt))
        }
    }

    /**
     * Releases the launcher's slot for [attempt] when the screen that opened
     * it has gone. A no-op for an attempt that does not hold the slot —
     * including the same order id in another application.
     */
    fun abandon(attempt: PaymentAttempt) {
        launcher.abandon(attempt)
    }

    /**
     * Polls [source] for [referenceId] in [applicationId] until the server
     * settles it or [policy] times out.
     *
     * Emits [PaymentConfirmation.Confirming] after every non-final read — and
     * after every read that could not be made — then exactly one final value:
     * [PaymentConfirmation.Paid], [PaymentConfirmation.Failed],
     * [PaymentConfirmation.RefundPending], [PaymentConfirmation.Refunded] or
     * [PaymentConfirmation.TimedOut]. Cold: nothing happens until collected,
     * and cancelling the collector stops the polling.
     *
     * The schedule is Momentum commerce's, unchanged: wait, then read; the
     * wait starts at 1s and grows by 1s per read up to 5s, because most
     * captures land in the first few seconds and a slow one should not be
     * hammered; stop reading at 180s.
     *
     * @throws IllegalArgumentException at the call, before any read, if
     *   [source] belongs to another application.
     */
    fun confirm(
        applicationId: String,
        referenceId: String,
        source: PaymentStatusSource,
        policy: PaymentPollPolicy = PaymentPollPolicy.Default,
    ): Flow<PaymentConfirmation> {
        PaymentApplication.requireValid(applicationId)
        require(source.applicationId == applicationId) {
            "a ${source.applicationId} status source cannot confirm a $applicationId payment"
        }
        return poll(referenceId, source, policy)
    }

    private fun poll(
        referenceId: String,
        source: PaymentStatusSource,
        policy: PaymentPollPolicy,
    ): Flow<PaymentConfirmation> = flow {
        var elapsed = 0
        var interval = policy.initialIntervalSeconds
        while (elapsed < policy.timeoutSeconds) {
            delay(interval * MILLIS_PER_SECOND)
            elapsed += interval
            interval = minOf(interval + policy.intervalStepSeconds, policy.maxIntervalSeconds)

            when (val reading = read(source, referenceId)) {
                PaymentStatusReading.Paid -> {
                    emit(PaymentConfirmation.Paid)
                    return@flow
                }

                is PaymentStatusReading.Failed -> {
                    emit(PaymentConfirmation.Failed(reading.reason, reading.retryable))
                    return@flow
                }

                PaymentStatusReading.RefundPending -> {
                    emit(PaymentConfirmation.RefundPending)
                    return@flow
                }

                PaymentStatusReading.Refunded -> {
                    emit(PaymentConfirmation.Refunded)
                    return@flow
                }

                // A transient failure to READ must not be reported as a
                // payment failure: we simply do not know yet.
                PaymentStatusReading.Confirming,
                is PaymentStatusReading.Unreachable,
                -> emit(PaymentConfirmation.Confirming(elapsed))
            }
        }
        // Still unconfirmed. The payment may yet land, so this is not a
        // failure: the product's copy sends the buyer to their orders.
        emit(PaymentConfirmation.TimedOut(policy.timeoutSeconds))
    }

    /**
     * One read. A thrown exception is an unreachable read, not a failed
     * payment; cancellation still propagates.
     */
    @Suppress("TooGenericExceptionCaught")
    private suspend fun read(source: PaymentStatusSource, referenceId: String): PaymentStatusReading =
        try {
            source.status(referenceId)
        } catch (e: CancellationException) {
            throw e
        } catch (e: Exception) {
            Log.w(TAG, "payment status read for ${source.applicationId}/$referenceId failed; still confirming", e)
            PaymentStatusReading.Unreachable(e.message)
        }

    private companion object {
        const val TAG = "PaymentCoordinator"
        const val MILLIS_PER_SECOND = 1000L
    }
}

/**
 * How long, and how often, [PaymentCoordinator.confirm] asks the server.
 *
 * The defaults are Momentum commerce's schedule as it shipped before this
 * module: 1s, 2s, 3s, 4s, then every 5s, for 180s in total.
 */
data class PaymentPollPolicy(
    val timeoutSeconds: Int = DEFAULT_TIMEOUT_SECONDS,
    val initialIntervalSeconds: Int = 1,
    val maxIntervalSeconds: Int = DEFAULT_MAX_INTERVAL_SECONDS,
    val intervalStepSeconds: Int = 1,
) {
    init {
        require(timeoutSeconds > 0) { "timeoutSeconds must be positive" }
        require(initialIntervalSeconds > 0) { "initialIntervalSeconds must be positive" }
        require(maxIntervalSeconds >= initialIntervalSeconds) { "maxIntervalSeconds < initialIntervalSeconds" }
        require(intervalStepSeconds >= 0) { "intervalStepSeconds must not be negative" }
    }

    companion object {
        const val DEFAULT_TIMEOUT_SECONDS = 180
        const val DEFAULT_MAX_INTERVAL_SECONDS = 5
        val Default = PaymentPollPolicy()
    }
}

/** What [PaymentCoordinator.confirm] reports. Only the server produces a final value. */
sealed interface PaymentConfirmation {

    /** Not settled yet, [elapsedSeconds] into the poll. */
    data class Confirming(val elapsedSeconds: Int) : PaymentConfirmation

    /** The server confirmed the capture. */
    data object Paid : PaymentConfirmation

    /** The server says the payment failed. [retryable] comes from the product. */
    data class Failed(val reason: String?, val retryable: Boolean) : PaymentConfirmation

    /** Money moved and is being returned. */
    data object RefundPending : PaymentConfirmation

    /** Money moved and has been returned. */
    data object Refunded : PaymentConfirmation

    /**
     * The poll gave up. NOT a failure: the payment may still land, and the
     * server will record it when it does.
     */
    data class TimedOut(val elapsedSeconds: Int) : PaymentConfirmation
}
