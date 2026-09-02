// MatchingDeclarationName: this file's primary export is the UsNavigationBar
// composable; UsNavItem is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.designsystem.component

import androidx.compose.foundation.background
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * One destination in the bottom bar.
 *
 * [contentDescription] is separate from [label] because the two are not always
 * the same sentence: "Me" is a fine visual label and a poor spoken one.
 */
@Immutable
data class UsNavItem(
    val label: String,
    val icon: ImageVector,
    val contentDescription: String = label,
)

/**
 * The app's bottom navigation — the FLOATING PILL from the Figma home frames
 * (81:138): a rounded card-dark bar hovering over the canvas, tiny labels
 * under each glyph, and the CENTER destination raised on a brand-chip circle.
 *
 * Deliberately dumb: it receives the item list, the selected index and a
 * callback. It knows nothing about routes, the back stack or which feature
 * owns a tab. Selection is passed as an index rather than a route so the
 * design system never gains a dependency on the navigation library.
 *
 * [centerIndex] names the raised brand-chip destination, or null for a bar
 * of plain tabs. It is the caller's decision because the item list is now
 * the user's own choice of modules: when Reels is off there is no reel to
 * raise, and promoting whatever landed in the middle would be arbitrary.
 */
@Composable
fun UsNavigationBar(
    items: List<UsNavItem>,
    selectedIndex: Int,
    onSelect: (index: Int) -> Unit,
    modifier: Modifier = Modifier,
    centerIndex: Int? = null,
) {
    Box(
        modifier = modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .padding(top = UsTheme.spacing.xs, bottom = UsTheme.spacing.l),
        contentAlignment = Alignment.Center,
    ) {
        Row(
            verticalAlignment = Alignment.CenterVertically,
            horizontalArrangement = Arrangement.spacedBy(PILL_ITEM_GAP),
            modifier = Modifier
                .clip(CircleShape)
                .background(UsTheme.extended.bgCardSolid)
                .padding(
                    horizontal = UsTheme.spacing.xxl,
                    vertical = UsTheme.spacing.m,
                ),
        ) {
            items.forEachIndexed { index, item ->
                val selected = index == selectedIndex
                if (index == centerIndex) {
                    CenterTab(item = item, selected = selected, onClick = { onSelect(index) })
                } else {
                    PillTab(item = item, selected = selected, onClick = { onSelect(index) })
                }
            }
        }
    }
}

/** A regular destination: glyph over a tiny label. */
@Composable
private fun PillTab(item: UsNavItem, selected: Boolean, onClick: () -> Unit) {
    val tint = if (selected) UsTheme.extended.textPrimary else UsTheme.extended.textMuted
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier
            .clip(CircleShape)
            .clickable(onClick = onClick)
            .padding(
                horizontal = UsTheme.spacing.m,
                vertical = UsTheme.spacing.s,
            )
            .semantics {
                contentDescription = item.contentDescription
                role = Role.Tab
                this.selected = selected
            },
    ) {
        Icon(
            imageVector = item.icon,
            contentDescription = null,
            tint = tint,
            modifier = Modifier.size(PILL_GLYPH),
        )
        Text(
            text = item.label,
            style = MaterialTheme.typography.labelSmall,
            fontSize = PILL_LABEL_SIZE,
            fontWeight = if (selected) FontWeight.ExtraBold else FontWeight.SemiBold,
            color = tint,
        )
    }
}

/**
 * The raised middle destination — the brand-chip circle from the frame. The
 * chip colour flips with the theme (white on dark, cream on light), so the
 * one label-less tab is also the one that cannot be missed.
 */
@Composable
private fun CenterTab(item: UsNavItem, selected: Boolean, onClick: () -> Unit) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(CENTER_TAB)
            .clip(CircleShape)
            .background(UsTheme.extended.brandChip)
            .clickable(onClick = onClick)
            .semantics {
                contentDescription = item.contentDescription
                role = Role.Tab
                this.selected = selected
            },
    ) {
        Icon(
            imageVector = item.icon,
            contentDescription = null,
            tint = UsTheme.extended.onBrandChip,
            modifier = Modifier.size(CENTER_GLYPH),
        )
    }
}

/**
 * The full five-tab bar, for the previews below.
 *
 * Presentation only. The shell (`:app`) builds the real list from the user's
 * module choices and pairs each item with a route; this sample exists so the
 * pill renders at its widest in the design-system previews. Index 2 is the
 * raised Reels chip from the Figma pill (81:138).
 */
val UsDefaultNavItems: List<UsNavItem> = listOf(
    UsNavItem("Home", UsIcons.Home),
    UsNavItem("Messages", UsIcons.Comment, contentDescription = "Messages"),
    UsNavItem("Reels", UsIcons.Reels),
    UsNavItem("Explore", UsIcons.Explore),
    UsNavItem("Me", UsIcons.Profile, contentDescription = "My profile"),
)

private val PILL_ITEM_GAP = 10.dp
private val PILL_GLYPH = 22.dp
private val PILL_LABEL_SIZE = 10.sp
private val CENTER_TAB = 52.dp
private val CENTER_GLYPH = 24.dp

@Preview(name = "Navigation pill", showBackground = true)
@Composable
private fun UsNavigationBarPreview() {
    UsTheme {
        UsNavigationBar(items = UsDefaultNavItems, selectedIndex = 0, onSelect = {}, centerIndex = 2)
    }
}

@Preview(name = "Navigation pill — last tab", showBackground = true)
@Composable
private fun UsNavigationBarLastPreview() {
    UsTheme {
        UsNavigationBar(items = UsDefaultNavItems, selectedIndex = 4, onSelect = {}, centerIndex = 2)
    }
}

@Preview(name = "Navigation pill — no raised centre", showBackground = true)
@Composable
private fun UsNavigationBarFlatPreview() {
    UsTheme {
        UsNavigationBar(items = UsDefaultNavItems.take(4), selectedIndex = 1, onSelect = {})
    }
}
