package com.us.android.feature.mopedu.captain

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.location.LocationTracker
import com.us.android.core.mobility.model.CaptainOffer
import com.us.android.core.mobility.model.CaptainState
import com.us.android.core.mobility.model.MoneyPaise
import com.us.android.core.mobility.model.PartnerDocument
import com.us.android.core.mobility.model.PartnerProfile
import com.us.android.core.mobility.model.PartnerSubscription
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RideStatus
import com.us.android.core.mobility.model.SubscriptionPlan
import com.us.android.core.mobility.model.Vehicle
import com.us.android.core.mobility.model.VehicleType
import com.us.android.feature.mopedu.captain.data.MopeduCaptainRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface CaptainUiState {
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

    data class Offline(
        val captainState: CaptainState,
        val profile: PartnerProfile? = null,
        val subscription: PartnerSubscription? = null,
        val errorMessage: String? = null,
    ) : CaptainUiState

    data class OnlineIdle(
        val captainState: CaptainState,
        val incomingOffer: CaptainOffer? = null,
        val isPolling: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState

    data class EnRouteToPickup(
        val booking: RideBooking,
        val isArrived: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState

    data class VerifyOtp(
        val booking: RideBooking,
        val otpInput: String = "",
        val isVerifying: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState

    data class TripInProgress(
        val booking: RideBooking,
        val distanceTraveledMeters: Int = 0,
        val isCompleting: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState

    data class CollectPayment(
        val booking: RideBooking,
        val isConfirming: Boolean = false,
        val cashConfirmed: Boolean = false,
        val errorMessage: String? = null,
    ) : CaptainUiState
}

@HiltViewModel
class MopeduCaptainViewModel @Inject constructor(
    private val locationTracker: LocationTracker,
    private val captainRepository: MopeduCaptainRepository,
) : ViewModel() {

    private val defaultCaptainState = CaptainState(
        isOnline = false,
        activeRideId = null,
        rating = 4.88,
        totalRidesCompleted = 0,
        todayEarnings = MoneyPaise(0L),
    )

    private val _uiState = MutableStateFlow<CaptainUiState>(
        CaptainUiState.Offline(captainState = defaultCaptainState)
    )
    val uiState: StateFlow<CaptainUiState> = _uiState.asStateFlow()

    private var offerPollingJob: Job? = null

    init {
        checkInitialStatus()

        viewModelScope.launch {
            locationTracker.telemetryFlow.collect { telemetry ->
                if ((_uiState.value as? CaptainUiState.OnlineIdle)?.captainState?.isOnline == true ||
                    _uiState.value is CaptainUiState.EnRouteToPickup ||
                    _uiState.value is CaptainUiState.TripInProgress
                ) {
                    captainRepository.sendLocation(telemetry)
                }
            }
        }
    }

    fun checkInitialStatus() {
        viewModelScope.launch {
            captainRepository.getProfile().onSuccess { profile ->
                if (profile.kycStatus != "approved") {
                    loadOnboardingData(step = determineNextOnboardingStep(profile))
                } else {
                    val sub = captainRepository.getMySubscription().getOrNull()
                    if (sub == null || sub.status !in setOf("trial", "active")) {
                        loadOnboardingData(step = OnboardingStep.SUBSCRIPTION)
                    } else {
                        _uiState.value = CaptainUiState.Offline(
                            captainState = defaultCaptainState.copy(rating = profile.rating, totalRidesCompleted = profile.ridesCompleted),
                            profile = profile,
                            subscription = sub,
                        )
                    }
                }
            }.onFailure {
                loadOnboardingData(step = OnboardingStep.PROFILE)
            }
        }
    }

    private fun determineNextOnboardingStep(profile: PartnerProfile): OnboardingStep {
        return when {
            profile.status == "draft" -> OnboardingStep.VEHICLE
            profile.kycStatus == "pending" -> OnboardingStep.DOCUMENTS
            else -> OnboardingStep.STATUS
        }
    }

    private fun loadOnboardingData(step: OnboardingStep) {
        viewModelScope.launch {
            _uiState.value = CaptainUiState.Onboarding(step = step, isLoading = true)
            val profile = captainRepository.getProfile().getOrNull()
            val vehicles = captainRepository.getVehicles().getOrNull() ?: emptyList()
            val docs = captainRepository.getDocuments().getOrNull() ?: emptyList()
            val plans = captainRepository.getSubscriptionPlans().getOrNull() ?: emptyList()
            val sub = captainRepository.getMySubscription().getOrNull()

            _uiState.value = CaptainUiState.Onboarding(
                step = step,
                profile = profile,
                vehicle = vehicles.firstOrNull(),
                documents = docs,
                plans = plans,
                subscription = sub,
                isLoading = false,
            )
        }
    }

    fun submitProfile(fullName: String, phone: String, email: String?) {
        viewModelScope.launch {
            val current = _uiState.value as? CaptainUiState.Onboarding ?: return@launch
            _uiState.value = current.copy(isLoading = true, errorMessage = null)
            captainRepository.createProfile(fullName, phone, email).onSuccess {
                loadOnboardingData(step = OnboardingStep.VEHICLE)
            }.onFailure {
                _uiState.value = current.copy(isLoading = false, errorMessage = "Profile creation failed: ${it.message}")
            }
        }
    }

    fun submitVehicle(type: VehicleType, regNumber: String, brand: String, model: String) {
        viewModelScope.launch {
            val current = _uiState.value as? CaptainUiState.Onboarding ?: return@launch
            _uiState.value = current.copy(isLoading = true, errorMessage = null)
            captainRepository.addVehicle(
                vehicleType = type,
                registrationNumber = regNumber,
                brand = brand,
                model = model,
            ).onSuccess {
                loadOnboardingData(step = OnboardingStep.DOCUMENTS)
            }.onFailure {
                _uiState.value = current.copy(isLoading = false, errorMessage = "Vehicle registration failed: ${it.message}")
            }
        }
    }

    fun submitDocument(type: String, number: String, fileUrl: String) {
        viewModelScope.launch {
            val current = _uiState.value as? CaptainUiState.Onboarding ?: return@launch
            _uiState.value = current.copy(isLoading = true, errorMessage = null)
            captainRepository.submitDocument(
                documentType = type,
                documentNumber = number,
                fileUrl = fileUrl,
            ).onSuccess {
                loadOnboardingData(step = OnboardingStep.DOCUMENTS)
            }.onFailure {
                _uiState.value = current.copy(isLoading = false, errorMessage = "Document submission failed: ${it.message}")
            }
        }
    }

    fun startDigiLocker() {
        viewModelScope.launch {
            val current = _uiState.value as? CaptainUiState.Onboarding ?: return@launch
            _uiState.value = current.copy(isLoading = true, errorMessage = null)
            captainRepository.startAadhaar().onSuccess { res ->
                captainRepository.callbackAadhaar(res.requestId, "mock-aadhaar-assertion-token").onSuccess {
                    loadOnboardingData(step = OnboardingStep.SUBSCRIPTION)
                }.onFailure {
                    loadOnboardingData(step = OnboardingStep.SUBSCRIPTION)
                }
            }.onFailure {
                loadOnboardingData(step = OnboardingStep.SUBSCRIPTION)
            }
        }
    }

    fun selectPlan(planId: String) {
        viewModelScope.launch {
            val current = _uiState.value as? CaptainUiState.Onboarding ?: return@launch
            _uiState.value = current.copy(isLoading = true, errorMessage = null)
            captainRepository.subscribe(planId = planId, paymentMethod = "wallet").onSuccess {
                loadOnboardingData(step = OnboardingStep.STATUS)
            }.onFailure {
                _uiState.value = current.copy(isLoading = false, errorMessage = "Subscription failed: ${it.message}")
            }
        }
    }

    fun refreshOnboardingStatus() {
        loadOnboardingData(step = OnboardingStep.STATUS)
    }

    fun proceedToConsole() {
        viewModelScope.launch {
            val profile = captainRepository.getProfile().getOrNull()
            val sub = captainRepository.getMySubscription().getOrNull()
            _uiState.value = CaptainUiState.Offline(
                captainState = defaultCaptainState.copy(
                    rating = profile?.rating ?: 4.88,
                    totalRidesCompleted = profile?.ridesCompleted ?: 0,
                ),
                profile = profile,
                subscription = sub,
            )
        }
    }

    fun openOnboarding(step: OnboardingStep = OnboardingStep.STATUS) {
        loadOnboardingData(step = step)
    }

    fun toggleOnline() {
        val current = _uiState.value
        when (current) {
            is CaptainUiState.Offline -> {
                viewModelScope.launch {
                    captainRepository.setOnline(true).onSuccess {
                        val updated = current.captainState.copy(isOnline = true)
                        _uiState.value = CaptainUiState.OnlineIdle(captainState = updated)
                        startPollingForIncomingOffers()
                    }.onFailure {
                        _uiState.value = current.copy(errorMessage = "Failed to go online: ${it.message}")
                    }
                }
            }
            is CaptainUiState.OnlineIdle -> {
                stopPollingForIncomingOffers()
                viewModelScope.launch {
                    captainRepository.setOnline(false).onSuccess {
                        val updated = current.captainState.copy(isOnline = false)
                        _uiState.value = CaptainUiState.Offline(captainState = updated)
                    }
                }
            }
            else -> {}
        }
    }

    private fun startPollingForIncomingOffers() {
        offerPollingJob?.cancel()
        offerPollingJob = viewModelScope.launch {
            while (isActive) {
                val state = _uiState.value as? CaptainUiState.OnlineIdle
                if (state != null && state.captainState.isOnline && state.incomingOffer == null) {
                    captainRepository.getIncomingOffers().onSuccess { offers ->
                        if (offers.isNotEmpty()) {
                            _uiState.value = state.copy(incomingOffer = offers.first())
                        }
                    }
                }
                delay(3000L)
            }
        }
    }

    private fun stopPollingForIncomingOffers() {
        offerPollingJob?.cancel()
        offerPollingJob = null
    }

    fun acceptOffer(offer: CaptainOffer) {
        viewModelScope.launch {
            stopPollingForIncomingOffers()
            captainRepository.acceptOffer(offer.id).onSuccess { rideId ->
                val booking = RideBooking(
                    id = rideId,
                    customerUserId = "customer-user-id",
                    partnerId = "my-partner-id",
                    vehicleId = "my-vehicle-id",
                    quoteId = null,
                    revision = 1,
                    vehicleType = VehicleType.BIKE,
                    status = RideStatus.PARTNER_ASSIGNED,
                    pickup = offer.pickup,
                    drop = offer.drop,
                    estimatedFare = offer.estimatedEarnings,
                    finalFare = null,
                    paymentMethod = "cash",
                    otp = null,
                    captain = null,
                    requestedAtEpochMs = System.currentTimeMillis(),
                    completedAtEpochMs = null,
                )
                _uiState.value = CaptainUiState.EnRouteToPickup(booking = booking)
            }.onFailure {
                _uiState.value = CaptainUiState.OnlineIdle(
                    captainState = defaultCaptainState.copy(isOnline = true),
                    errorMessage = "Offer acceptance failed: ${it.message}",
                )
                startPollingForIncomingOffers()
            }
        }
    }

    fun rejectOffer() {
        val state = _uiState.value as? CaptainUiState.OnlineIdle ?: return
        val offer = state.incomingOffer ?: return
        viewModelScope.launch {
            captainRepository.rejectOffer(offer.id)
            _uiState.value = state.copy(incomingOffer = null)
        }
    }

    fun markArrived() {
        val state = _uiState.value as? CaptainUiState.EnRouteToPickup ?: return
        viewModelScope.launch {
            captainRepository.markArrived(state.booking.id).onSuccess {
                _uiState.value = CaptainUiState.VerifyOtp(booking = state.booking.copy(status = RideStatus.ARRIVED))
            }.onFailure {
                _uiState.value = state.copy(errorMessage = "Mark arrived failed: ${it.message}")
            }
        }
    }

    fun onOtpInputChanged(otp: String) {
        val state = _uiState.value as? CaptainUiState.VerifyOtp ?: return
        _uiState.value = state.copy(otpInput = otp, errorMessage = null)
    }

    fun verifyOtpAndStart() {
        val state = _uiState.value as? CaptainUiState.VerifyOtp ?: return
        if (state.otpInput.length < 4) {
            _uiState.value = state.copy(errorMessage = "Please enter complete 4-digit OTP")
            return
        }
        viewModelScope.launch {
            _uiState.value = state.copy(isVerifying = true, errorMessage = null)
            captainRepository.verifyOtpAndStart(state.booking.id, state.otpInput).onSuccess {
                _uiState.value = CaptainUiState.TripInProgress(
                    booking = state.booking.copy(status = RideStatus.IN_PROGRESS),
                )
            }.onFailure {
                _uiState.value = state.copy(
                    isVerifying = false,
                    errorMessage = "OTP verification failed. Check code with rider.",
                )
            }
        }
    }

    fun completeTrip() {
        val state = _uiState.value as? CaptainUiState.TripInProgress ?: return
        viewModelScope.launch {
            _uiState.value = state.copy(isCompleting = true, errorMessage = null)
            val distKM = 4.5
            val durMin = 10
            captainRepository.completeRide(state.booking.id, distKM, durMin).onSuccess {
                _uiState.value = CaptainUiState.CollectPayment(
                    booking = state.booking.copy(status = RideStatus.COMPLETED),
                )
            }.onFailure {
                _uiState.value = state.copy(isCompleting = false, errorMessage = "Complete ride failed: ${it.message}")
            }
        }
    }

    fun confirmCashPayment() {
        val state = _uiState.value as? CaptainUiState.CollectPayment ?: return
        viewModelScope.launch {
            _uiState.value = state.copy(isConfirming = true, errorMessage = null)
            captainRepository.confirmCashPayment(state.booking.id).onSuccess {
                _uiState.value = state.copy(isConfirming = false, cashConfirmed = true)
                delay(1500L)
                _uiState.value = CaptainUiState.OnlineIdle(captainState = defaultCaptainState.copy(isOnline = true))
                startPollingForIncomingOffers()
            }.onFailure {
                _uiState.value = state.copy(isConfirming = false, errorMessage = "Cash confirmation failed: ${it.message}")
            }
        }
    }
}
