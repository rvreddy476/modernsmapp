package com.us.android.feature.settings.account

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.AccountSummary
import com.us.android.core.profile.data.ManageAccountRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface ManageAccountUiState {
    data object Loading : ManageAccountUiState
    data class Error(val message: String) : ManageAccountUiState
    data class Loaded(
        val account: AccountSummary,
        val region: String,
        val savingRegion: Boolean = false,
        val message: String? = null,
    ) : ManageAccountUiState
}

/** Identity facts from auth-service plus the region from user-service — one screen, one repository. */
@HiltViewModel
class ManageAccountViewModel @Inject constructor(
    private val repository: ManageAccountRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<ManageAccountUiState>(ManageAccountUiState.Loading)
    val state: StateFlow<ManageAccountUiState> = _state.asStateFlow()

    init { load() }

    fun load() {
        _state.value = ManageAccountUiState.Loading
        viewModelScope.launch {
            val account = repository.account()
            val region = repository.region()
            _state.value = if (account is AppResult.Success && region is AppResult.Success) {
                ManageAccountUiState.Loaded(account.data, region.data)
            } else {
                ManageAccountUiState.Error("Account details could not be loaded.")
            }
        }
    }

    /** The region select row commits immediately — there is no separate Save on this screen. */
    fun setRegion(countryCode: String) {
        val current = _state.value as? ManageAccountUiState.Loaded ?: return
        if (current.savingRegion || countryCode == current.region) return
        _state.update { (it as? ManageAccountUiState.Loaded)?.copy(savingRegion = true, message = null) ?: it }
        viewModelScope.launch {
            _state.value = when (val result = repository.updateRegion(countryCode)) {
                is AppResult.Success -> current.copy(region = result.data, savingRegion = false)
                is AppResult.Failure ->
                    current.copy(savingRegion = false, message = "Region could not be updated. Please try again.")
            }
        }
    }
}
