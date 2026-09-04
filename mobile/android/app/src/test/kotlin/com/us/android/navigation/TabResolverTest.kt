package com.us.android.navigation

import com.google.common.truth.Truth.assertThat
import com.us.android.core.profile.data.AppModule
import com.us.android.core.profile.data.ModulePreferences
import org.junit.Test

class TabResolverTest {

    private fun prefs(
        modules: Set<AppModule> = AppModule.selectable.toSet(),
        home: AppModule = AppModule.FEED,
    ) = ModulePreferences(modules = modules, homeModule = home, onboardingCompleted = true)

    /** The bar (founder, 2026-09-05): Home, Reels, "+", Explore, Me. */
    @Test
    fun `every module on yields the full bar in fixed order`() {
        assertThat(TabResolver.resolve(prefs()))
            .containsExactly(
                TopLevelDestination.HOME,
                TopLevelDestination.REELS,
                TopLevelDestination.EXPLORE,
                TopLevelDestination.ME,
            )
            .inOrder()
    }

    /** Reels is the only module-gated tab; switching it off drops it and nothing else. */
    @Test
    fun `reels off drops its tab and nothing else`() {
        val tabs = TabResolver.resolve(prefs(modules = setOf(AppModule.CHAT)))

        assertThat(tabs).containsExactly(
            TopLevelDestination.HOME,
            TopLevelDestination.EXPLORE,
            TopLevelDestination.ME,
        ).inOrder()
    }

    @Test
    fun `home, explore and me survive every choice`() {
        val tabs = TabResolver.resolve(prefs(modules = emptySet()))

        assertThat(tabs).containsExactly(
            TopLevelDestination.HOME,
            TopLevelDestination.EXPLORE,
            TopLevelDestination.ME,
        ).inOrder()
    }

    /** The inbox opens from the header and the launcher; Friends from the launcher. Neither is in the bar. */
    @Test
    fun `messages and friends are never tabs`() {
        val tabs = TabResolver.resolve(prefs(modules = AppModule.selectable.toSet()))

        assertThat(tabs).containsNoneOf(TopLevelDestination.MESSAGES, TopLevelDestination.FRIENDS)
    }

    /**
     * The "+" slot sits after the first half of the tabs, so four tabs put it
     * between Reels and Explore — the frame's two-"+"-two.
     */
    @Test
    fun `the create slot splits the full bar between reels and explore`() {
        val tabs = TabResolver.resolve(prefs())
        val split = tabs.size / 2

        assertThat(tabs.take(split)).containsExactly(TopLevelDestination.HOME, TopLevelDestination.REELS).inOrder()
        assertThat(tabs.drop(split)).containsExactly(TopLevelDestination.EXPLORE, TopLevelDestination.ME).inOrder()
    }

    /** The home module no longer reorders the bar; it only picks the first screen. */
    @Test
    fun `the home module does not move its tab`() {
        val tabs = TabResolver.resolve(prefs(home = AppModule.REELS))

        assertThat(tabs).containsExactly(
            TopLevelDestination.HOME,
            TopLevelDestination.REELS,
            TopLevelDestination.EXPLORE,
            TopLevelDestination.ME,
        ).inOrder()
    }

    @Test
    fun `a reels home opens on reels`() {
        assertThat(TabResolver.startDestination(prefs(home = AppModule.REELS)))
            .isEqualTo(TopLevelDestination.REELS)
    }

    @Test
    fun `a feed home opens on home`() {
        assertThat(TabResolver.startDestination(prefs(home = AppModule.FEED)))
            .isEqualTo(TopLevelDestination.HOME)
    }

    /** Chat is a module with a root but no bar item; Commerce has no screen at all. */
    @Test
    fun `a home module without a bar tab opens on home`() {
        assertThat(TabResolver.startDestination(prefs(home = AppModule.CHAT))).isEqualTo(TopLevelDestination.HOME)
        assertThat(TabResolver.startDestination(prefs(home = AppModule.COMMERCE)))
            .isEqualTo(TopLevelDestination.HOME)
    }

    /** The start tab is always in the bar, so something is selected on frame one. */
    @Test
    fun `the start destination is always a resolved tab`() {
        AppModule.entries.forEach { home ->
            val p = prefs(home = home)
            assertThat(TabResolver.resolve(p)).contains(TabResolver.startDestination(p))
        }
        // Reels chosen as home but then switched off: the bar has no Reels,
        // so the start falls back to Home rather than a tab that is not there.
        val reelsOff = prefs(modules = emptySet(), home = AppModule.REELS)
        assertThat(TabResolver.startDestination(reelsOff)).isEqualTo(TopLevelDestination.HOME)
    }

    /**
     * Modules without a screen are recorded server-side but must never
     * produce a tab: there is no [TopLevelDestination] for any of them, and
     * choosing only them yields the always-on bar.
     */
    @Test
    fun `unbuilt modules never become tabs`() {
        val unbuilt = AppModule.selectable.filter { !it.hasScreen }.toSet()
        assertThat(unbuilt).isNotEmpty()

        val tabs = TabResolver.resolve(prefs(modules = unbuilt))

        assertThat(tabs.mapNotNull { it.module }).containsExactly(AppModule.FEED)
        assertThat(TopLevelDestination.entries.mapNotNull { it.module }.filter { !it.hasScreen }).isEmpty()
    }
}
