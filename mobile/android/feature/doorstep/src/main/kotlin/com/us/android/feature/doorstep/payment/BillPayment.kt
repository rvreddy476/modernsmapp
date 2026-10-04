package com.us.android.feature.doorstep.payment

import androidx.lifecycle.SavedStateHandle
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentPollPolicy
import com.us.android.core.payments.PaymentStateStore
import com.us.android.feature.doorstep.data.DoorstepCodes
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.code
import com.us.android.feature.doorstep.data.userMessage
import kotlinx.coroutines.CoroutineScope
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch

/** Where paying one extras bill stands. Only the server produces [Paid]. */
sealed interface BillPayState {
    data object Idle : BillPayState

    data object Starting : BillPayState

    data class Opening(val request: DoorstepPaymentRequest) : BillPayState

    data class Confirming(val billId: String) : BillPayState

    data class Paid(val billId: String) : BillPayState

    data class Failed(val billId: String, val reason: String?) : BillPayState

    data class Refunding(val billId: String) : BillPayState

    data class StillConfirming(val billId: String) : BillPayState
}

/**
 * Paying an extras bill — from the booking (extras above the charge-now
 * threshold, or the end-of-visit bill) or from the dues screen (a bill that
 * became outstanding). One class so both screens keep the same rules as
 * checkout: the server prices the intent (`doorstep_extras`, reference = the
 * bill id), every sheet ending is confirmed against
 * `GET /bookings/{booking}/payment`, only `succeeded` is paid, an ending is
 * applied only to THIS attempt, and the attempt survives process death in
 * saved state (never a provider session).
 */
class BillPayment(
    private val scope: CoroutineScope,
    private val repository: DoorstepRepository,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    handle: SavedStateHandle,
    private val policy: PaymentPollPolicy = PaymentPollPolicy(),
    private val onSettled: (billId: String) -> Unit = {},
) {

    private val inFlight = InFlightPayment(
        store = object : PaymentStateStore {
            override fun get(key: String): String? = handle[KEY_PREFIX + key]
            override fun set(key: String, value: String?) {
                handle[KEY_PREFIX + key] = value
            }
        },
        applicationId = DOORSTEP_PAYMENT_APPLICATION_ID,
    )

    /** bill id → booking id, for the bill being paid: the status rows are read per booking. */
    private val billBooking = object {
        var bookingId: String?
            get() = handle[KEY_BOOKING]
            set(v) {
                handle[KEY_BOOKING] = v
            }
    }

    private val _state = MutableStateFlow<BillPayState>(BillPayState.Idle)
    val state: StateFlow<BillPayState> = _state.asStateFlow()

    private var confirmJob: Job? = null

    init {
        scope.launch {
            handoff.events(DOORSTEP_PAYMENT_APPLICATION_ID).collect { event ->
                val mine = inFlight.attempt
                if (mine == null || event.attempt != mine) return@collect
                if (handoff.isConsumed(mine)) return@collect
                handoff.consume(mine)
                when (event) {
                    is PaymentHandoffEvent.SheetClosed -> confirm(mine.referenceId)
                    is PaymentHandoffEvent.Unavailable -> _state.value = BillPayState.Failed(mine.referenceId, event.reason)
                }
            }
        }
        // Process death with a sheet requested: ask the server, never reopen.
        inFlight.attempt?.let { confirm(it.referenceId) }
    }

    /** Opens payment for [billId] of [bookingId]. Ignored while another bill payment is in progress. */
    fun pay(bookingId: String, billId: String) {
        if (_state.value is BillPayState.Starting || _state.value is BillPayState.Opening || _state.value is BillPayState.Confirming) return
        _state.value = BillPayState.Starting
        billBooking.bookingId = bookingId
        scope.launch {
            when (val result = repository.extrasPaymentIntent(billId)) {
                // Already captured: the server only needs to be asked.
                is DoorstepResult.Failure -> if (result.error.code == DoorstepCodes.PAYMENT_ALREADY_SETTLED) {
                    confirm(billId)
                } else {
                    _state.value = BillPayState.Failed(billId, result.error.userMessage())
                }
                is DoorstepResult.Success -> {
                    // The dev stub-confirm route settles bookings only: a stub extras session has no
                    // sheet and no confirm path, so it is unavailable rather than a sheet that refuses.
                    val session = result.value.takeIf { it.checkout.provider != STUB_PROVIDER }
                        ?.toPaymentSession(description = "Doorstep extras")
                    if (session == null) {
                        _state.value = BillPayState.Failed(billId, "Online payment isn't available right now.")
                        return@launch
                    }
                    val attempt = PaymentAttempt(DOORSTEP_PAYMENT_APPLICATION_ID, billId, repository.newIdempotencyKey())
                    inFlight.attempt = attempt
                    _state.value = BillPayState.Opening(DoorstepPaymentRequest(attempt, session))
                }
            }
        }
    }

    fun reset() {
        if (_state.value is BillPayState.Failed || _state.value is BillPayState.Paid || _state.value is BillPayState.Refunding) {
            _state.value = BillPayState.Idle
        }
    }

    private fun confirm(billId: String) {
        val bookingId = billBooking.bookingId ?: run {
            _state.value = BillPayState.StillConfirming(billId)
            return
        }
        confirmJob?.cancel()
        _state.value = BillPayState.Confirming(billId)
        val source = DoorstepPaymentStatusSource(repository, bookingId, DoorstepReference.EXTRAS)
        confirmJob = scope.launch {
            payments.confirm(DOORSTEP_PAYMENT_APPLICATION_ID, billId, source, policy).collect { confirmation ->
                _state.value = when (confirmation) {
                    is PaymentConfirmation.Confirming -> BillPayState.Confirming(billId)
                    PaymentConfirmation.Paid -> {
                        inFlight.clear()
                        onSettled(billId)
                        BillPayState.Paid(billId)
                    }
                    is PaymentConfirmation.Failed -> BillPayState.Failed(billId, null)
                    PaymentConfirmation.RefundPending, PaymentConfirmation.Refunded -> BillPayState.Refunding(billId)
                    is PaymentConfirmation.TimedOut -> BillPayState.StillConfirming(billId)
                }
            }
        }
    }

    private companion object {
        const val KEY_PREFIX = "doorstep.bill."
        const val KEY_BOOKING = "doorstep.bill.bookingId"
    }
}
