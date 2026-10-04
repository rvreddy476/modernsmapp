package com.us.android.feature.doorsteppro.jobs

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.Lifecycle
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.LifecycleEventEffect
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.ProJobDto
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.domain.JobsForDate
import com.us.android.feature.doorsteppro.domain.ProClock
import com.us.android.feature.doorsteppro.home.JobRow
import com.us.android.feature.doorsteppro.ui.IST
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LoadingPane
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.asMessage
import com.us.android.feature.doorsteppro.ui.dayChip
import com.us.android.feature.doorsteppro.ui.listPadding
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.async
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.LocalDate
import javax.inject.Inject

data class JobsUiState(
    val loading: Boolean = true,
    val today: LocalDate = LocalDate.now(IST),
    val selected: LocalDate = LocalDate.now(IST),
    val jobs: List<ProJobDto> = emptyList(),
    val message: UsMessage? = null,
) {
    val days: List<LocalDate> get() = (-PAST_DAYS..FUTURE_DAYS).map { today.plusDays(it.toLong()) }

    val onSelected: List<ProJobDto> get() = JobsForDate.on(jobs, selected)

    val busyDays: Set<LocalDate> get() = JobsForDate.daysWithJobs(jobs)

    companion object {
        const val PAST_DAYS = 7
        const val FUTURE_DAYS = 13
    }
}

/**
 * Jobs for a date. The contract's list filters by `upcoming | active | past`
 * with a cursor, not by date (see the report), so this reads the first page
 * of each and groups by the India-time day of `slot_start`.
 */
@HiltViewModel
class JobsViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    clock: ProClock,
) : ViewModel() {

    private val today = clock.now().atZone(IST).toLocalDate()

    private val _state = MutableStateFlow(JobsUiState(today = today, selected = today))
    val state: StateFlow<JobsUiState> = _state.asStateFlow()

    fun refresh() {
        viewModelScope.launch {
            val lists = STATUSES.map { status -> async { repository.jobs(status = status, cursor = null) } }.map { it.await() }
            val jobs = lists.flatMap { (it as? ProResult.Success)?.value?.items.orEmpty() }
            val failure = lists.firstNotNullOfOrNull { (it as? ProResult.Failure)?.error }
            _state.update { it.copy(loading = false, jobs = jobs, message = if (jobs.isEmpty()) failure?.asMessage() else null) }
        }
    }

    fun select(day: LocalDate) = _state.update { it.copy(selected = day) }

    fun dismissMessage() = _state.update { it.copy(message = null) }

    private companion object {
        val STATUSES = listOf("active", "upcoming", "past")
    }
}

@Composable
fun JobsScreen(onBack: () -> Unit, onOpenJob: (String) -> Unit, viewModel: JobsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    LifecycleEventEffect(Lifecycle.Event.ON_RESUME) { viewModel.refresh() }
    ProScreen(title = "My jobs", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        if (state.loading) {
            LoadingPane()
            return@ProScreen
        }
        LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            item {
                LazyRow(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s), contentPadding = PaddingValues(vertical = UsTheme.spacing.s)) {
                    items(state.days, key = { it.toString() }) { day ->
                        DayChip(day, state.today, selected = day == state.selected, busy = day in state.busyDays, onClick = { viewModel.select(day) })
                    }
                }
            }
            if (state.onSelected.isEmpty()) item { InfoNote("No jobs on this day.") }
            items(state.onSelected, key = { it.bookingId }) { job -> JobRow(job, onClick = { onOpenJob(job.bookingId) }) }
        }
    }
}

@Composable
private fun DayChip(day: LocalDate, today: LocalDate, selected: Boolean, busy: Boolean, onClick: () -> Unit) {
    val (name, number) = dayChip(day, today)
    val shape = RoundedCornerShape(UsTheme.radii.medium)
    Column(
        modifier = Modifier
            .width(56.dp)
            .clip(shape)
            .background(if (selected) UsTheme.extended.accentSolid.copy(alpha = SELECTED_ALPHA) else UsTheme.extended.bgCard)
            .border(if (selected) 2.dp else 1.dp, if (selected) UsTheme.extended.accentSolid else UsTheme.extended.borderSubtle, shape)
            .clickable(onClick = onClick)
            .padding(vertical = UsTheme.spacing.m),
        horizontalAlignment = Alignment.CenterHorizontally,
    ) {
        Text(name, style = MaterialTheme.typography.labelSmall, color = UsTheme.extended.textMuted)
        Text(number, style = MaterialTheme.typography.titleMedium, fontWeight = FontWeight.SemiBold, color = UsTheme.extended.textPrimary)
        Box(
            modifier = Modifier
                .padding(top = 4.dp)
                .size(6.dp)
                .background(if (busy) UsTheme.extended.accentSolid else UsTheme.extended.bgCard, CircleShape),
        )
    }
}

private const val SELECTED_ALPHA = 0.12f
