package com.us.android.feature.kitchen.location

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.LocationDto
import com.us.android.core.food.network.LocationRequest
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.success
import com.us.android.feature.kitchen.ui.warning
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.channels.Channel
import kotlinx.coroutines.flow.Flow
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.receiveAsFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject
import kotlin.math.roundToInt

data class LocationUiState(
    val line1: String = "",
    val line2: String = "",
    val city: String = "",
    val state: String = "",
    val postalCode: String = "",
    val radiusKm: Float = DEFAULT_RADIUS_KM,
    val coordinates: Coordinates? = null,
    val step: LocationStepState = LocationStepState.Idle,
    /** Keyed by the request field name food-service reports (`address_line1`, `state`, …). */
    val errors: Map<String, String> = emptyMap(),
    val locatingAddress: Boolean = false,
    val saving: Boolean = false,
    val saved: LocationDto? = null,
    val message: UsMessage? = null,
)

/**
 * The location step: a pin (current location, or the typed address geocoded),
 * the address, the state and the delivery radius → `PUT …/location`.
 *
 * ROUTE GAP: there is no read of a saved location, so the form starts empty.
 */
@HiltViewModel
class LocationViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val food: FoodRepository,
    private val location: CurrentLocationSource,
    private val lookup: AddressLookup,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()
    private val permissionFlow = LocationPermissionFlow()

    private val _state = MutableStateFlow(LocationUiState())
    val state: StateFlow<LocationUiState> = _state.asStateFlow()

    private val _effects = Channel<LocationEffect>(Channel.BUFFERED)

    /** One-shot requests the screen must act on — only ever [LocationEffect.RequestPermission]. */
    val effects: Flow<LocationEffect> = _effects.receiveAsFlow()

    fun onUseCurrentLocation() = handle(permissionFlow.onUseCurrentLocation(location.hasPermission()))

    fun onRationaleAccepted() = handle(permissionFlow.onRationaleAccepted())

    fun onRationaleDismissed() {
        permissionFlow.onRationaleDismissed()
        syncStep()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) =
        handle(permissionFlow.onPermissionResult(granted, canAskAgain))

    fun onLine1Change(value: String) = edit("address_line1") { it.copy(line1 = value) }

    fun onLine2Change(value: String) = edit("address_line2") { it.copy(line2 = value) }

    fun onCityChange(value: String) = edit("city") { it.copy(city = value) }

    fun onPostalCodeChange(value: String) = edit("postal_code") { it.copy(postalCode = value.filter(Char::isDigit).take(PIN_LENGTH)) }

    fun onStateChange(value: String) = edit("state") { it.copy(state = value) }

    fun onRadiusChange(value: Float) = edit("delivery_radius_km") {
        it.copy(radiusKm = ((value * 2f).roundToInt() / 2f).coerceIn(MIN_RADIUS_KM, MAX_RADIUS_KM))
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    /** Geocodes the typed address into the pin — the path for a partner who declined location. */
    fun locateFromAddress() {
        val s = _state.value
        val query = listOf(s.line1, s.line2, s.city, s.state, s.postalCode).filter { it.isNotBlank() }.joinToString(", ")
        if (query.isBlank()) {
            _state.update { it.copy(errors = it.errors + ("address_line1" to "Type the address first")) }
            return
        }
        _state.update { it.copy(locatingAddress = true) }
        viewModelScope.launch {
            val found = lookup.forward("$query, India")
            _state.update {
                if (found != null) {
                    it.copy(locatingAddress = false, coordinates = found, errors = it.errors - "latitude")
                } else {
                    it.copy(
                        locatingAddress = false,
                        message = warning("Couldn't find that address. Check it, or use your current location."),
                    )
                }
            }
        }
    }

    fun save() {
        val s = _state.value
        val errors = buildMap {
            if (s.coordinates == null) put("latitude", "Pin the kitchen: use your current location or find the address")
            if (s.line1.isBlank()) put("address_line1", "Enter the building and street")
            if (s.city.isBlank()) put("city", "Enter the city")
            if (s.state.isBlank()) put("state", "Choose the state")
            if (s.postalCode.isNotBlank() && s.postalCode.length != PIN_LENGTH) put("postal_code", "A PIN code is 6 digits")
        }
        val pin = s.coordinates
        if (errors.isNotEmpty() || pin == null) {
            _state.update { it.copy(errors = errors) }
            return
        }
        _state.update { it.copy(saving = true, errors = emptyMap()) }
        viewModelScope.launch {
            val request = LocationRequest(
                latitude = pin.latitude,
                longitude = pin.longitude,
                addressLine1 = s.line1.trim(),
                addressLine2 = s.line2.trim().ifBlank { null },
                city = s.city.trim(),
                state = s.state,
                postalCode = s.postalCode.trim(),
                deliveryRadiusKm = s.radiusKm.toDouble(),
            )
            when (val result = food.putLocation(restaurantId, request)) {
                is FoodResult.Success -> _state.update {
                    it.copy(saving = false, saved = result.value, message = success("Location saved"))
                }
                is FoodResult.Failure -> _state.update { current ->
                    val error = result.error
                    if (error is FoodError.InvalidField && error.field != null) {
                        current.copy(saving = false, errors = mapOf(error.field!! to error.message))
                    } else {
                        current.copy(saving = false, message = error.asMessage())
                    }
                }
            }
        }
    }

    private fun handle(effect: LocationEffect) {
        syncStep()
        when (effect) {
            LocationEffect.RequestPermission -> _effects.trySend(effect)
            LocationEffect.FetchLocation -> fetchFix()
            LocationEffect.None -> Unit
        }
    }

    private fun fetchFix() {
        viewModelScope.launch {
            val fix = location.current()
            permissionFlow.onLocationResult(fix)
            syncStep()
            if (fix == null) return@launch
            _state.update { it.copy(coordinates = fix, errors = it.errors - "latitude") }
            val address = lookup.reverse(fix) ?: return@launch
            _state.update {
                it.copy(
                    line1 = it.line1.ifBlank { address.line1 },
                    city = it.city.ifBlank { address.city },
                    state = it.state.ifBlank { address.state },
                    postalCode = it.postalCode.ifBlank { address.postalCode.filter(Char::isDigit).take(PIN_LENGTH) },
                )
            }
        }
    }

    private fun syncStep() {
        _state.update { it.copy(step = permissionFlow.state) }
    }

    private inline fun edit(field: String, crossinline change: (LocationUiState) -> LocationUiState) {
        _state.update { change(it).copy(errors = it.errors - field) }
    }
}

const val MIN_RADIUS_KM = 1f
const val MAX_RADIUS_KM = 15f
private const val DEFAULT_RADIUS_KM = 5f
private const val PIN_LENGTH = 6
