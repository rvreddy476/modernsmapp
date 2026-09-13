package com.us.android.feature.kitchen.hours

import androidx.lifecycle.SavedStateHandle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.food.network.OperatingHoursDto
import com.us.android.core.food.repository.FoodError
import com.us.android.core.food.repository.FoodRepository
import com.us.android.core.food.repository.FoodResult
import com.us.android.feature.kitchen.navigation.restaurantId
import com.us.android.feature.kitchen.ui.asMessage
import com.us.android.feature.kitchen.ui.success
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import javax.inject.Inject

data class HoursUiState(
    val week: List<DayHours> = OperatingHoursRules.defaultWeek(),
    /** Keyed by day of week, or [OperatingHoursRules.ALL_CLOSED]. */
    val errors: Map<Int, String> = emptyMap(),
    val saving: Boolean = false,
    val saved: OperatingHoursDto? = null,
    val message: UsMessage? = null,
)

/** The weekly opening hours → `PUT …/operating-hours` (Asia/Kolkata). */
@HiltViewModel
class OperatingHoursViewModel @Inject constructor(
    savedStateHandle: SavedStateHandle,
    private val food: FoodRepository,
) : ViewModel() {

    private val restaurantId = savedStateHandle.restaurantId()

    private val _state = MutableStateFlow(HoursUiState())
    val state: StateFlow<HoursUiState> = _state.asStateFlow()

    fun setOpen(dayOfWeek: Int, open: Boolean) = editDay(dayOfWeek) { it.copy(open = open) }

    fun setOpensAt(dayOfWeek: Int, value: String) = editDay(dayOfWeek) { it.copy(opensAt = timeInput(value)) }

    fun setClosesAt(dayOfWeek: Int, value: String) = editDay(dayOfWeek) { it.copy(closesAt = timeInput(value)) }

    fun copyToAll(fromDay: Int) {
        _state.update { current ->
            val template = current.week.first { it.dayOfWeek == fromDay }
            current.copy(
                week = current.week.map { it.copy(open = template.open, opensAt = template.opensAt, closesAt = template.closesAt) },
                errors = emptyMap(),
            )
        }
    }

    fun dismissMessage() {
        _state.update { it.copy(message = null) }
    }

    fun save() {
        val week = _state.value.week.map {
            it.copy(
                opensAt = OperatingHoursRules.normalizeTime(it.opensAt),
                closesAt = OperatingHoursRules.normalizeTime(it.closesAt),
            )
        }
        val errors = OperatingHoursRules.validate(week)
        _state.update { it.copy(week = week, errors = errors) }
        if (errors.isNotEmpty()) return
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            when (val result = food.putOperatingHours(restaurantId, OperatingHoursRules.toRequest(week))) {
                is FoodResult.Success -> _state.update {
                    it.copy(saving = false, saved = result.value, message = success("Opening hours saved"))
                }
                is FoodResult.Failure -> _state.update { current ->
                    val error = result.error
                    if (error is FoodError.InvalidField) {
                        val day = OperatingHoursRules.dayForServerField(error.field, week) ?: OperatingHoursRules.ALL_CLOSED
                        current.copy(saving = false, errors = mapOf(day to error.message))
                    } else {
                        current.copy(saving = false, message = error.asMessage())
                    }
                }
            }
        }
    }

    private fun editDay(dayOfWeek: Int, change: (DayHours) -> DayHours) {
        _state.update { current ->
            current.copy(
                week = current.week.map { if (it.dayOfWeek == dayOfWeek) change(it) else it },
                errors = current.errors - dayOfWeek - OperatingHoursRules.ALL_CLOSED,
            )
        }
    }

    private fun timeInput(value: String): String = value.filter { it.isDigit() || it == ':' }.take(MAX_TIME_INPUT)

    private companion object {
        const val MAX_TIME_INPUT = 5
    }
}
