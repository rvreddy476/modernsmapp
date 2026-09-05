package com.us.android.feature.tube.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.graphics.Color
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import androidx.compose.ui.semantics.Role
import androidx.compose.ui.semantics.contentDescription
import androidx.compose.ui.semantics.role
import androidx.compose.ui.semantics.selected
import androidx.compose.ui.semantics.semantics
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.Dp
import androidx.compose.ui.unit.dp
import androidx.compose.ui.unit.sp
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.UsTheme

/**
 * Tube's three pages — the ones its bar switches between without leaving
 * the mini-app. Home is the root: Back from either of the others returns
 * to it, Back from Home leaves Tube.
 */
enum class TubeTab(val label: String, val icon: ImageVector) {
    HOME("Home", UsIcons.Home),
    SUBSCRIPTIONS("Subscriptions", UsIcons.ListVideo),
    YOU("You", UsIcons.Profile),
}

/**
 * The five slots on Tube's bar, in order: Home, Reels, "+", Explore, You
 * (founder, 2026-09-05: Subscriptions left the bar for the You page, and
 * Explore took its slot "so the user can go to any app from there";
 * "Shorts" was renamed to Reels the same day). Three of them leave Tube —
 * Reels is the app's Reels tab, Explore the launcher, "+" the Create hub
 * on its Video surface — so the slot is not the same thing as a [TubeTab].
 */
enum class TubeBarItem(val label: String, val icon: ImageVector, val contentDescription: String = label) {
    HOME("Home", UsIcons.Home, "Tube home"),
    REELS("Reels", UsIcons.Reels),
    EXPLORE("Explore", UsIcons.Explore, "Explore apps"),
    YOU("You", UsIcons.Profile, "Your videos"),
}

/** What a tap on the bar does. Resolved by `:app` for the two that leave Tube. */
sealed interface TubeBarAction {
    data class OpenTab(val tab: TubeTab) : TubeBarAction

    /** The app's Reels tab. */
    data object OpenReels : TubeBarAction

    /** The app's Explore launcher — the way to every other mini-app. */
    data object OpenExplore : TubeBarAction

    /** The Create hub, opened on Video. */
    data object CreateVideo : TubeBarAction
}

/** The slot's action. Pure, so the mapping is a table test. */
fun TubeBarItem.action(): TubeBarAction = when (this) {
    TubeBarItem.HOME -> TubeBarAction.OpenTab(TubeTab.HOME)
    TubeBarItem.REELS -> TubeBarAction.OpenReels
    TubeBarItem.EXPLORE -> TubeBarAction.OpenExplore
    TubeBarItem.YOU -> TubeBarAction.OpenTab(TubeTab.YOU)
}

/**
 * Which slot lights for a page. Subscriptions lives under You (it is reached
 * from the You page), so You stays lit there; Reels and Explore never light —
 * they are not Tube pages.
 */
fun TubeTab.barIndex(): Int = when (this) {
    TubeTab.HOME -> TubeBarItem.HOME.ordinal
    TubeTab.SUBSCRIPTIONS -> TubeBarItem.YOU.ordinal
    TubeTab.YOU -> TubeBarItem.YOU.ordinal
}

/**
 * Tube's bottom bar (Momentum look, 2026-09-05): NOT the shell's flat bar
 * but a floating glass pill — 64dp tall, 16dp above the navigation inset,
 * a hairline border — with Home, Reels, Explore, You, and the shell's own
 * "+" in the middle: the outlined rounded square the founder asked to keep
 * ("it was looking awesome"; the raised ember disc was dropped 2026-09-05).
 * The selected item is white; the rest are muted. No ripples: a slot dips
 * on press like everything else in Tube.
 *
 * [selected] is null on a page the bar does not own (a channel page), and
 * then nothing is lit.
 */
@Composable
fun TubeBottomBar(
    selected: TubeTab?,
    onAction: (TubeBarAction) -> Unit,
    modifier: Modifier = Modifier,
) {
    val lit = selected?.barIndex() ?: -1
    Box(
        modifier = modifier
            .fillMaxWidth()
            .navigationBarsPadding()
            .padding(start = BAR_SIDE, end = BAR_SIDE, bottom = BAR_LIFT)
            .testTag("tube_bar"),
        contentAlignment = Alignment.TopCenter,
    ) {
        val shape = RoundedCornerShape(UsTheme.radii.full)
        Row(
            modifier = Modifier
                .fillMaxWidth()
                .height(BAR_HEIGHT)
                .clip(shape)
                .background(UsTheme.extended.bgCardSolid.copy(alpha = PILL_GROUND_ALPHA))
                .background(UsTheme.extended.glassBg)
                .border(HAIRLINE, UsTheme.extended.glassBorder, shape)
                .padding(horizontal = UsTheme.spacing.s),
            verticalAlignment = Alignment.CenterVertically,
        ) {
            val split = TubeBarItem.entries.size / 2
            TubeBarItem.entries.forEachIndexed { index, item ->
                if (index == split) PlusSlot(onClick = { onAction(TubeBarAction.CreateVideo) })
                BarSlot(
                    item = item,
                    selected = index == lit,
                    onClick = { onAction(item.action()) },
                    modifier = Modifier.weight(1f),
                )
            }
        }
    }
}

@Composable
private fun BarSlot(item: TubeBarItem, selected: Boolean, onClick: () -> Unit, modifier: Modifier = Modifier) {
    val tint = if (selected) UsTheme.extended.textPrimary else UsTheme.extended.textMuted
    Column(
        modifier = modifier
            .pressScale(onClick)
            .padding(vertical = UsTheme.spacing.m)
            .semantics {
                contentDescription = item.contentDescription
                role = Role.Tab
                this.selected = selected
            }
            .testTag("tube_bar:${item.name.lowercase()}"),
        horizontalAlignment = Alignment.CenterHorizontally,
        verticalArrangement = Arrangement.spacedBy(UsTheme.spacing.xs),
    ) {
        Icon(imageVector = item.icon, contentDescription = null, tint = tint, modifier = Modifier.size(SLOT_GLYPH))
        Text(
            text = item.label,
            style = MaterialTheme.typography.labelSmall,
            fontSize = SLOT_LABEL_SIZE,
            fontWeight = if (selected) FontWeight.Bold else FontWeight.Medium,
            color = tint,
            maxLines = 1,
        )
    }
}

/**
 * The "+" slot: the shell bar's create tile — a 40dp outlined rounded square
 * with a muted plus, no fill, no glow — centred in a slot as wide as the
 * others. Same drawing as the shell's so the two bars read as one family.
 */
@Composable
private fun PlusSlot(onClick: () -> Unit) {
    val muted = UsTheme.extended.textMuted
    val shape = RoundedCornerShape(PLUS_RADIUS)
    Box(modifier = Modifier.width(PLUS_SLOT), contentAlignment = Alignment.Center) {
        PlusTile(onClick = onClick, muted = muted, shape = shape)
    }
}

@Composable
private fun PlusTile(onClick: () -> Unit, muted: Color, shape: RoundedCornerShape) {
    Box(
        contentAlignment = Alignment.Center,
        modifier = Modifier
            .size(PLUS_SIZE)
            .border(PLUS_OUTLINE, muted, shape)
            .pressScale(onClick)
            .semantics {
                contentDescription = "Post a video"
                role = Role.Button
            }
            .testTag("tube_bar:create"),
    ) {
        Icon(
            imageVector = UsIcons.Create,
            contentDescription = null,
            tint = muted,
            modifier = Modifier.size(PLUS_GLYPH),
        )
    }
}

/** How much of the screen's bottom the floating bar takes: its own height and its lift. */
val TubeBarClearance: Dp get() = BAR_HEIGHT + BAR_LIFT

private const val PILL_GROUND_ALPHA = 0.88f
private val BAR_HEIGHT = 64.dp
private val BAR_LIFT = 16.dp
private val BAR_SIDE = 20.dp
private val HAIRLINE = 1.dp
private val PLUS_SIZE = 40.dp
private val PLUS_SLOT = 64.dp
private val PLUS_RADIUS = 14.dp
private val PLUS_OUTLINE = 1.5.dp
private val PLUS_GLYPH = 22.dp
private val SLOT_GLYPH = 22.dp
private val SLOT_LABEL_SIZE = 10.sp
