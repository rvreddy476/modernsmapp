package com.us.android.feature.premium.payment

import com.us.android.core.common.result.AppResult
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentPollPolicy
import com.us.android.core.payments.PaymentStateStore
import com.us.android.feature.premium.data.PremiumCheckout
import com.us.android.feature.premium.data.PremiumPass
import com.us.android.feature.premium.data.PremiumReceipt
import com.us.android.feature.premium.data.PremiumRepository
import com.us.android.feature.premium.data.PremiumWords
import com.us.android.feature.premium.data.amountsDisagree
import com.us.android.feature.premium.data.isPremiumOff
import com.us.android.feature.premium.data.planLength
import com.us.android.feature.premium.data.premiumErrorMessage
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.util.UUID

/** Where one Premium purchase stands, as the screen draws it. */
sealed interface PurchaseState {
    /** Nothing in flight. */
    data object Idle : PurchaseState

    /** `POST /checkout` is on its way for [plan]. */
    data class Starting(val plan: String) : PurchaseState

    /** The server's checkout is open: Price, GST and Total are shown; nothing is paid yet. */
    data class Review(val plan: String, val checkout: PremiumCheckout) : PurchaseState

    /** Pay was pressed; the Activity is to open the sheet for [request]. */
    data class OpeningPayment(val request: PremiumPaymentRequest) : PurchaseState

    /** The sheet is open; its ending will arrive on the handoff bus. */
    data object InSheet : PurchaseState

    /** The sheet ended; the SERVER has not said paid. */
    data class Confirming(val elapsedSeconds: Int) : PurchaseState

    /**
     * The server says the pass is paid — the ONLY way Premium is shown as
     * bought. [invoiceId] arrives with the pass or a few seconds later;
     * [invoiceGaveUp] once the short wait for it is over.
     */
    data class Paid(
        val endsAt: String?,
        val receipt: PremiumReceipt?,
        val invoiceId: String?,
        val invoiceGaveUp: Boolean = false,
    ) : PurchaseState

    /** The poll gave up without an answer. NOT a failure: the payment may still land. */
    data object StillConfirming : PurchaseState

    /** It didn't work, in words; [plan] is what Try again buys. */
    data class Failed(val message: String, val plan: String?) : PurchaseState

    /** `PREMIUM_NOT_ENABLED` at checkout: the server's switch went off under us. */
    data object Unavailable : PurchaseState
}

/** Busy: the screen must not take another Buy press. */
val PurchaseState.busy: Boolean
    get() = this is PurchaseState.Starting || this is PurchaseState.Review ||
        this is PurchaseState.OpeningPayment || this is PurchaseState.InSheet || this is PurchaseState.Confirming

/**
 * About two minutes, then "still confirming". The waits are `:core:payments`'
 * (1, 2, 3, 4 s, then every 5 s), and the last one lands exactly on 120 s.
 */
val PREMIUM_POLL_POLICY = PaymentPollPolicy(timeoutSeconds = 120)

/**
 * One Premium purchase on `:core:payments`, application "premium":
 *
 *  1. Buy sends `POST /checkout {"plan": code}` — never an amount; the server
 *     prices the pass and answers its GST split and total.
 *  2. The confirm step shows the server's Price, GST and Total; Pay opens the
 *     sheet with the server's `client_session` and its total
 *     ([toPaymentSession]). A checkout whose amount is not its GST total is
 *     never opened.
 *  3. A sheet ending is evidence, never proof: EVERY ending polls
 *     `GET /passes/:id` through [PremiumPassStatusSource], and only its `paid`
 *     reaches [PurchaseState.Paid]. A failed read keeps confirming; a refund
 *     outranks paid. The app never confirms a payment.
 *  4. An ending is applied only if it is THIS flow's attempt, from Premium's
 *     own stream, and only once.
 *  5. After process death the server is asked ([resume]); a sheet is never
 *     reopened by itself.
 */
// One state machine; splitting it would scatter the money rules across files.
@Suppress("TooManyFunctions", "LongParameterList")
class PremiumPurchaseFlow(
    private val scope: CoroutineScope,
    store: PaymentStateStore,
    private val repository: PremiumRepository,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    private val policy: PaymentPollPolicy = PREMIUM_POLL_POLICY,
    private val newAttemptId: () -> String = { UUID.randomUUID().toString() },
    private val invoiceWaitMillis: Long = INVOICE_WAIT_MILLIS,
    private val invoiceWaitReads: Int = INVOICE_WAIT_READS,
) {
    private val saved = Saved(store)
    private val statusSource = PremiumPassStatusSource(repository)
    private val _state = MutableStateFlow<PurchaseState>(PurchaseState.Idle)
    val state: StateFlow<PurchaseState> = _state.asStateFlow()

    private var confirmJob: Job? = null
    private var invoiceJob: Job? = null

    init {
        scope.launch {
            handoff.events(PREMIUM_PAYMENT_APPLICATION_ID).collect { event -> onHandoff(event) }
        }
    }

    /** The attempt the sheet was requested for, while one is. */
    val activeAttempt: PaymentAttempt? get() = saved.inFlight.attempt

    /** The pass being paid for, once a checkout exists. */
    val passId: String? get() = saved.passId

    /** Buy [planCode]: opens the server's checkout for it. Ignored while busy. */
    fun buy(planCode: String) {
        if (_state.value.busy || planCode.isBlank()) return
        stopJobs()
        saved.clear()
        _state.value = PurchaseState.Starting(planCode)
        scope.launch { start(planCode) }
    }

    private suspend fun start(planCode: String) {
        when (val result = repository.checkout(planCode)) {
            is AppResult.Failure -> _state.value = if (result.error.isPremiumOff()) {
                PurchaseState.Unavailable
            } else {
                PurchaseState.Failed(premiumErrorMessage(result.error), planCode)
            }
            is AppResult.Success -> adopt(planCode, result.data)
        }
    }

    private fun adopt(planCode: String, checkout: PremiumCheckout) {
        _state.value = when {
            // The charge must be the GST total the buyer is shown; never open a sheet otherwise.
            checkout.amountsDisagree() -> PurchaseState.Failed(PremiumWords.AMOUNT_MISMATCH, planCode)
            checkout.toPaymentSession(label(checkout)) == null ->
                PurchaseState.Failed(PremiumWords.NO_ONLINE_PAYMENT, planCode)
            else -> PurchaseState.Review(planCode, checkout)
        }
    }

    /** Pay on the confirm step: the sheet opens with the server's session and total. */
    fun pay() {
        val review = _state.value as? PurchaseState.Review ?: return
        val checkout = review.checkout
        val session = checkout.toPaymentSession(label(checkout)) ?: run {
            _state.value = PurchaseState.Failed(PremiumWords.NO_ONLINE_PAYMENT, review.plan)
            return
        }
        val attempt = PaymentAttempt(
            applicationId = PREMIUM_PAYMENT_APPLICATION_ID,
            referenceId = checkout.passId,
            id = newAttemptId(),
        )
        saved.passId = checkout.passId
        saved.plan = review.plan
        saved.inFlight.attempt = attempt
        saved.phase = Phase.SHEET_REQUESTED
        _state.value = PurchaseState.OpeningPayment(PremiumPaymentRequest(attempt, session))
    }

    /** The screen handed the request to the Activity: do not open it again (a rotation re-runs effects). */
    fun onSheetOpened(request: PremiumPaymentRequest) {
        val current = _state.value as? PurchaseState.OpeningPayment ?: return
        if (current.request.attempt == request.attempt) _state.value = PurchaseState.InSheet
    }

    /** After "still confirming": ask the server again, for the same pass. */
    fun checkAgain() {
        if (_state.value != PurchaseState.StillConfirming) return
        saved.passId?.let(::confirm)
    }

    /** Try again after a failure: a new checkout for the same plan. */
    fun retry() {
        val failed = _state.value as? PurchaseState.Failed ?: return
        val plan = failed.plan ?: return
        _state.value = PurchaseState.Idle
        buy(plan)
    }

    /**
     * Leaves the purchase: Cancel on the confirm step (nothing was paid), Done
     * or Close after an outcome, or Close while confirming (the server keeps
     * the pass; the status card reads it).
     */
    fun reset() {
        stopJobs()
        saved.clear()
        _state.value = PurchaseState.Idle
    }

    /** After a recreation: if a sheet had been requested, ask the server; never reopen the sheet. */
    fun resume() {
        if (_state.value != PurchaseState.Idle) return
        when (saved.phase) {
            Phase.FRESH -> Unit
            Phase.SHEET_REQUESTED, Phase.CONFIRMING -> saved.passId?.let(::confirm)
        }
    }

    private fun onHandoff(event: PaymentHandoffEvent) {
        val mine = saved.inFlight.attempt
        // Not ours: another screen's, or an earlier attempt.
        if (mine == null || event.attempt != mine) return
        // Already acted on: a replay after a rotation must not restart anything.
        if (handoff.isConsumed(mine)) return
        handoff.consume(mine)
        when (event) {
            // However the sheet ended, including the SDK's "success": ask the server.
            is PaymentHandoffEvent.SheetClosed -> confirm(mine.referenceId)
            is PaymentHandoffEvent.Unavailable -> {
                val plan = saved.plan
                saved.clear()
                _state.value = PurchaseState.Failed(event.reason.ifBlank { SHEET_UNAVAILABLE }, plan)
            }
        }
    }

    private fun confirm(passId: String) {
        stopJobs()
        saved.phase = Phase.CONFIRMING
        _state.value = PurchaseState.Confirming(elapsedSeconds = 0)
        confirmJob = scope.launch {
            payments.confirm(PREMIUM_PAYMENT_APPLICATION_ID, passId, statusSource, policy).collect { c ->
                when (c) {
                    is PaymentConfirmation.Confirming -> _state.value = PurchaseState.Confirming(c.elapsedSeconds)
                    PaymentConfirmation.Paid -> paid(passId, statusSource.lastPass)
                    is PaymentConfirmation.Failed -> ended(PremiumWords.FAILED)
                    PaymentConfirmation.RefundPending, PaymentConfirmation.Refunded -> ended(PremiumWords.REFUNDED)
                    is PaymentConfirmation.TimedOut -> {
                        // The pass id stays saved: Check again asks about the same pass.
                        saved.phase = Phase.FRESH
                        saved.inFlight.clear()
                        _state.value = PurchaseState.StillConfirming
                    }
                }
            }
        }
    }

    private fun ended(message: String) {
        val plan = saved.plan
        saved.clear()
        _state.value = PurchaseState.Failed(message, plan)
    }

    private fun paid(passId: String, pass: PremiumPass?) {
        saved.clear()
        _state.value = PurchaseState.Paid(endsAt = pass?.endsAt, receipt = pass?.receipt, invoiceId = pass?.invoiceId)
        if (pass?.invoiceId == null) awaitInvoice(passId)
    }

    /** The invoice is issued on the signed event, a moment after paid: read the pass every 2 s, eight times. */
    private fun awaitInvoice(passId: String) {
        invoiceJob = scope.launch {
            repeat(invoiceWaitReads) {
                delay(invoiceWaitMillis)
                val pass = (repository.pass(passId) as? AppResult.Success)?.data
                val invoiceId = pass?.invoiceId
                if (invoiceId != null) {
                    _state.update { s -> if (s is PurchaseState.Paid) s.copy(invoiceId = invoiceId) else s }
                    return@launch
                }
            }
            _state.update { s -> if (s is PurchaseState.Paid) s.copy(invoiceGaveUp = true) else s }
        }
    }

    private fun stopJobs() {
        confirmJob?.cancel()
        invoiceJob?.cancel()
    }

    private fun label(checkout: PremiumCheckout): String =
        if (checkout.durationDays > 0) "Momentum Premium, ${planLength(checkout.durationDays)}" else "Momentum Premium"

    internal enum class Phase { FRESH, SHEET_REQUESTED, CONFIRMING }

    /** What survives process death: identities and how far the buyer got. Never a provider session. */
    private class Saved(private val store: PaymentStateStore) {
        var phase: Phase
            get() = store[KEY_PHASE]?.let { runCatching { Phase.valueOf(it) }.getOrNull() } ?: Phase.FRESH
            set(v) {
                store[KEY_PHASE] = v.name
            }
        var passId: String?
            get() = store[KEY_PASS]
            set(v) {
                store[KEY_PASS] = v
            }
        var plan: String?
            get() = store[KEY_PLAN]
            set(v) {
                store[KEY_PLAN] = v
            }
        val inFlight = InFlightPayment(store, PREMIUM_PAYMENT_APPLICATION_ID)

        fun clear() {
            phase = Phase.FRESH
            passId = null
            plan = null
            inFlight.clear()
        }
    }

    companion object {
        const val INVOICE_WAIT_MILLIS = 2_000L
        const val INVOICE_WAIT_READS = 8
        private const val KEY_PHASE = "premium.purchase.phase"
        private const val KEY_PASS = "premium.purchase.passId"
        private const val KEY_PLAN = "premium.purchase.plan"
        private const val SHEET_UNAVAILABLE = "The payment sheet couldn't open. Nothing was charged."
    }
}
