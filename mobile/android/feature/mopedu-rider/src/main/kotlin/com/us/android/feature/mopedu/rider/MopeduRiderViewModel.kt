package com.us.android.feature.mopedu.rider

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.mobility.model.CaptainInfo
import com.us.android.core.mobility.model.GeoPoint
import com.us.android.core.mobility.model.QuoteOption
import com.us.android.core.mobility.model.QuoteSnapshot
import com.us.android.core.mobility.model.RideBooking
import com.us.android.core.mobility.model.RideReceipt
import com.us.android.core.mobility.model.RideStatus
import com.us.android.feature.mopedu.rider.data.MopeduRiderRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.Job
import kotlinx.coroutines.delay
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.isActive
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface RiderUiState {
    data class LocationSelect(
        val pickup: GeoPoint,
        val drop: GeoPoint,
        val isLoading: Boolean = false,
        val error: String? = null,
    ) : RiderUiState

    data class QuoteSelect(
        val quote: QuoteSnapshot,
        val selectedOption: QuoteOption,
        val isBooking: Boolean = false,
        val error: String? = null,
    ) : RiderUiState

    data class SearchingCaptain(
        val booking: RideBooking,
        val searchElapsedSeconds: Int = 0,
        val error: String? = null,
    ) : RiderUiState

    data class CaptainAssigned(
        val booking: RideBooking,
        val captainLocation: GeoPoint,
        val etaMinutes: Int,
    ) : RiderUiState

    data class ArrivedAtPickup(
        val booking: RideBooking,
        val otp: String,
    ) : RiderUiState

    data class TripInProgress(
        val booking: RideBooking,
        val currentSpeedMps: Double = 0.0,
        val remainingDistanceMeters: Int = 0,
        val shareLink: String? = null,
        val sosTriggered: Boolean = false,
    ) : RiderUiState

    data class TripCompleted(
        val receipt: RideReceipt,
        val ratingSubmitted: Boolean = false,
    ) : RiderUiState
}

@HiltViewModel
class MopeduRiderViewModel @Inject constructor(
    private val riderRepository: MopeduRiderRepository,
) : ViewModel() {

    private val initialPickup = GeoPoint(12.9716, 77.5946, "MG Road Metro, Bengaluru", "Pickup Location")
    private val initialDrop = GeoPoint(12.9352, 77.6245, "Koramangala 5th Block, Bengaluru", "Dropoff Location")

    private val _uiState = MutableStateFlow<RiderUiState>(
        RiderUiState.LocationSelect(
            pickup = initialPickup,
            drop = initialDrop,
        )
    )
    val uiState: StateFlow<RiderUiState> = _uiState.asStateFlow()

    private var activeRidePollingJob: Job? = null

    init {
        checkActiveRide()
    }

    fun checkActiveRide() {
        viewModelScope.launch {
            riderRepository.getActiveRide().onSuccess { booking ->
                if (booking != null) {
                    syncActiveBookingState(booking)
                    startActiveRidePolling(booking.id)
                }
            }
        }
    }

    fun onSelectPickup(pickup: GeoPoint) {
        val current = _uiState.value
        if (current is RiderUiState.LocationSelect) {
            _uiState.value = current.copy(pickup = pickup, error = null)
        }
    }

    fun onSelectDrop(drop: GeoPoint) {
        val current = _uiState.value
        if (current is RiderUiState.LocationSelect) {
            _uiState.value = current.copy(drop = drop, error = null)
        }
    }

    fun requestQuote() {
        val current = _uiState.value as? RiderUiState.LocationSelect ?: return
        _uiState.value = current.copy(isLoading = true, error = null)
        viewModelScope.launch {
            riderRepository.getQuote(current.pickup, current.drop)
                .onSuccess { quote ->
                    val defaultOption = quote.options.firstOrNull { it.available }
                        ?: quote.options.firstOrNull()
                    if (defaultOption != null) {
                        _uiState.value = RiderUiState.QuoteSelect(
                            quote = quote,
                            selectedOption = defaultOption,
                        )
                    } else {
                        _uiState.value = current.copy(isLoading = false, error = "No available vehicles found for this route")
                    }
                }
                .onFailure {
                    _uiState.value = current.copy(isLoading = false, error = "Failed to fetch estimate: ${it.message ?: "Network error"}")
                }
        }
    }

    fun selectVehicleOption(option: QuoteOption) {
        val current = _uiState.value as? RiderUiState.QuoteSelect ?: return
        _uiState.value = current.copy(selectedOption = option, error = null)
    }

    fun confirmBooking() {
        val current = _uiState.value as? RiderUiState.QuoteSelect ?: return
        _uiState.value = current.copy(isBooking = true, error = null)
        viewModelScope.launch {
            riderRepository.bookRide(
                quoteId = current.quote.quoteId,
                pickup = current.quote.pickup,
                drop = current.quote.drop,
                vehicleType = current.selectedOption.vehicleType,
            ).onSuccess { booking ->
                _uiState.value = RiderUiState.SearchingCaptain(booking = booking)
                startActiveRidePolling(booking.id)
            }.onFailure {
                _uiState.value = current.copy(isBooking = false, error = "Booking failed: ${it.message ?: "Please retry"}")
            }
        }
    }

    private fun startActiveRidePolling(rideId: String) {
        activeRidePollingJob?.cancel()
        activeRidePollingJob = viewModelScope.launch {
            while (isActive) {
                delay(2500)
                riderRepository.getActiveRide().onSuccess { booking ->
                    if (booking != null && booking.id == rideId) {
                        syncActiveBookingState(booking)
                        if (booking.status == RideStatus.COMPLETED) {
                            fetchReceipt(rideId)
                            return@launch
                        }
                    }
                }
            }
        }
    }

    private fun syncActiveBookingState(booking: RideBooking) {
        when (booking.status) {
            RideStatus.SEARCHING_PARTNER, RideStatus.REQUESTED -> {
                val current = _uiState.value
                val elapsed = if (current is RiderUiState.SearchingCaptain) current.searchElapsedSeconds + 2 else 0
                _uiState.value = RiderUiState.SearchingCaptain(booking = booking, searchElapsedSeconds = elapsed)
            }
            RideStatus.PARTNER_ASSIGNED, RideStatus.PARTNER_ARRIVING -> {
                _uiState.value = RiderUiState.CaptainAssigned(
                    booking = booking,
                    captainLocation = booking.pickup,
                    etaMinutes = 3,
                )
            }
            RideStatus.ARRIVED -> {
                _uiState.value = RiderUiState.ArrivedAtPickup(
                    booking = booking,
                    otp = booking.otp ?: "",
                )
            }
            RideStatus.IN_PROGRESS -> {
                val current = _uiState.value
                val share = (current as? RiderUiState.TripInProgress)?.shareLink
                val sos = (current as? RiderUiState.TripInProgress)?.sosTriggered ?: false
                _uiState.value = RiderUiState.TripInProgress(
                    booking = booking,
                    currentSpeedMps = 8.5,
                    remainingDistanceMeters = 3200,
                    shareLink = share,
                    sosTriggered = sos,
                )
            }
            RideStatus.COMPLETED -> {
                fetchReceipt(booking.id)
            }
            else -> {}
        }
    }

    fun triggerSOS() {
        val current = _uiState.value as? RiderUiState.TripInProgress ?: return
        viewModelScope.launch {
            riderRepository.triggerSOS(current.booking.id, current.booking.pickup.lat, current.booking.pickup.lng)
        }
        _uiState.value = current.copy(sosTriggered = true)
    }

    fun generateShareLink() {
        val current = _uiState.value as? RiderUiState.TripInProgress ?: return
        viewModelScope.launch {
            riderRepository.createShareLink(current.booking.id).onSuccess { link ->
                _uiState.value = current.copy(shareLink = link)
            }
        }
    }

    fun fetchReceipt(rideId: String) {
        activeRidePollingJob?.cancel()
        viewModelScope.launch {
            riderRepository.getReceipt(rideId).onSuccess { receipt ->
                _uiState.value = RiderUiState.TripCompleted(receipt = receipt)
            }
        }
    }

    fun submitRating(rating: Int, feedback: String) {
        val current = _uiState.value as? RiderUiState.TripCompleted ?: return
        viewModelScope.launch {
            riderRepository.rateRide(current.receipt.rideId, rating, feedback)
        }
        _uiState.value = current.copy(ratingSubmitted = true)
    }

    fun resetToNewBooking() {
        activeRidePollingJob?.cancel()
        _uiState.value = RiderUiState.LocationSelect(
            pickup = initialPickup,
            drop = initialDrop,
        )
    }

    override fun onCleared() {
        super.onCleared()
        activeRidePollingJob?.cancel()
    }
}
