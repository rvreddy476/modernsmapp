package com.us.android.core.feed.ui.schedule

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.rememberScrollState
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.foundation.verticalScroll
import androidx.compose.material3.DatePicker
import androidx.compose.material3.DatePickerDefaults
import androidx.compose.material3.DatePickerState
import androidx.compose.material3.DisplayMode
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.ModalBottomSheet
import androidx.compose.material3.SelectableDates
import androidx.compose.material3.Text
import androidx.compose.material3.TimePicker
import androidx.compose.material3.TimePickerDefaults
import androidx.compose.material3.TimePickerState
import androidx.compose.material3.rememberDatePickerState
import androidx.compose.material3.rememberModalBottomSheetState
import androidx.compose.material3.rememberTimePickerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.component.UsButton
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.publish.ScheduleWindow
import java.time.Instant
import java.time.LocalTime
import java.time.ZoneId
import java.time.ZoneOffset

/**
 * "Schedule" (2026-09-05): a date, then a time, inside [ScheduleWindow] —
 * five minutes to thirty days ahead. The calendar only offers days in the
 * window; the time is checked against the window as it changes, and the
 * button says why an instant is refused rather than letting the server.
 * "Post now instead" clears a schedule that was set. Shared by the reel form
 * and Tube's scheduled list (2026-09-05), so both pick from one window.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
fun ScheduleSheet(
    initial: Instant?,
    onSchedule: (Instant) -> Unit,
    onClear: () -> Unit,
    onDismiss: () -> Unit,
) {
    val zone = remember { ZoneId.systemDefault() }
    val now = remember { Instant.now() }
    val start = remember(initial) { (initial ?: now.plusMillis(DEFAULT_AHEAD_MILLIS)).atZone(zone) }
    var step by rememberSaveable { mutableStateOf(Step.DATE) }
    val dateState = rememberDatePickerState(
        initialSelectedDateMillis = start.toLocalDate().atStartOfDay(ZoneOffset.UTC).toInstant().toEpochMilli(),
        initialDisplayMode = DisplayMode.Picker,
        selectableDates = remember(now) { windowDates(now, zone) },
    )
    val timeState = rememberTimePickerState(initialHour = start.hour, initialMinute = start.minute, is24Hour = true)
    val chosen = chosenInstant(dateState, timeState, zone)
    val check = chosen?.let { ScheduleWindow.check(it, Instant.now()) }

    ModalBottomSheet(
        onDismissRequest = onDismiss,
        sheetState = rememberModalBottomSheetState(skipPartiallyExpanded = true),
        containerColor = UsTheme.extended.bgCardSolid,
        contentColor = UsTheme.extended.textPrimary,
        shape = RoundedCornerShape(topStart = SHEET_RADIUS, topEnd = SHEET_RADIUS),
        scrimColor = Color.Black.copy(alpha = SCRIM_ALPHA),
        dragHandle = null,
        modifier = Modifier.testTag("schedule-sheet"),
    ) {
        Column(
            modifier = Modifier
                .fillMaxWidth()
                .verticalScroll(rememberScrollState())
                .padding(horizontal = UsTheme.spacing.pageHorizontal)
                .padding(bottom = UsTheme.spacing.xxl)
                .navigationBarsPadding(),
        ) {
            Handle()
            SheetTitles(step = step, preview = chosen?.let { "Scheduled for ${ScheduleWindow.label(it, zone)}" })
            Spacer(Modifier.height(UsTheme.spacing.l))
            when (step) {
                Step.DATE -> DatePicker(
                    state = dateState,
                    title = null,
                    headline = null,
                    showModeToggle = false,
                    colors = DatePickerDefaults.colors(containerColor = UsTheme.extended.bgCardSolid),
                )
                Step.TIME -> Box(modifier = Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
                    TimePicker(
                        state = timeState,
                        colors = TimePickerDefaults.colors(containerColor = UsTheme.extended.bgCardSolid),
                    )
                }
            }
            if (step == Step.TIME) WindowNotice(check = check)
            Spacer(Modifier.height(UsTheme.spacing.l))
            val canProceed =
                if (step == Step.DATE) dateState.selectedDateMillis != null else check == ScheduleWindow.Check.Ok
            StepButton(
                step = step,
                canProceed = canProceed,
                onNext = { step = Step.TIME },
                onSchedule = { chosen?.let(onSchedule) },
            )
            if (initial != null) ClearRow(onClear = onClear)
        }
    }
}

private enum class Step { DATE, TIME }

/** The instant the two pickers name together, in the viewer's zone; null until a day is picked. */
@OptIn(ExperimentalMaterial3Api::class)
private fun chosenInstant(dateState: DatePickerState, timeState: TimePickerState, zone: ZoneId): Instant? =
    dateState.selectedDateMillis?.let { millis ->
        val date = Instant.ofEpochMilli(millis).atZone(ZoneOffset.UTC).toLocalDate()
        date.atTime(LocalTime.of(timeState.hour, timeState.minute)).atZone(zone).toInstant()
    }

@Composable
private fun SheetTitles(step: Step, preview: String?) {
    Text(
        text = if (step == Step.DATE) "Pick a day" else "Pick a time",
        style = MaterialTheme.typography.titleLarge.copy(fontSize = TITLE_SIZE),
        fontWeight = FontWeight.Bold,
        color = UsTheme.extended.textPrimary,
    )
    Text(
        text = preview ?: "Up to 30 days ahead.",
        style = MaterialTheme.typography.bodySmall,
        color = UsTheme.extended.textMuted,
        modifier = Modifier.testTag("schedule-preview"),
    )
}

/** Why the instant is refused, under the time picker; nothing when it is fine. */
@Composable
private fun WindowNotice(check: ScheduleWindow.Check?) {
    val message = check?.let { ScheduleWindow.message(it) } ?: return
    Text(
        text = message,
        style = MaterialTheme.typography.bodySmall,
        color = MaterialTheme.colorScheme.error,
        modifier = Modifier.testTag("schedule-error"),
    )
    Spacer(Modifier.height(UsTheme.spacing.m))
}

/** "Next" on the day, "Schedule" on the time. */
@Composable
private fun StepButton(step: Step, canProceed: Boolean, onNext: () -> Unit, onSchedule: () -> Unit) {
    when (step) {
        Step.DATE -> UsButton(
            text = "Next",
            onClick = onNext,
            enabled = canProceed,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("schedule-next"),
        )
        Step.TIME -> UsButton(
            text = "Schedule",
            onClick = onSchedule,
            enabled = canProceed,
            modifier = Modifier
                .fillMaxWidth()
                .testTag("schedule-confirm"),
        )
    }
}

/** "Post now instead" — only when there is a schedule to clear. */
@Composable
private fun ClearRow(onClear: () -> Unit) {
    Spacer(Modifier.height(UsTheme.spacing.s))
    Box(
        modifier = Modifier
            .fillMaxWidth()
            .clip(RoundedCornerShape(UsTheme.radii.full))
            .clickable(onClick = onClear)
            .padding(vertical = UsTheme.spacing.l)
            .testTag("schedule-clear"),
        contentAlignment = Alignment.Center,
    ) {
        Text(
            text = "Post now instead",
            style = MaterialTheme.typography.labelLarge,
            color = UsTheme.extended.textMuted,
        )
    }
}

/** The calendar offers today through thirty days out; the time step checks the five-minute floor. */
@OptIn(ExperimentalMaterial3Api::class)
private fun windowDates(now: Instant, zone: ZoneId): SelectableDates {
    val first = now.atZone(zone).toLocalDate()
    val last = now.plusMillis(ScheduleWindow.MAX_AHEAD_MILLIS).atZone(zone).toLocalDate()
    return object : SelectableDates {
        override fun isSelectableDate(utcTimeMillis: Long): Boolean {
            val date = Instant.ofEpochMilli(utcTimeMillis).atZone(ZoneOffset.UTC).toLocalDate()
            return !date.isBefore(first) && !date.isAfter(last)
        }

        override fun isSelectableYear(year: Int): Boolean = year >= first.year && year <= last.year
    }
}

@Composable
private fun Handle() {
    Row(
        modifier = Modifier
            .fillMaxWidth()
            .padding(top = UsTheme.spacing.m, bottom = UsTheme.spacing.l),
        horizontalArrangement = Arrangement.Center,
    ) {
        Box(
            modifier = Modifier
                .size(width = HANDLE_WIDTH, height = HANDLE_HEIGHT)
                .clip(CircleShape)
                .background(UsTheme.extended.textMuted.copy(alpha = HANDLE_ALPHA)),
        )
    }
}

/** Where the picker opens by default: an hour ahead, on the hour's minute. */
private const val DEFAULT_AHEAD_MILLIS = 60L * 60L * 1_000L
private const val SCRIM_ALPHA = 0.55f
private const val HANDLE_ALPHA = 0.35f
private val SHEET_RADIUS = 28.dp
private val HANDLE_WIDTH = 32.dp
private val HANDLE_HEIGHT = 4.dp
private val TITLE_SIZE = 20.sp
