package com.us.android.feature.mopedu.rider.history

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.mobility.model.OutstandingCharge
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentPollPolicy
import com.us.android.core.payments.PaymentStateStore
import com.us.android.feature.mopedu.rider.data.MopeduResult
import com.us.android.feature.mopedu.rider.data.MopeduRiderRepository
import com.us.android.feature.mopedu.rider.data.userMessage
import com.us.android.feature.mopedu.rider.payment.MOPEDU_PAYMENT_APPLICATION_ID
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentRequest
import com.us.android.feature.mopedu.rider.payment.OutstandingPaymentStatusSource
import com.us.android.feature.mopedu.rider.payment.toPaymentSession
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

/** Paying one outstanding cancellation fee: the same server-confirmed flow as the fare. */
sealed interface OutstandingPayment {
    data object Idle : OutstandingPayment

    data class CreatingIntent(val chargeId: String) : OutstandingPayment

    data class OpeningSheet(val chargeId: String, val request: MopeduPaymentRequest) : OutstandingPayment

    data class Confirming(val chargeId: String, val elapsedSeconds: Int) : OutstandingPayment

    data class Paid(val chargeId: String) : OutstandingPayment

    data class Failed(val chargeId: String, val reason: String?) : OutstandingPayment

    data class StillConfirming(val chargeId: String) : OutstandingPayment
}

data class RideHistoryState(
    val loading: Boolean = true,
    val rides: List<RideBooking> = emptyList(),
    val outstanding: List<OutstandingCharge> = emptyList(),
    val showOutstanding: Boolean = false,
    val payment: OutstandingPayment = OutstandingPayment.Idle,
    val error: String? = null,
) {
    val outstandingTotalPaise: Long get() = outstanding.filter { it.isPending }.sumOf { it.amount.paise }
}

/**
 * Past rides, and the "Outstanding" sheet: cancellation fees not yet paid,
 * each payable through `:core:payments` stamped "mopedu" with the charge id as
 * the reference. Paid is read back from the outstanding list, never from the sheet.
 */
@HiltViewModel
class RideHistoryViewModel @Inject constructor(
    private val repository: MopeduRiderRepository,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val inFlight = InFlightPayment(
        store = object : PaymentStateStore {
            override fun get(key: String): String? = savedState[key]
            override fun set(key: String, value: String?) {
                savedState[key] = value
            }
        },
        applicationId = MOPEDU_PAYMENT_APPLICATION_ID,
    )
    private val statusSource = OutstandingPaymentStatusSource(repository)
    private val policy = PaymentPollPolicy()

    private val _state = MutableStateFlow(RideHistoryState())
    val state: StateFlow<RideHistoryState> = _state.asStateFlow()

    private var confirmJob: Job? = null

    init {
        observeHandoff()
        load()
        inFlight.attempt?.let { confirm(it.referenceId) }
    }

    fun load() {
        _state.update { it.copy(loading = true, error = null) }
        viewModelScope.launch {
            val rides = repository.rideHistory()
            val outstanding = repository.outstanding()
            _state.update { current ->
                current.copy(
                    loading = false,
                    rides = (rides as? MopeduResult.Success)?.value ?: current.rides,
                    outstanding = (outstanding as? MopeduResult.Success)?.value ?: current.outstanding,
                    error = listOfNotNull(
                        (rides as? MopeduResult.Failure)?.error?.userMessage(),
                        (outstanding as? MopeduResult.Failure)?.error?.userMessage(),
                    ).firstOrNull(),
                )
            }
        }
    }

    fun openOutstanding() = _state.update { it.copy(showOutstanding = true) }

    fun closeOutstanding() = _state.update { it.copy(showOutstanding = false) }

    fun dismissError() = _state.update { it.copy(error = null) }

    /** Creates the intent for [chargeId] (UPI; the sheet offers the buyer's instruments) and asks `:app` to open it. */
    fun pay(chargeId: String, method: PaymentMethod = PaymentMethod.UPI) {
        if (_state.value.payment !is OutstandingPayment.Idle && _state.value.payment !is OutstandingPayment.Failed) return
        val charge = _state.value.outstanding.firstOrNull { it.id == chargeId } ?: return
        _state.update { it.copy(payment = OutstandingPayment.CreatingIntent(chargeId)) }
        viewModelScope.launch {
            when (val intent = repository.outstandingPaymentIntent(chargeId, method)) {
                is MopeduResult.Failure -> _state.update { it.copy(payment = OutstandingPayment.Failed(chargeId, intent.error.userMessage())) }
                is MopeduResult.Success -> {
                    val session = intent.value.toPaymentSession(description = charge.reason)
                    if (session == null) {
                        _state.update { it.copy(payment = OutstandingPayment.Failed(chargeId, "Online payment isn't available right now.")) }
                        return@launch
                    }
                    val attempt = PaymentAttempt(MOPEDU_PAYMENT_APPLICATION_ID, referenceId = chargeId, id = repository.newIdempotencyKey())
                    inFlight.attempt = attempt
                    _state.update { it.copy(payment = OutstandingPayment.OpeningSheet(chargeId, MopeduPaymentRequest(attempt, session))) }
                }
            }
        }
    }

    fun done() {
        confirmJob?.cancel()
        inFlight.clear()
        _state.update { it.copy(payment = OutstandingPayment.Idle) }
        load()
    }

    private fun observeHandoff() {
        viewModelScope.launch {
            handoff.events(MOPEDU_PAYMENT_APPLICATION_ID).collect { event ->
                val mine = inFlight.attempt
                if (mine == null || event.attempt != mine) return@collect
                if (handoff.isConsumed(mine)) return@collect
                handoff.consume(mine)
                when (event) {
                    is PaymentHandoffEvent.SheetClosed -> confirm(mine.referenceId)
                    is PaymentHandoffEvent.Unavailable -> {
                        inFlight.clear()
                        _state.update { it.copy(payment = OutstandingPayment.Failed(mine.referenceId, event.reason)) }
                    }
                }
            }
        }
    }

    private fun confirm(chargeId: String) {
        confirmJob?.cancel()
        _state.update { it.copy(payment = OutstandingPayment.Confirming(chargeId, 0)) }
        confirmJob = viewModelScope.launch {
            payments.confirm(MOPEDU_PAYMENT_APPLICATION_ID, chargeId, statusSource, policy).collect { confirmation ->
                val phase = when (confirmation) {
                    is PaymentConfirmation.Confirming -> OutstandingPayment.Confirming(chargeId, confirmation.elapsedSeconds)
                    PaymentConfirmation.Paid -> {
                        inFlight.clear()
                        OutstandingPayment.Paid(chargeId)
                    }
                    is PaymentConfirmation.Failed -> {
                        inFlight.clear()
                        OutstandingPayment.Failed(chargeId, confirmation.reason)
                    }
                    PaymentConfirmation.RefundPending, PaymentConfirmation.Refunded -> {
                        inFlight.clear()
                        OutstandingPayment.Failed(chargeId, "This payment is being refunded.")
                    }
                    is PaymentConfirmation.TimedOut -> OutstandingPayment.StillConfirming(chargeId)
                }
                _state.update { it.copy(payment = phase) }
                if (phase is OutstandingPayment.Paid) load()
            }
        }
    }

    internal fun activeAttempt(): PaymentAttempt? = inFlight.attempt
}
