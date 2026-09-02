package com.us.android.navigation

import com.google.common.truth.Truth.assertThat
import com.us.android.core.designsystem.component.UsDefaultNavItems
import org.junit.Test

/**
 * The tab enum and the design system's item list are two parallel ordered
 * lists, and the bottom bar maps between them by INDEX. Nothing in the type
 * system stops someone adding a tab to one and not the other, and the symptom
 * would be a bar where tapping "Reels" opens Explore — a bug that looks like a
 * navigation fault rather than a list-length mismatch.
 */
class TopLevelDestinationTest {

    @Test
    fun `every tab has a matching nav item`() {
        assertThat(UsDefaultNavItems).hasSize(TopLevelDestination.entries.size)
    }

    @Test
    fun `each tab resolves the nav item at its own ordinal`() {
        TopLevelDestination.entries.forEach { destination ->
            assertThat(destination.item).isEqualTo(UsDefaultNavItems[destination.ordinal])
        }
    }

    @Test
    fun `tab order matches the product's shell order`() {
        assertThat(UsDefaultNavItems.map { it.label })
            .containsExactly("Home", "Messages", "Reels", "Explore", "Me")
            .inOrder()
    }

    @Test
    fun `every tab maps to a distinct route`() {
        val routes = TopLevelDestination.entries.map { it.route }
        assertThat(routes).containsNoDuplicates()
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
    fun `no nav item has a blank label or description`() {
        UsDefaultNavItems.forEach { item ->
            assertThat(item.label).isNotEmpty()
            assertThat(item.contentDescription).isNotEmpty()
        }
    }
}
