package com.us.android.feature.echo.ui

import androidx.compose.material3.AlertDialog
import androidx.compose.material3.DatePicker
import androidx.compose.material3.DatePickerDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.SelectableDates
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.TimePicker
import androidx.compose.material3.rememberDatePickerState
import androidx.compose.material3.rememberTimePickerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Modifier
import androidx.compose.ui.platform.testTag
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.echo.data.EchoScheduleRules
import java.time.Instant
import java.time.LocalDate
import java.time.LocalTime
import java.time.ZoneId
import java.time.ZoneOffset

/**
 * X's schedule picker: the date (today to 18 months ahead), then the time.
 * The 5-minute floor is checked by the caller ([com.us.android.feature.echo.data.scheduleProblem]),
 * which says why a time was refused.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun SchedulePicker(
    initial: Instant,
    now: Instant,
    onPicked: (Instant) -> Unit,
    onDismiss: () -> Unit,
    zone: ZoneId = ZoneId.systemDefault(),
) {
    var date by remember { mutableStateOf<LocalDate?>(null) }
    val start = initial.atZone(zone)
    val firstDay = now.atZone(zone).toLocalDate()
    val lastDay = now.plus(EchoScheduleRules.MAX_AHEAD).atZone(zone).toLocalDate()
    val picked = date
    if (picked == null) {
        val dateState = rememberDatePickerState(
            initialSelectedDateMillis = start.toLocalDate().atStartOfDay(ZoneOffset.UTC).toInstant().toEpochMilli(),
            selectableDates = remember(firstDay, lastDay) {
                object : SelectableDates {
                    override fun isSelectableDate(utcTimeMillis: Long): Boolean {
                        val day = Instant.ofEpochMilli(utcTimeMillis).atZone(ZoneOffset.UTC).toLocalDate()
                        return !day.isBefore(firstDay) && !day.isAfter(lastDay)
                    }

                    override fun isSelectableYear(year: Int): Boolean = year in firstDay.year..lastDay.year
                }
            },
        )
        DatePickerDialog(
            onDismissRequest = onDismiss,
            confirmButton = {
                TextButton(
                    onClick = {
                        dateState.selectedDateMillis?.let {
                            date = Instant.ofEpochMilli(it).atZone(ZoneOffset.UTC).toLocalDate()
                        }
                    },
                    enabled = dateState.selectedDateMillis != null,
                    modifier = Modifier.testTag("echo_schedule_date_next"),
                ) { Text("Next", color = UsTheme.extended.accent) }
            },
            dismissButton = {
                TextButton(onClick = onDismiss) { Text("Cancel", color = UsTheme.extended.textSecondary) }
            },
        ) { DatePicker(state = dateState) }
    } else {
        val timeState = rememberTimePickerState(initialHour = start.hour, initialMinute = start.minute)
        AlertDialog(
            onDismissRequest = onDismiss,
            title = { Text("Time") },
            text = { TimePicker(state = timeState) },
            confirmButton = {
                TextButton(
                    onClick = {
                        onPicked(picked.atTime(LocalTime.of(timeState.hour, timeState.minute)).atZone(zone).toInstant())
                    },
                    modifier = Modifier.testTag("echo_schedule_confirm"),
                ) { Text("Confirm", color = UsTheme.extended.accent) }
            },
            dismissButton = {
                TextButton(onClick = { date = null }) { Text("Back", color = UsTheme.extended.textSecondary) }
            },
        )
    }
}
