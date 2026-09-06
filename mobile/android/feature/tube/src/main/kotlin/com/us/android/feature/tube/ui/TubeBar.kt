package com.us.android.feature.tube.ui

import androidx.compose.runtime.Composable
import androidx.compose.ui.Modifier
import androidx.compose.ui.graphics.vector.ImageVector
import androidx.compose.ui.platform.testTag
import com.us.android.core.designsystem.component.UsNavItem
import com.us.android.core.designsystem.component.UsNavigationBar
import com.us.android.core.designsystem.icon.UsIcons

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

    /**
     * Create, from inside Tube: the Create sheet scoped to Tube's three —
     * video, reel, live. Raised by the bar's centre tile and by the
     * header's "+" (2026-09-06); `:app` resolves both the same way.
     */
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
 * Tube's bottom bar: the app's own flat Momentum bar — [UsNavigationBar],
 * the composable the shell draws its bar with — carrying Tube's slots, so
 * the two are the same height, wear the same top hairline, the same
 * outlined "+" tile in the middle, the same glyph and label sizes, and
 * cannot drift apart. It sits flush on the bottom edge under every Tube
 * page, with no lift and no side margins (founder, 2026-09-05: "keep the
 * previous bottom, it was looking good; keep it the same everywhere,
 * stick it to the bottom" — this replaced a floating glass pill).
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
    UsNavigationBar(
        items = TubeBarItem.entries.map { UsNavItem(it.label, it.icon, it.contentDescription) },
        selectedIndex = selected?.barIndex() ?: NOTHING_LIT,
        onSelect = { index -> onAction(TubeBarItem.entries[index].action()) },
        centerAction = { onAction(TubeBarAction.CreateVideo) },
        modifier = modifier.testTag("tube_bar"),
    )
}

/** An index no slot has, so the bar lights none of them. */
private const val NOTHING_LIT = -1
