package com.us.android.feature.doorstep.checkout

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentPollPolicy
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.feature.doorstep.data.AddressDto
import com.us.android.feature.doorstep.data.BookingCreateRequestDto
import com.us.android.feature.doorstep.data.DoorstepCodes
import com.us.android.feature.doorstep.data.DoorstepError
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.PaymentIntentDto
import com.us.android.feature.doorstep.data.QuoteDto
import com.us.android.feature.doorstep.data.code
import com.us.android.feature.doorstep.data.userMessage
import com.us.android.feature.doorstep.payment.CheckoutRoute
import com.us.android.feature.doorstep.payment.DOORSTEP_PAYMENT_APPLICATION_ID
import com.us.android.feature.doorstep.payment.DoorstepPaymentConfig
import com.us.android.feature.doorstep.payment.DoorstepPaymentRequest
import com.us.android.feature.doorstep.payment.DoorstepPaymentStatusSource
import com.us.android.feature.doorstep.payment.DoorstepReference
import com.us.android.feature.doorstep.payment.checkoutRoute
import com.us.android.feature.doorstep.ui.instantOrNull
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.Instant
import javax.inject.Inject

sealed interface CheckoutState {
    data object Loading : CheckoutState

    data class Ready(
        val quote: QuoteDto,
        val address: AddressDto,
        val slotStart: String,
        val requireFemalePro: Boolean,
        val notes: String = "",
        val placing: Boolean = false,
        /** The server's words when it refused the booking (slot taken, quote expired, …). */
        val refusal: String? = null,
        /** The refusal means the slot is gone: offer to pick another. */
        val pickAnotherSlot: Boolean = false,
    ) : CheckoutState

    /** The sheet is being opened from the Activity. */
    data class OpeningPayment(val request: DoorstepPaymentRequest, val holdExpiresAt: Instant?) : CheckoutState

    /** The sheet ended; the SERVER has not said paid. */
    data class Confirming(val bookingId: String, val elapsedSeconds: Int) : CheckoutState

    /** The server confirmed the capture (the signed event). The only way to get here. */
    data class Paid(val bookingId: String) : CheckoutState

    /** The server said failed, or the sheet never opened. The booking and its hold stay; pay again. */
    data class PaymentFailed(val bookingId: String, val reason: String?, val holdExpiresAt: Instant?) : CheckoutState

    /** Money moved after the hold lapsed and is being returned. Never a booked visit. */
    data class Refunding(val bookingId: String) : CheckoutState

    /** The poll gave up without an answer. NOT a failure: the payment may still land. */
    data class StillConfirming(val bookingId: String) : CheckoutState

    /** The 10-minute hold lapsed before payment: pick a slot again. */
    data object HoldExpired : CheckoutState

    /** Unpaid extras from an earlier visit (DOORSTEP_OUTSTANDING_DUE). */
    data object BlockedByDues : CheckoutState

    data class Failed(val message: String) : CheckoutState
}

/**
 * Doorstep checkout: book the slot, pay, and believe only the server.
 *
 *  1. ONE Idempotency-Key per booking decision, persisted in saved state
 *     BEFORE `POST /bookings` leaves and reused on a resend, so a lost
 *     response can never become a second booking (or a second hold).
 *  2. The amount is the server's: the quote's total and the intent's
 *     `amount_paise`. The client sends no amount anywhere.
 *  3. A sheet ending is evidence, never proof (A1/R-3): every ending moves to
 *     [CheckoutState.Confirming] and polls `GET /bookings/{id}/payment`
 *     through [PaymentCoordinator.confirm]; only the server's `succeeded`
 *     renders [CheckoutState.Paid], and a read that fails keeps confirming.
 *  4. An ending is applied only if it is for THIS checkout's active attempt,
 *     read from Doorstep's own stream of the shared bus.
 *  5. A failed payment is retried on the SAME booking with a NEW attempt
 *     (`POST /bookings/{id}/payment/intent`) while the hold lasts, and never
 *     re-booked.
 *  6. After process death the saved phase decides and the server is asked
 *     before anything is offered; a sheet is never reopened by itself.
 *  7. A `stub` checkout (a dev stack's test gateway) opens no sheet: in a DEV
 *     build it calls `POST /bookings/{id}/payment/stub-confirm` and then polls
 *     like rule 3; in any other build it is "payment unavailable". The stub
 *     route's answer is never read as paid.
 */
@HiltViewModel
class CheckoutViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val repository: DoorstepRepository,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    private val paymentConfig: DoorstepPaymentConfig,
) : ViewModel() {

    private val quoteId: String = checkNotNull(savedStateHandle.get<String>("quoteId")) { "navigation argument 'quoteId' is missing" }
    private val addressId: String = checkNotNull(savedStateHandle.get<String>("addressId")) { "navigation argument 'addressId' is missing" }
    private val slotStart: String = checkNotNull(savedStateHandle.get<String>("slotStart")) { "navigation argument 'slotStart' is missing" }
    private val requireFemalePro: Boolean = savedStateHandle.get<Boolean>("requireFemalePro") ?: false

    private val saved = CheckoutContinuation(savedStateHandle)
    private val policy = PaymentPollPolicy()

    private val _state = MutableStateFlow<CheckoutState>(CheckoutState.Loading)
    val state: StateFlow<CheckoutState> = _state.asStateFlow()

    private var confirmJob: Job? = null
    private var stubJob: Job? = null

    init {
        observeHandoff()
        viewModelScope.launch { start() }
    }

    fun onNotes(text: String) {
        _state.update { if (it is CheckoutState.Ready && !it.placing) it.copy(notes = text.take(MAX_NOTES)) else it }
    }

    fun book() {
        val ready = _state.value as? CheckoutState.Ready ?: return
        if (ready.placing) return
        saved.notes = ready.notes
        // Recorded BEFORE the request leaves: recovery must know a request may
        // have created a booking (and held a professional).
        saved.phase = CheckoutContinuation.Phase.SUBMITTING
        _state.value = ready.copy(placing = true, refusal = null, pickAnotherSlot = false)
        viewModelScope.launch { submit(ready) }
    }

    /** Pay again for the booking that already exists: a NEW attempt, the SAME booking. */
    fun retryPayment() {
        val bookingId = when (val s = _state.value) {
            is CheckoutState.PaymentFailed -> s.bookingId
            is CheckoutState.StillConfirming -> s.bookingId
            else -> return
        }
        confirmJob?.cancel()
        stubJob?.cancel()
        _state.value = CheckoutState.Loading
        viewModelScope.launch {
            when (val result = repository.bookingPaymentIntent(bookingId)) {
                is DoorstepResult.Success -> openPayment(bookingId, result.value)
                is DoorstepResult.Failure -> when (result.error.code) {
                    // Already captured: the server only needs to be asked.
                    DoorstepCodes.PAYMENT_ALREADY_SETTLED -> confirm(bookingId)
                    DoorstepCodes.HOLD_EXPIRED -> expire()
                    else -> _state.value = CheckoutState.PaymentFailed(bookingId, result.error.userMessage(), saved.holdExpiresAt)
                }
            }
        }
    }

    fun reload() {
        if (_state.value is CheckoutState.Failed) viewModelScope.launch { start() }
    }

    private suspend fun start() {
        when (saved.phase) {
            CheckoutContinuation.Phase.FRESH -> load()
            CheckoutContinuation.Phase.SUBMITTING -> if (saved.bookingId == null) resubmit() else resumeBooking()
            CheckoutContinuation.Phase.SHEET_REQUESTED,
            CheckoutContinuation.Phase.SETTLED,
            -> resumeBooking()
        }
    }

    private suspend fun load(): CheckoutState.Ready? {
        _state.value = CheckoutState.Loading
        val quote = when (val result = repository.quote(quoteId)) {
            is DoorstepResult.Success -> result.value
            is DoorstepResult.Failure -> {
                _state.value = CheckoutState.Failed(result.error.userMessage())
                return null
            }
        }
        val address = when (val result = repository.addresses()) {
            is DoorstepResult.Success -> result.value.firstOrNull { it.id == addressId }
            is DoorstepResult.Failure -> {
                _state.value = CheckoutState.Failed(result.error.userMessage())
                return null
            }
        }
        if (address == null) {
            _state.value = CheckoutState.Failed("That address was removed. Pick the address again.")
            return null
        }
        val ready = CheckoutState.Ready(quote, address, slotStart, requireFemalePro, notes = saved.notes)
        _state.value = ready
        return ready
    }

    private suspend fun submit(ready: CheckoutState.Ready?) {
        // The key is read-through-and-minted into saved state FIRST; only then
        // does the request leave.
        val key = saved.bookingKey { repository.newIdempotencyKey() }
        val request = BookingCreateRequestDto(
            quoteId = quoteId,
            addressId = addressId,
            slotStart = slotStart,
            requireFemalePro = requireFemalePro,
            notes = saved.notes.trim().ifBlank { null },
        )
        when (val result = repository.createBooking(key, request)) {
            is DoorstepResult.Success -> {
                val booking = result.value.booking
                saved.bookingId = booking.id
                saved.holdExpiresAt = instantOrNull(booking.holdExpiresAt)
                openPayment(booking.id, result.value.paymentIntent)
            }
            is DoorstepResult.Failure -> refused(result.error, ready)
        }
    }

    private fun refused(error: DoorstepError, ready: CheckoutState.Ready?) {
        saved.phase = CheckoutContinuation.Phase.FRESH
        // A definite refusal created no booking, and the next attempt is a new
        // decision: a new key. A network failure keeps the key — the booking
        // may exist, and a resend under the same key returns it.
        if (error !is DoorstepError.Network) saved.clearBookingKey()
        when (error.code) {
            DoorstepCodes.OUTSTANDING_DUE -> {
                _state.value = CheckoutState.BlockedByDues
                return
            }
            DoorstepCodes.HOLD_EXPIRED -> {
                _state.value = CheckoutState.HoldExpired
                return
            }
        }
        val slotGone = error.code in SLOT_GONE
        val message = error.userMessage()
        _state.value = ready?.copy(placing = false, refusal = message, pickAnotherSlot = slotGone)
            ?: CheckoutState.Failed(message)
    }

    private fun openPayment(bookingId: String, intent: PaymentIntentDto) {
        val session = when (val route = intent.checkoutRoute(description = "Doorstep booking", config = paymentConfig)) {
            is CheckoutRoute.Sheet -> route.session
            CheckoutRoute.DevStub -> {
                stubConfirm(bookingId)
                return
            }
            CheckoutRoute.Unavailable -> {
                saved.phase = CheckoutContinuation.Phase.SHEET_REQUESTED
                _state.value = CheckoutState.PaymentFailed(bookingId, "Online payment isn't available right now.", saved.holdExpiresAt)
                return
            }
        }
        val attempt = PaymentAttempt(
            applicationId = DOORSTEP_PAYMENT_APPLICATION_ID,
            referenceId = bookingId,
            id = repository.newIdempotencyKey(),
        )
        saved.inFlight.attempt = attempt
        saved.phase = CheckoutContinuation.Phase.SHEET_REQUESTED
        _state.value = CheckoutState.OpeningPayment(DoorstepPaymentRequest(attempt, session), saved.holdExpiresAt)
    }

    /**
     * DEV BUILD, dev stack, `stub` provider: there is no sheet. Ask
     * doorstep-service to have payments-service's stub gateway settle, then
     * poll `GET /payment` exactly as after a real sheet. The stub-confirm
     * answer is never read as paid (the repository drops its rows); only the
     * signed event's `succeeded` row — through [confirm] — reaches
     * [CheckoutState.Paid].
     */
    private fun stubConfirm(bookingId: String) {
        // Recovery after process death asks the server; it never re-books.
        saved.phase = CheckoutContinuation.Phase.SHEET_REQUESTED
        saved.inFlight.clear()
        confirmJob?.cancel()
        stubJob?.cancel()
        _state.value = CheckoutState.Confirming(bookingId, elapsedSeconds = 0)
        stubJob = viewModelScope.launch {
            when (val result = repository.stubConfirmBookingPayment(bookingId)) {
                is DoorstepResult.Success -> confirm(bookingId)
                is DoorstepResult.Failure -> stubRefused(bookingId, result.error)
            }
        }
    }

    private fun stubRefused(bookingId: String, error: DoorstepError) {
        when {
            // The settlement may have landed (or already had): only the server's rows can say.
            error is DoorstepError.Network ||
                error.code == DoorstepCodes.PAYMENT_ALREADY_SETTLED ||
                error.code == DoorstepCodes.INVALID_TRANSITION -> confirm(bookingId)
            error.code == DoorstepCodes.HOLD_EXPIRED -> expire()
            error.code == DoorstepCodes.STUB_UNAVAILABLE || error.code == DoorstepCodes.NOT_FOUND ->
                _state.value = CheckoutState.PaymentFailed(
                    bookingId,
                    "This server has no test payment gateway. Pay through checkout instead.",
                    saved.holdExpiresAt,
                )
            else -> _state.value = CheckoutState.PaymentFailed(bookingId, error.userMessage(), saved.holdExpiresAt)
        }
    }

    private fun observeHandoff() {
        viewModelScope.launch {
            handoff.events(DOORSTEP_PAYMENT_APPLICATION_ID).collect { event ->
                val mine = saved.inFlight.attempt
                // Not ours: another Doorstep payment (extras), or an earlier attempt at this booking.
                if (mine == null || event.attempt != mine) return@collect
                // Already acted on: a replay after rotation must not restart anything.
                if (handoff.isConsumed(mine)) return@collect
                handoff.consume(mine)
                when (event) {
                    // However the sheet ended — including the SDK's "success" — ask the server.
                    is PaymentHandoffEvent.SheetClosed -> confirm(mine.referenceId)
                    is PaymentHandoffEvent.Unavailable ->
                        _state.value = CheckoutState.PaymentFailed(mine.referenceId, event.reason, saved.holdExpiresAt)
                }
            }
        }
    }

    private fun confirm(bookingId: String) {
        confirmJob?.cancel()
        _state.value = CheckoutState.Confirming(bookingId, elapsedSeconds = 0)
        val source = DoorstepPaymentStatusSource(repository, bookingId, DoorstepReference.BOOKING)
        confirmJob = viewModelScope.launch {
            payments.confirm(DOORSTEP_PAYMENT_APPLICATION_ID, bookingId, source, policy).collect { confirmation ->
                _state.value = when (confirmation) {
                    is PaymentConfirmation.Confirming -> CheckoutState.Confirming(bookingId, confirmation.elapsedSeconds)
                    PaymentConfirmation.Paid -> settled(bookingId)
                    is PaymentConfirmation.Failed -> CheckoutState.PaymentFailed(bookingId, null, saved.holdExpiresAt)
                    PaymentConfirmation.RefundPending,
                    PaymentConfirmation.Refunded,
                    -> CheckoutState.Refunding(bookingId)
                    is PaymentConfirmation.TimedOut -> CheckoutState.StillConfirming(bookingId)
                }
            }
        }
    }

    private fun settled(bookingId: String): CheckoutState.Paid {
        saved.phase = CheckoutContinuation.Phase.SETTLED
        saved.inFlight.clear()
        return CheckoutState.Paid(bookingId)
    }

    private fun expire() {
        saved.phase = CheckoutContinuation.Phase.FRESH
        saved.clearBookingKey()
        saved.inFlight.clear()
        _state.value = CheckoutState.HoldExpired
    }

    /** SUBMITTING with no booking id: the response was lost. Resend under the SAME key. */
    private suspend fun resubmit() {
        val ready = load() ?: return
        _state.value = ready.copy(placing = true)
        submit(ready)
    }

    /** A booking exists. Ask the server what happened; never book again, never reopen a sheet. */
    private suspend fun resumeBooking() {
        val bookingId = saved.bookingId ?: run {
            load()
            return
        }
        _state.value = CheckoutState.Loading
        val reading = DoorstepPaymentStatusSource(repository, bookingId, DoorstepReference.BOOKING).status(bookingId)
        _state.value = when (reading) {
            PaymentStatusReading.Paid -> settled(bookingId)
            is PaymentStatusReading.Failed -> CheckoutState.PaymentFailed(bookingId, null, saved.holdExpiresAt)
            PaymentStatusReading.RefundPending,
            PaymentStatusReading.Refunded,
            -> CheckoutState.Refunding(bookingId)
            PaymentStatusReading.Confirming,
            is PaymentStatusReading.Unreachable,
            -> {
                confirm(bookingId)
                return
            }
        }
    }

    private companion object {
        const val MAX_NOTES = 500
        val SLOT_GONE = setOf(DoorstepCodes.SLOT_TAKEN, DoorstepCodes.SLOT_UNAVAILABLE, DoorstepCodes.QUOTE_EXPIRED)
    }
}

/**
 * The checkout state that survives process death. Nothing here is a provider
 * session — only identities and how far the customer got.
 */
internal class CheckoutContinuation(private val handle: SavedStateHandle) {

    enum class Phase { FRESH, SUBMITTING, SHEET_REQUESTED, SETTLED }

    var phase: Phase
        get() = handle.get<String>(KEY_PHASE)?.let { runCatching { Phase.valueOf(it) }.getOrNull() } ?: Phase.FRESH
        set(v) {
            handle[KEY_PHASE] = v.name
        }

    /** Read-through-and-mint, persisted immediately, so a resend uses the key the lost request used. */
    fun bookingKey(mint: () -> String): String =
        handle.get<String>(KEY_BOOKING_KEY) ?: mint().also { handle[KEY_BOOKING_KEY] = it }

    /** The persisted key, if a booking request may have left. */
    val savedBookingKey: String? get() = handle[KEY_BOOKING_KEY]

    fun clearBookingKey() {
        handle[KEY_BOOKING_KEY] = null
    }

    var bookingId: String?
        get() = handle[KEY_BOOKING_ID]
        set(v) {
            handle[KEY_BOOKING_ID] = v
        }

    var holdExpiresAt: Instant?
        get() = handle.get<String>(KEY_HOLD)?.let(::instantOrNull)
        set(v) {
            handle[KEY_HOLD] = v?.toString()
        }

    var notes: String
        get() = handle[KEY_NOTES] ?: ""
        set(v) {
            handle[KEY_NOTES] = v
        }

    /** The attempt, in `:core:payments`' record keyed by Doorstep's application id. */
    val inFlight = InFlightPayment(
        store = object : PaymentStateStore {
            override fun get(key: String): String? = handle[key]
            override fun set(key: String, value: String?) {
                handle[key] = value
            }
        },
        applicationId = DOORSTEP_PAYMENT_APPLICATION_ID,
    )

    companion object {
        const val KEY_PHASE = "doorstep.checkout.phase"
        const val KEY_BOOKING_KEY = "doorstep.checkout.bookingKey"
        const val KEY_BOOKING_ID = "doorstep.checkout.bookingId"
        const val KEY_HOLD = "doorstep.checkout.holdExpiresAt"
        const val KEY_NOTES = "doorstep.checkout.notes"
    }
}
