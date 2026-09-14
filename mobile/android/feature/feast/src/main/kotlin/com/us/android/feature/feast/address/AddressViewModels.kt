package com.us.android.feature.feast.address

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.FeastAddressDto
import com.us.android.core.food.network.FeastAddressRequest
import com.us.android.core.food.repository.FeastRepository
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.serverMessage
import com.us.android.feature.feast.FeastSession
import com.us.android.feature.feast.ui.errorMessage
import com.us.android.feature.feast.ui.infoMessage
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

data class AddressesUiState(
    val loading: Boolean = true,
    val addresses: List<FeastAddressDto> = emptyList(),
    val selectedId: String? = null,
    val deletingId: String? = null,
    val error: String? = null,
    val message: UsMessage? = null,
)

/** The customer's saved addresses: pick where the food goes, or remove one. */
@HiltViewModel
class AddressesViewModel @Inject constructor(
    private val repository: FeastRepository,
    private val session: FeastSession,
) : ViewModel() {

    private val _state = MutableStateFlow(AddressesUiState())
    val state: StateFlow<AddressesUiState> = _state.asStateFlow()

    fun load() {
        viewModelScope.launch {
            when (val result = repository.addresses()) {
                is FoodResult.Success -> {
                    session.reconcileAddresses(result.value)
                    _state.update {
                        it.copy(loading = false, addresses = result.value, selectedId = session.address.value?.id, error = null)
                    }
                }
                is FoodResult.Failure -> _state.update { it.copy(loading = false, error = describe(result.error)) }
            }
        }
    }

    fun select(address: FeastAddressDto) {
        session.selectAddress(address)
        _state.update { it.copy(selectedId = address.id) }
    }

    fun delete(address: FeastAddressDto) {
        _state.update { it.copy(deletingId = address.id) }
        viewModelScope.launch {
            when (val result = repository.deleteAddress(address.id)) {
                is FoodResult.Success -> {
                    val remaining = _state.value.addresses.filterNot { it.id == address.id }
                    session.reconcileAddresses(remaining)
                    _state.update {
                        it.copy(deletingId = null, addresses = remaining, selectedId = session.address.value?.id)
                    }
                }
                is FoodResult.Failure -> _state.update {
                    it.copy(deletingId = null, message = errorMessage(describe(result.error)))
                }
            }
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }
}

enum class AddressLabel(val wire: String) { HOME("Home"), WORK("Work"), OTHER("Other") }

data class AddAddressUiState(
    val label: AddressLabel = AddressLabel.HOME,
    val line1: String = "",
    val line2: String = "",
    val landmark: String = "",
    val city: String = "",
    val state: String = "",
    val postalCode: String = "",
    val coordinates: Coordinates? = null,
    val step: LocationStep = LocationStep.Idle,
    val errors: Map<String, String> = emptyMap(),
    val locatingAddress: Boolean = false,
    val saving: Boolean = false,
    val saved: FeastAddressDto? = null,
    val message: UsMessage? = null,
)

/**
 * A new delivery address: "Use my current location" (rationale first, always)
 * or typed fields pinned with the platform geocoder.
 *
 * The pin is required: `POST /orders` refuses an address without one
 * (FOOD_ADDRESS_LOCATION_REQUIRED) because range is measured from it.
 */
@HiltViewModel
class AddAddressViewModel @Inject constructor(
    private val repository: FeastRepository,
    private val session: FeastSession,
    private val location: CurrentLocationSource,
    private val lookup: AddressLookup,
) : ViewModel() {

    private val permissionFlow = LocationPermissionFlow()

    private val _state = MutableStateFlow(AddAddressUiState())
    val state: StateFlow<AddAddressUiState> = _state.asStateFlow()

    private val _effects = Channel<LocationEffect>(Channel.BUFFERED)

    /** One-shot requests the screen acts on — only ever [LocationEffect.RequestPermission]. */
    val effects: Flow<LocationEffect> = _effects.receiveAsFlow()

    fun onUseCurrentLocation() = handle(permissionFlow.onUseCurrentLocation(location.hasPermission()))

    fun onRationaleAccepted() = handle(permissionFlow.onRationaleAccepted())

    fun onRationaleDismissed() {
        permissionFlow.onRationaleDismissed()
        syncStep()
    }

    fun onPermissionResult(granted: Boolean, canAskAgain: Boolean) =
        handle(permissionFlow.onPermissionResult(granted, canAskAgain))

    fun onLabel(label: AddressLabel) = _state.update { it.copy(label = label) }

    fun onLine1(value: String) = edit("address_line1") { it.copy(line1 = value) }

    fun onLine2(value: String) = edit("address_line2") { it.copy(line2 = value) }

    fun onLandmark(value: String) = edit("landmark") { it.copy(landmark = value) }

    fun onCity(value: String) = edit("city") { it.copy(city = value) }

    fun onState(value: String) = edit("state") { it.copy(state = value) }

    fun onPostalCode(value: String) = edit("postal_code") { it.copy(postalCode = value.filter(Char::isDigit).take(PIN_LENGTH)) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    /** Pins the typed address — the path for a customer who declined location. */
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
                    it.copy(locatingAddress = false, message = infoMessage("Couldn't find that address. Check it, or use your location."))
                }
            }
        }
    }

    fun save() {
        val s = _state.value
        val errors = buildMap {
            if (s.coordinates == null) put("latitude", "Pin the address: use your location or find the typed address")
            if (s.line1.isBlank()) put("address_line1", "Enter the house or flat and street")
            if (s.city.isBlank()) put("city", "Enter the city")
            if (s.postalCode.isNotBlank() && s.postalCode.length != PIN_LENGTH) put("postal_code", "A PIN code is 6 digits")
        }
        val pin = s.coordinates
        if (errors.isNotEmpty() || pin == null) {
            _state.update { it.copy(errors = errors) }
            return
        }
        _state.update { it.copy(saving = true, errors = emptyMap()) }
        viewModelScope.launch {
            val request = FeastAddressRequest(
                label = s.label.wire,
                addressLine1 = s.line1.trim(),
                city = s.city.trim(),
                country = COUNTRY,
                addressLine2 = s.line2.trim().ifBlank { null },
                landmark = s.landmark.trim().ifBlank { null },
                state = s.state.trim().ifBlank { null },
                postalCode = s.postalCode.ifBlank { null },
                latitude = pin.latitude,
                longitude = pin.longitude,
            )
            when (val result = repository.createAddress(request)) {
                is FoodResult.Success -> {
                    session.selectAddress(result.value)
                    _state.update { it.copy(saving = false, saved = result.value) }
                }
                is FoodResult.Failure -> _state.update {
                    it.copy(saving = false, message = errorMessage(describe(result.error)))
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
            val found = lookup.reverse(fix) ?: return@launch
            _state.update {
                it.copy(
                    line1 = it.line1.ifBlank { found.line1 },
                    line2 = it.line2.ifBlank { found.locality },
                    city = it.city.ifBlank { found.city },
                    state = it.state.ifBlank { found.state },
                    postalCode = it.postalCode.ifBlank { found.postalCode.filter(Char::isDigit).take(PIN_LENGTH) },
                )
            }
        }
    }

    private fun syncStep() = _state.update { it.copy(step = permissionFlow.step) }

    private inline fun edit(field: String, crossinline change: (AddAddressUiState) -> AddAddressUiState) =
        _state.update { change(it).copy(errors = it.errors - field) }

    private companion object {
        const val PIN_LENGTH = 6
        const val COUNTRY = "India"
    }
}

private fun describe(error: FoodError): String = when (error) {
    is FoodError.Network -> "Check your connection and try again."
    else -> error.serverMessage ?: "Something went wrong. Please try again."
}
