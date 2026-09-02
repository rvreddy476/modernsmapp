package com.us.android.core.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.selection.selectable
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.RadioButton
import androidx.compose.material3.RadioButtonDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import com.us.android.core.designsystem.theme.UsTheme

/**
 * One option in a single-choice list. The whole row is the target, and it
 * carries [Role.RadioButton] so a screen reader announces "1 of 3, selected"
 * rather than a label followed by an unrelated control.
 */
@Composable
fun UsSettingsRadioRow(
    title: String,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    description: String? = null,
    enabled: Boolean = true,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .selectable(selected = selected, enabled = enabled, role = Role.RadioButton, onClick = onClick)
            .padding(vertical = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
    ) {
        RadioButton(
            selected = selected,
            // The row handles the click; a null here keeps one target, not two.
            onClick = null,
            enabled = enabled,
            colors = RadioButtonDefaults.colors(selectedColor = UsTheme.extended.chatAccent),
        )
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Text(title, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            description?.let {
                Text(it, style = MaterialTheme.typography.bodySmall, color = UsTheme.extended.textMuted)
            }
        }
    }
}
