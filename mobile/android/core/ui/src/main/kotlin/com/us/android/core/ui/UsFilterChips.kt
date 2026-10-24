package com.us.android.core.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.PaddingValues
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.lazy.LazyRow
import androidx.compose.foundation.lazy.items
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.designsystem.theme.UsTypeScale

/** One chip of a single-select row: the value it stands for and the words on it. */
data class UsFilterChip(val key: String, val label: String)

/**
 * A sideways-scrolling row of single-select pills — the web's category chips
 * (Reels Explore, Tube Trending and Topics, 2026-10-24) on Tube's chip look:
 * the selected one inverted, the rest on glass with a hairline.
 */
@Composable
fun UsFilterChipRow(
    chips: List<UsFilterChip>,
    selectedKey: String,
    onSelect: (String) -> Unit,
    modifier: Modifier = Modifier,
    tagPrefix: String = "filter_chip",
) {
    LazyRow(
        modifier = modifier
            .fillMaxWidth()
            .testTag("${tagPrefix}s"),
        horizontalArrangement = Arrangement.spacedBy(UsTheme.spacing.s),
        contentPadding = PaddingValues(horizontal = UsTheme.spacing.pageHorizontal, vertical = UsTheme.spacing.s),
    ) {
        items(chips, key = { it.key }) { chip ->
            UsFilterChipPill(
                chip = chip,
                active = chip.key == selectedKey,
                onClick = { onSelect(chip.key) },
                tag = "$tagPrefix:${chip.key.ifEmpty { "all" }}",
            )
        }
    }
}

@Composable
private fun UsFilterChipPill(chip: UsFilterChip, active: Boolean, onClick: () -> Unit, tag: String) {
    val shape = RoundedCornerShape(UsTheme.radii.full)
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .height(CHIP_HEIGHT)
            .clip(shape)
            .background(if (active) UsTheme.extended.textPrimary else UsTheme.extended.glassBg, shape)
            .border(HAIRLINE, if (active) UsTheme.extended.textPrimary else UsTheme.extended.glassBorder, shape)
            .selectable(selected = active, role = Role.Tab, onClick = onClick)
            .padding(horizontal = CHIP_HORIZONTAL)
            .testTag(tag),
    ) {
        Text(
            text = chip.label,
            style = MaterialTheme.typography.labelLarge,
            fontSize = UsTypeScale.sm.size,
            fontWeight = FontWeight.SemiBold,
            color = if (active) UsTheme.extended.bgCardSolid else UsTheme.extended.textPrimary,
            maxLines = 1,
        )
    }
}

private val CHIP_HEIGHT = 34.dp
private val CHIP_HORIZONTAL = 14.dp
private val HAIRLINE = 1.dp
