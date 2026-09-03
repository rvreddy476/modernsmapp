package com.us.android.core.ui

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Switch
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * One notification category with its two delivery switches side by side —
 * the TikTok layout, where "In-app" and "Push" are columns and each row is a
 * kind of event. Pair it with [UsSettingsDualSwitchHeader] above the first
 * row so the columns are labelled once rather than on every line.
 *
 * Each switch carries its own content description ("<title>, in-app"), so a
 * screen reader distinguishes the two controls that share a row.
 */
@Composable
fun UsSettingsDualSwitchRow(
    title: String,
    inApp: Boolean,
    push: Boolean,
    onInAppChange: (Boolean) -> Unit,
    onPushChange: (Boolean) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    pushEnabled: Boolean = enabled,
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(vertical = UsTheme.spacing.m),
        verticalAlignment = Alignment.CenterVertically,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Text(
            title,
            style = MaterialTheme.typography.bodyLarge,
            color = UsTheme.extended.textPrimary,
            modifier = Modifier.weight(1f),
        )
        Column(
            modifier = Modifier
                .width(SWITCH_COLUMN_WIDTH)
                .semantics { contentDescription = "$title, in-app" },
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Switch(checked = inApp, onCheckedChange = onInAppChange, enabled = enabled)
        }
        Column(
            modifier = Modifier
                .width(SWITCH_COLUMN_WIDTH)
                .semantics { contentDescription = "$title, push" },
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Switch(checked = push, onCheckedChange = onPushChange, enabled = pushEnabled)
        }
    }
}

/** The column labels for a block of [UsSettingsDualSwitchRow]s. */
@Composable
fun UsSettingsDualSwitchHeader(
    modifier: Modifier = Modifier,
    inAppLabel: String = "In-app",
    pushLabel: String = "Push",
) {
    Row(
        modifier = modifier
            .fillMaxWidth()
            .padding(bottom = UsTheme.spacing.xs),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.l),
    ) {
        Text("", modifier = Modifier.weight(1f))
        ColumnLabel(inAppLabel)
        ColumnLabel(pushLabel)
    }
}

@Composable
private fun ColumnLabel(text: String) {
    Text(
        text,
        style = MaterialTheme.typography.labelMedium,
        color = UsTheme.extended.textMuted,
        textAlign = TextAlign.Center,
        modifier = Modifier.width(SWITCH_COLUMN_WIDTH),
    )
}

private val SWITCH_COLUMN_WIDTH = 64.dp
