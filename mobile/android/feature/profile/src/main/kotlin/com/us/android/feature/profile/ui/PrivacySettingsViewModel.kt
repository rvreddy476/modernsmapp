package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.PrivacySettings
import com.us.android.core.profile.data.PrivacySettingsRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface PrivacySettingsUiState {
    data object Loading : PrivacySettingsUiState
    data class Error(val message: String) : PrivacySettingsUiState
    data class Editing(
        val original: PrivacySettings,
        val value: PrivacySettings,
        val saving: Boolean = false,
        val message: String? = null,
    ) : PrivacySettingsUiState {
        val dirty get() = original != value
    }
}

enum class PrivacyEnumField { MESSAGE, PHOTO }
enum class PrivacyToggleField { STRICT, TC_POSTS }

@HiltViewModel
class PrivacySettingsViewModel @Inject constructor(
    private val repository: PrivacySettingsRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<PrivacySettingsUiState>(PrivacySettingsUiState.Loading)
    val state: StateFlow<PrivacySettingsUiState> = _state.asStateFlow()
    init { load() }

    fun load() {
        _state.value = PrivacySettingsUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.get()) {
                is AppResult.Success -> PrivacySettingsUiState.Editing(result.data, result.data)
                is AppResult.Failure -> PrivacySettingsUiState.Error("Privacy settings could not be loaded.")
            }
        }
    }

    fun select(field: PrivacyEnumField, value: String) = edit { current ->
        when (field) {
            PrivacyEnumField.MESSAGE -> current.copy(whoCanMessage = value)
            PrivacyEnumField.PHOTO -> current.copy(whoCanSeeProfilePhoto = value)
        }
    }

    fun toggle(field: PrivacyToggleField, enabled: Boolean) = edit { current ->
        when (field) {
            PrivacyToggleField.STRICT -> if (enabled) current.strict() else current.copy(strictPrivacyMode = false)
            PrivacyToggleField.TC_POSTS -> current.copy(trustedCircleCloseFriendsPosts = enabled)
        }
    }

    fun save() {
        val current = _state.value as? PrivacySettingsUiState.Editing ?: return
        if (!current.dirty || current.saving) return
        _state.value = current.copy(saving = true, message = null)
        viewModelScope.launch {
            _state.value = when (val result = repository.save(current.value)) {
                is AppResult.Success -> PrivacySettingsUiState.Editing(
                    result.data,
                    result.data,
                    message = "Privacy settings saved.",
                )
                is AppResult.Failure -> current.copy(saving = false, message = "Nothing changed. Please try again.")
            }
        }
    }

    private fun edit(block: (PrivacySettings) -> PrivacySettings) = _state.update { state ->
        val editing = state as? PrivacySettingsUiState.Editing ?: return@update state
        if (editing.saving) {
            return@update editing
        }
        editing.copy(value = block(editing.value), message = null)
    }
}

private fun PrivacySettings.strict() = copy(
    strictPrivacyMode = true,
    whoCanMessage = "connections_only",
    whoCanCall = "no_one",
    whoCanAddToGroups = "no_one",
    whoCanSeeOnlineStatus = "connections_only",
    whoCanSeeReadReceipts = "connections_only",
    whoCanSeeLastSeen = "connections_only",
    whoCanSeeProfilePhoto = "connections_only",
    blockUnknownCalls = true,
    autoFilterAbusiveContent = true,
    allowPhoneDiscovery = false,
    discoverableByPhoneToContacts = false,
)
