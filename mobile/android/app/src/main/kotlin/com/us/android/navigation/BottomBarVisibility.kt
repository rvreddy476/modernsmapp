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
 *    mode, where a double-tap takes away the header and the bar — through
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
 * Reels (founder, 2026-09-04) and, since the full-screen feed (2026-10-18),
 * Home: the post fills the frame from the very top and the translucent
 * header floats over it, padding itself under the status bar. The shell
 * hands such a tab no top inset; every other tab's own scaffold reserves
 * the status bar as before.
 */
val TopLevelDestination.drawsUnderStatusBar: Boolean
    get() = isStage

/**
 * Tabs that are a dark video stage whatever the theme: Home's full-screen
 * feed and Reels (2026-10-18). The shell paints its bottom bar dark under
 * them (UsNavigationBar's stage variant).
 */
val TopLevelDestination.isStage: Boolean
    get() = this == TopLevelDestination.REELS || this == TopLevelDestination.HOME

/**
 * Whether the Explore launcher wears a back arrow (2026-10-17): not when it
 * is a root of the bar — a tab root has nowhere back to go, and the arrow
 * read as a pushed screen — but yes when it is not in the bar and was
 * opened from a header, where Back is the way out.
 */
fun exploreShowsBack(tabs: List<TopLevelDestination>): Boolean = TopLevelDestination.EXPLORE !in tabs

/** How much of the system bars the shell reserves above and below the current screen. */
enum class ShellInsets {
    /** Every other screen: the shell keeps the status and navigation bars clear. */
    ALL,

    /** Reels: no inset at all; the video fills the frame and the header pads itself. */
    NONE,

    /**
     * A dark stage pushed over the shell (LIVE, 2026-10-17): no TOP inset,
     * so the screen's own dark ground runs under the status bar and its top
     * bar pads itself below it. The bottom stays reserved as before.
     */
    NO_TOP,
}

/**
 * The shell's insets for a screen. Pure, so the table is a test. [darkStage]
 * is a pushed route that is dark edge to edge — the LIVE screens — where a
 * reserved status-bar band would show the app's LIGHT ground behind the
 * light glyphs those screens ask for, a white bar over a black room.
 */
fun shellInsetsFor(currentTab: TopLevelDestination?, darkStage: Boolean): ShellInsets = when {
    currentTab?.drawsUnderStatusBar == true -> ShellInsets.NONE
    darkStage -> ShellInsets.NO_TOP
    else -> ShellInsets.ALL
}
