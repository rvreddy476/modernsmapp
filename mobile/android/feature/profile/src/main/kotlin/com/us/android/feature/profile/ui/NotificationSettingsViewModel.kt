package com.us.android.feature.profile.ui

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.NotificationCategory
import com.us.android.core.profile.data.NotificationChannels
import com.us.android.core.profile.data.NotificationSettings
import com.us.android.core.profile.data.NotificationSettingsRepository
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface NotificationSettingsUiState {
    data object Loading : NotificationSettingsUiState

    data class Error(val message: String) : NotificationSettingsUiState

    data class Editing(
        val original: NotificationSettings,
        val value: NotificationSettings,
        /** Whether the "More" section of secondary categories is expanded. */
        val moreExpanded: Boolean = false,
        val saving: Boolean = false,
        val message: String? = null,
    ) : NotificationSettingsUiState {
        val dirty get() = original != value
        val quietHoursValid get() = !value.quietHoursEnabled || (
            TIME.matches(value.quietHoursStart) &&
                TIME.matches(value.quietHoursEnd) &&
                value.quietHoursTimeZone.isNotBlank()
            )

        companion object {
            val TIME = Regex("^(?:[01]\\d|2[0-3]):[0-5]\\d$")
        }
    }
}

@HiltViewModel
class NotificationSettingsViewModel @Inject constructor(
    private val repository: NotificationSettingsRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<NotificationSettingsUiState>(NotificationSettingsUiState.Loading)
    val state: StateFlow<NotificationSettingsUiState> = _state.asStateFlow()
    init {
        load()
    }

    fun load() {
        _state.value = NotificationSettingsUiState.Loading
        viewModelScope.launch {
            _state.value = when (val result = repository.get()) {
                is AppResult.Success -> NotificationSettingsUiState.Editing(result.data, result.data)
                is AppResult.Failure -> NotificationSettingsUiState.Error("Notification settings could not be loaded.")
            }
        }
    }

    fun toggleGlobal(push: Boolean? = null, email: Boolean? = null, quiet: Boolean? = null) = edit {
        it.copy(
            pushEnabled = push ?: it.pushEnabled,
            emailEnabled = email ?: it.emailEnabled,
            quietHoursEnabled = quiet ?: it.quietHoursEnabled,
        )
    }

    /** One category's in-app and/or push switch. Null leaves that side unchanged. */
    fun setChannel(category: NotificationCategory, inApp: Boolean? = null, push: Boolean? = null) = edit { value ->
        val current = value.channels(category)
        value.withChannels(
            category,
            NotificationChannels(inApp = inApp ?: current.inApp, push = push ?: current.push),
        )
    }

    fun toggleMore() = _state.update { state ->
        val editing = state as? NotificationSettingsUiState.Editing ?: return@update state
        editing.copy(moreExpanded = !editing.moreExpanded)
    }

    fun quietHours(start: String? = null, end: String? = null, timezone: String? = null) = edit {
        it.copy(
            quietHoursStart = start ?: it.quietHoursStart,
            quietHoursEnd = end ?: it.quietHoursEnd,
            quietHoursTimeZone = timezone ?: it.quietHoursTimeZone,
        )
    }
    fun digest(value: String) = edit { it.copy(emailDigest = value) }

    fun save() {
        val current = _state.value as? NotificationSettingsUiState.Editing ?: return
        if (!current.dirty || current.saving || !current.quietHoursValid) return
        _state.value = current.copy(saving = true, message = null)
        viewModelScope.launch {
            _state.value = when (val result = repository.save(current.value)) {
                is AppResult.Success -> NotificationSettingsUiState.Editing(
                    result.data,
                    result.data,
                    moreExpanded = current.moreExpanded,
                    message = "Notification settings saved.",
                )
                is AppResult.Failure -> current.copy(saving = false, message = "Nothing changed. Please try again.")
            }
        }
    }

    private fun edit(block: (NotificationSettings) -> NotificationSettings) = _state.update { state ->
        val current = state as? NotificationSettingsUiState.Editing ?: return@update state
        if (current.saving) current else current.copy(value = block(current.value), message = null)
    }
}
