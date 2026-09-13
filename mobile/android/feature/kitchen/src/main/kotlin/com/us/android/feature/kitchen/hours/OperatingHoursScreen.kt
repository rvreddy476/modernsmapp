package com.us.android.feature.kitchen.hours

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyColumn
import androidx.compose.foundation.lazy.items
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.input.KeyboardType
import androidx.compose.ui.unit.dp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.component.UsTextField
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.kitchen.ui.FieldError
import com.us.android.feature.kitchen.ui.InfoNote
import com.us.android.feature.kitchen.ui.KitchenCard
import com.us.android.feature.kitchen.ui.KitchenScreen
import com.us.android.feature.kitchen.ui.PillTone
import com.us.android.feature.kitchen.ui.kitchenSwitchColors
import com.us.android.feature.kitchen.ui.listPadding

@Composable
fun OperatingHoursScreen(onBack: () -> Unit, viewModel: OperatingHoursViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    KitchenScreen(
        title = "Opening hours",
        onBack = onBack,
        message = state.message,
        onDismissMessage = viewModel::dismissMessage,
    ) { padding ->
        LazyColumn(
            contentPadding = listPadding(padding),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
        ) {
            item(key = "notes") {
                Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    InfoNote("Times are India Standard Time. A closing time earlier than the opening time means you close after midnight.")
                    InfoNote(
                        text = "Saved hours can't be read back into the app yet, so this starts from 09:00–22:00. Saving replaces the whole week.",
                        tone = PillTone.Warning,
                    )
                }
            }
            items(OperatingHoursRules.displayOrder, key = { "day-$it" }) { dayOfWeek ->
                val day = state.week.first { it.dayOfWeek == dayOfWeek }
                DayCard(
                    day = day,
                    error = state.errors[dayOfWeek],
                    onOpenChange = { viewModel.setOpen(dayOfWeek, it) },
                    onOpensAtChange = { viewModel.setOpensAt(dayOfWeek, it) },
                    onClosesAtChange = { viewModel.setClosesAt(dayOfWeek, it) },
                )
            }
            item(key = "actions") {
                Column(verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
                    TextButton(onClick = { viewModel.copyToAll(MONDAY) }) {
                        Text("Use Monday's hours every day", color = UsTheme.extended.accentSolid)
                    }
                    FieldError(state.errors[OperatingHoursRules.ALL_CLOSED])
                    UsButton(
                        text = "Save opening hours",
                        onClick = viewModel::save,
                        loading = state.saving,
                        modifier = Modifier.fillMaxWidth(),
                    )
                    state.saved?.let {
                        InfoNote(
                            text = if (it.isOpenNow) "Saved. You're open right now." else "Saved. You're closed right now.",
                            tone = PillTone.Positive,
                        )
                    }
                }
            }
        }
    }
}

@Composable
private fun DayCard(
    day: DayHours,
    error: String?,
    onOpenChange: (Boolean) -> Unit,
    onOpensAtChange: (String) -> Unit,
    onClosesAtChange: (String) -> Unit,
) {
    KitchenCard {
        Row(verticalAlignment = Alignment.CenterVertically) {
            Text(
                text = OperatingHoursRules.dayName(day.dayOfWeek),
                style = MaterialTheme.typography.titleSmall,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            Text(
                text = if (day.open) "Open" else "Closed",
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
                modifier = Modifier.padding(end = 8.dp),
            )
            Switch(checked = day.open, onCheckedChange = onOpenChange, colors = kitchenSwitchColors())
        }
        if (day.open) {
            Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l)) {
                UsTextField(
                    value = day.opensAt,
                    onValueChange = onOpensAtChange,
                    label = "Opens",
                    placeholder = "09:00",
                    keyboardType = KeyboardType.Number,
                    modifier = Modifier.weight(1f),
                )
                UsTextField(
                    value = day.closesAt,
                    onValueChange = onClosesAtChange,
                    label = "Closes",
                    placeholder = "22:00",
                    keyboardType = KeyboardType.Number,
                    modifier = Modifier.weight(1f),
                )
            }
            if (OperatingHoursRules.isOvernight(day)) InfoNote("Closes after midnight, the next day.")
        }
        FieldError(error)
    }
}

private const val MONDAY = 1
