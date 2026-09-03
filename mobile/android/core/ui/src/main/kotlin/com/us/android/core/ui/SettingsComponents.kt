package com.us.android.core.ui

import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.DropdownMenu
import androidx.compose.material3.DropdownMenuItem
import androidx.compose.material3.HorizontalDivider
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.SwitchColors
import androidx.compose.material3.SwitchDefaults
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.heading
import androidx.compose.ui.semantics.semantics
import com.us.android.core.designsystem.theme.UsTheme

/** Shared settings primitives. Product screens supply policy and copy only. */
@Composable
fun UsSettingsSection(
    title: String,
    modifier: Modifier = Modifier,
    content: @Composable () -> Unit,
) {
    Column(modifier = modifier.fillMaxWidth()) {
        Text(
            text = title,
            style = MaterialTheme.typography.titleMedium,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier
                .padding(vertical = UsTheme.spacing.l)
                .semantics { heading() },
        )
        content()
    }
}

@Composable
fun UsSettingsSwitchRow(
    title: String,
    checked: Boolean,
    onCheckedChange: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
    description: String? = null,
    enabled: Boolean = true,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .clickable(enabled = enabled, role = Role.Switch) { onCheckedChange(!checked) }
            .padding(vertical = UsTheme.spacing.l),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.xl),
    ) {
        Column(
            modifier = Modifier.weight(1f),
            verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        ) {
            Text(title, style = MaterialTheme.typography.bodyLarge, color = UsTheme.extended.textPrimary)
            description?.let {
                Text(
                    it,
                    style = MaterialTheme.typography.bodySmall,
                    color = UsTheme.extended.textMuted,
                )
            }
        }
        Switch(
            checked = checked,
            onCheckedChange = onCheckedChange,
            enabled = enabled,
            colors = usSwitchColors(),
        )
    }
}

@Composable
fun UsSettingsLinkRow(
    title: String,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    description: String? = null,
    value: String? = null,
    enabled: Boolean = true,
) {
    Column(
        modifier = modifier
            .fillMaxWidth()
            .clickable(enabled = enabled, onClick = onClick)
            .padding(vertical = UsTheme.spacing.xl),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Row(
            horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            Text(
                title,
                style = MaterialTheme.typography.bodyLarge,
                color = UsTheme.extended.textPrimary,
                modifier = Modifier.weight(1f),
            )
            value?.let {
                Text(
                    text = it,
                    style = MaterialTheme.typography.bodyMedium,
                    color = UsTheme.extended.textMuted,
                )
            }
        }
        description?.let {
            Text(
                text = it,
                style = MaterialTheme.typography.bodySmall,
                color = UsTheme.extended.textMuted,
            )
        }
        HorizontalDivider(color = UsTheme.extended.borderSubtle)
    }
}

data class UsSettingsOption(val value: String, val label: String)

@Composable
fun UsSettingsSelectRow(
    title: String,
    selected: String,
    options: List<UsSettingsOption>,
    onSelected: (String) -> Unit,
    modifier: Modifier = Modifier,
    description: String? = null,
    enabled: Boolean = true,
) {
    var expanded by remember { mutableStateOf(false) }
    // An empty selection is a real state (nothing chosen yet), and a row with
    // no value on it reads as broken rather than unset.
    val label = options.firstOrNull { it.value == selected }?.label ?: selected.ifBlank { "Not set" }
    Column(modifier = modifier.fillMaxWidth()) {
        UsSettingsLinkRow(
            title = title,
            value = label,
            description = description,
            enabled = enabled,
            onClick = { expanded = true },
        )
        DropdownMenu(expanded = expanded, onDismissRequest = { expanded = false }) {
            options.forEach { option ->
                DropdownMenuItem(
                    text = { Text(option.label) },
                    onClick = {
                        expanded = false
                        onSelected(option.value)
                    },
                )
            }
        }
    }
}

/**
 * Switch colours for every settings toggle.
 *
 * Material's default paints a checked track in the theme primary, which is
 * the brand orange. Settings toggles read as state, not as calls to action,
 * so they take the green accent the chat surfaces already use; the thumb
 * stays white so the on/off contrast survives both themes.
 */
@Composable
fun usSwitchColors(): SwitchColors = SwitchDefaults.colors(
    checkedTrackColor = UsTheme.extended.chatAccent,
    checkedThumbColor = androidx.compose.ui.graphics.Color.White,
)
