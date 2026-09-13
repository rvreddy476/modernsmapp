package com.us.android.feature.rider.payout

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.PayoutAccountDto
import com.us.android.core.food.network.PayoutAccountRequest
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.feature.rider.kyc.BankAccountNumber
import com.us.android.feature.rider.kyc.Ifsc
import com.us.android.feature.rider.kycui.BankAccountFormRules
import com.us.android.feature.rider.kycui.BankAccountFormState
import com.us.android.feature.rider.kycui.BankField
import com.us.android.feature.rider.ui.asMessage
import com.us.android.feature.rider.ui.success
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class PayoutUiState(
    val loading: Boolean = true,
    val existing: PayoutAccountDto? = null,
    val replacing: Boolean = false,
    val form: BankAccountFormState = BankAccountFormState(),
    val errors: Map<BankField, String> = emptyMap(),
    val saving: Boolean = false,
    val message: UsMessage? = null,
) {
    val showForm: Boolean get() = !loading && (existing == null || replacing)
}

/**
 * The rider's payout account: `GET`/`PUT /v1/food/delivery/payout-account`.
 * After a save the typed number is dropped; only the masked one is shown.
 */
@HiltViewModel
class PayoutViewModel @Inject constructor(
    private val food: FoodRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(PayoutUiState())
    val state: StateFlow<PayoutUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            when (val result = food.getDeliveryPayoutAccount()) {
                is FoodResult.Success -> _state.update { it.copy(loading = false, existing = result.value) }
                is FoodResult.Failure -> _state.update {
                    if (result.error == FoodError.NotFound) it.copy(loading = false) else it.copy(loading = false, message = result.error.asMessage())
                }
            }
        }
    }

    fun onHolderName(value: String) = edit(BankField.HOLDER) { it.copy(holderName = value) }

    fun onAccountNumber(value: String) = edit(BankField.ACCOUNT) { it.copy(accountNumber = value) }

    fun onConfirmAccountNumber(value: String) = edit(BankField.CONFIRM) { it.copy(confirmAccountNumber = value) }

    fun onIfsc(value: String) = edit(BankField.IFSC) { it.copy(ifsc = value) }

    fun startReplacing() = _state.update { it.copy(replacing = true, form = BankAccountFormState(), errors = emptyMap()) }

    fun cancelReplacing() = _state.update { it.copy(replacing = false, form = BankAccountFormState(), errors = emptyMap()) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun save() {
        val form = _state.value.form
        val errors = BankAccountFormRules.validate(form)
        val account = BankAccountNumber.normalize(form.accountNumber)
        val ifsc = Ifsc.normalize(form.ifsc)
        if (errors.isNotEmpty() || account == null || ifsc == null) {
            _state.update { it.copy(errors = errors) }
            return
        }
        _state.update { it.copy(saving = true, errors = emptyMap()) }
        viewModelScope.launch {
            val request = PayoutAccountRequest(holderName = form.holderName.trim(), accountNumber = account, ifsc = ifsc)
            when (val result = food.putDeliveryPayoutAccount(request)) {
                is FoodResult.Success -> _state.update {
                    it.copy(saving = false, existing = result.value, replacing = false, form = BankAccountFormState(), message = success("Bank account saved"))
                }
                is FoodResult.Failure -> _state.update { current ->
                    val error = result.error
                    val field = BankAccountFormRules.fieldFor((error as? FoodError.InvalidField)?.field)
                    if (error is FoodError.InvalidField && field != null) {
                        current.copy(saving = false, errors = mapOf(field to error.message))
                    } else {
                        current.copy(saving = false, message = error.asMessage())
                    }
                }
            }
        }
    }

    private inline fun edit(field: BankField, crossinline change: (BankAccountFormState) -> BankAccountFormState) {
        _state.update { it.copy(form = change(it.form), errors = it.errors - field) }
    }
}
