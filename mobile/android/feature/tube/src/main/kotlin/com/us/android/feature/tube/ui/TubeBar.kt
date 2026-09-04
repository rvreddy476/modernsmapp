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
 * The five slots on Tube's bar, in order: Home, Shorts, "+", Subscriptions,
 * You (founder, 2026-09-05, from YouTube's bar). Two of them leave Tube —
 * Shorts is the app's Reels tab and "+" the Create hub on its Video
 * surface — so the slot is not the same thing as a [TubeTab].
 */
enum class TubeBarItem(val label: String, val icon: ImageVector, val contentDescription: String = label) {
    HOME("Home", UsIcons.Home, "Tube home"),
    SHORTS("Shorts", UsIcons.Reels),
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
    TubeBarItem.SHORTS -> TubeBarAction.OpenReels
    TubeBarItem.SUBSCRIPTIONS -> TubeBarAction.OpenTab(TubeTab.SUBSCRIPTIONS)
    TubeBarItem.YOU -> TubeBarAction.OpenTab(TubeTab.YOU)
}

/** Which slot lights for a page; Shorts never does — it is not a Tube page. */
fun TubeTab.barIndex(): Int = when (this) {
    TubeTab.HOME -> TubeBarItem.HOME.ordinal
    TubeTab.SUBSCRIPTIONS -> TubeBarItem.SUBSCRIPTIONS.ordinal
    TubeTab.YOU -> TubeBarItem.YOU.ordinal
}

/**
 * Tube's bottom bar: the app's flat Momentum bar with Tube's slots in it —
 * the "+" is the bar's own centre tile, placed after the first two, so the
 * two bars are drawn by one composable and cannot drift.
 */
@Composable
fun TubeBottomBar(
    selected: TubeTab,
    onAction: (TubeBarAction) -> Unit,
    modifier: Modifier = Modifier,
) {
    UsNavigationBar(
        items = TubeBarItem.entries.map { UsNavItem(it.label, it.icon, it.contentDescription) },
        selectedIndex = selected.barIndex(),
        onSelect = { index -> onAction(TubeBarItem.entries[index].action()) },
        centerAction = { onAction(TubeBarAction.CreateVideo) },
        modifier = modifier.testTag("tube_bar"),
    )
}
