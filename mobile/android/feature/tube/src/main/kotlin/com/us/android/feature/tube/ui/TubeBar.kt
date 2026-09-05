package com.us.android.feature.tube.ui

import androidx.compose.foundation.background
import androidx.compose.foundation.border
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Box
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.Row
import androidx.compose.foundation.layout.Spacer
import androidx.compose.foundation.layout.fillMaxWidth
import androidx.compose.foundation.layout.height
import androidx.compose.foundation.layout.navigationBarsPadding
import androidx.compose.foundation.layout.offset
import androidx.compose.foundation.layout.padding
import androidx.compose.foundation.layout.size
import androidx.compose.foundation.layout.width
import androidx.compose.foundation.shape.CircleShape
import androidx.compose.foundation.shape.RoundedCornerShape
import androidx.compose.material3.Icon
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.draw.clip
import androidx.compose.ui.draw.shadow
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
 * The five slots on Tube's bar, in order: Home, Reels, "+", Subscriptions,
 * You (founder, 2026-09-05; "Shorts" renamed to Reels the same day — the
 * app has one word for short video). Two of them leave Tube — Reels is the
 * app's Reels tab and "+" the Create hub on its Video surface — so the slot
 * is not the same thing as a [TubeTab].
 */
enum class TubeBarItem(val label: String, val icon: ImageVector, val contentDescription: String = label) {
    HOME("Home", UsIcons.Home, "Tube home"),
    REELS("Reels", UsIcons.Reels),
    SUBSCRIPTIONS("Subscriptions", UsIcons.ListVideo),
    YOU("You", UsIcons.Profile, "Your videos"),
}

/** What a tap on the bar does. Resolved by `:app` for the two that leave Tube. */
sealed interface TubeBarAction {
    data class OpenTab(val tab: TubeTab) : TubeBarAction

    /** The app's Reels tab. */
    data object OpenReels : TubeBarAction

    /** The Create hub, opened on Video. */
    data object CreateVideo : TubeBarAction
}

/** The slot's action. Pure, so the mapping is a table test. */
fun TubeBarItem.action(): TubeBarAction = when (this) {
    TubeBarItem.HOME -> TubeBarAction.OpenTab(TubeTab.HOME)
    TubeBarItem.REELS -> TubeBarAction.OpenReels
    TubeBarItem.SUBSCRIPTIONS -> TubeBarAction.OpenTab(TubeTab.SUBSCRIPTIONS)
    TubeBarItem.YOU -> TubeBarAction.OpenTab(TubeTab.YOU)
}

/** Which slot lights for a page; Reels never does — it is not a Tube page. */
fun TubeTab.barIndex(): Int = when (this) {
    TubeTab.HOME -> TubeBarItem.HOME.ordinal
    TubeTab.SUBSCRIPTIONS -> TubeBarItem.SUBSCRIPTIONS.ordinal
    TubeTab.YOU -> TubeBarItem.YOU.ordinal
}

/**
 * Tube's bottom bar (Momentum look, 2026-09-05): NOT the shell's flat bar
 * but a floating glass pill — 64dp tall, 16dp above the navigation inset,
 * a hairline border — with Home, Reels, Subscriptions, You, and a raised
 * ember "+" overlapping its top edge at the centre, casting the ember glow.
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
            .padding(start = BAR_SIDE, end = BAR_SIDE, bottom = BAR_LIFT, top = PLUS_OVERLAP)
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
                if (index == split) Spacer(Modifier.width(PLUS_SLOT))
                BarSlot(
                    item = item,
                    selected = index == lit,
                    onClick = { onAction(item.action()) },
                    modifier = Modifier.weight(1f),
                )
            }
        }
        PlusButton(
            onClick = { onAction(TubeBarAction.CreateVideo) },
            modifier = Modifier.offset(y = -PLUS_OVERLAP),
        )
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

/** The raised "+": a 56dp ember disc with the ember glow beneath it, a white plus in it. */
@Composable
private fun PlusButton(onClick: () -> Unit, modifier: Modifier = Modifier) {
    val glow = UsTheme.extended.accentSolid
    Box(
        contentAlignment = Alignment.Center,
        modifier = modifier
            .size(PLUS_SIZE)
            .shadow(elevation = PLUS_GLOW, shape = CircleShape, ambientColor = glow, spotColor = glow)
            .background(UsTheme.extended.ctaGradient, CircleShape)
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
            tint = Color.White,
            modifier = Modifier.size(PLUS_GLYPH),
        )
    }
}

/** How much of the screen's bottom the floating bar takes: its own height, its lift, and the overlap above it. */
val TubeBarClearance: Dp get() = BAR_HEIGHT + BAR_LIFT + PLUS_OVERLAP

private const val PILL_GROUND_ALPHA = 0.88f
private val BAR_HEIGHT = 64.dp
private val BAR_LIFT = 16.dp
private val BAR_SIDE = 20.dp
private val HAIRLINE = 1.dp
private val PLUS_SIZE = 56.dp
private val PLUS_SLOT = 64.dp
private val PLUS_OVERLAP = 20.dp
private val PLUS_GLOW = 14.dp
private val PLUS_GLYPH = 26.dp
private val SLOT_GLYPH = 22.dp
private val SLOT_LABEL_SIZE = 10.sp
