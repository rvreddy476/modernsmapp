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

    @Test
    fun `every module on yields the full bar in shell order`() {
        assertThat(TabResolver.resolve(prefs()))
            .containsExactly(
                TopLevelDestination.HOME,
                TopLevelDestination.MESSAGES,
                TopLevelDestination.REELS,
                TopLevelDestination.EXPLORE,
                TopLevelDestination.ME,
            )
            .inOrder()
    }

    @Test
    fun `a module switched off drops its tab and nothing else`() {
        val tabs = TabResolver.resolve(prefs(modules = setOf(AppModule.CHAT)))

        assertThat(tabs).containsExactly(
            TopLevelDestination.HOME,
            TopLevelDestination.MESSAGES,
            TopLevelDestination.EXPLORE,
            TopLevelDestination.ME,
        ).inOrder()
    }

    @Test
    fun `explore, me and home survive every choice`() {
        val tabs = TabResolver.resolve(prefs(modules = emptySet()))

        assertThat(tabs).containsExactly(
            TopLevelDestination.HOME,
            TopLevelDestination.EXPLORE,
            TopLevelDestination.ME,
        ).inOrder()
    }

    @Test
    fun `the home module's tab comes first`() {
        val tabs = TabResolver.resolve(prefs(home = AppModule.REELS))

        assertThat(tabs.first()).isEqualTo(TopLevelDestination.REELS)
        assertThat(tabs).containsExactly(
            TopLevelDestination.REELS,
            TopLevelDestination.HOME,
            TopLevelDestination.MESSAGES,
            TopLevelDestination.EXPLORE,
            TopLevelDestination.ME,
        ).inOrder()
    }

    @Test
    fun `a home module with no tab leaves the order alone`() {
        val tabs = TabResolver.resolve(prefs(home = AppModule.COMMERCE))

        assertThat(tabs.first()).isEqualTo(TopLevelDestination.HOME)
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
