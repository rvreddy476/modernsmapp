package com.us.android.navigation

/**
 * Whether the shell shows its bottom bar. Pure, so the matrix is a table test.
 *
 * Two inputs, in order of authority:
 *  - The ROUTE. A null tab is a screen that is not a top-level root — an auth
 *    screen, the splash, the picker, a pushed profile — and a root that is not
 *    in the bar (the inbox, Explore) opens from the header and leaves by Back;
 *    neither shows a bar. An empty [tabs] list means the shell is not Ready.
 *  - The SCREEN's request. A tab root may ask for the bar to go — Reels' full
 *    mode, where a double-tap leaves only the video — through
 *    [com.us.android.core.ui.ChromeVisibility]. The request only ever hides;
 *    it can never show a bar the route would not.
 */
fun bottomBarVisible(
    currentTab: TopLevelDestination?,
    tabs: List<TopLevelDestination>,
    chromeHidden: Boolean,
): Boolean = routeShowsBottomBar(currentTab, tabs) && !chromeHidden

/** The route half of [bottomBarVisible]: is this a screen that has a bar at all? */
fun routeShowsBottomBar(currentTab: TopLevelDestination?, tabs: List<TopLevelDestination>): Boolean =
    currentTab != null && currentTab in tabs

/**
 * Tabs whose content draws under the status bar rather than below it.
 *
 * Reels is the one (founder, 2026-09-04, evening): no header, the video
 * fills the frame from the very top. The shell hands such a tab no top
 * inset; every other tab's own scaffold reserves the status bar as before.
 */
val TopLevelDestination.drawsUnderStatusBar: Boolean
    get() = this == TopLevelDestination.REELS
