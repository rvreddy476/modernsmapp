package com.us.android.feature.commerce.address

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.commerce.model.Address
import com.us.android.core.commerce.repository.CommerceRepository
import com.us.android.core.commerce.repository.CommerceResult
import com.us.android.feature.commerce.ui.describe
import com.us.android.feature.commerce.ui.isRetryable
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface AddressUiState {
    data object Loading : AddressUiState

    data class Content(
        val addresses: List<Address>,
        val selectedId: String?,
    ) : AddressUiState

    /** No saved addresses. The buyer must add one before checkout. */
    data object Empty : AddressUiState

    data class Failed(val message: String, val retryable: Boolean) : AddressUiState
}

/** The new-address form. Validation is local; the server re-validates. */
data class AddAddressForm(
    val label: String = "Home",
    val contactName: String = "",
    val phone: String = "",
    val line1: String = "",
    val line2: String = "",
    val landmark: String = "",
    val city: String = "",
    val state: String = "",
    val postalCode: String = "",
    val saving: Boolean = false,
    val error: String? = null,
) {
    /**
     * Client-side completeness only.
     *
     * This is NOT a claim that the address is deliverable — serviceability is
     * a server decision made against the courier at quote time, and a PIN
     * that looks valid here can still come back unserviceable. Checking the
     * shape locally just avoids a round trip for an obviously empty form.
     */
    val isComplete: Boolean
        get() = contactName.isNotBlank() &&
            phone.trim().length >= MIN_PHONE_DIGITS &&
            line1.isNotBlank() &&
            city.isNotBlank() &&
            state.isNotBlank() &&
            postalCode.trim().length == PIN_LENGTH &&
            postalCode.all { it.isDigit() }

    fun toAddress() = Address(
        id = "",
        label = label.ifBlank { "Home" },
        contactName = contactName.trim(),
        phone = phone.trim(),
        line1 = line1.trim(),
        line2 = line2.trim().takeIf { it.isNotBlank() },
        landmark = landmark.trim().takeIf { it.isNotBlank() },
        city = city.trim(),
        state = state.trim(),
        postalCode = postalCode.trim(),
        isDefault = false,
    )
}

@HiltViewModel
class AddressViewModel @Inject constructor(
    private val repo: CommerceRepository,
) : ViewModel() {

    private val _state = MutableStateFlow<AddressUiState>(AddressUiState.Loading)
    val state: StateFlow<AddressUiState> = _state.asStateFlow()

    private val _form = MutableStateFlow(AddAddressForm())
    val form: StateFlow<AddAddressForm> = _form.asStateFlow()

    init {
        refresh()
    }

    fun refresh() {
        _state.value = AddressUiState.Loading
        viewModelScope.launch { load() }
    }

    private suspend fun load(selectId: String? = null) {
        when (val r = repo.addresses()) {
            is CommerceResult.Failure ->
                _state.value =
                    AddressUiState.Failed(r.error.describe(), r.error.isRetryable())

            is CommerceResult.Success -> {
                val list = r.value
                _state.value = if (list.isEmpty()) {
                    AddressUiState.Empty
                } else {
                    AddressUiState.Content(
                        addresses = list,
                        // Preselect the newly saved one, else the default,
                        // else the only one. Never guess when there are
                        // several and none is marked default.
                        selectedId = selectId
                            ?: list.firstOrNull { it.isDefault }?.id
                            ?: list.singleOrNull()?.id,
                    )
                }
            }
        }
    }

    fun select(addressId: String) {
        val current = _state.value as? AddressUiState.Content ?: return
        _state.value = current.copy(selectedId = addressId)
    }

    fun updateForm(transform: (AddAddressForm) -> AddAddressForm) {
        _form.value = transform(_form.value).copy(error = null)
    }

    /** Saves the form and selects the result. [onSaved] fires only on success. */
    fun saveAddress(onSaved: (Address) -> Unit) {
        val form = _form.value
        if (!form.isComplete || form.saving) return

        _form.value = form.copy(saving = true, error = null)
        viewModelScope.launch {
            when (val r = repo.addAddress(form.toAddress())) {
                is CommerceResult.Failure ->
                    _form.value = form.copy(saving = false, error = r.error.describe())

                is CommerceResult.Success -> {
                    _form.value = AddAddressForm()
                    load(selectId = r.value.id)
                    onSaved(r.value)
                }
            }
        }
    }
}

private const val PIN_LENGTH = 6
private const val MIN_PHONE_DIGITS = 10
