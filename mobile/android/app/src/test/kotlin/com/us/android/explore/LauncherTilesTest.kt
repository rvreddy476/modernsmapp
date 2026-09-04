package com.us.android.explore

import com.google.common.truth.Truth.assertThat
import com.us.android.core.profile.data.AppModule
import org.junit.Test

/**
 * The launcher's rule (founder, 2026-09-05): a fixed order, every app always
 * present — Explore is where apps are found, the module choices shape the
 * home page and the bar — and an app this build has no screen for is a
 * "Soon" tile rather than a dead one.
 */
class LauncherTilesTest {

    @Test
    fun `all nine tiles, in the founder's order`() {
        assertThat(launcherTiles().map { it.app }).containsExactly(
            LauncherApp.CHAT,
            LauncherApp.FRIENDS,
            LauncherApp.ALERTS,
            LauncherApp.LIVE,
            LauncherApp.SHOP,
            LauncherApp.MATCH,
            LauncherApp.ASK,
            LauncherApp.FEAST,
            LauncherApp.TUBE,
        ).inOrder()
    }

    @Test
    fun `the always-on tiles are never soon`() {
        val tiles = launcherTiles().associate { it.app to it.soon }
        listOf(LauncherApp.CHAT, LauncherApp.FRIENDS, LauncherApp.ALERTS, LauncherApp.LIVE).forEach { app ->
            assertThat(tiles.getValue(app)).isFalse()
        }
    }

    @Test
    fun `an app without a screen is a soon tile, not a hidden one`() {
        val tiles = launcherTiles().associate { it.app to it.soon }
        LauncherApp.entries.filter { it.module != null }.forEach { app ->
            assertThat(tiles.getValue(app)).isEqualTo(!app.module!!.hasScreen)
        }
    }

    /**
     * Every optional module has a tile, so a new module cannot ship without one.
     * Reels has a bar tab instead; Chat is an always-on tile (its tile is not
     * tied to the module switch, so it carries no [AppModule]).
     */
    @Test
    fun `every optional module without a bar tab has a launcher tile`() {
        val covered = LauncherApp.entries.mapNotNull { it.module }.toSet()
        val expected = AppModule.selectable.filter { it != AppModule.REELS && it != AppModule.CHAT }.toSet()
        assertThat(covered).containsAtLeastElementsIn(expected)
    }

    @Test
    fun `the soon message names the app`() {
        assertThat(comingSoonMessage(LauncherApp.SHOP)).isEqualTo("Shop is coming soon")
    }
}
