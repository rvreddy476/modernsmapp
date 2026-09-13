package com.us.android.feature.rider.profile

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.DeliveryPartnerDto
import com.us.android.core.food.network.DeliveryPartnerRequest
import com.us.android.core.food.repository.FoodResult
import com.us.android.core.food.repository.RiderRepository
import com.us.android.feature.rider.kyc.VehicleRegistration
import com.us.android.feature.rider.onboarding.Vehicle
import com.us.android.feature.rider.onboarding.VehicleClass
import com.us.android.feature.rider.ui.asMessage
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

data class ProfileForm(
    val fullName: String = "",
    val phone: String = "",
    val vehicleType: String = "",
    val vehicleNumber: String = "",
    val city: String = "",
)

enum class ProfileField { NAME, PHONE, VEHICLE_TYPE, VEHICLE_NUMBER }

data class ProfileUiState(
    val loading: Boolean = true,
    val existing: DeliveryPartnerDto? = null,
    val form: ProfileForm = ProfileForm(),
    val errors: Map<ProfileField, String> = emptyMap(),
    val saving: Boolean = false,
    val message: UsMessage? = null,
) {
    /** A bicycle rider is asked for no registration number, and owes no DL or RC. */
    val needsRegistration: Boolean get() = Vehicle.classify(form.vehicleType) != VehicleClass.BICYCLE
}

/** Pure form rules, mirrored from what riderkyc and the profile upsert accept. */
object ProfileRules {
    fun validate(form: ProfileForm): Map<ProfileField, String> = buildMap {
        if (form.fullName.isBlank()) put(ProfileField.NAME, "Enter your name as it is on your documents")
        if (normalizedPhone(form.phone) == null) put(ProfileField.PHONE, "Enter a 10-digit mobile number")
        when (Vehicle.classify(form.vehicleType)) {
            VehicleClass.UNKNOWN -> put(ProfileField.VEHICLE_TYPE, "Choose what you ride")
            VehicleClass.MOTORISED -> if (VehicleRegistration.parse(form.vehicleNumber) == null) {
                put(ProfileField.VEHICLE_NUMBER, VehicleRegistration.INVALID_MESSAGE)
            }
            VehicleClass.BICYCLE -> Unit
        }
    }

    /** `98765 43210`, `+91 98765 43210` → `9876543210`. Null unless ten digits remain. */
    fun normalizedPhone(input: String): String? {
        val digits = input.filter(Char::isDigit).let { if (it.length == INDIA_PREFIXED && it.startsWith("91")) it.drop(2) else it }
        return digits.takeIf { it.length == MOBILE_DIGITS }
    }

    fun request(form: ProfileForm): DeliveryPartnerRequest = DeliveryPartnerRequest(
        fullName = form.fullName.trim(),
        phone = normalizedPhone(form.phone).orEmpty(),
        vehicleType = form.vehicleType,
        vehicleNumber = if (Vehicle.classify(form.vehicleType) == VehicleClass.BICYCLE) {
            ""
        } else {
            VehicleRegistration.parse(form.vehicleNumber)?.normalized.orEmpty()
        },
        city = form.city.trim(),
    )

    private const val MOBILE_DIGITS = 10
    private const val INDIA_PREFIXED = 12
}

/** Creates the partner profile ("become a rider") or edits it. The server replaces every field on save. */
@HiltViewModel
class ProfileViewModel @Inject constructor(
    private val rider: RiderRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(ProfileUiState())
    val state: StateFlow<ProfileUiState> = _state.asStateFlow()

    private val saved = Channel<Unit>(Channel.CONFLATED)
    val savedEvents: Flow<Unit> = saved.receiveAsFlow()

    init {
        viewModelScope.launch {
            when (val result = rider.profile()) {
                is FoodResult.Success -> {
                    val p = result.value
                    _state.update {
                        it.copy(
                            loading = false,
                            existing = p,
                            form = if (p == null) it.form else ProfileForm(p.fullName, p.phone, p.vehicleType.uppercase(), p.vehicleNumber, p.city),
                        )
                    }
                }
                is FoodResult.Failure -> _state.update { it.copy(loading = false, message = result.error.asMessage()) }
            }
        }
    }

    fun edit(field: ProfileField?, change: (ProfileForm) -> ProfileForm) {
        _state.update { it.copy(form = change(it.form), errors = if (field == null) it.errors else it.errors - field) }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    fun save() {
        val current = _state.value
        val errors = ProfileRules.validate(current.form)
        if (errors.isNotEmpty()) {
            _state.update { it.copy(errors = errors) }
            return
        }
        _state.update { it.copy(saving = true, errors = emptyMap()) }
        viewModelScope.launch {
            val request = ProfileRules.request(current.form)
            val result = if (current.existing == null) rider.createProfile(request) else rider.updateProfile(request)
            when (result) {
                is FoodResult.Success -> {
                    _state.update { it.copy(saving = false, existing = result.value) }
                    saved.trySend(Unit)
                }
                is FoodResult.Failure -> _state.update { it.copy(saving = false, message = result.error.asMessage()) }
            }
        }
    }
}
