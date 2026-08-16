package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.DatePicker
import androidx.compose.material3.DatePickerDefaults
import androidx.compose.material3.DatePickerDialog
import androidx.compose.material3.ExperimentalMaterial3Api
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.SelectableDates
import androidx.compose.material3.Text
import androidx.compose.material3.TextButton
import androidx.compose.material3.rememberDatePickerState
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme
import java.time.Instant
import java.time.LocalDate
import java.time.ZoneOffset
import java.time.format.DateTimeFormatter

/**
 * The app's single date input.
 *
 * Every screen that needs a date uses this one component — there is no second
 * date field anywhere in the app, and there should never be. A hand-rolled
 * text field per screen is how you end up with four different date formats,
 * four different validation rules, and a birthday that parses on one screen
 * and not another.
 *
 * Contract:
 *  - The value in and out is an ISO `YYYY-MM-DD` string, which is exactly what
 *    the backend expects (`ParseDOB` uses the `2006-01-02` layout). No caller
 *    ever formats a date by hand.
 *  - The field is read-only and opens a calendar. Free typing is not offered,
 *    which removes the entire class of "is 03/04 March or April" ambiguity.
 *  - [minDate] / [maxDate] disable out-of-range days *in the calendar itself*,
 *    so an age gate is enforced by not letting the user pick, rather than by
 *    scolding them after they do.
 */
@OptIn(ExperimentalMaterial3Api::class)
@Composable
@Suppress("LongParameterList")
fun UsDatePickerField(
    value: String,
    onValueChange: (String) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    placeholder: String = "Select a date",
    errorText: String? = null,
    enabled: Boolean = true,
    minDate: LocalDate? = null,
    maxDate: LocalDate? = null,
    /** Where the calendar opens when nothing is selected yet. */
    initialDisplayedDate: LocalDate? = null,
) {
    var showDialog by remember { mutableStateOf(false) }
    val selected = remember(value) { value.toLocalDateOrNull() }
    val isError = errorText != null

    Column(modifier = modifier) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = if (isError) MaterialTheme.colorScheme.error else UsTheme.extended.textMuted,
            modifier = Modifier.padding(bottom = UsTheme.spacing.s),
        )

        Box(
            modifier = Modifier
                .fillMaxWidth()
                .defaultMinSize(minHeight = 56.dp)
                .background(
                    UsTheme.extended.bgCard,
                    RoundedCornerShape(UsTheme.radii.medium),
                )
                .border(
                    width = 1.dp,
                    color = if (isError) {
                        MaterialTheme.colorScheme.error
                    } else {
                        UsTheme.extended.borderMedium
                    },
                    shape = RoundedCornerShape(UsTheme.radii.medium),
                )
                .clickable(enabled = enabled) { showDialog = true }
                .padding(horizontal = UsTheme.spacing.xxl, vertical = UsTheme.spacing.xl),
            contentAlignment = Alignment.CenterStart,
        ) {
            Text(
                text = selected?.format(DISPLAY_FORMAT) ?: placeholder,
                style = MaterialTheme.typography.bodyLarge,
                color = when {
                    !enabled -> UsTheme.extended.textDim
                    selected != null -> UsTheme.extended.textPrimary
                    else -> UsTheme.extended.textDim
                },
            )
        }

        if (errorText != null) {
            Text(
                text = errorText,
                style = MaterialTheme.typography.labelMedium,
                color = MaterialTheme.colorScheme.error,
                modifier = Modifier.padding(start = 12.dp, top = 4.dp),
            )
        }
    }

    if (showDialog) {
        CalendarDialog(
            selected = selected,
            minDate = minDate,
            maxDate = maxDate,
            initialDisplayedDate = initialDisplayedDate,
            onDismiss = { showDialog = false },
            onPicked = { onValueChange(it.format(ISO_FORMAT)) },
        )
    }
}

@OptIn(ExperimentalMaterial3Api::class)
@Composable
private fun CalendarDialog(
    selected: LocalDate?,
    minDate: LocalDate?,
    maxDate: LocalDate?,
    initialDisplayedDate: LocalDate?,
    onDismiss: () -> Unit,
    onPicked: (LocalDate) -> Unit,
) {
    val pickerState = rememberDatePickerState(
        initialSelectedDateMillis = selected?.toUtcMillis(),
        initialDisplayedMonthMillis = (selected ?: initialDisplayedDate)?.toUtcMillis(),
        selectableDates = rangeOf(minDate, maxDate),
    )

    DatePickerDialog(
        onDismissRequest = onDismiss,
        // An opaque surface on purpose: the theme's bgCard is a 4%-alpha
        // white, and a translucent dialog renders the calendar over whatever
        // happens to be behind it.
        colors = DatePickerDefaults.colors(
            containerColor = MaterialTheme.colorScheme.surface,
        ),
        confirmButton = {
            TextButton(
                onClick = {
                    pickerState.selectedDateMillis?.toLocalDateUtc()?.let(onPicked)
                    onDismiss()
                },
            ) {
                Text("Select", color = MaterialTheme.colorScheme.primary)
            }
        },
        dismissButton = {
            TextButton(onClick = onDismiss) {
                Text("Cancel", color = UsTheme.extended.textMuted)
            }
        },
    ) {
        DatePicker(state = pickerState, showModeToggle = false)
    }
}

/**
 * Restricts which days the calendar will accept.
 *
 * Comparison is done in UTC because that is the frame the picker reports
 * selections in; mixing it with the device's local zone is how a birthday
 * lands a day off for anyone east or west of UTC.
 */
@OptIn(ExperimentalMaterial3Api::class)
private fun rangeOf(minDate: LocalDate?, maxDate: LocalDate?): SelectableDates =
    object : SelectableDates {
        override fun isSelectableDate(utcTimeMillis: Long): Boolean {
            val date = utcTimeMillis.toLocalDateUtc()
            if (minDate != null && date.isBefore(minDate)) return false
            if (maxDate != null && date.isAfter(maxDate)) return false
            return true
        }

        override fun isSelectableYear(year: Int): Boolean {
            if (minDate != null && year < minDate.year) return false
            if (maxDate != null && year > maxDate.year) return false
            return true
        }
    }

private fun LocalDate.toUtcMillis(): Long =
    atStartOfDay(ZoneOffset.UTC).toInstant().toEpochMilli()

private fun Long.toLocalDateUtc(): LocalDate =
    Instant.ofEpochMilli(this).atZone(ZoneOffset.UTC).toLocalDate()

private fun String.toLocalDateOrNull(): LocalDate? =
    runCatching { LocalDate.parse(trim()) }.getOrNull()

/** ISO on the wire (`YYYY-MM-DD`), human-readable on screen. */
private val ISO_FORMAT: DateTimeFormatter = DateTimeFormatter.ISO_LOCAL_DATE
private val DISPLAY_FORMAT: DateTimeFormatter = DateTimeFormatter.ofPattern("d MMM yyyy")

@Preview(name = "Date field", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun UsDatePickerFieldPreview() {
    UsTheme {
        Column(
            modifier = Modifier
                .background(MaterialTheme.colorScheme.background)
                .padding(UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            UsDatePickerField(value = "", onValueChange = {}, label = "Date of birth")
            UsDatePickerField(
                value = "1990-04-12",
                onValueChange = {},
                label = "Date of birth",
            )
            UsDatePickerField(
                value = "",
                onValueChange = {},
                label = "Date of birth",
                errorText = "You must be at least 18 to create an account",
            )
        }
    }
}
