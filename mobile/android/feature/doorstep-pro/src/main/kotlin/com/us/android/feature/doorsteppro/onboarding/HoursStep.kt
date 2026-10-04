package com.us.android.feature.doorsteppro.onboarding

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.Icon
import androidx.compose.material3.IconButton
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsDatePickerField
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.component.UsPillButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DayOffDto
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.domain.HoursRules
import com.us.android.feature.doorsteppro.domain.HoursWindow
import com.us.android.feature.doorsteppro.ui.BottomAction
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LoadingPane
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.SectionLabel
import com.us.android.feature.doorsteppro.ui.Tone
import com.us.android.feature.doorsteppro.ui.WEEKDAY_NAMES
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.errorMessage
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.longDateText
import com.us.android.feature.doorsteppro.ui.successMessage
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.LocalDate
import javax.inject.Inject

data class HoursUiState(
    val loading: Boolean = true,
    val windows: List<HoursWindow> = emptyList(),
    val problem: String? = null,
    val saving: Boolean = false,
    val daysOff: List<DayOffDto> = emptyList(),
    val newDayOff: String = "",
    val newDayOffReason: String = "",
    val addingDayOff: Boolean = false,
    val message: UsMessage? = null,
)

/**
 * Weekly hours (several windows a day, India time) → `PUT /pro/me/hours`, and
 * days off → `POST`/`DELETE /pro/me/days-off`. The editor applies the server's
 * own rules first (no overlaps, end after start, ≤ 6 windows a day); a day off
 * with an accepted job is refused by the server (409) and said so.
 */
@HiltViewModel
class HoursViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
) : ViewModel() {

    private val _state = MutableStateFlow(HoursUiState())
    val state: StateFlow<HoursUiState> = _state.asStateFlow()

    init {
        viewModelScope.launch {
            val hours = repository.hours()
            val daysOff = repository.daysOff()
            _state.update {
                it.copy(
                    loading = false,
                    windows = (hours as? ProResult.Success)?.value?.items?.mapNotNull(HoursRules::fromDto).orEmpty(),
                    daysOff = (daysOff as? ProResult.Success)?.value.orEmpty(),
                    message = (hours as? ProResult.Failure)?.error?.asMessage(),
                )
            }
        }
    }

    fun addWindow(weekday: Int) {
        val window = HoursRules.newWindow(_state.value.windows, weekday)
        if (window == null) {
            _state.update { it.copy(message = errorMessage("No room for another window on ${WEEKDAY_NAMES[weekday]}.")) }
            return
        }
        setWindows(_state.value.windows + window)
    }

    fun removeWindow(window: HoursWindow) = setWindows(_state.value.windows - window)

    fun setStart(window: HoursWindow, minutes: Int) = replace(window, window.copy(startMinutes = minutes))

    fun setEnd(window: HoursWindow, minutes: Int) = replace(window, window.copy(endMinutes = minutes))

    fun dismissMessage() = _state.update { it.copy(message = null) }

    fun save() {
        val windows = HoursRules.sorted(_state.value.windows)
        val problem = HoursRules.validate(windows)
        if (problem != null) {
            _state.update { it.copy(problem = describe(windows, problem.index, problem.message)) }
            return
        }
        if (_state.value.saving) return
        _state.update { it.copy(saving = true) }
        viewModelScope.launch {
            when (val result = repository.saveHours(windows.map(HoursWindow::toDto))) {
                is ProResult.Success -> _state.update {
                    it.copy(
                        saving = false,
                        windows = result.value.items.mapNotNull(HoursRules::fromDto),
                        message = successMessage("Hours saved."),
                    )
                }
                is ProResult.Failure -> _state.update { it.copy(saving = false, message = result.error.asMessage()) }
            }
        }
    }

    fun onNewDayOff(date: String) = _state.update { it.copy(newDayOff = date) }

    fun onNewDayOffReason(reason: String) = _state.update { it.copy(newDayOffReason = reason.take(MAX_REASON)) }

    fun addDayOff() {
        val date = _state.value.newDayOff
        if (date.isBlank() || _state.value.addingDayOff) return
        _state.update { it.copy(addingDayOff = true) }
        viewModelScope.launch {
            when (val result = repository.addDayOff(date, _state.value.newDayOffReason.trim().ifBlank { null })) {
                is ProResult.Success -> _state.update {
                    it.copy(
                        addingDayOff = false,
                        daysOff = (it.daysOff.filterNot { d -> d.date == result.value.date } + result.value).sortedBy(DayOffDto::date),
                        newDayOff = "",
                        newDayOffReason = "",
                    )
                }
                is ProResult.Failure -> _state.update { it.copy(addingDayOff = false, message = errorMessage(result.error.userMessage())) }
            }
        }
    }

    fun removeDayOff(date: String) {
        viewModelScope.launch {
            when (val result = repository.removeDayOff(date)) {
                is ProResult.Success -> _state.update { it.copy(daysOff = it.daysOff.filterNot { d -> d.date == date }) }
                is ProResult.Failure -> _state.update { it.copy(message = result.error.asMessage()) }
            }
        }
    }

    private fun replace(old: HoursWindow, new: HoursWindow) = setWindows(_state.value.windows.map { if (it == old) new else it })

    private fun setWindows(windows: List<HoursWindow>) = _state.update { it.copy(windows = windows, problem = null) }

    private fun describe(windows: List<HoursWindow>, index: Int, message: String): String {
        val day = windows.getOrNull(index)?.weekday?.let { WEEKDAY_NAMES.getOrNull(it) }
        return if (day != null) "$day: $message" else message
    }

    private companion object {
        const val MAX_REASON = 120
    }
}

@Composable
fun HoursStepScreen(onBack: () -> Unit, viewModel: HoursViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    ProScreen(
        title = "Working hours",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
        bottomBar = { BottomAction(label = "Save hours", onClick = viewModel::save, loading = state.saving, summary = state.problem) },
    ) { padding ->
        if (state.loading) {
            LoadingPane()
            return@ProScreen
        }
        LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = listPadding(padding)) {
            item { InfoNote("India time. Add more than one window a day for a split shift, e.g. 9–1 and 3–7.") }
            items(WEEKDAY_NAMES.indices.toList(), key = { "day-$it" }) { weekday ->
                val windows = state.windows.filter { it.weekday == weekday }.sortedBy { it.startMinutes }
                ProCard(modifier = Modifier.padding(top = UsTheme.spacing.m)) {
                    Row(verticalAlignment = Alignment.CenterVertically) {
                        CardHeading(WEEKDAY_NAMES[weekday], if (windows.isEmpty()) "Not working" else null, modifier = Modifier.weight(1f))
                        UsPillButton(text = "Add", onClick = { viewModel.addWindow(weekday) }, filled = false)
                    }
                    windows.forEach { window ->
                        Row(verticalAlignment = Alignment.CenterVertically, horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s)) {
                            TimePicker(window.startMinutes, onPick = { viewModel.setStart(window, it) }, modifier = Modifier.weight(1f))
                            Text("to", style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
                            TimePicker(window.endMinutes, onPick = { viewModel.setEnd(window, it) }, modifier = Modifier.weight(1f))
                            IconButton(onClick = { viewModel.removeWindow(window) }) {
                                Icon(UsIcons.Trash, contentDescription = "Remove window", tint = UsTheme.extended.textMuted)
                            }
                        }
                    }
                }
            }
            item { SectionLabel("Days off") }
            item {
                ProCard {
                    UsDatePickerField(
                        value = state.newDayOff,
                        onValueChange = viewModel::onNewDayOff,
                        label = "Date",
                        minDate = LocalDate.now(),
                        maxDate = LocalDate.now().plusYears(1),
                    )
                    UsTextField(value = state.newDayOffReason, onValueChange = viewModel::onNewDayOffReason, label = "Reason (optional)")
                    UsPillButton(
                        text = "Add day off",
                        onClick = viewModel::addDayOff,
                        enabled = state.newDayOff.isNotBlank(),
                        busy = state.addingDayOff,
                    )
                    InfoNote("A day with a job you've accepted can't be taken off.", tone = Tone.Neutral)
                }
            }
            items(state.daysOff, key = { "off-" + it.date }) { day ->
                Row(
                    modifier = Modifier.fillMaxWidth().padding(vertical = UsTheme.spacing.s),
                    verticalAlignment = Alignment.CenterVertically,
                ) {
                    Text(
                        longDateText(day.date) + (day.reason?.let { " · $it" } ?: ""),
                        style = MaterialTheme.typography.bodyMedium,
                        color = UsTheme.extended.textPrimary,
                        modifier = Modifier.weight(1f),
                    )
                    IconButton(onClick = { viewModel.removeDayOff(day.date) }) {
                        Icon(UsIcons.Trash, contentDescription = "Remove day off", tint = UsTheme.extended.textMuted)
                    }
                }
            }
        }
    }
}

/** A half-hour time choice as a dropdown. */
@Composable
private fun TimePicker(minutes: Int, onPick: (Int) -> Unit, modifier: Modifier = Modifier) {
    var open by remember { mutableStateOf(false) }
    Box(modifier = modifier) {
        UsPillButton(text = HoursRules.label(minutes), onClick = { open = true }, filled = false, modifier = Modifier.fillMaxWidth())
        DropdownMenu(expanded = open, onDismissRequest = { open = false }) {
            HoursRules.choices.forEach { choice ->
                DropdownMenuItem(
                    text = { Text(HoursRules.label(choice)) },
                    onClick = {
                        open = false
                        onPick(choice)
                    },
                )
            }
        }
    }
}
