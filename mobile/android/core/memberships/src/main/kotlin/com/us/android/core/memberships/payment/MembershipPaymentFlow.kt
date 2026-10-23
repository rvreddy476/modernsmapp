package com.us.android.core.memberships.payment

import com.us.android.core.common.error.AppError
import com.us.android.core.common.result.AppResult
import com.us.android.core.memberships.data.MembershipCheckout
import com.us.android.core.memberships.data.MembershipsRepository
import com.us.android.core.memberships.data.isTransport
import com.us.android.core.memberships.data.membershipCode
import com.us.android.core.memberships.data.membershipRefusal
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentPollPolicy
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.payments.PaymentStatusReading
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Where one membership payment stands, as the screen draws it. */
sealed interface MembershipPayState {
    /** Nothing in flight. */
    data object Idle : MembershipPayState

    /** The join / renewal / change request is on its way to the server. */
    data object Submitting : MembershipPayState

    /** The checkout exists; the Activity is to open the sheet for [request]. */
    data class OpeningPayment(val request: MembershipPaymentRequest) : MembershipPayState

    /** The sheet is open; its ending will arrive on the handoff bus. */
    data object InSheet : MembershipPayState

    /** The sheet ended; the SERVER has not said paid. */
    data class Confirming(val elapsedSeconds: Int) : MembershipPayState

    /** The server applied the payment. The ONLY way access is shown as started. */
    data object Paid : MembershipPayState

    /** Nothing needed paying (a downgrade, or an upgrade with no difference): [message] says what happened. */
    data class Applied(val message: String) : MembershipPayState

    /** The server said the payment failed, or the sheet never opened. [retryable]: Pay again is offered. */
    data class Failed(val message: String, val retryable: Boolean) : MembershipPayState

    /** The server refused the request itself (with its [code]); nothing was charged. */
    data class Refused(val message: String, val code: String?) : MembershipPayState

    /** Money moved and is being returned. Never access. */
    data object Refunding : MembershipPayState

    /** The poll gave up without an answer. NOT a failure: the payment may still land. */
    data object StillConfirming : MembershipPayState
}

/**
 * One membership payment — a join, a renewal or a paid level change — on
 * `:core:payments`, exactly as Dating, Feast and MStore pay:
 *
 *  1. ONE idempotency key per decision ([pay]'s `decision`), saved BEFORE the
 *     request leaves and reused on every resend of that decision, so a lost
 *     response is never a second join. A different decision (another level)
 *     mints a new key; so does `CHECKOUT_EXPIRED`, which says to.
 *  2. The client never sends a price: the server prices the checkout.
 *  3. A sheet ending is evidence, never proof: every ending polls the payment
 *     status through [MembershipPaymentStatusSource], and only its `paid`
 *     reaches [MembershipPayState.Paid]. A failed read keeps confirming.
 *  4. An ending is applied only if it is THIS flow's attempt, from
 *     memberships' own stream, and only once.
 *  5. After process death the server is asked first ([resume]); a sheet is
 *     never reopened by itself.
 *
 * Not a ViewModel: the Join sheet and the membership detail screen each own
 * one, over their own saved state, with their own [prefix].
 */
class MembershipPaymentFlow(
    private val scope: CoroutineScope,
    store: PaymentStateStore,
    private val repository: MembershipsRepository,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    prefix: String,
    private val policy: PaymentPollPolicy = PaymentPollPolicy.Default,
) {
    private val saved = Saved(store, prefix)
    private val statusSource = MembershipPaymentStatusSource(repository)
    private val _state = MutableStateFlow<MembershipPayState>(MembershipPayState.Idle)
    val state: StateFlow<MembershipPayState> = _state.asStateFlow()

    private var confirmJob: Job? = null
    private var pending: (suspend (String) -> AppResult<MembershipCheckout?>)? = null
    private var description: String = ""

    init {
        scope.launch {
            handoff.events(MEMBERSHIPS_PAYMENT_APPLICATION_ID).collect { event -> onHandoff(event) }
        }
    }

    /** The idempotency key the current decision uses, or null. For tests and diagnostics. */
    val currentKey: String? get() = saved.key

    /** The attempt the sheet was requested for, while one is. */
    val activeAttempt: PaymentAttempt? get() = saved.inFlight.attempt

    /**
     * Starts (or resends) [decision]: [open] is called with the decision's
     * idempotency key and answers the checkout to pay, or null when nothing
     * needs paying. [label] is the line under the merchant name on the sheet.
     */
    fun pay(decision: String, label: String, open: suspend (idempotencyKey: String) -> AppResult<MembershipCheckout?>) {
        if (_state.value.isBusy()) return
        if (saved.decision != decision) {
            saved.clearDecision()
            saved.decision = decision
        }
        description = label
        pending = open
        saved.phase = Phase.SUBMITTING
        _state.value = MembershipPayState.Submitting
        scope.launch { submit(open) }
    }

    /**
     * After a failure the same decision again, under the SAME key: the server
     * reopens a failed join payment for it. After a timeout, the poll again.
     */
    fun retry() {
        when (_state.value) {
            is MembershipPayState.Failed, is MembershipPayState.Refused -> {
                val open = pending ?: return
                saved.phase = Phase.SUBMITTING
                _state.value = MembershipPayState.Submitting
                scope.launch { submit(open) }
            }
            MembershipPayState.StillConfirming -> saved.paymentId?.let(::confirm)
            else -> Unit
        }
    }

    /** The screen handed the request to the Activity: do not open it again (a rotation re-runs effects). */
    fun onSheetOpened(request: MembershipPaymentRequest) {
        val current = _state.value as? MembershipPayState.OpeningPayment ?: return
        if (current.request.attempt == request.attempt) _state.value = MembershipPayState.InSheet
    }

    /** Leaves a settled outcome: the next [pay] starts a new decision. */
    fun reset() {
        confirmJob?.cancel()
        saved.clearDecision()
        saved.phase = Phase.FRESH
        _state.value = MembershipPayState.Idle
    }

    /**
     * After a recreation: a request may have left without an answer (resend
     * it under its key — the caller supplies [open] again), or a sheet may
     * have been requested (ask the server; never reopen the sheet).
     */
    fun resume(open: (suspend (String) -> AppResult<MembershipCheckout?>)?, label: String) {
        description = label
        if (open != null) pending = open
        when (saved.phase) {
            Phase.FRESH -> Unit
            Phase.SUBMITTING -> {
                val retryOpen = open ?: return
                _state.value = MembershipPayState.Submitting
                scope.launch { submit(retryOpen) }
            }
            Phase.SHEET_REQUESTED, Phase.CONFIRMING -> saved.paymentId?.let { id ->
                scope.launch { resumeFromServer(id) }
            }
        }
    }

    /** True while a phase is saved that [resume] would act on. */
    val hasSomethingToResume: Boolean get() = saved.phase != Phase.FRESH

    private suspend fun submit(open: suspend (String) -> AppResult<MembershipCheckout?>) {
        val key = saved.key ?: repository.newIdempotencyKey().also { saved.key = it }
        when (val result = open(key)) {
            is AppResult.Success -> adopt(result.data)
            is AppResult.Failure -> refused(result.error, open)
        }
    }

    private suspend fun refused(error: AppError, open: suspend (String) -> AppResult<MembershipCheckout?>) {
        saved.phase = Phase.FRESH
        val code = error.membershipCode()
        when {
            // The request may have landed: keep the key, so a resend replays it.
            error.isTransport -> _state.value = MembershipPayState.Failed(membershipRefusal(error), retryable = true)
            // "start again with a new Idempotency-Key" — once, at once.
            code == CODE_CHECKOUT_EXPIRED && !saved.expiredOnce -> {
                saved.key = null
                saved.expiredOnce = true
                saved.phase = Phase.SUBMITTING
                submit(open)
            }
            else -> {
                // A definite refusal opened nothing: the next try is a new decision.
                saved.key = null
                _state.value = MembershipPayState.Refused(membershipRefusal(error), code)
            }
        }
    }

    private fun adopt(checkout: MembershipCheckout?) {
        if (checkout == null) {
            saved.phase = Phase.FRESH
            saved.clearDecision()
            _state.value = MembershipPayState.Applied("Done.")
            return
        }
        saved.paymentId = checkout.paymentId
        if (checkout.status != MembershipCheckout.STATUS_PENDING_PAYMENT) {
            // A replay of a join that is already paid answers the subscription's
            // status and no session: ask the payment, which says paid.
            confirm(checkout.paymentId)
            return
        }
        val session = checkout.toPaymentSession(description)
        if (session == null) {
            saved.phase = Phase.FRESH
            _state.value = MembershipPayState.Failed("Online payment isn't available right now.", retryable = true)
            return
        }
        val attempt = PaymentAttempt(
            applicationId = MEMBERSHIPS_PAYMENT_APPLICATION_ID,
            referenceId = checkout.paymentId,
            id = repository.newIdempotencyKey(),
        )
        saved.inFlight.attempt = attempt
        saved.phase = Phase.SHEET_REQUESTED
        _state.value = MembershipPayState.OpeningPayment(MembershipPaymentRequest(attempt, session))
    }

    private fun onHandoff(event: PaymentHandoffEvent) {
        val mine = saved.inFlight.attempt
        // Not ours: another screen's, or an earlier attempt at this payment.
        if (mine == null || event.attempt != mine) return
        // Already acted on: a replay after a rotation must not restart anything.
        if (handoff.isConsumed(mine)) return
        handoff.consume(mine)
        when (event) {
            // However the sheet ended, including the SDK's "success": ask the server.
            is PaymentHandoffEvent.SheetClosed -> confirm(mine.referenceId)
            is PaymentHandoffEvent.Unavailable -> {
                saved.phase = Phase.FRESH
                _state.value = MembershipPayState.Failed(event.reason.ifBlank { SHEET_UNAVAILABLE }, retryable = true)
            }
        }
    }

    private fun confirm(paymentId: String) {
        confirmJob?.cancel()
        saved.phase = Phase.CONFIRMING
        _state.value = MembershipPayState.Confirming(elapsedSeconds = 0)
        confirmJob = scope.launch {
            payments.confirm(MEMBERSHIPS_PAYMENT_APPLICATION_ID, paymentId, statusSource, policy).collect { c ->
                _state.value = when (c) {
                    is PaymentConfirmation.Confirming -> MembershipPayState.Confirming(c.elapsedSeconds)
                    PaymentConfirmation.Paid -> settled()
                    is PaymentConfirmation.Failed -> failed()
                    PaymentConfirmation.RefundPending, PaymentConfirmation.Refunded -> refunding()
                    is PaymentConfirmation.TimedOut -> MembershipPayState.StillConfirming
                }
            }
        }
    }

    private suspend fun resumeFromServer(paymentId: String) {
        when (val result = repository.paymentStatus(paymentId)) {
            is AppResult.Failure -> confirm(paymentId)
            is AppResult.Success -> when (result.data.toReading()) {
                PaymentStatusReading.Paid -> _state.value = settled()
                is PaymentStatusReading.Failed -> _state.value = failed()
                PaymentStatusReading.RefundPending, PaymentStatusReading.Refunded -> _state.value = refunding()
                PaymentStatusReading.Confirming, is PaymentStatusReading.Unreachable -> confirm(paymentId)
            }
        }
    }

    private fun settled(): MembershipPayState {
        saved.phase = Phase.FRESH
        saved.clearDecision()
        return MembershipPayState.Paid
    }

    /** Failed: the SAME key reopens the payment, so the decision (and its key) is kept for Pay again. */
    private fun failed(): MembershipPayState {
        saved.phase = Phase.FRESH
        saved.inFlight.clear()
        return MembershipPayState.Failed("Your payment didn't go through. Nothing was charged.", retryable = true)
    }

    private fun refunding(): MembershipPayState {
        saved.phase = Phase.FRESH
        saved.clearDecision()
        return MembershipPayState.Refunding
    }

    private fun MembershipPayState.isBusy(): Boolean =
        this is MembershipPayState.Submitting || this is MembershipPayState.OpeningPayment ||
            this is MembershipPayState.InSheet || this is MembershipPayState.Confirming

    internal enum class Phase { FRESH, SUBMITTING, SHEET_REQUESTED, CONFIRMING }

    /** What survives process death: identities and how far the payer got. Never a provider session. */
    private class Saved(private val store: PaymentStateStore, private val prefix: String) {
        var phase: Phase
            get() = store["$prefix.phase"]?.let { runCatching { Phase.valueOf(it) }.getOrNull() } ?: Phase.FRESH
            set(v) {
                store["$prefix.phase"] = v.name
            }
        var decision: String?
            get() = store["$prefix.decision"]
            set(v) {
                store["$prefix.decision"] = v
            }
        var key: String?
            get() = store["$prefix.key"]
            set(v) {
                store["$prefix.key"] = v
            }
        var paymentId: String?
            get() = store["$prefix.paymentId"]
            set(v) {
                store["$prefix.paymentId"] = v
            }
        var expiredOnce: Boolean
            get() = store["$prefix.expiredOnce"] == "true"
            set(v) {
                store["$prefix.expiredOnce"] = if (v) "true" else null
            }
        val inFlight = InFlightPayment(store, MEMBERSHIPS_PAYMENT_APPLICATION_ID)

        fun clearDecision() {
            decision = null
            key = null
            paymentId = null
            expiredOnce = false
            inFlight.clear()
        }
    }

    private companion object {
        const val CODE_CHECKOUT_EXPIRED = "CHECKOUT_EXPIRED"
        const val SHEET_UNAVAILABLE = "The payment sheet couldn't open. Nothing was charged."
    }
}
