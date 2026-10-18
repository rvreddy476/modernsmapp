package com.us.android.navigation

import com.google.common.truth.Truth.assertThat
import org.junit.Test

/**
 * The bar rule the shell applies: the route decides whether a bar exists at
 * all, and a screen's chrome request can only take it away.
 */
class BottomBarVisibilityTest {

    private val tabs = listOf(
        TopLevelDestination.HOME,
        TopLevelDestination.REELS,
        TopLevelDestination.EXPLORE,
        TopLevelDestination.ME,
    )

    @Test
    fun `a tab root shows the bar`() {
        tabs.forEach { tab ->
            assertThat(bottomBarVisible(tab, tabs, chromeHidden = false)).isTrue()
        }
    }

    /** Reels' full mode: the screen asks, the bar goes. */
    @Test
    fun `a screen's request hides the bar on a tab root`() {
        assertThat(bottomBarVisible(TopLevelDestination.REELS, tabs, chromeHidden = true)).isFalse()
    }

    /** A pushed screen, the splash, an auth screen: no tab, no bar — whatever the request says. */
    @Test
    fun `no tab means no bar`() {
        assertThat(bottomBarVisible(null, tabs, chromeHidden = false)).isFalse()
        assertThat(bottomBarVisible(null, tabs, chromeHidden = true)).isFalse()
    }

    /** The inbox and Friends are roots that open from the header or the launcher; the bar hides under them. */
    @Test
    fun `a root outside the bar shows no bar`() {
        assertThat(bottomBarVisible(TopLevelDestination.MESSAGES, tabs, chromeHidden = false)).isFalse()
        assertThat(bottomBarVisible(TopLevelDestination.FRIENDS, tabs, chromeHidden = false)).isFalse()
    }

    /** Before the shell is Ready there are no tabs, so there is nothing to draw. */
    @Test
    fun `an empty tab list shows no bar`() {
        assertThat(bottomBarVisible(TopLevelDestination.HOME, emptyList(), chromeHidden = false)).isFalse()
    }

    /** The request is a one-way switch: it cannot summon a bar the route would not show. */
    @Test
    fun `the request never shows a bar the route hides`() {
        assertThat(routeShowsBottomBar(null, tabs)).isFalse()
        assertThat(bottomBarVisible(null, tabs, chromeHidden = false)).isEqualTo(routeShowsBottomBar(null, tabs))
    }

    /** Reels and the full-screen Home feed fill the frame from the top; every other tab keeps its inset. */
    @Test
    fun `reels and home are the tabs that draw under the status bar`() {
        assertThat(TopLevelDestination.entries.filter { it.drawsUnderStatusBar })
            .containsExactly(TopLevelDestination.REELS, TopLevelDestination.HOME)
    }

    /** The dark bar is for the two video stages only (2026-10-18). */
    @Test
    fun `only home and reels get the stage bar`() {
        assertThat(TopLevelDestination.entries.filter { it.isStage })
            .containsExactly(TopLevelDestination.REELS, TopLevelDestination.HOME)
    }

    /** Explore in the bar is a root: no back arrow. Out of the bar it was opened from a header and keeps one. */
    @Test
    fun `explore shows back only when it is not a bar root`() {
        assertThat(exploreShowsBack(tabs)).isFalse()
        assertThat(exploreShowsBack(tabs - TopLevelDestination.EXPLORE)).isTrue()
    }

    /** LIVE (2026-10-17): the dark stage gets the status bar to itself; Reels gets no inset; the rest get both. */
    @Test
    fun `the shell's insets follow the screen`() {
        assertThat(shellInsetsFor(TopLevelDestination.REELS, darkStage = false)).isEqualTo(ShellInsets.NONE)
        assertThat(shellInsetsFor(null, darkStage = true)).isEqualTo(ShellInsets.NO_TOP)
        assertThat(shellInsetsFor(null, darkStage = false)).isEqualTo(ShellInsets.ALL)
        assertThat(shellInsetsFor(TopLevelDestination.HOME, darkStage = false)).isEqualTo(ShellInsets.NONE)
        assertThat(shellInsetsFor(TopLevelDestination.ME, darkStage = false)).isEqualTo(ShellInsets.ALL)
        assertThat(shellInsetsFor(null, darkStage = false, fullBleed = true)).isEqualTo(ShellInsets.NONE)
    }
}
