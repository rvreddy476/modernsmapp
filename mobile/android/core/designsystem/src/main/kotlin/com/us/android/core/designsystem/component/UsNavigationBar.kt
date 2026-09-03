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
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.shadow
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.Color
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
 * The app's bottom navigation — Momentum's FLAT bar: ground-coloured, a 1dp
 * top border, plain labelled items, and — when [centerAction] is supplied —
 * a raised gradient "+" between the middle two items.
 *
 * Deliberately dumb: it receives the item list, the selected index and a
 * callback. It knows nothing about routes, the back stack or which feature
 * owns a tab. Selection is passed as an index rather than a route so the
 * design system never gains a dependency on the navigation library.
 *
 * This replaced the earlier floating-pill bar with a raised centre TAB (see
 * git history for `centerIndex`): Momentum's centre slot is always the
 * create action, never a selectable destination, so it is a callback rather
 * than an index into [items].
 */
@Composable
fun UsNavigationBar(
    items: List<UsNavItem>,
    selectedIndex: Int,
    onSelect: (index: Int) -> Unit,
    modifier: Modifier = Modifier,
    /** The raised centre "+" action, or null for a bar of plain tabs only. */
    centerAction: (() -> Unit)? = null,
) {
    val border = UsTheme.extended.borderMedium
    Column(
        modifier = modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCanvas)
            // Top edge only: the bar is flush with the screen's sides and
            // bottom, so a full border would draw a visible box.
            .drawBehind {
                val stroke = BAR_BORDER.toPx()
                drawLine(
                    color = border,
                    start = Offset(0f, stroke / 2),
                    end = Offset(size.width, stroke / 2),
                    strokeWidth = stroke,
                )
            }
            .navigationBarsPadding(),
    ) {
        // The create button is a SLOT in the row, the same width as a tab,
        // placed after the first half of the tabs — exactly the Figma frame
        // (two tabs, "+", two tabs). Overlaying it at the row's midpoint
        // instead looked right only for an even count and sat on top of a
        // label otherwise.
        Row(
            modifier = Modifier.fillMaxWidth(),
            horizontalArrangement = Arrangement.SpaceEvenly,
            verticalAlignment = Alignment.Bottom,
        ) {
            val split = items.size / 2
            items.forEachIndexed { index, item ->
                if (centerAction != null && index == split) {
                    CenterSlot(onClick = centerAction)
                }
                FlatTab(
                    item = item,
                    selected = index == selectedIndex,
                    onClick = { onSelect(index) },
                )
            }
            if (centerAction != null && items.size <= split) {
                CenterSlot(onClick = centerAction)
            }
        }
    }
}

/** The create button in a tab-width slot so the row spaces it like a tab. */
@Composable
private fun CenterSlot(onClick: () -> Unit) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier.width(TAB_WIDTH).padding(vertical = BAR_VERTICAL),
    ) {
        CenterCreateButton(onClick = onClick)
    }
}

/** A regular destination: glyph over a tiny label, 64dp wide per the frame. */
@Composable
private fun FlatTab(item: UsNavItem, selected: Boolean, onClick: () -> Unit) {
    val tint = if (selected) UsTheme.extended.accentSolid else UsTheme.extended.textMuted
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = Modifier
            .width(TAB_WIDTH)
            .clickable(onClick = onClick)
            .padding(vertical = BAR_VERTICAL)
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
            modifier = Modifier.size(TAB_GLYPH),
        )
        Text(
            text = item.label,
            style = MaterialTheme.typography.labelSmall,
            fontSize = TAB_LABEL_SIZE,
            fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
            color = tint,
        )
    }
}

/**
 * The raised centre create button — a 40dp gradient square with 14dp
 * corners and the design's red drop shadow (`0 3 5 #DC2626`), floating
 * above the flat bar rather than sitting in its row.
 */
@Composable
private fun CenterCreateButton(onClick: () -> Unit, modifier: Modifier = Modifier) {
    val shape = RoundedCornerShape(CENTER_BUTTON_RADIUS)
    val shadow = UsTheme.extended.accentDeep
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(CENTER_BUTTON)
            .shadow(
                elevation = CENTER_SHADOW,
                shape = shape,
                ambientColor = shadow,
                spotColor = shadow,
            )
            .background(UsTheme.extended.ctaGradient, shape)
            .clickable(onClick = onClick)
            .semantics {
                contentDescription = "Create"
                role = Role.Button
            },
    ) {
        Icon(
            imageVector = UsIcons.Create,
            contentDescription = null,
            tint = Color.White,
            modifier = Modifier.size(CENTER_GLYPH),
        )
    }
}

/**
 * The full five-tab bar, for the previews below.
 *
 * Presentation only. The shell (`:app`) builds the real list from the user's
 * module choices and pairs each item with a route; this sample exists so the
 * bar renders at its widest in the design-system previews.
 */
val UsDefaultNavItems: List<UsNavItem> = listOf(
    UsNavItem("Home", UsIcons.Home),
    UsNavItem("Messages", UsIcons.Comment, contentDescription = "Messages"),
    UsNavItem("Reels", UsIcons.Reels),
    UsNavItem("Explore", UsIcons.Explore),
    UsNavItem("Me", UsIcons.Profile, contentDescription = "My profile"),
)

private val TAB_WIDTH = 64.dp
private val TAB_GLYPH = 24.dp
private val TAB_LABEL_SIZE = 10.sp
private val BAR_VERTICAL = 10.dp
private val BAR_BORDER = 1.dp
private val CENTER_BUTTON = 40.dp
private val CENTER_BUTTON_RADIUS = 14.dp
private val CENTER_SHADOW = 5.dp
private val CENTER_GLYPH = 22.dp

@Preview(name = "Navigation bar — with create button", showBackground = true)
@Composable
private fun UsNavigationBarPreview() {
    UsTheme {
        UsNavigationBar(items = UsDefaultNavItems, selectedIndex = 0, onSelect = {}, centerAction = {})
    }
}

@Preview(name = "Navigation bar — last tab selected", showBackground = true)
@Composable
private fun UsNavigationBarLastPreview() {
    UsTheme {
        UsNavigationBar(items = UsDefaultNavItems, selectedIndex = 4, onSelect = {}, centerAction = {})
    }
}

@Preview(name = "Navigation bar — no create button", showBackground = true)
@Composable
private fun UsNavigationBarFlatPreview() {
    UsTheme {
        UsNavigationBar(items = UsDefaultNavItems.take(4), selectedIndex = 1, onSelect = {})
    }
}
