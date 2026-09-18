package com.us.android.feature.mopedu.captain

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.mobility.model.CaptainOffer
import com.us.android.core.mobility.model.CaptainState
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PartnerDocument
import com.us.android.core.mobility.model.PartnerProfile
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.PaymentMethod
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RidePaymentStatus
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType
import com.us.android.feature.mopedu.captain.data.CaptainResult
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import com.us.android.feature.mopedu.captain.data.userMessage
import com.us.android.feature.mopedu.captain.data.valueOrNull
import com.us.android.feature.mopedu.captain.home.DutyEffect
import com.us.android.feature.mopedu.captain.home.DutyState
import com.us.android.feature.mopedu.captain.home.GoOnlineFlow
import com.us.android.feature.mopedu.captain.home.LocationDisclosureStore
import com.us.android.feature.mopedu.captain.location.CaptainDuty
import com.us.android.feature.mopedu.captain.location.DutyStatus
import com.us.android.feature.mopedu.captain.location.OfflineReason
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

/** A wall clock the offer countdown reads. Injected so tests move it by hand. */
fun interface CaptainClock {
    fun nowMillis(): Long
}

enum class OnboardingStep(val stepNumber: Int, val title: String) {
    PROFILE(1, "Profile"),
    VEHICLE(2, "Vehicle"),
    DOCUMENTS(3, "Documents"),
    SUBSCRIPTION(4, "Plan"),
    STATUS(5, "Status"),
}

/** How the fare is being collected after the trip. Online money is settled by the SERVER only. */
sealed interface CollectPhase {
    data class Cash(val amount: MoneyPaise, val isConfirming: Boolean = false, val confirmed: Boolean = false) : CollectPhase

    /** UPI/card: "Waiting for customer payment" until `GET /rides/{id}/payment` says paid. */
    data class WaitingForCustomer(val method: PaymentMethod, val amount: MoneyPaise) : CollectPhase

    data class OnlinePaid(val method: PaymentMethod, val amount: MoneyPaise) : CollectPhase

    /** The customer's payment failed; they may retry or switch to cash on their side. */
    data class OnlineFailed(val method: PaymentMethod, val amount: MoneyPaise) : CollectPhase

    data object Refunding : CollectPhase
}

sealed interface CaptainUiState {
    data object Loading : CaptainUiState

    data class Onboarding(
        val step: OnboardingStep = OnboardingStep.PROFILE,
        val profile: PartnerProfile? = null,
        val vehicle: Vehicle? = null,
        val documents: List<PartnerDocument> = emptyList(),
        val plans: List<SubscriptionPlan> = emptyList(),
        val subscription: PartnerSubscription? = null,
        val isLoading: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState

    /** Offline or online-and-waiting: the duty toggle, today's numbers and, when one arrives, the offer card. */
    data class Home(
        val stats: CaptainState,
        val profile: PartnerProfile? = null,
        val subscription: PartnerSubscription? = null,
        val duty: DutyState = DutyState.Offline(),
        val lastPingOk: Boolean? = null,
        val incomingOffer: CaptainOffer? = null,
        val offerSecondsLeft: Long = 0,
        val isAccepting: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState {
        val isOnline: Boolean get() = duty == DutyState.Online
    }

    data class EnRouteToPickup(val booking: RideBooking, val isArriving: Boolean = false, val errorMessage: String? = null) : CaptainUiState

    data class VerifyOtp(
        val booking: RideBooking,
        val otpInput: String = "",
        val isVerifying: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState

    data class TripInProgress(val booking: RideBooking, val isCompleting: Boolean = false, val errorMessage: String? = null) : CaptainUiState

    data class CollectPayment(val booking: RideBooking, val phase: CollectPhase, val errorMessage: String? = null) : CaptainUiState
}

/** One-shot instructions only the screen can carry out. */
sealed interface CaptainEvent {
    data object RequestLocationPermission : CaptainEvent

    data object StartLocationService : CaptainEvent
}

/**
 * The captain's day: the onboarding gate, then Home with the Go-online
 * toggle ([GoOnlineFlow]: disclosure → permission → server → service), offers
 * with a countdown, the ride (en route, OTP, trip) and collecting the fare.
 *
 * Cash is settled by the captain's confirmation. UPI/card is settled ONLY when
 * `GET /rides/{id}/payment` says paid: the card reads "Waiting for customer
 * payment" until then, and nothing on this device can mark it paid.
 */
@HiltViewModel
@Suppress("TooManyFunctions", "LargeClass")
class MopeduCaptainViewModel @Inject constructor(
    private val repository: MopeduCaptainRepository,
    private val duty: CaptainDuty,
    private val disclosure: LocationDisclosureStore,
    private val clock: CaptainClock,
) : ViewModel() {

    private val flow = GoOnlineFlow()

    private val _uiState = MutableStateFlow<CaptainUiState>(CaptainUiState.Loading)
    val uiState: StateFlow<CaptainUiState> = _uiState.asStateFlow()

    private val _events = MutableSharedFlow<CaptainEvent>(extraBufferCapacity = 4, onBufferOverflow = BufferOverflow.DROP_OLDEST)
    val events: SharedFlow<CaptainEvent> = _events.asSharedFlow()

    private var stats: CaptainState = CaptainState(false, null, PartnerProfile.DEFAULT_RATING, 0, MoneyPaise.ZERO)
    private var profile: PartnerProfile? = null
    private var subscription: PartnerSubscription? = null
    private var offerJob: Job? = null
    private var countdownJob: Job? = null
    private var paymentJob: Job? = null

    init {
        viewModelScope.launch { duty.status.collect(::onDutyStatus) }
        checkInitialStatus()
    }

    // ── The gate ────────────────────────────────────────────────────────

    fun checkInitialStatus() {
        viewModelScope.launch {
            when (val result = repository.profile()) {
                is CaptainResult.Failure -> loadOnboarding(OnboardingStep.PROFILE)
                is CaptainResult.Success -> {
                    val captain = result.value
                    profile = captain.profile
                    if (captain.profile.kycStatus != APPROVED) {
                        loadOnboarding(nextStepFor(captain.profile))
                        return@launch
                    }
                    subscription = repository.mySubscription().valueOrNull()
                    if (subscription?.isUsable != true) {
                        loadOnboarding(OnboardingStep.SUBSCRIPTION)
                        return@launch
                    }
                    // The server still thinks the captain is online, but nothing on
                    // this device is sharing location (the app was killed): put it right.
                    if (captain.isOnline && !duty.isOnline) repository.setOnline(false)
                    showHome()
                    refreshEarnings()
                }
            }
        }
    }

    private fun nextStepFor(profile: PartnerProfile): OnboardingStep = when {
        profile.status == "draft" -> OnboardingStep.VEHICLE
        profile.kycStatus == "pending" -> OnboardingStep.DOCUMENTS
        else -> OnboardingStep.STATUS
    }

    private fun loadOnboarding(step: OnboardingStep) {
        viewModelScope.launch {
            _uiState.value = CaptainUiState.Onboarding(step = step, isLoading = true)
            val loaded = repository.profile().valueOrNull()?.profile
            if (loaded != null) profile = loaded
            val vehicles = repository.vehicles().valueOrNull().orEmpty()
            val docs = repository.documents().valueOrNull().orEmpty()
            val plans = repository.subscriptionPlans().valueOrNull().orEmpty()
            subscription = repository.mySubscription().valueOrNull()
            _uiState.value = CaptainUiState.Onboarding(
                step = step,
                profile = profile,
                vehicle = vehicles.firstOrNull(),
                documents = docs,
                plans = plans,
                subscription = subscription,
                isLoading = false,
            )
        }
    }

    private fun onboardingAction(failure: String, action: suspend () -> CaptainResult<*>, next: OnboardingStep) {
        val current = _uiState.value as? CaptainUiState.Onboarding ?: return
        _uiState.value = current.copy(isLoading = true, errorMessage = null)
        viewModelScope.launch {
            when (val result = action()) {
                is CaptainResult.Success -> loadOnboarding(next)
                is CaptainResult.Failure -> _uiState.value = current.copy(isLoading = false, errorMessage = "$failure: ${result.error.userMessage()}")
            }
        }
    }

    fun submitProfile(fullName: String, phone: String, email: String?) =
        onboardingAction("Profile not saved", { repository.createProfile(fullName, phone, email) }, OnboardingStep.VEHICLE)

    fun submitVehicle(type: VehicleType, regNumber: String, brand: String, model: String) =
        onboardingAction("Vehicle not saved", { repository.addVehicle(type, regNumber, brand.ifBlank { null }, model.ifBlank { null }) }, OnboardingStep.DOCUMENTS)

    fun submitDocument(type: String, number: String, fileUrl: String) =
        onboardingAction("Document not submitted", { repository.submitDocument(type, number.ifBlank { null }, fileUrl) }, OnboardingStep.DOCUMENTS)

    /** Starts DigiLocker. The assertion comes back through the app link; until then the status step waits. */
    fun startDigiLocker() =
        onboardingAction("DigiLocker not started", { repository.startAadhaar() }, OnboardingStep.STATUS)

    fun selectPlan(planId: String) =
        onboardingAction("Subscription failed", { repository.subscribe(planId, repository.newIdempotencyKey()) }, OnboardingStep.STATUS)

    fun refreshOnboardingStatus() = loadOnboarding(OnboardingStep.STATUS)

    fun openOnboarding(step: OnboardingStep = OnboardingStep.STATUS) = loadOnboarding(step)

    fun proceedToConsole() {
        viewModelScope.launch {
            profile = repository.profile().valueOrNull()?.profile ?: profile
            subscription = repository.mySubscription().valueOrNull()
            showHome()
            refreshEarnings()
        }
    }

    // ── Home: duty ──────────────────────────────────────────────────────

    private fun showHome(errorMessage: String? = null) {
        val previous = _uiState.value as? CaptainUiState.Home
        _uiState.value = CaptainUiState.Home(
            stats = stats,
            profile = profile,
            subscription = subscription,
            duty = flow.state,
            lastPingOk = previous?.lastPingOk,
            errorMessage = errorMessage,
        )
        if (flow.state == DutyState.Online) startOfferPolling()
    }

    private suspend fun refreshEarnings() {
        val earned = repository.earnings().valueOrNull() ?: return
        stats = earned.copy(rating = profile?.rating ?: earned.rating)
        updateHome { it.copy(stats = stats) }
    }

    fun onGoOnlineTapped(permissionGranted: Boolean) = perform(flow.onGoOnlineTapped(permissionGranted, disclosure.accepted()))

    fun onDisclosureAccepted(permissionGranted: Boolean) {
        disclosure.markAccepted()
        perform(flow.onDisclosureAccepted(permissionGranted))
    }

    fun onDisclosureDeclined() {
        flow.onDisclosureDeclined()
        publishDuty()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) = perform(flow.onPermissionResult(granted, canAskAgain))

    fun onGoOfflineTapped() = perform(flow.onGoOfflineTapped())

    private fun perform(effect: DutyEffect) {
        publishDuty()
        when (effect) {
            DutyEffect.None -> Unit
            DutyEffect.RequestPermission -> _events.tryEmit(CaptainEvent.RequestLocationPermission)
            DutyEffect.SetServerOnline -> viewModelScope.launch {
                val result = repository.setOnline(true)
                if (result is CaptainResult.Failure) updateHome { it.copy(errorMessage = result.error.userMessage()) }
                perform(flow.onServerOnline(accepted = result is CaptainResult.Success))
            }
            DutyEffect.StartService -> {
                _events.tryEmit(CaptainEvent.StartLocationService)
                startOfferPolling()
            }
            DutyEffect.StopService -> {
                stopOfferPolling()
                duty.requestStop(OfflineReason.TOGGLED_OFF)
            }
        }
    }

    private fun onDutyStatus(status: DutyStatus) {
        when (status) {
            is DutyStatus.Online -> {
                if (flow.state !is DutyState.Online) flow.onServiceRunning()
                updateHome { it.copy(lastPingOk = status.lastPingOk) }
                startOfferPolling()
            }
            is DutyStatus.Offline -> {
                val wasOnDuty = flow.state == DutyState.Online || flow.state == DutyState.GoingOffline
                val reason = status.reason
                if (wasOnDuty && reason != null) {
                    flow.onServiceStopped(reason)
                    stopOfferPolling()
                    if (reason != OfflineReason.TOGGLED_OFF) updateHome { it.copy(errorMessage = reason.explanation()) }
                }
            }
        }
        publishDuty()
    }

    private fun publishDuty() = updateHome { it.copy(duty = flow.state) }

    // ── Home: offers ────────────────────────────────────────────────────

    private fun startOfferPolling() {
        if (offerJob?.isActive == true) return
        offerJob = viewModelScope.launch {
            while (isActive) {
                val home = _uiState.value as? CaptainUiState.Home
                if (home != null && home.isOnline && home.incomingOffer == null) {
                    repository.incomingOffers().valueOrNull()
                        ?.firstOrNull { !it.isExpiredAt(clock.nowMillis()) }
                        ?.let { offer ->
                            updateHome { it.copy(incomingOffer = offer, offerSecondsLeft = offer.secondsLeftAt(clock.nowMillis())) }
                            startCountdown(offer)
                        }
                }
                delay(OFFER_POLL_MILLIS)
            }
        }
    }

    private fun stopOfferPolling() {
        offerJob?.cancel()
        offerJob = null
        countdownJob?.cancel()
        updateHome { it.copy(incomingOffer = null, offerSecondsLeft = 0) }
    }

    private fun startCountdown(offer: CaptainOffer) {
        countdownJob?.cancel()
        countdownJob = viewModelScope.launch {
            while (isActive) {
                delay(COUNTDOWN_MILLIS)
                val home = _uiState.value as? CaptainUiState.Home ?: return@launch
                if (home.incomingOffer?.id != offer.id) return@launch
                val left = offer.secondsLeftAt(clock.nowMillis())
                if (left <= 0L) {
                    updateHome { it.copy(incomingOffer = null, offerSecondsLeft = 0) }
                    return@launch
                }
                updateHome { it.copy(offerSecondsLeft = left) }
            }
        }
    }

    fun acceptOffer() {
        val home = _uiState.value as? CaptainUiState.Home ?: return
        val offer = home.incomingOffer ?: return
        if (home.isAccepting) return
        updateHome { it.copy(isAccepting = true, errorMessage = null) }
        viewModelScope.launch {
            when (val result = repository.acceptOffer(offer.id)) {
                is CaptainResult.Success -> {
                    countdownJob?.cancel()
                    offerJob?.cancel()
                    duty.setOnRide(true)
                    _uiState.value = CaptainUiState.EnRouteToPickup(booking = offer.toBooking(result.value))
                    viewModelScope.launch { repository.markArriving(result.value) }
                }
                is CaptainResult.Failure -> {
                    updateHome { it.copy(isAccepting = false, incomingOffer = null, offerSecondsLeft = 0, errorMessage = result.error.userMessage()) }
                }
            }
        }
    }

    fun rejectOffer() {
        val home = _uiState.value as? CaptainUiState.Home ?: return
        val offer = home.incomingOffer ?: return
        countdownJob?.cancel()
        updateHome { it.copy(incomingOffer = null, offerSecondsLeft = 0) }
        viewModelScope.launch { repository.rejectOffer(offer.id) }
    }

    // ── The ride ────────────────────────────────────────────────────────

    fun markArrived() {
        val state = _uiState.value as? CaptainUiState.EnRouteToPickup ?: return
        if (state.isArriving) return
        _uiState.value = state.copy(isArriving = true, errorMessage = null)
        viewModelScope.launch {
            when (val result = repository.markArrived(state.booking.id)) {
                is CaptainResult.Success -> _uiState.value = CaptainUiState.VerifyOtp(booking = state.booking.copy(status = RideStatus.ARRIVED))
                is CaptainResult.Failure -> _uiState.value = state.copy(isArriving = false, errorMessage = result.error.userMessage())
            }
        }
    }

    fun onOtpInputChanged(otp: String) {
        val state = _uiState.value as? CaptainUiState.VerifyOtp ?: return
        _uiState.value = state.copy(otpInput = otp.filter { it.isDigit() }.take(OTP_LENGTH), errorMessage = null)
    }

    fun verifyOtpAndStart() {
        val state = _uiState.value as? CaptainUiState.VerifyOtp ?: return
        if (state.isVerifying) return
        if (state.otpInput.length < OTP_LENGTH) {
            _uiState.value = state.copy(errorMessage = "Enter the customer's 4-digit code.")
            return
        }
        _uiState.value = state.copy(isVerifying = true, errorMessage = null)
        viewModelScope.launch {
            when (val result = repository.verifyOtpAndStart(state.booking.id, state.otpInput)) {
                is CaptainResult.Success -> _uiState.value = CaptainUiState.TripInProgress(booking = state.booking.copy(status = RideStatus.IN_PROGRESS))
                is CaptainResult.Failure -> _uiState.value = state.copy(isVerifying = false, errorMessage = "That code didn't match. Check it with the customer.")
            }
        }
    }

    fun completeTrip(finalDistanceKm: Double = 0.0, finalDurationMin: Int = 0) {
        val state = _uiState.value as? CaptainUiState.TripInProgress ?: return
        if (state.isCompleting) return
        _uiState.value = state.copy(isCompleting = true, errorMessage = null)
        viewModelScope.launch {
            when (val result = repository.completeRide(state.booking.id, finalDistanceKm, finalDurationMin, repository.newIdempotencyKey())) {
                is CaptainResult.Success -> {
                    duty.setOnRide(false)
                    val booking = state.booking.copy(status = RideStatus.COMPLETED)
                    val amount = booking.finalFare ?: booking.estimatedFare
                    val phase = if (booking.paymentMethod.isOnline) {
                        CollectPhase.WaitingForCustomer(booking.paymentMethod, amount)
                    } else {
                        CollectPhase.Cash(amount)
                    }
                    _uiState.value = CaptainUiState.CollectPayment(booking = booking, phase = phase)
                    watchPayment(booking.id)
                }
                is CaptainResult.Failure -> _uiState.value = state.copy(isCompleting = false, errorMessage = result.error.userMessage())
            }
        }
    }

    // ── Collecting the fare ─────────────────────────────────────────────

    fun confirmCashPayment() {
        val state = _uiState.value as? CaptainUiState.CollectPayment ?: return
        val cash = state.phase as? CollectPhase.Cash ?: return
        if (cash.isConfirming || cash.confirmed) return
        _uiState.value = state.copy(phase = cash.copy(isConfirming = true), errorMessage = null)
        viewModelScope.launch {
            when (val result = repository.confirmCashPayment(state.booking.id)) {
                is CaptainResult.Success -> {
                    paymentJob?.cancel()
                    _uiState.value = state.copy(phase = cash.copy(isConfirming = false, confirmed = true))
                    delay(SETTLED_PAUSE_MILLIS)
                    finishRide()
                }
                is CaptainResult.Failure -> _uiState.value = state.copy(phase = cash.copy(isConfirming = false), errorMessage = result.error.userMessage())
            }
        }
    }

    /**
     * Polls `GET /rides/{id}/payment` until the SERVER settles it. Cash confirmed
     * by the customer's side (a switch, an admin) settles too; the captain's own
     * tap is [confirmCashPayment].
     */
    private fun watchPayment(rideId: String) {
        paymentJob?.cancel()
        paymentJob = viewModelScope.launch {
            while (isActive) {
                delay(PAYMENT_POLL_MILLIS)
                val state = _uiState.value as? CaptainUiState.CollectPayment ?: return@launch
                val payment = repository.ridePayment(rideId).valueOrNull() ?: continue
                val amount = payment.amount.takeIf { !it.isZero } ?: state.booking.finalFare ?: state.booking.estimatedFare
                val phase: CollectPhase? = when (payment.status) {
                    RidePaymentStatus.PAID -> CollectPhase.OnlinePaid(payment.method, amount)
                    RidePaymentStatus.CASH_CONFIRMED -> CollectPhase.Cash(amount, confirmed = true)
                    RidePaymentStatus.FAILED -> CollectPhase.OnlineFailed(payment.method, amount)
                    RidePaymentStatus.REFUNDED, RidePaymentStatus.PARTIALLY_REFUNDED -> CollectPhase.Refunding
                    // The customer switched to cash: the captain collects it.
                    RidePaymentStatus.CASH_PENDING -> (state.phase as? CollectPhase.Cash) ?: CollectPhase.Cash(amount)
                    // An online payment still pending, or a status newer than this build: keep waiting.
                    RidePaymentStatus.CONFIRMING, RidePaymentStatus.UNKNOWN ->
                        if (payment.method.isOnline && state.phase !is CollectPhase.WaitingForCustomer) {
                            CollectPhase.WaitingForCustomer(payment.method, amount)
                        } else {
                            null
                        }
                }
                if (phase != null && phase != state.phase) _uiState.value = state.copy(phase = phase)
                if (phase is CollectPhase.OnlinePaid || (phase is CollectPhase.Cash && phase.confirmed)) {
                    delay(SETTLED_PAUSE_MILLIS)
                    finishRide()
                    return@launch
                }
            }
        }
    }

    /** The captain leaves a failed or refunded payment to the customer and goes back to Home. */
    fun finishRide() {
        paymentJob?.cancel()
        viewModelScope.launch {
            showHome()
            refreshEarnings()
        }
    }

    fun dismissError() = _uiState.update { s ->
        when (s) {
            is CaptainUiState.Home -> s.copy(errorMessage = null)
            is CaptainUiState.EnRouteToPickup -> s.copy(errorMessage = null)
            is CaptainUiState.VerifyOtp -> s.copy(errorMessage = null)
            is CaptainUiState.TripInProgress -> s.copy(errorMessage = null)
            is CaptainUiState.CollectPayment -> s.copy(errorMessage = null)
            is CaptainUiState.Onboarding -> s.copy(errorMessage = null)
            CaptainUiState.Loading -> s
        }
    }

    private inline fun updateHome(transform: (CaptainUiState.Home) -> CaptainUiState.Home) =
        _uiState.update { s -> if (s is CaptainUiState.Home) transform(s) else s }

    override fun onCleared() {
        offerJob?.cancel()
        countdownJob?.cancel()
        paymentJob?.cancel()
        super.onCleared()
    }

    private companion object {
        const val APPROVED = "approved"
        const val OTP_LENGTH = 4
        const val OFFER_POLL_MILLIS = 3_000L
        const val COUNTDOWN_MILLIS = 1_000L
        const val PAYMENT_POLL_MILLIS = 3_000L
        const val SETTLED_PAUSE_MILLIS = 1_500L
    }
}

/** The accepted offer as the ride the captain drives. The server's ride carries the rest. */
internal fun CaptainOffer.toBooking(rideId: String): RideBooking = RideBooking(
    id = rideId,
    customerUserId = "",
    partnerId = null,
    vehicleId = null,
    quoteId = null,
    revision = 1,
    vehicleType = vehicleType,
    status = RideStatus.PARTNER_ASSIGNED,
    pickup = pickup,
    drop = drop,
    estimatedFare = estimatedEarnings,
    finalFare = null,
    paymentMethod = paymentMethod,
    otp = null,
    requestedAtEpochMs = 0L,
)

/** What Home says when the service went offline on its own. */
fun OfflineReason.explanation(): String = when (this) {
    OfflineReason.TOGGLED_OFF -> "You went offline."
    OfflineReason.SIGNED_OUT -> "You were signed out, so you went offline."
    OfflineReason.PERMISSION_REVOKED -> "Location access was turned off, so you went offline."
    OfflineReason.NO_FIX -> "We lost your location for two minutes, so you went offline."
    OfflineReason.SERVER_REFUSED -> "Mopedu couldn't put you online just now."
}
