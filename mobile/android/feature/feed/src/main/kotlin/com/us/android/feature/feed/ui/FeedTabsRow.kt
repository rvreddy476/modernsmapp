package com.us.android.feature.feed.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.IntrinsicSize
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxHeight
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import com.us.android.core.designsystem.theme.UsTheme

/**
 * "For You | Following | HashTag" — the row directly under the home header
 * in the Figma home frame (YsWb936muw8pwIxgb0je2A, node 2:10).
 *
 * Three equal columns at 44dp; the label 13sp bold, the active one in the
 * primary text colour over a 2dp WHITE underline (the ember one read as a
 * stray accent — founder, 2026-09-05), the others muted; a 1dp
 * `borderMedium` rule closes the row. The underline is the label's width,
 * not the column's — a full-width bar under a short word reads as a border,
 * not a selection.
 *
 * Every tab is a `Role.Tab` in a selectable group, so TalkBack announces
 * "For You, tab, 1 of 3, selected" rather than three unrelated buttons.
 */
@Composable
internal fun FeedTabsRow(
    selected: FeedTab,
    onSelect: (FeedTab) -> Unit,
    modifier: Modifier = Modifier,
) {
    val rule = UsTheme.extended.borderMedium
    Row(
        modifier = modifier
            .fillMaxWidth()
            .height(TABS_ROW_HEIGHT)
            .drawBehind {
                val stroke = RULE_HEIGHT.toPx()
                drawLine(
                    color = rule,
                    start = Offset(0f, size.height - stroke / 2),
                    end = Offset(size.width, size.height - stroke / 2),
                    strokeWidth = stroke,
                )
            }
            .selectableGroup(),
    ) {
        FeedTab.entries.forEach { tab ->
            FeedTabItem(
                tab = tab,
                active = tab == selected,
                onClick = { onSelect(tab) },
                modifier = Modifier.weight(1f),
            )
        }
    }
}

@Composable
private fun FeedTabItem(
    tab: FeedTab,
    active: Boolean,
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
) {
    Box(
        modifier = modifier
            .fillMaxHeight()
            .selectable(selected = active, role = Role.Tab, onClick = onClick),
        contentAlignment = Alignment.Center,
    ) {
        // The column is as wide as its label, so the underline below matches it.
        Column(
            modifier = Modifier.width(IntrinsicSize.Max),
            horizontalAlignment = Alignment.CenterHorizontally,
        ) {
            Text(
                text = tab.label,
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.Bold,
                color = if (active) UsTheme.extended.textPrimary else UsTheme.extended.textMuted,
                maxLines = 1,
            )
            // Always laid out, transparent when inactive, so the label does
            // not shift by 2dp when the selection moves.
            Box(
                modifier = Modifier
                    .padding(top = UNDERLINE_GAP)
                    .fillMaxWidth()
                    .height(UNDERLINE_HEIGHT)
                    .background(if (active) UsTheme.extended.textPrimary else Color.Transparent),
            )
        }
    }
}

private val TABS_ROW_HEIGHT = 44.dp
private val RULE_HEIGHT = 1.dp
private val UNDERLINE_HEIGHT = 2.dp
private val UNDERLINE_GAP = 4.dp

@Preview(name = "Feed tabs — For You", showBackground = true, backgroundColor = 0xFF0B1220)
@Composable
private fun FeedTabsRowPreview() {
    UsTheme { FeedTabsRow(selected = FeedTab.FOR_YOU, onSelect = {}) }
}

@Preview(name = "Feed tabs — HashTag", showBackground = true, backgroundColor = 0xFF0B1220)
@Composable
private fun FeedTabsRowHashtagPreview() {
    UsTheme { FeedTabsRow(selected = FeedTab.HASHTAG, onSelect = {}) }
}
