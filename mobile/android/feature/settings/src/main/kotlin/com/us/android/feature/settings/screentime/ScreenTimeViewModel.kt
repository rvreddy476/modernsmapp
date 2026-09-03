package com.us.android.feature.settings.screentime

import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.common.result.AppResult
import com.us.android.core.profile.data.ScreenTimeWeek
import com.us.android.core.profile.data.WellbeingRepository
import com.us.android.core.profile.data.WellbeingSettings
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

sealed interface ScreenTimeUiState {
    data object Loading : ScreenTimeUiState
    data class Error(val message: String) : ScreenTimeUiState
    data class Editing(
        val original: WellbeingSettings,
        val value: WellbeingSettings,
        val week: ScreenTimeWeek?,
        val saving: Boolean = false,
        val message: String? = null,
    ) : ScreenTimeUiState {
        val dirty get() = original != value

        /** Both bedtime fields must be set together, or both cleared — never one alone. */
        val sleepHoursValid get() = (value.bedtimeStart == null) == (value.bedtimeEnd == null)
    }
}

/** Daily-limit options shown on the picker, in minutes. `null` is "Off". */
@Suppress("MagicNumber")
val DAILY_LIMIT_OPTIONS = listOf(null, 30, 45, 60, 90, 120, 180)

/** 30-minute slots covering a full day, `HH:mm`. */
val SLEEP_HOUR_SLOTS: List<String> = (0 until MINUTES_PER_DAY step SLOT_MINUTES).map { minutes ->
    "%02d:%02d".format(minutes / MINUTES_PER_HOUR, minutes % MINUTES_PER_HOUR)
}

private const val MINUTES_PER_DAY = 24 * 60
private const val MINUTES_PER_HOUR = 60
private const val SLOT_MINUTES = 30

@HiltViewModel
class ScreenTimeViewModel @Inject constructor(
    private val repository: WellbeingRepository,
) : ViewModel() {
    private val _state = MutableStateFlow<ScreenTimeUiState>(ScreenTimeUiState.Loading)
    val state: StateFlow<ScreenTimeUiState> = _state.asStateFlow()

    init { load() }

    fun load() {
        _state.value = ScreenTimeUiState.Loading
        viewModelScope.launch {
            val settings = repository.settings()
            val week = repository.week()
            _state.value = if (settings is AppResult.Success) {
                ScreenTimeUiState.Editing(settings.data, settings.data, (week as? AppResult.Success)?.data)
            } else {
                ScreenTimeUiState.Error("Screen time settings could not be loaded.")
            }
        }
    }

    fun setDailyLimit(minutes: Int?) = edit { it.copy(dailyLimitMins = minutes) }

    fun setSleepHours(start: String?, end: String?) = edit { it.copy(bedtimeStart = start, bedtimeEnd = end) }

    fun turnOffSleepHours() = edit { it.copy(bedtimeStart = null, bedtimeEnd = null) }

    fun save() {
        val current = _state.value as? ScreenTimeUiState.Editing ?: return
        if (!current.dirty || current.saving || !current.sleepHoursValid) return
        _state.value = current.copy(saving = true, message = null)
        viewModelScope.launch {
            _state.value = when (val result = repository.save(current.value)) {
                is AppResult.Success -> ScreenTimeUiState.Editing(
                    result.data,
                    result.data,
                    current.week,
                    message = "Screen time settings saved.",
                )
                is AppResult.Failure -> current.copy(saving = false, message = "Nothing changed. Please try again.")
            }
        }
    }

    private fun edit(block: (WellbeingSettings) -> WellbeingSettings) = _state.update { state ->
        val editing = state as? ScreenTimeUiState.Editing ?: return@update state
        if (editing.saving) editing else editing.copy(value = block(editing.value), message = null)
    }
}
