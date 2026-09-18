package com.us.android.feature.mopedu.rider

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.mobility.model.CancellationRule
import com.us.android.core.mobility.model.CouponValidation
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.QuoteOption
import com.us.android.core.mobility.model.QuoteSnapshot
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.RideReceipt
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.payments.InFlightPayment
import com.us.android.core.payments.PaymentAttempt
import com.us.android.core.payments.PaymentConfirmation
import com.us.android.core.payments.PaymentCoordinator
import com.us.android.core.payments.PaymentHandoff
import com.us.android.core.payments.PaymentHandoffEvent
import com.us.android.core.payments.PaymentPollPolicy
import com.us.android.core.payments.PaymentStateStore
import com.us.android.core.payments.PaymentStatusReading
import com.us.android.feature.mopedu.rider.data.MopeduError
import com.us.android.feature.mopedu.rider.data.MopeduResult
import com.us.android.feature.mopedu.rider.data.MopeduRiderRepository
import com.us.android.feature.mopedu.rider.data.userMessage
import com.us.android.feature.mopedu.rider.data.valueOrNull
import com.us.android.feature.mopedu.rider.location.CurrentLocationSource
import com.us.android.feature.mopedu.rider.location.LocationEffect
import com.us.android.feature.mopedu.rider.location.LocationPermissionFlow
import com.us.android.feature.mopedu.rider.location.LocationStep
import com.us.android.feature.mopedu.rider.payment.MOPEDU_PAYMENT_APPLICATION_ID
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentRequest
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentStatusSource
import com.us.android.feature.mopedu.rider.payment.toPaymentSession
import com.us.android.feature.mopedu.rider.payment.toReading
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.channels.BufferOverflow
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableSharedFlow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.SharedFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asSharedFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

/** A wall clock the cancel prompt and the quote expiry read. Injected so tests move it by hand. */
fun interface RideClock {
    fun nowMillis(): Long
}

sealed interface CouponStatus {
    data object None : CouponStatus

    data object Checking : CouponStatus

    /** The server accepted the code and the quote has been re-priced with it. */
    data class Applied(val coupon: CouponValidation) : CouponStatus

    /** The server's 422 message, shown under the field. */
    data class Rejected(val message: String) : CouponStatus
}

/**
 * The cancel dialog's facts, read from the ride's own rule at the moment the
 * customer asks: [fee] is what the server will charge if they confirm now;
 * [freeSecondsLeft] is how long the free window still runs, or null.
 */
data class CancelPrompt(
    val fee: MoneyPaise,
    val freeSecondsLeft: Long?,
    val isCancelling: Boolean = false,
) {
    val isFree: Boolean get() = fee.isZero
}

/** Where the fare's money is after the trip. Only the SERVER moves it to a settled state. */
sealed interface PaymentPhase {
    /** Cash: pay the captain; the captain confirms on their side. */
    data class CashPending(val amount: MoneyPaise) : PaymentPhase

    data object CashConfirmed : PaymentPhase

    /** UPI or card, not yet paid: "Pay now" creates the intent and opens the sheet. */
    data class ReadyToPay(val method: PaymentMethod, val amount: MoneyPaise, val creatingIntent: Boolean = false) : PaymentPhase

    /** The sheet is being opened from the Activity. */
    data class OpeningSheet(val request: MopeduPaymentRequest) : PaymentPhase

    /** The sheet ended; the SERVER has not said paid. */
    data class Confirming(val elapsedSeconds: Int) : PaymentPhase

    /** The server confirmed the capture. The only way to get here. */
    data object Paid : PaymentPhase

    /** The server said failed, or the sheet never opened. Retry with a new intent, or switch to cash. */
    data class Failed(val method: PaymentMethod, val amount: MoneyPaise, val reason: String?, val retryable: Boolean) : PaymentPhase

    /** Money moved and is being returned. */
    data object Refunding : PaymentPhase

    /** The poll gave up without an answer. NOT a failure: the payment may still land. */
    data object StillConfirming : PaymentPhase
}

sealed interface RiderUiState {
    data class LocationSelect(
        val pickup: GeoPoint? = null,
        val drop: GeoPoint? = null,
        val pickupQuery: String = "",
        val dropQuery: String = "",
        val locationStep: LocationStep = LocationStep.Idle,
        val isLoading: Boolean = false,
        val error: String? = null,
    ) : RiderUiState

    data class QuoteSelect(
        val quote: QuoteSnapshot,
        val selectedOption: QuoteOption,
        val paymentMethod: PaymentMethod = PaymentMethod.CASH,
        val couponInput: String = "",
        val coupon: CouponStatus = CouponStatus.None,
        val isRepricing: Boolean = false,
        val isBooking: Boolean = false,
        val error: String? = null,
    ) : RiderUiState

    data class SearchingCaptain(
        val booking: RideBooking,
        val searchElapsedSeconds: Int = 0,
        val cancel: CancelPrompt? = null,
        val error: String? = null,
    ) : RiderUiState

    data class CaptainAssigned(
        val booking: RideBooking,
        val captainLocation: GeoPoint,
        val etaMinutes: Int,
        val cancel: CancelPrompt? = null,
        val error: String? = null,
    ) : RiderUiState

    data class ArrivedAtPickup(
        val booking: RideBooking,
        val otp: String,
        val cancel: CancelPrompt? = null,
        val error: String? = null,
    ) : RiderUiState

    data class TripInProgress(
        val booking: RideBooking,
        val shareLink: String? = null,
        val sosTriggered: Boolean = false,
    ) : RiderUiState

    /** The ride ended without a trip. [fee] is what the server charged, from the ride's rule. */
    data class Cancelled(val booking: RideBooking, val fee: MoneyPaise, val byCustomer: Boolean) : RiderUiState

    data class TripCompleted(
        val receipt: RideReceipt,
        val payment: PaymentPhase,
        val ratingSubmitted: Boolean = false,
        val error: String? = null,
    ) : RiderUiState
}

/** One-shot instructions only the screen can carry out. */
sealed interface RiderEvent {
    data object RequestLocationPermission : RiderEvent
}

/**
 * The customer's ride, from "where to" to a settled fare.
 *
 *  1. The quote is the server's: surge, window, coupon discount and any
 *     outstanding cancellation fee are read from it and shown, never computed.
 *  2. A coupon is validated first, then the estimate is re-requested WITH the
 *     code; the re-priced quote is what gets booked.
 *  3. The payment method (cash / UPI / card) is chosen before booking.
 *  4. Cancelling shows the ride's fee rule BEFORE the confirm.
 *  5. After `completed`, UPI/card pays through `:core:payments` stamped
 *     "mopedu": the sheet ending is evidence, never proof — every ending polls
 *     `GET /rides/{id}/payment` and only its `paid` renders [PaymentPhase.Paid].
 *     Cash is settled only when the server says the captain confirmed it.
 *  6. After process death the server is asked first; a sheet never reopens by itself.
 */
@HiltViewModel
@Suppress("TooManyFunctions", "LargeClass")
class MopeduRiderViewModel @Inject constructor(
    private val repository: MopeduRiderRepository,
    private val location: CurrentLocationSource,
    private val handoff: PaymentHandoff,
    private val payments: PaymentCoordinator,
    private val clock: RideClock,
    savedState: SavedStateHandle,
) : ViewModel() {

    private val saved = RiderContinuation(savedState)
    private val statusSource = MopeduPaymentStatusSource(repository)
    private val policy = PaymentPollPolicy()
    private val locationFlow = LocationPermissionFlow()

    private val _uiState = MutableStateFlow<RiderUiState>(RiderUiState.LocationSelect())
    val uiState: StateFlow<RiderUiState> = _uiState.asStateFlow()

    private val _events = MutableSharedFlow<RiderEvent>(extraBufferCapacity = 4, onBufferOverflow = BufferOverflow.DROP_OLDEST)
    val events: SharedFlow<RiderEvent> = _events.asSharedFlow()

    private var pollingJob: Job? = null
    private var confirmJob: Job? = null
    private var cashJob: Job? = null

    init {
        observeHandoff()
        val pending = saved.inFlight.attempt
        if (pending != null) {
            viewModelScope.launch { resumePayment(pending) }
        } else {
            checkActiveRide()
        }
    }

    // ── Where to ────────────────────────────────────────────────────────

    fun checkActiveRide() {
        viewModelScope.launch {
            val booking = repository.activeRide().valueOrNull() ?: return@launch
            syncBooking(booking)
            if (booking.status.isActive) startPolling(booking.id)
        }
    }

    fun onPickupQueryChanged(text: String) = updateSelect { it.copy(pickupQuery = text, pickup = null, error = null) }

    fun onDropQueryChanged(text: String) = updateSelect { it.copy(dropQuery = text, drop = null, error = null) }

    fun useCurrentLocation() = performLocation(locationFlow.onUseCurrentLocation(location.hasPermission()))

    fun onLocationRationaleAccepted() = performLocation(locationFlow.onRationaleAccepted())

    fun onLocationRationaleDismissed() {
        locationFlow.onRationaleDismissed()
        publishLocationStep()
    }

    fun onLocationPermissionResult(granted: Boolean, canAskAgain: Boolean) =
        performLocation(locationFlow.onPermissionResult(granted, canAskAgain))

    private fun performLocation(effect: LocationEffect) {
        publishLocationStep()
        when (effect) {
            LocationEffect.None -> Unit
            LocationEffect.RequestPermission -> _events.tryEmit(RiderEvent.RequestLocationPermission)
            LocationEffect.FetchLocation -> viewModelScope.launch {
                val point = location.current()
                locationFlow.onLocationResult(point)
                updateSelect { it.copy(pickup = point, pickupQuery = point?.address ?: it.pickupQuery) }
                publishLocationStep()
            }
        }
    }

    private fun publishLocationStep() = updateSelect { it.copy(locationStep = locationFlow.step) }

    fun requestQuote() {
        val current = _uiState.value as? RiderUiState.LocationSelect ?: return
        _uiState.value = current.copy(isLoading = true, error = null)
        viewModelScope.launch {
            val pickup = current.pickup ?: location.geocode(current.pickupQuery)
            val drop = current.drop ?: location.geocode(current.dropQuery)
            when {
                pickup == null -> _uiState.value = current.copy(isLoading = false, error = "We couldn't find that pickup. Try a fuller address.")
                drop == null -> _uiState.value = current.copy(isLoading = false, error = "We couldn't find that destination. Try a fuller address.")
                else -> when (val result = repository.estimate(pickup, drop)) {
                    is MopeduResult.Success -> showQuote(result.value, couponStatus = CouponStatus.None)
                        ?: run { _uiState.value = current.copy(pickup = pickup, drop = drop, isLoading = false, error = NO_VEHICLES) }
                    is MopeduResult.Failure ->
                        _uiState.value = current.copy(pickup = pickup, drop = drop, isLoading = false, error = result.error.userMessage())
                }
            }
        }
    }

    /** Shows [quote], keeping the earlier vehicle and payment choices. Null when it has no option to pick. */
    private fun showQuote(quote: QuoteSnapshot, couponStatus: CouponStatus): RiderUiState.QuoteSelect? {
        val previous = _uiState.value as? RiderUiState.QuoteSelect
        val option = quote.options.firstOrNull { it.vehicleType == previous?.selectedOption?.vehicleType && it.available }
            ?: quote.options.firstOrNull { it.available }
            ?: quote.options.firstOrNull()
            ?: return null
        return RiderUiState.QuoteSelect(
            quote = quote,
            selectedOption = option,
            paymentMethod = previous?.paymentMethod ?: PaymentMethod.CASH,
            couponInput = previous?.couponInput ?: quote.couponCode.orEmpty(),
            coupon = couponStatus,
        ).also { _uiState.value = it }
    }

    // ── The quote ───────────────────────────────────────────────────────

    fun selectVehicleOption(option: QuoteOption) = updateQuote { it.copy(selectedOption = option, error = null) }

    fun selectPaymentMethod(method: PaymentMethod) = updateQuote { it.copy(paymentMethod = method, error = null) }

    fun onCouponInputChanged(text: String) = updateQuote { it.copy(couponInput = text, error = null) }

    /** Validates the code, then re-requests the estimate WITH it. The re-priced quote is what gets booked. */
    fun applyCoupon() {
        val current = _uiState.value as? RiderUiState.QuoteSelect ?: return
        val code = current.couponInput.trim()
        if (code.isEmpty() || current.isRepricing) return
        _uiState.value = current.copy(coupon = CouponStatus.Checking, isRepricing = true, error = null)
        viewModelScope.launch {
            when (val validation = repository.validateCoupon(code, vehicleType = current.selectedOption.vehicleType)) {
                is MopeduResult.Failure -> updateQuote {
                    it.copy(coupon = CouponStatus.Rejected(validation.error.userMessage()), isRepricing = false)
                }
                is MopeduResult.Success -> {
                    when (val priced = repository.estimate(current.quote.pickup, current.quote.drop, couponCode = validation.value.code)) {
                        is MopeduResult.Success -> showQuote(priced.value, CouponStatus.Applied(validation.value))
                            ?: updateQuote { it.copy(coupon = CouponStatus.None, isRepricing = false, error = NO_VEHICLES) }
                        is MopeduResult.Failure -> updateQuote {
                            it.copy(coupon = CouponStatus.Rejected(priced.error.userMessage()), isRepricing = false)
                        }
                    }
                }
            }
        }
    }

    fun removeCoupon() {
        val current = _uiState.value as? RiderUiState.QuoteSelect ?: return
        if (current.isRepricing) return
        _uiState.value = current.copy(couponInput = "", coupon = CouponStatus.None, isRepricing = true, error = null)
        viewModelScope.launch {
            when (val priced = repository.estimate(current.quote.pickup, current.quote.drop)) {
                is MopeduResult.Success -> showQuote(priced.value, CouponStatus.None)
                    ?: updateQuote { it.copy(isRepricing = false, error = NO_VEHICLES) }
                is MopeduResult.Failure -> updateQuote { it.copy(isRepricing = false, error = priced.error.userMessage()) }
            }
        }
    }

    fun backToLocations() {
        val current = _uiState.value as? RiderUiState.QuoteSelect ?: return
        _uiState.value = RiderUiState.LocationSelect(
            pickup = current.quote.pickup,
            drop = current.quote.drop,
            pickupQuery = current.quote.pickup.address,
            dropQuery = current.quote.drop.address,
        )
    }

    fun confirmBooking() {
        val current = _uiState.value as? RiderUiState.QuoteSelect ?: return
        if (current.isBooking || current.isRepricing) return
        if (current.quote.isExpiredAt(clock.nowMillis())) {
            _uiState.value = current.copy(error = "This fare has expired. Fetch a fresh one.")
            return
        }
        _uiState.value = current.copy(isBooking = true, error = null)
        viewModelScope.launch {
            val key = saved.bookingKey { repository.newIdempotencyKey() }
            when (val result = repository.bookRide(current.quote, current.selectedOption.vehicleType, current.paymentMethod, key)) {
                is MopeduResult.Success -> {
                    saved.clearBooking()
                    syncBooking(result.value)
                    startPolling(result.value.id)
                }
                is MopeduResult.Failure -> {
                    // A definite refusal is a new decision next time; a network failure keeps the key.
                    if (result.error !is MopeduError.Network) saved.clearBooking()
                    _uiState.value = current.copy(isBooking = false, error = result.error.userMessage())
                }
            }
        }
    }

    // ── The ride ────────────────────────────────────────────────────────

    private fun startPolling(rideId: String) {
        pollingJob?.cancel()
        pollingJob = viewModelScope.launch {
            while (isActive) {
                delay(POLL_MILLIS)
                when (val result = repository.activeRide()) {
                    is MopeduResult.Failure -> Unit
                    is MopeduResult.Success -> {
                        val booking = result.value
                        when {
                            booking != null && booking.id == rideId -> {
                                syncBooking(booking)
                                if (booking.status.isTerminal) return@launch
                            }
                            // No active ride any more: it ended while we were not looking.
                            booking == null -> {
                                onRideGone(rideId)
                                return@launch
                            }
                        }
                    }
                }
            }
        }
    }

    private fun syncBooking(booking: RideBooking) {
        val current = _uiState.value
        when (booking.status) {
            RideStatus.REQUESTED, RideStatus.SEARCHING_PARTNER -> {
                val elapsed = (current as? RiderUiState.SearchingCaptain)?.searchElapsedSeconds?.plus(POLL_SECONDS) ?: 0
                _uiState.value = RiderUiState.SearchingCaptain(
                    booking = booking,
                    searchElapsedSeconds = elapsed,
                    cancel = (current as? RiderUiState.SearchingCaptain)?.cancel,
                )
            }
            RideStatus.PARTNER_ASSIGNED, RideStatus.PARTNER_ARRIVING -> _uiState.value = RiderUiState.CaptainAssigned(
                booking = booking,
                captainLocation = booking.pickup,
                etaMinutes = DEFAULT_ETA_MINUTES,
                cancel = (current as? RiderUiState.CaptainAssigned)?.cancel,
            )
            RideStatus.ARRIVED, RideStatus.OTP_VERIFIED -> _uiState.value = RiderUiState.ArrivedAtPickup(
                booking = booking,
                otp = booking.otp.orEmpty(),
                cancel = (current as? RiderUiState.ArrivedAtPickup)?.cancel,
            )
            RideStatus.IN_PROGRESS -> {
                val trip = current as? RiderUiState.TripInProgress
                _uiState.value = RiderUiState.TripInProgress(
                    booking = booking,
                    shareLink = trip?.shareLink,
                    sosTriggered = trip?.sosTriggered ?: false,
                )
            }
            RideStatus.COMPLETED -> viewModelScope.launch { onCompleted(booking) }
            RideStatus.CANCELLED_BY_CUSTOMER -> _uiState.value = RiderUiState.Cancelled(
                booking = booking,
                fee = booking.cancellationFeeAt(clock.nowMillis()),
                byCustomer = true,
            )
            RideStatus.CANCELLED_BY_PARTNER, RideStatus.CANCELLED_BY_ADMIN, RideStatus.EXPIRED, RideStatus.FAILED ->
                _uiState.value = RiderUiState.Cancelled(booking = booking, fee = MoneyPaise.ZERO, byCustomer = false)
            RideStatus.SCHEDULED -> Unit
        }
    }

    private suspend fun onRideGone(rideId: String) {
        when (val receipt = repository.receipt(rideId)) {
            is MopeduResult.Success -> onCompleted(receipt.value)
            is MopeduResult.Failure -> _uiState.value = RiderUiState.LocationSelect(error = "Your ride has ended.")
        }
    }

    fun triggerSOS() {
        val current = _uiState.value as? RiderUiState.TripInProgress ?: return
        _uiState.value = current.copy(sosTriggered = true)
        viewModelScope.launch {
            repository.triggerSOS(current.booking.id, current.booking.pickup.lat, current.booking.pickup.lng)
        }
    }

    fun generateShareLink() {
        val current = _uiState.value as? RiderUiState.TripInProgress ?: return
        viewModelScope.launch {
            repository.createShareLink(current.booking.id).valueOrNull()?.let { link ->
                (_uiState.value as? RiderUiState.TripInProgress)?.let { _uiState.value = it.copy(shareLink = link) }
            }
        }
    }

    // ── Cancelling ──────────────────────────────────────────────────────

    /** Opens the cancel dialog with the ride's fee rule as it stands NOW. */
    fun requestCancel() {
        val booking = cancellableBooking() ?: return
        val now = clock.nowMillis()
        val prompt = CancelPrompt(
            fee = booking.cancellationFeeAt(now),
            freeSecondsLeft = CancellationRule.freeSecondsLeft(booking, now),
        )
        setCancelPrompt(prompt)
    }

    fun dismissCancel() = setCancelPrompt(null)

    fun confirmCancel() {
        val booking = cancellableBooking() ?: return
        val prompt = currentCancelPrompt() ?: return
        if (prompt.isCancelling) return
        setCancelPrompt(prompt.copy(isCancelling = true))
        viewModelScope.launch {
            when (val result = repository.cancelRide(booking.id, reason = CANCEL_REASON)) {
                is MopeduResult.Success -> {
                    pollingJob?.cancel()
                    _uiState.value = RiderUiState.Cancelled(
                        booking = booking.copy(status = RideStatus.CANCELLED_BY_CUSTOMER),
                        fee = booking.cancellationFeeAt(clock.nowMillis()),
                        byCustomer = true,
                    )
                }
                is MopeduResult.Failure -> {
                    setCancelPrompt(null)
                    setRideError(result.error.userMessage())
                }
            }
        }
    }

    private fun cancellableBooking(): RideBooking? = when (val s = _uiState.value) {
        is RiderUiState.SearchingCaptain -> s.booking
        is RiderUiState.CaptainAssigned -> s.booking
        is RiderUiState.ArrivedAtPickup -> s.booking
        else -> null
    }

    private fun currentCancelPrompt(): CancelPrompt? = when (val s = _uiState.value) {
        is RiderUiState.SearchingCaptain -> s.cancel
        is RiderUiState.CaptainAssigned -> s.cancel
        is RiderUiState.ArrivedAtPickup -> s.cancel
        else -> null
    }

    private fun setCancelPrompt(prompt: CancelPrompt?) = _uiState.update { s ->
        when (s) {
            is RiderUiState.SearchingCaptain -> s.copy(cancel = prompt)
            is RiderUiState.CaptainAssigned -> s.copy(cancel = prompt)
            is RiderUiState.ArrivedAtPickup -> s.copy(cancel = prompt)
            else -> s
        }
    }

    private fun setRideError(message: String?) = _uiState.update { s ->
        when (s) {
            is RiderUiState.SearchingCaptain -> s.copy(error = message)
            is RiderUiState.CaptainAssigned -> s.copy(error = message)
            is RiderUiState.ArrivedAtPickup -> s.copy(error = message)
            is RiderUiState.TripCompleted -> s.copy(error = message)
            else -> s
        }
    }

    fun dismissError() {
        setRideError(null)
        updateSelect { it.copy(error = null) }
        updateQuote { it.copy(error = null) }
    }

    // ── After the trip: the money ───────────────────────────────────────

    private suspend fun onCompleted(booking: RideBooking) {
        pollingJob?.cancel()
        val receipt = repository.receipt(booking.id).valueOrNull() ?: booking.toFallbackReceipt()
        onCompleted(receipt)
    }

    private suspend fun onCompleted(receipt: RideReceipt) {
        pollingJob?.cancel()
        val rating = (_uiState.value as? RiderUiState.TripCompleted)?.ratingSubmitted ?: false
        val payment = repository.ridePayment(receipt.rideId).valueOrNull()
        val method = payment?.method ?: receipt.paymentMethod
        val amount = payment?.amount?.takeIf { !it.isZero } ?: receipt.totalFare
        val phase: PaymentPhase = when (payment?.toReading() ?: receipt.paymentStatus.toReading()) {
            PaymentStatusReading.Paid -> if (method.isOnline) PaymentPhase.Paid else PaymentPhase.CashConfirmed
            is PaymentStatusReading.Failed -> PaymentPhase.Failed(method, amount, reason = null, retryable = true)
            PaymentStatusReading.RefundPending, PaymentStatusReading.Refunded -> PaymentPhase.Refunding
            PaymentStatusReading.Confirming, is PaymentStatusReading.Unreachable ->
                if (method.isOnline) PaymentPhase.ReadyToPay(method, amount) else PaymentPhase.CashPending(amount)
        }
        _uiState.value = RiderUiState.TripCompleted(receipt = receipt, payment = phase, ratingSubmitted = rating)
        if (phase is PaymentPhase.CashPending) watchCash(receipt.rideId)
    }

    /** Cash is settled only when the server says the captain confirmed it. */
    private fun watchCash(rideId: String) {
        cashJob?.cancel()
        cashJob = viewModelScope.launch {
            while (isActive && currentPhase() is PaymentPhase.CashPending) {
                delay(CASH_POLL_MILLIS)
                val payment = repository.ridePayment(rideId).valueOrNull() ?: continue
                when (payment.status) {
                    RidePaymentStatus.CASH_CONFIRMED, RidePaymentStatus.PAID -> setPhase(PaymentPhase.CashConfirmed)
                    RidePaymentStatus.REFUNDED, RidePaymentStatus.PARTIALLY_REFUNDED -> setPhase(PaymentPhase.Refunding)
                    else -> Unit
                }
            }
        }
    }

    /** Creates the intent for the chosen method and asks `:app` to open the sheet. */
    fun payNow() {
        val completed = _uiState.value as? RiderUiState.TripCompleted ?: return
        val ready = when (val phase = completed.payment) {
            is PaymentPhase.ReadyToPay -> if (phase.creatingIntent) return else phase
            is PaymentPhase.Failed -> PaymentPhase.ReadyToPay(phase.method, phase.amount)
            else -> return
        }
        setPhase(ready.copy(creatingIntent = true))
        viewModelScope.launch {
            val rideId = completed.receipt.rideId
            when (val intent = repository.paymentIntent(rideId, ready.method)) {
                is MopeduResult.Failure -> setPhase(PaymentPhase.Failed(ready.method, ready.amount, intent.error.userMessage(), retryable = true))
                is MopeduResult.Success -> {
                    val session = intent.value.toPaymentSession(description = "Mopedu ride ${rideId.takeLast(RIDE_TAIL)}")
                    if (session == null) {
                        setPhase(PaymentPhase.Failed(ready.method, ready.amount, "Online payment isn't available right now.", retryable = true))
                        return@launch
                    }
                    val attempt = PaymentAttempt(
                        applicationId = MOPEDU_PAYMENT_APPLICATION_ID,
                        referenceId = rideId,
                        id = repository.newIdempotencyKey(),
                    )
                    saved.inFlight.attempt = attempt
                    saved.paymentMethod = ready.method
                    setPhase(PaymentPhase.OpeningSheet(MopeduPaymentRequest(attempt, session)))
                }
            }
        }
    }

    /** The fallback when the sheet fails: the captain collects cash instead. */
    fun switchToCash() {
        val completed = _uiState.value as? RiderUiState.TripCompleted ?: return
        val rideId = completed.receipt.rideId
        viewModelScope.launch {
            when (val result = repository.switchToCash(rideId)) {
                is MopeduResult.Failure -> setRideError(result.error.userMessage())
                is MopeduResult.Success -> {
                    confirmJob?.cancel()
                    saved.inFlight.clear()
                    val amount = result.value.amount.takeIf { !it.isZero } ?: completed.receipt.totalFare
                    setPhase(
                        if (result.value.status == RidePaymentStatus.CASH_CONFIRMED) PaymentPhase.CashConfirmed else PaymentPhase.CashPending(amount),
                    )
                    watchCash(rideId)
                }
            }
        }
    }

    private fun observeHandoff() {
        viewModelScope.launch {
            handoff.events(MOPEDU_PAYMENT_APPLICATION_ID).collect { event ->
                val mine = saved.inFlight.attempt
                // Not ours: another screen, or an earlier attempt at this ride.
                if (mine == null || event.attempt != mine) return@collect
                // Already acted on: a replay after rotation must not restart anything.
                if (handoff.isConsumed(mine)) return@collect
                handoff.consume(mine)
                when (event) {
                    // However the sheet ended — including the SDK's "success" — ask the server.
                    is PaymentHandoffEvent.SheetClosed -> confirm(mine.referenceId)
                    is PaymentHandoffEvent.Unavailable -> {
                        saved.inFlight.clear()
                        val method = saved.paymentMethod ?: PaymentMethod.UPI
                        setPhase(PaymentPhase.Failed(method, currentAmount(), event.reason, retryable = true))
                    }
                }
            }
        }
    }

    private fun confirm(rideId: String) {
        confirmJob?.cancel()
        setPhase(PaymentPhase.Confirming(elapsedSeconds = 0))
        confirmJob = viewModelScope.launch {
            payments.confirm(MOPEDU_PAYMENT_APPLICATION_ID, rideId, statusSource, policy).collect { confirmation ->
                val method = saved.paymentMethod ?: PaymentMethod.UPI
                setPhase(
                    when (confirmation) {
                        is PaymentConfirmation.Confirming -> PaymentPhase.Confirming(confirmation.elapsedSeconds)
                        PaymentConfirmation.Paid -> settled()
                        is PaymentConfirmation.Failed -> {
                            saved.inFlight.clear()
                            PaymentPhase.Failed(method, currentAmount(), confirmation.reason, confirmation.retryable)
                        }
                        PaymentConfirmation.RefundPending, PaymentConfirmation.Refunded -> {
                            saved.inFlight.clear()
                            PaymentPhase.Refunding
                        }
                        is PaymentConfirmation.TimedOut -> PaymentPhase.StillConfirming
                    },
                )
            }
        }
    }

    /** Still confirming after a timeout: ask the server again. */
    fun checkPaymentAgain() {
        val completed = _uiState.value as? RiderUiState.TripCompleted ?: return
        if (completed.payment !is PaymentPhase.StillConfirming) return
        confirm(completed.receipt.rideId)
    }

    private fun settled(): PaymentPhase {
        saved.inFlight.clear()
        return PaymentPhase.Paid
    }

    /** A sheet was requested before the process died. Ask the server what happened; never reopen a sheet. */
    private suspend fun resumePayment(attempt: PaymentAttempt) {
        val rideId = attempt.referenceId
        val receipt = repository.receipt(rideId).valueOrNull()
        if (receipt == null) {
            saved.inFlight.clear()
            checkActiveRide()
            return
        }
        val method = saved.paymentMethod ?: receipt.paymentMethod
        _uiState.value = RiderUiState.TripCompleted(receipt = receipt, payment = PaymentPhase.Confirming(0))
        when (val payment = repository.ridePayment(rideId)) {
            is MopeduResult.Failure -> confirm(rideId)
            is MopeduResult.Success -> when (payment.value.toReading()) {
                PaymentStatusReading.Paid -> setPhase(settled())
                is PaymentStatusReading.Failed -> {
                    saved.inFlight.clear()
                    setPhase(PaymentPhase.Failed(method, payment.value.amount, null, retryable = true))
                }
                PaymentStatusReading.RefundPending, PaymentStatusReading.Refunded -> {
                    saved.inFlight.clear()
                    setPhase(PaymentPhase.Refunding)
                }
                PaymentStatusReading.Confirming, is PaymentStatusReading.Unreachable -> confirm(rideId)
            }
        }
    }

    fun submitRating(rating: Int, feedback: String) {
        val current = _uiState.value as? RiderUiState.TripCompleted ?: return
        _uiState.value = current.copy(ratingSubmitted = true)
        viewModelScope.launch { repository.rateRide(current.receipt.rideId, rating, feedback) }
    }

    fun resetToNewBooking() {
        pollingJob?.cancel()
        confirmJob?.cancel()
        cashJob?.cancel()
        saved.inFlight.clear()
        saved.paymentMethod = null
        _uiState.value = RiderUiState.LocationSelect()
    }

    /** The screen handed the request to the Activity. */
    internal fun activeAttempt(): PaymentAttempt? = saved.inFlight.attempt

    private fun currentPhase(): PaymentPhase? = (_uiState.value as? RiderUiState.TripCompleted)?.payment

    private fun currentAmount(): MoneyPaise = when (val phase = currentPhase()) {
        is PaymentPhase.ReadyToPay -> phase.amount
        is PaymentPhase.Failed -> phase.amount
        is PaymentPhase.OpeningSheet -> MoneyPaise(phase.request.session.amountMinor)
        else -> (_uiState.value as? RiderUiState.TripCompleted)?.receipt?.totalFare ?: MoneyPaise.ZERO
    }

    private fun setPhase(phase: PaymentPhase) = _uiState.update { s ->
        if (s is RiderUiState.TripCompleted) s.copy(payment = phase, error = null) else s
    }

    private inline fun updateSelect(transform: (RiderUiState.LocationSelect) -> RiderUiState.LocationSelect) =
        _uiState.update { s -> if (s is RiderUiState.LocationSelect) transform(s) else s }

    private inline fun updateQuote(transform: (RiderUiState.QuoteSelect) -> RiderUiState.QuoteSelect) =
        _uiState.update { s -> if (s is RiderUiState.QuoteSelect) transform(s) else s }

    override fun onCleared() {
        pollingJob?.cancel()
        confirmJob?.cancel()
        cashJob?.cancel()
        super.onCleared()
    }

    private companion object {
        const val POLL_MILLIS = 2_500L
        const val POLL_SECONDS = 2
        const val CASH_POLL_MILLIS = 4_000L
        const val DEFAULT_ETA_MINUTES = 3
        const val RIDE_TAIL = 6
        const val CANCEL_REASON = "customer_request"
        const val NO_VEHICLES = "No vehicles are available for this route right now."
    }
}

/** A booking's own facts as a receipt, for a `completed` ride whose receipt is not ready yet. */
internal fun RideBooking.toFallbackReceipt(): RideReceipt = RideReceipt(
    rideId = id,
    customerUserId = customerUserId,
    partnerId = partnerId,
    vehicleType = vehicleType,
    status = status.code,
    pickupAddress = pickup.address,
    dropAddress = drop.address,
    distanceMeters = 0,
    durationSeconds = 0,
    totalFare = finalFare ?: estimatedFare,
    paymentMethod = paymentMethod,
    paymentStatus = RidePaymentStatus.UNKNOWN,
    completedAtEpochMs = completedAtEpochMs,
)

private fun RidePaymentStatus.toReading(): PaymentStatusReading = when (this) {
    RidePaymentStatus.PAID, RidePaymentStatus.CASH_CONFIRMED -> PaymentStatusReading.Paid
    RidePaymentStatus.FAILED -> PaymentStatusReading.Failed(retryable = true)
    RidePaymentStatus.REFUNDED -> PaymentStatusReading.Refunded
    RidePaymentStatus.PARTIALLY_REFUNDED -> PaymentStatusReading.RefundPending
    RidePaymentStatus.CASH_PENDING, RidePaymentStatus.CONFIRMING, RidePaymentStatus.UNKNOWN -> PaymentStatusReading.Confirming
}

/**
 * The ride state that survives process death. Nothing here is a provider
 * session — only identities and how far the customer got.
 */
internal class RiderContinuation(private val handle: SavedStateHandle) {

    /** Read-through-and-mint, persisted immediately, so a resend uses the key the lost request used. */
    fun bookingKey(mint: () -> String): String =
        handle.get<String>(KEY_BOOKING_KEY) ?: mint().also { handle[KEY_BOOKING_KEY] = it }

    fun clearBooking() {
        handle[KEY_BOOKING_KEY] = null
    }

    var paymentMethod: PaymentMethod?
        get() = handle.get<String>(KEY_PAYMENT_METHOD)?.let { PaymentMethod.fromCode(it) }
        set(v) {
            handle[KEY_PAYMENT_METHOD] = v?.code
        }

    /** The attempt, in `:core:payments`' record keyed by Mopedu's application id. */
    val inFlight = InFlightPayment(
        store = object : PaymentStateStore {
            override fun get(key: String): String? = handle[key]
            override fun set(key: String, value: String?) {
                handle[key] = value
            }
        },
        applicationId = MOPEDU_PAYMENT_APPLICATION_ID,
    )

    private companion object {
        const val KEY_BOOKING_KEY = "mopedu.ride.bookingKey"
        const val KEY_PAYMENT_METHOD = "mopedu.ride.paymentMethod"
    }
}
