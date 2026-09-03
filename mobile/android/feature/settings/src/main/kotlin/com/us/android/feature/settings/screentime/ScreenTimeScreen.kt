package com.us.android.feature.settings.screentime

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Modifier
import androidx.hilt.lifecycle.viewmodel.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsTopBar
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.profile.data.ScreenTimeDay
import com.us.android.core.profile.data.ScreenTimeWeek
import com.us.android.core.ui.UsErrorState
import com.us.android.core.ui.UsLoadingState
import com.us.android.core.ui.UsSettingsOption
import com.us.android.core.ui.UsSettingsSection
import com.us.android.core.ui.UsSettingsSelectRow

@Composable
fun ScreenTimeScreen(
    onBack: () -> Unit,
    viewModel: ScreenTimeViewModel = hiltViewModel(),
) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    UsScaffold(
        topBar = { UsTopBar("Screen time", onBack = onBack) },
        applyPageGutter = false,
    ) { padding ->
        when (val current = state) {
            ScreenTimeUiState.Loading -> UsLoadingState(Modifier.padding(padding), "Loading screen time")
            is ScreenTimeUiState.Error ->
                UsErrorState(current.message, Modifier.padding(padding), onRetry = viewModel::load)
            is ScreenTimeUiState.Editing -> ScreenTimeForm(current, viewModel, Modifier.padding(padding))
        }
    }
}

@Composable
private fun ScreenTimeForm(
    state: ScreenTimeUiState.Editing,
    viewModel: ScreenTimeViewModel,
    modifier: Modifier,
) {
    val value = state.value
    Column(
        modifier = modifier
            .fillMaxSize()
            .verticalScroll(rememberScrollState())
            .padding(horizontal = UsTheme.spacing.pageHorizontal),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
    ) {
        state.week?.let { TodaySection(it) }
        UsSettingsSection("Limits") {
            UsSettingsSelectRow(
                title = "Daily screen time",
                selected = value.dailyLimitMins?.toString().orEmpty(),
                options = DAILY_LIMIT_LABELS,
                onSelected = { viewModel.setDailyLimit(it.toIntOrNull()) },
                enabled = !state.saving,
            )
        }
        UsSettingsSection("Sleep hours") {
            UsSettingsSelectRow(
                title = "Start",
                selected = value.bedtimeStart.orEmpty(),
                options = SLEEP_HOUR_OPTIONS,
                onSelected = { viewModel.setSleepHours(start = it, end = value.bedtimeEnd ?: it) },
                enabled = !state.saving,
            )
            UsSettingsSelectRow(
                title = "End",
                selected = value.bedtimeEnd.orEmpty(),
                options = SLEEP_HOUR_OPTIONS,
                onSelected = { viewModel.setSleepHours(start = value.bedtimeStart ?: it, end = it) },
                enabled = !state.saving,
            )
            if (value.sleepHoursEnabled) {
                UsButton(
                    text = "Turn off sleep hours",
                    onClick = viewModel::turnOffSleepHours,
                    modifier = Modifier.fillMaxWidth(),
                    enabled = !state.saving,
                )
            }
        }
        if (!state.sleepHoursValid) {
            Text(
                "Set both a start and an end time, or turn sleep hours off.",
                color = MaterialTheme.colorScheme.error,
            )
        }
        state.message?.let {
            Text(
                it,
                color = if (state.dirty) MaterialTheme.colorScheme.error else UsTheme.extended.statusSuccess,
            )
        }
        UsButton(
            text = "Save screen time settings",
            onClick = viewModel::save,
            modifier = Modifier.fillMaxWidth(),
            enabled = state.dirty && state.sleepHoursValid,
            loading = state.saving,
        )
    }
}

@Composable
private fun TodaySection(week: ScreenTimeWeek) {
    UsSettingsSection("Today") {
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceBetween,
        ) {
            Text("Today so far", style = MaterialTheme.typography.bodyLarge)
            Text(
                week.dailyLimitMins?.let { "${week.todayMinutes} / $it min" } ?: "${week.todayMinutes} min",
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textMuted,
            )
        }
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
        week.days.forEach { DayRow(it) }
    }
}

@Composable
private fun DayRow(day: ScreenTimeDay) {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.s),
        horizontalArrangement = Arrangement.SpaceBetween,
    ) {
        Text(day.date, style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
        Text("${day.minutes} min", style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textMuted)
    }
}

private val DAILY_LIMIT_LABELS: List<UsSettingsOption> = DAILY_LIMIT_OPTIONS.map { minutes ->
    UsSettingsOption(minutes?.toString().orEmpty(), minutes?.let { "$it min" } ?: "Off")
}

private val SLEEP_HOUR_OPTIONS: List<UsSettingsOption> = SLEEP_HOUR_SLOTS.map { UsSettingsOption(it, it) }
