package com.us.android.feature.dating.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.ExperimentalLayoutApi
import androidx.compose.foundation.layout.FlowRow
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.FilterChip
import androidx.compose.material3.FilterChipDefaults
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.dating.profile.ProfileOption

/*
 * Chips for the M6 option lists: many at once (interests, languages, the pass
 * filters), one at a time (a lifestyle basic, the distance bucket), and the
 * read-only kind a card shows. Labels only — a chip never shows a code.
 */

/** Many-at-once: tapping toggles. [atLimit] disables the chips not yet chosen. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun MultiOptionChips(
    options: List<ProfileOption>,
    selected: Collection<String>,
    onToggle: (String) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    atLimit: Boolean = false,
) {
    FlowRow(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        options.forEach { option ->
            val chosen = option.code in selected
            OptionChip(
                label = option.label,
                selected = chosen,
                enabled = enabled && (chosen || !atLimit),
                onClick = { onToggle(option.code) },
            )
        }
    }
}

/**
 * One-at-a-time. [noneLabel], when given, is a chip that stands for "no
 * answer" and is selected while [selected] is null.
 */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun SingleOptionChips(
    options: List<ProfileOption>,
    selected: String?,
    onSelect: (String?) -> Unit,
    modifier: Modifier = Modifier,
    enabled: Boolean = true,
    noneLabel: String? = null,
) {
    FlowRow(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        options.forEach { option ->
            OptionChip(label = option.label, selected = option.code == selected, enabled = enabled, onClick = { onSelect(option.code) })
        }
        if (noneLabel != null) {
            OptionChip(label = noneLabel, selected = selected == null, enabled = enabled, onClick = { onSelect(null) })
        }
    }
}

/** Read-only chips on a card: an interest list, already resolved to labels. */
@OptIn(ExperimentalLayoutApi::class)
@Composable
fun LabelChips(labels: List<String>, modifier: Modifier = Modifier) {
    if (labels.isEmpty()) return
    FlowRow(
        modifier = modifier,
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
    ) {
        labels.forEach { label ->
            Text(
                text = label,
                style = MaterialTheme.typography.labelMedium,
                color = UsTheme.extended.textPrimary,
                maxLines = 1,
                modifier = Modifier
                    .background(UsTheme.extended.bgRaised, RoundedCornerShape(UsTheme.radii.full))
                    .padding(horizontal = UsTheme.spacing.m, vertical = UsTheme.spacing.xs),
            )
        }
    }
}

@Composable
private fun OptionChip(label: String, selected: Boolean, enabled: Boolean, onClick: () -> Unit) {
    FilterChip(
        selected = selected,
        onClick = onClick,
        enabled = enabled,
        label = { Text(label) },
        shape = RoundedCornerShape(UsTheme.radii.full),
        colors = FilterChipDefaults.filterChipColors(
            containerColor = UsTheme.extended.bgCardSolid,
            labelColor = UsTheme.extended.textSecondary,
            selectedContainerColor = UsTheme.extended.accentSolid.copy(alpha = SELECTED_ALPHA),
            selectedLabelColor = UsTheme.extended.textPrimary,
            disabledLabelColor = UsTheme.extended.textDim,
        ),
    )
}

private const val SELECTED_ALPHA = 0.22f
