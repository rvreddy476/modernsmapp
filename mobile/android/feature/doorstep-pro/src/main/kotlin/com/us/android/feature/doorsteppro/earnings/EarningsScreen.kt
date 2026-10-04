package com.us.android.feature.doorsteppro.earnings

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.ViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.lifecycle.viewModelScope
import com.us.android.core.designsystem.component.UsMessage
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.doorsteppro.data.DoorstepProRepository
import com.us.android.feature.doorsteppro.data.EarningLineDto
import com.us.android.feature.doorsteppro.data.EarningsDto
import com.us.android.feature.doorsteppro.data.ProResult
import com.us.android.feature.doorsteppro.data.userMessage
import com.us.android.feature.doorsteppro.domain.ProClock
import com.us.android.feature.doorsteppro.model.Paise
import com.us.android.feature.doorsteppro.model.toRupeeText
import com.us.android.feature.doorsteppro.ui.CardHeading
import com.us.android.feature.doorsteppro.ui.ChoiceRow
import com.us.android.feature.doorsteppro.ui.IST
import com.us.android.feature.doorsteppro.ui.InfoNote
import com.us.android.feature.doorsteppro.ui.LoadingPane
import com.us.android.feature.doorsteppro.ui.MessagePane
import com.us.android.feature.doorsteppro.ui.ProCard
import com.us.android.feature.doorsteppro.ui.ProScreen
import com.us.android.feature.doorsteppro.ui.listPadding
import com.us.android.feature.doorsteppro.ui.slotRangeText
import dagger.hilt.android.lifecycle.HiltViewModel
import kotlinx.coroutines.flow.MutableStateFlow
import kotlinx.coroutines.flow.StateFlow
import kotlinx.coroutines.flow.asStateFlow
import kotlinx.coroutines.flow.update
import kotlinx.coroutines.launch
import java.time.DayOfWeek
import java.time.LocalDate
import java.time.temporal.TemporalAdjusters
import javax.inject.Inject

/** The periods the earnings screen offers, in India time. */
enum class EarningsPeriod(val label: String) {
    TODAY("Today"),
    THIS_WEEK("This week"),
    THIS_MONTH("This month"),
    ;

    /** `from`/`to` dates (inclusive) for `GET /pro/earnings`. */
    fun range(today: LocalDate): Pair<LocalDate, LocalDate> = when (this) {
        TODAY -> today to today
        THIS_WEEK -> today.with(TemporalAdjusters.previousOrSame(DayOfWeek.MONDAY)) to today
        THIS_MONTH -> today.withDayOfMonth(1) to today
    }
}

data class EarningsUiState(
    val loading: Boolean = true,
    val period: EarningsPeriod = EarningsPeriod.THIS_WEEK,
    val earnings: EarningsDto? = null,
    val error: String? = null,
    val message: UsMessage? = null,
)

/** Read-only earning lines and their total (`GET /pro/earnings?from&to`). Computed by the server; payouts are OFF. */
@HiltViewModel
class EarningsViewModel @Inject constructor(
    private val repository: DoorstepProRepository,
    private val clock: ProClock,
) : ViewModel() {

    private val _state = MutableStateFlow(EarningsUiState())
    val state: StateFlow<EarningsUiState> = _state.asStateFlow()

    init {
        load(EarningsPeriod.THIS_WEEK)
    }

    fun load(period: EarningsPeriod) {
        _state.update { it.copy(period = period, loading = true) }
        val (from, to) = period.range(clock.now().atZone(IST).toLocalDate())
        viewModelScope.launch {
            when (val result = repository.earnings(from.toString(), to.toString())) {
                is ProResult.Success -> _state.update { it.copy(loading = false, earnings = result.value, error = null) }
                is ProResult.Failure -> _state.update { it.copy(loading = false, earnings = null, error = result.error.userMessage()) }
            }
        }
    }

    fun dismissMessage() = _state.update { it.copy(message = null) }
}

@Composable
fun EarningsScreen(onBack: () -> Unit, viewModel: EarningsViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    ProScreen(title = "Earnings", onBack = onBack, message = state.message, onDismissMessage = viewModel::dismissMessage) { padding ->
        LazyColumn(modifier = Modifier.fillMaxSize(), contentPadding = listPadding(padding), verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            item {
                Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s), modifier = Modifier.fillMaxWidth()) {
                    EarningsPeriod.entries.forEach { period ->
                        ChoiceRow(selected = period == state.period, onClick = { viewModel.load(period) }, modifier = Modifier.weight(1f)) {
                            Text(period.label, style = MaterialTheme.typography.labelMedium, color = UsTheme.extended.textPrimary)
                        }
                    }
                }
            }
            val earnings = state.earnings
            when {
                state.loading -> item { LoadingPane() }
                earnings == null -> item {
                    MessagePane(title = "Couldn't load earnings", body = state.error.orEmpty(), primaryLabel = "Try again", onPrimary = { viewModel.load(state.period) })
                }
                else -> {
                    item {
                        ProCard {
                            CardHeading(state.period.label, "After Doorstep's commission")
                            Text(
                                Paise(earnings.totalPaise).toRupeeText(),
                                style = MaterialTheme.typography.headlineMedium,
                                fontWeight = FontWeight.SemiBold,
                                color = UsTheme.extended.textPrimary,
                            )
                            InfoNote("Payouts to your bank start later. Every job is recorded here from day one.")
                        }
                    }
                    if (earnings.lines.isEmpty()) item { InfoNote("Nothing in this period yet.") }
                    items(earnings.lines, key = { it.id }) { line -> LineRow(line) }
                }
            }
        }
    }
}

@Composable
private fun LineRow(line: EarningLineDto) {
    Row(modifier = Modifier.fillMaxWidth(), verticalAlignment = Alignment.CenterVertically) {
        androidx.compose.foundation.layout.Column(Modifier.weight(1f)) {
            Text(kindLabel(line.kind), style = MaterialTheme.typography.bodyMedium, color = UsTheme.extended.textPrimary)
            Text(slotRangeText(line.createdAt, line.createdAt).substringBefore(" ·"), style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
        }
        val amount = Paise(line.amountPaise)
        Text(
            amount.toRupeeText(),
            style = MaterialTheme.typography.titleSmall,
            color = if (amount.value < 0) UsTheme.extended.statusDanger else UsTheme.extended.textPrimary,
        )
    }
}

private fun kindLabel(kind: String): String = when (kind) {
    "job" -> "Job"
    "extras" -> "Extras"
    "incentive" -> "Incentive"
    "penalty" -> "Penalty"
    "adjustment" -> "Adjustment"
    "commission" -> "Doorstep commission"
    else -> kind
}
