package com.us.android.navigation

import com.google.common.truth.Truth.assertThat
import com.us.android.core.profile.data.AppModule
import com.us.android.feature.chat.navigation.ChatInboxRoute
import com.us.android.feature.feed.navigation.FeedRoute
import com.us.android.feature.feed.navigation.ReelsRoute
import com.us.android.feature.profile.navigation.OwnProfileRoute
import org.junit.Test

/**
 * Each tab carries its own presentation, route and module. The bar is built
 * from a resolved subset of these, so the invariants worth asserting are per
 * entry — not that two parallel lists happen to line up.
 */
class TopLevelDestinationTest {

    @Test
    fun `every tab maps to a distinct route`() {
        assertThat(TopLevelDestination.entries.map { it.route }).containsNoDuplicates()
    }

    @Test
    fun `every tab's root route is an instance of its route class`() {
        TopLevelDestination.entries.forEach { tab ->
            assertThat(tab.rootRoute).isInstanceOf(tab.route.java)
        }
    }

    @Test
    fun `the root routes are the feature objects`() {
        assertThat(TopLevelDestination.HOME.rootRoute).isEqualTo(FeedRoute)
        assertThat(TopLevelDestination.MESSAGES.rootRoute).isEqualTo(ChatInboxRoute)
        assertThat(TopLevelDestination.REELS.rootRoute).isEqualTo(ReelsRoute)
        assertThat(TopLevelDestination.EXPLORE.rootRoute).isEqualTo(ExploreRoute)
        assertThat(TopLevelDestination.ME.rootRoute).isEqualTo(OwnProfileRoute)
    }

    @Test
    fun `tabs map to the module that switches them on`() {
        assertThat(TopLevelDestination.HOME.module).isEqualTo(AppModule.FEED)
        assertThat(TopLevelDestination.MESSAGES.module).isEqualTo(AppModule.CHAT)
        assertThat(TopLevelDestination.REELS.module).isEqualTo(AppModule.REELS)
        assertThat(TopLevelDestination.EXPLORE.module).isNull()
        assertThat(TopLevelDestination.ME.module).isNull()
    }

    /** A tab for a module this build cannot render would open nothing. */
    @Test
    fun `every module-backed tab has a screen`() {
        TopLevelDestination.entries.mapNotNull { it.module }.forEach { module ->
            assertThat(module.hasScreen).isTrue()
        }
    }

    /**
     * A null destination is the state before the first navigation resolves.
     * Returning a tab there would flash the bottom bar over the login screen
     * on cold start.
     */
    @Test
    fun `a null destination selects no tab`() {
        assertThat(TopLevelDestination.forDestination(null)).isNull()
    }

    /** Labels are user-visible; an accidental blank ships to the launcher. */
    @Test
    fun `no tab has a blank label or description`() {
        TopLevelDestination.entries.forEach { tab ->
            assertThat(tab.item.label).isNotEmpty()
            assertThat(tab.item.contentDescription).isNotEmpty()
        }
        assertThat(TopLevelDestination.entries.map { it.item.label }).containsNoDuplicates()
    }
}
