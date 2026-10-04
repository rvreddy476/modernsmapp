package com.us.android.feature.doorstep.address

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.feature.doorstep.DoorstepSession
import com.us.android.feature.doorstep.data.AddressDto
import com.us.android.feature.doorstep.data.AddressInputDto
import com.us.android.feature.doorstep.data.DoorstepCodes
import com.us.android.feature.doorstep.data.DoorstepRepository
import com.us.android.feature.doorstep.data.DoorstepResult
import com.us.android.feature.doorstep.data.code
import com.us.android.feature.doorstep.data.userMessage
import com.us.android.feature.doorstep.ui.errorMessage
import com.us.android.feature.doorstep.ui.infoMessage
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
    val addresses: List<AddressDto> = emptyList(),
    val selectedId: String? = null,
    val deletingId: String? = null,
    val error: String? = null,
    val message: UsMessage? = null,
)

/** The customer's Doorstep addresses (`/v1/doorstep/addresses`, not Feast's): pick where the visit is, or remove one. */
@HiltViewModel
class AddressesViewModel @Inject constructor(
    private val repository: DoorstepRepository,
    private val session: DoorstepSession,
) : ViewModel() {

    private val _state = MutableStateFlow(AddressesUiState())
    val state: StateFlow<AddressesUiState> = _state.asStateFlow()

    fun load() {
        viewModelScope.launch {
            when (val result = repository.addresses()) {
                is DoorstepResult.Success -> {
                    session.reconcileAddresses(result.value)
                    _state.update {
                        it.copy(loading = false, addresses = result.value, selectedId = session.address.value?.id, error = null)
                    }
                }
                is DoorstepResult.Failure -> _state.update { it.copy(loading = false, error = result.error.userMessage()) }
            }
        }
    }

    fun select(address: AddressDto) {
        session.selectAddress(address)
        _state.update { it.copy(selectedId = address.id) }
    }

    fun delete(address: AddressDto) {
        _state.update { it.copy(deletingId = address.id) }
        viewModelScope.launch {
            when (val result = repository.deleteAddress(address.id)) {
                is DoorstepResult.Success -> {
                    val remaining = _state.value.addresses.filterNot { it.id == address.id }
                    session.reconcileAddresses(remaining)
                    _state.update { it.copy(deletingId = null, addresses = remaining, selectedId = session.address.value?.id) }
                }
                is DoorstepResult.Failure -> _state.update {
                    it.copy(deletingId = null, message = errorMessage(result.error.userMessage()))
                }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }
}

enum class AddressLabel(val wire: String) { HOME("Home"), WORK("Work"), OTHER("Other") }

data class AddAddressUiState(
    val label: AddressLabel = AddressLabel.HOME,
    val line1: String = "",
    val line2: String = "",
    val landmark: String = "",
    val locality: String = "",
    val pincode: String = "",
    val coordinates: Coordinates? = null,
    val step: LocationStep = LocationStep.Idle,
    val errors: Map<String, String> = emptyMap(),
    val locatingAddress: Boolean = false,
    val saving: Boolean = false,
    val saved: AddressDto? = null,
    val message: UsMessage? = null,
)

/**
 * A new visit address — Feast's flow, copied: "Use my current location" with
 * the rationale ALWAYS before the system prompt, or typed fields pinned with
 * the platform geocoder. The pin is required: the server checks the point is
 * inside a service zone (DOORSTEP_OUTSIDE_SERVICE_AREA) and the professional
 * navigates to it. Only the locality is shown to a professional before they
 * accept the job.
 */
@HiltViewModel
class AddAddressViewModel @Inject constructor(
    private val repository: DoorstepRepository,
    private val session: DoorstepSession,
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

    fun onLine1(value: String) = edit("line1") { it.copy(line1 = value) }

    fun onLine2(value: String) = edit("line2") { it.copy(line2 = value) }

    fun onLandmark(value: String) = edit("landmark") { it.copy(landmark = value) }

    fun onLocality(value: String) = edit("locality") { it.copy(locality = value) }

    fun onPincode(value: String) = edit("pincode") { it.copy(pincode = value.filter(Char::isDigit).take(PIN_LENGTH)) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    /** Pins the typed address — the path for a customer who declined location. */
    fun locateFromAddress() {
        val s = _state.value
        val query = listOf(s.line1, s.line2, s.locality, s.pincode).filter { it.isNotBlank() }.joinToString(", ")
        if (query.isBlank()) {
            _state.update { it.copy(errors = it.errors + ("line1" to "Type the address first")) }
            return
        }
        _state.update { it.copy(locatingAddress = true) }
        viewModelScope.launch {
            val found = lookup.forward("$query, India")
            _state.update {
                if (found != null) {
                    it.copy(locatingAddress = false, coordinates = found, errors = it.errors - "pin")
                } else {
                    it.copy(locatingAddress = false, message = infoMessage("Couldn't find that address. Check it, or use your location."))
                }
            }
        }
    }

    fun save() {
        val s = _state.value
        val errors = buildMap {
            if (s.coordinates == null) put("pin", "Pin the address: use your location or find the typed address")
            if (s.line1.isBlank()) put("line1", "Enter the house or flat and street")
            if (s.locality.isBlank()) put("locality", "Enter the area or locality")
            if (s.pincode.length != PIN_LENGTH) put("pincode", "A PIN code is 6 digits")
        }
        val pin = s.coordinates
        if (errors.isNotEmpty() || pin == null) {
            _state.update { it.copy(errors = errors) }
            return
        }
        _state.update { it.copy(saving = true, errors = emptyMap()) }
        viewModelScope.launch {
            val input = AddressInputDto(
                label = s.label.wire,
                line1 = s.line1.trim(),
                line2 = s.line2.trim().ifBlank { null },
                landmark = s.landmark.trim().ifBlank { null },
                locality = s.locality.trim(),
                pincode = s.pincode,
                lat = pin.latitude,
                lng = pin.longitude,
            )
            when (val result = repository.createAddress(input)) {
                is DoorstepResult.Success -> {
                    session.selectAddress(result.value)
                    _state.update { it.copy(saving = false, saved = result.value) }
                }
                is DoorstepResult.Failure -> _state.update {
                    val text = if (result.error.code == DoorstepCodes.OUTSIDE_SERVICE_AREA) {
                        "Doorstep doesn't serve this address yet. Try another address in the city."
                    } else {
                        result.error.userMessage()
                    }
                    it.copy(saving = false, message = errorMessage(text))
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
            _state.update { it.copy(coordinates = fix, errors = it.errors - "pin") }
            val found = lookup.reverse(fix) ?: return@launch
            _state.update {
                it.copy(
                    line1 = it.line1.ifBlank { found.line1 },
                    locality = it.locality.ifBlank { found.locality.ifBlank { found.city } },
                    pincode = it.pincode.ifBlank { found.postalCode.filter(Char::isDigit).take(PIN_LENGTH) },
                )
            }
        }
    }

    private fun syncStep() = _state.update { it.copy(step = permissionFlow.step) }

    private inline fun edit(field: String, crossinline change: (AddAddressUiState) -> AddAddressUiState) =
        _state.update { change(it).copy(errors = it.errors - field) }

    private companion object {
        const val PIN_LENGTH = 6
    }
}
