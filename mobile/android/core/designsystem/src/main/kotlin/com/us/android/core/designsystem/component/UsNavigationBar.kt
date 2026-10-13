// MatchingDeclarationName: this file's primary export is the UsNavigationBar
// composable; UsNavItem is the value type it consumes.
@file:Suppress("MatchingDeclarationName")

package com.us.android.core.designsystem.component

import androidx.compose.animation.core.animateFloatAsState
import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.clickable
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.BoxWithConstraints
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.heightIn
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.widthIn
import androidx.compose.foundation.selection.selectable
import androidx.compose.foundation.selection.selectableGroup
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.Immutable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.drawBehind
import androidx.compose.ui.draw.rotate
import androidx.compose.ui.geometry.Offset
import androidx.compose.ui.graphics.lerp
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.LocalDensity
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.text.style.TextAlign
import androidx.compose.ui.tooling.preview.Preview
import androidx.compose.ui.unit.dp
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
 * A quiet, adaptive navigation surface. Slots share available width instead
 * of demanding 64dp each (six slots used to overflow small phones). Selection
 * has a compact tinted plate; every slot has at least a 48dp touch height.
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
    /**
     * True while the Create sheet is open: the "+" turns 45° into a "×", so
     * the same tile reads as the way to close what it opened.
     */
    centerActive: Boolean = false,
) {
    val border = UsTheme.extended.borderMedium
    Column(
        modifier = modifier
            .fillMaxWidth()
            .background(UsTheme.extended.bgCardSolid)
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
        BoxWithConstraints(Modifier.fillMaxWidth(), contentAlignment = Alignment.Center) {
            // At large accessibility sizes a six-slot phone bar cannot fit
            // full words. Keep the spoken labels and full touch targets;
            // never split a destination name through the middle of a word.
            val showLabels = maxWidth >= LABEL_WIDE_BREAKPOINT ||
                LocalDensity.current.fontScale <= LABEL_FONT_SCALE_LIMIT
            Row(
                modifier = Modifier.widthIn(max = BAR_MAX_WIDTH).fillMaxWidth().selectableGroup(),
                verticalAlignment = Alignment.CenterVertically,
            ) {
                val split = items.size / 2
                items.forEachIndexed { index, item ->
                    if (centerAction != null && index == split) {
                        CenterSlot(onClick = centerAction, active = centerActive, modifier = Modifier.weight(1f))
                    }
                    FlatTab(
                        item = item,
                        selected = index == selectedIndex,
                        onClick = { onSelect(index) },
                        modifier = Modifier.weight(1f),
                        showLabel = showLabels,
                    )
                }
                if (centerAction != null && items.size <= split) {
                    CenterSlot(onClick = centerAction, active = centerActive, modifier = Modifier.weight(1f))
                }
            }
        }
    }
}

/** The create button in a tab-width slot so the row spaces it like a tab. */
@Composable
private fun CenterSlot(onClick: () -> Unit, active: Boolean, modifier: Modifier) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier.heightIn(min = TAB_MIN_HEIGHT).padding(vertical = BAR_VERTICAL),
    ) {
        CenterCreateButton(onClick = onClick, active = active)
    }
}

/** A regular destination: glyph over a readable, wrapping label. */
@Composable
private fun FlatTab(
    item: UsNavItem,
    selected: Boolean,
    onClick: () -> Unit,
    modifier: Modifier,
    showLabel: Boolean,
) {
    val tint = if (selected) UsTheme.extended.accentSolid else UsTheme.extended.textMuted
    Column(
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
        modifier = modifier
            .heightIn(min = TAB_MIN_HEIGHT)
            .selectable(selected = selected, role = Role.Tab, onClick = onClick)
            .padding(vertical = BAR_VERTICAL)
            .semantics {
                contentDescription = item.contentDescription
                role = Role.Tab
                this.selected = selected
            },
    ) {
        Box(
            modifier = Modifier
                .size(width = TAB_PLATE_WIDTH, height = TAB_PLATE_HEIGHT)
                .background(
                    if (selected) UsTheme.extended.unreadRow else androidx.compose.ui.graphics.Color.Transparent,
                    RoundedCornerShape(UsTheme.radii.full),
                ),
            contentAlignment = Alignment.Center,
        ) {
            Icon(
                imageVector = item.icon,
                contentDescription = null,
                tint = tint,
                modifier = Modifier.size(TAB_GLYPH),
            )
        }
        if (showLabel) {
            Text(
                text = item.label,
                style = MaterialTheme.typography.labelSmall,
                fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
                color = tint,
                textAlign = TextAlign.Center,
                modifier = Modifier.padding(horizontal = 2.dp),
            )
        }
    }
}

/**
 * The centre create button — a 48dp rounded square that, AT REST, is drawn
 * the way the other glyphs are: a muted 1.5dp outline with a muted plus.
 * Only while the Create sheet is open does it fill white with the navy plus
 * and turn into a "×". The always-white tile (and before it the red glow)
 * read as "already selected" to the founder (2026-09-04); a highlight has
 * to mean something, so it is reserved for the open state.
 */
@Composable
private fun CenterCreateButton(
    onClick: () -> Unit,
    modifier: Modifier = Modifier,
    active: Boolean = false,
) {
    val shape = RoundedCornerShape(CENTER_BUTTON_RADIUS)
    // The whole tile turns, not just the glyph, so the square's corners say
    // "this is the same button" while the plus becomes a cross.
    val rotation by animateFloatAsState(
        targetValue = if (active) CENTER_ACTIVE_ROTATION else 0f,
        label = "createRotation",
    )
    val fill by animateFloatAsState(
        targetValue = if (active) 1f else 0f,
        label = "createFill",
    )
    val muted = UsTheme.extended.textMuted
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(CENTER_BUTTON)
            .rotate(rotation)
            .background(UsTheme.extended.textPrimary.copy(alpha = fill), shape)
            .border(CENTER_OUTLINE, muted.copy(alpha = 1f - fill), shape)
            .clickable(onClick = onClick)
            .semantics {
                contentDescription = if (active) "Close create" else "Create"
                role = Role.Button
            },
    ) {
        Icon(
            imageVector = UsIcons.Create,
            contentDescription = null,
            tint = lerp(muted, UsTheme.extended.bgCardSolid, fill),
            modifier = Modifier.size(CENTER_GLYPH),
        )
    }
}

/** A quarter-turn less a bit: "+" read as "×". */
private const val CENTER_ACTIVE_ROTATION = 45f

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

private val BAR_MAX_WIDTH = 720.dp
private val LABEL_WIDE_BREAKPOINT = 480.dp
private const val LABEL_FONT_SCALE_LIMIT = 1.2f
private val TAB_MIN_HEIGHT = 64.dp
private val TAB_GLYPH = 22.dp
private val TAB_PLATE_WIDTH = 42.dp
private val TAB_PLATE_HEIGHT = 28.dp
private val BAR_VERTICAL = 6.dp
private val BAR_BORDER = 1.dp
private val CENTER_BUTTON = 48.dp
private val CENTER_BUTTON_RADIUS = 14.dp
private val CENTER_OUTLINE = 1.5.dp
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
