package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.defaultMinSize
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Single-select from a short list of options, rendered as a row of pills.
 *
 * The app's one choice control for small option sets — gender here, and
 * anything else with three or four mutually exclusive values later. Uses
 * `selectable` with [Role.RadioButton] so TalkBack announces it as a radio
 * group rather than a row of unrelated buttons.
 *
 * [selected] is nullable and [onSelect] can clear: an optional question must
 * be answerable with "no answer", and a control that cannot be unset quietly
 * turns an optional field into a required one.
 */
@Composable
fun <T> UsChoiceRow(
    options: List<UsChoice<T>>,
    selected: T?,
    onSelect: (T?) -> Unit,
    label: String,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    errorText: String? = null,
    /** When true, tapping the selected option clears it. */
    allowDeselect: Boolean = true,
) {
    Column(modifier = modifier) {
        Text(
            text = label,
            style = MaterialTheme.typography.bodySmall,
            color = if (errorText != null) {
                MaterialTheme.colorScheme.error
            } else {
                UsTheme.extended.textMuted
            },
            modifier = Modifier.padding(bottom = UsTheme.spacing.m),
        )

        Row(horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.m)) {
            options.forEach { option ->
                val isSelected = option.value == selected
                Box(
                    modifier = Modifier
                        .weight(1f)
                        .defaultMinSize(minHeight = 48.dp)
                        .background(
                            color = if (isSelected) {
                                MaterialTheme.colorScheme.primary
                            } else {
                                UsTheme.extended.bgCard
                            },
                            shape = RoundedCornerShape(UsTheme.radii.full),
                        )
                        .border(
                            width = 1.dp,
                            color = if (isSelected) {
                                MaterialTheme.colorScheme.primary
                            } else {
                                UsTheme.extended.borderMedium
                            },
                            shape = RoundedCornerShape(UsTheme.radii.full),
                        )
                        .selectable(
                            selected = isSelected,
                            enabled = enabled,
                            role = Role.RadioButton,
                            onClick = {
                                onSelect(
                                    if (isSelected && allowDeselect) null else option.value,
                                )
                            },
                        )
                        .padding(vertical = UsTheme.spacing.l),
                    contentAlignment = Alignment.Center,
                ) {
                    Text(
                        text = option.label,
                        style = MaterialTheme.typography.labelLarge,
                        color = when {
                            !enabled -> UsTheme.extended.textDim
                            isSelected -> androidx.compose.ui.graphics.Color.Black
                            else -> UsTheme.extended.textSecondary
                        },
                    )
                }
            }
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
}

/**
 * One option: what the user reads, and what goes on the wire.
 *
 * Kept as a pair on purpose. The label is presentation and will be
 * translated; [value] is the stored token and must not drift when the
 * wording changes.
 */
data class UsChoice<T>(val value: T, val label: String)

@Preview(name = "Choice row", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun UsChoiceRowPreview() {
    UsTheme {
        Column(
            modifier = Modifier
                .background(MaterialTheme.colorScheme.background)
                .padding(UsTheme.spacing.pageHorizontal),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xxl),
        ) {
            UsChoiceRow(
                options = listOf(
                    UsChoice("male", "Male"),
                    UsChoice("female", "Female"),
                    UsChoice("other", "Others"),
                ),
                selected = "female",
                onSelect = {},
                label = "Gender",
            )
            UsChoiceRow(
                options = listOf(
                    UsChoice("male", "Male"),
                    UsChoice("female", "Female"),
                    UsChoice("other", "Others"),
                ),
                selected = null,
                onSelect = {},
                label = "Gender",
                errorText = "Select an option",
            )
        }
    }
}
