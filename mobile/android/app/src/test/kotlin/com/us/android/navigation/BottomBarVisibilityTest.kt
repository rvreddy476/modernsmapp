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
        TopLevelDestination.FRIENDS,
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

    /** The inbox and Explore are roots that open from the header; the bar hides under them. */
    @Test
    fun `a root outside the bar shows no bar`() {
        assertThat(bottomBarVisible(TopLevelDestination.MESSAGES, tabs, chromeHidden = false)).isFalse()
        assertThat(bottomBarVisible(TopLevelDestination.EXPLORE, tabs, chromeHidden = false)).isFalse()
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

    /** Only Reels fills the frame from the top; every other tab keeps its status-bar inset. */
    @Test
    fun `reels is the only tab that draws under the status bar`() {
        assertThat(TopLevelDestination.entries.filter { it.drawsUnderStatusBar })
            .containsExactly(TopLevelDestination.REELS)
    }
}
