package com.us.android.navigation

import androidx.lifecycle.Lifecycle
import androidx.lifecycle.LifecycleOwner
import androidx.lifecycle.LifecycleRegistry
import androidx.lifecycle.ViewModelStore
import androidx.navigation.NavDestination
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavDestinationBuilder
import androidx.navigation.NavGraph.Companion.findStartDestination
import androidx.navigation.NavHostController
import androidx.navigation.Navigator
import androidx.navigation.createGraph
import androidx.navigation.navOptions
import com.google.common.truth.Truth.assertThat
import com.us.android.feature.feed.navigation.FeedRoute
import com.us.android.feature.feed.navigation.ReelsRoute
import com.us.android.feature.tube.navigation.TubeHomeRoute
import com.us.android.feature.tube.navigation.TubeYouRoute
import org.junit.Test
import org.junit.runner.RunWith
import org.robolectric.RobolectricTestRunner
import org.robolectric.RuntimeEnvironment
import kotlin.reflect.KClass

/**
 * Protects the way back to the Explore launcher from inside a mini-app
 * (founder's phone, 2026-10-02: "Explore cannot be opened from PostTube").
 *
 * A REAL `NavController` over the app's real routes, with a navigator that
 * draws nothing: the defect was in what the navigation library does with
 * `popUpTo { saveState }` + `restoreState`, so a model of it would prove
 * nothing. The first test pins the defect itself, with the options the tab
 * switch used to pass; the rest pin the repair.
 */
@RunWith(RobolectricTestRunner::class)
class LauncherNavigationTest {

    /** A navigator with no UI: destinations are entries on the back stack and nothing else. */
    @Navigator.Name("plain")
    private class PlainNavigator : Navigator<NavDestination>() {
        override fun createDestination(): NavDestination = NavDestination(this)
    }

    private class Owner : LifecycleOwner {
        private val registry = LifecycleRegistry(this).apply { currentState = Lifecycle.State.RESUMED }
        override val lifecycle: Lifecycle get() = registry
    }

    private val routes: List<KClass<*>> = listOf(
        FeedRoute::class,
        ReelsRoute::class,
        ExploreRoute::class,
        TubeHomeRoute::class,
        TubeYouRoute::class,
    )

    private fun controller(): NavHostController {
        val nav = NavHostController(RuntimeEnvironment.getApplication())
        val navigator = PlainNavigator()
        nav.navigatorProvider.addNavigator(navigator)
        nav.setLifecycleOwner(Owner())
        nav.setViewModelStore(ViewModelStore())
        nav.graph = nav.createGraph(startDestination = FeedRoute) {
            routes.forEach { route -> addDestination(NavDestinationBuilder(navigator, route, emptyMap()).build()) }
        }
        return nav
    }

    private fun NavHostController.isOn(route: KClass<*>): Boolean = currentDestination?.hasRoute(route) == true

    private fun NavHostController.stack(): List<String> = currentBackStack.value
        .mapNotNull { entry -> routes.firstOrNull { entry.destination.hasRoute(it) }?.simpleName }

    /** Home, then the Explore tab, then the Tube tile: how the founder got into Tube. */
    private fun insideTube(): NavHostController = controller().apply {
        navigateToTopLevel(TopLevelDestination.EXPLORE)
        navigate(TubeHomeRoute)
        check(stack() == listOf("FeedRoute", "ExploreRoute", "TubeHomeRoute")) { stack() }
    }

    @Test
    fun `THE DEFECT - a restoring tab switch to Explore from inside Tube lands back on Tube`() {
        val nav = insideTube()

        // What Tube's Explore slot did until 2026-10-02: the bar's tab switch, restoring.
        nav.navigate(
            ExploreRoute,
            navOptions {
                popUpTo(nav.graph.findStartDestination().id) { saveState = true }
                launchSingleTop = true
                restoreState = true
            },
        )

        assertThat(nav.isOn(TubeHomeRoute::class)).isTrue()
        assertThat(nav.isOn(ExploreRoute::class)).isFalse()
    }

    @Test
    fun `Tube's Explore slot lands on the launcher, and Back from it returns Home`() {
        val nav = insideTube()

        nav.navigateToLauncher()

        assertThat(nav.isOn(ExploreRoute::class)).isTrue()
        assertThat(nav.stack()).containsExactly("FeedRoute", "ExploreRoute").inOrder()
    }

    /** Going BACK to the launcher, not opening a second one: the entry the user left is the entry they return to. */
    @Test
    fun `it returns to the same launcher entry it was opened from`() {
        val nav = insideTube()
        val launcher = nav.getBackStackEntry<ExploreRoute>().id

        nav.navigateToLauncher()

        assertThat(nav.currentBackStackEntry?.id).isEqualTo(launcher)
    }

    @Test
    fun `it leaves every Tube page behind, not only the top one`() {
        val nav = insideTube()
        nav.navigate(TubeYouRoute)

        nav.navigateToLauncher()

        assertThat(nav.stack()).containsExactly("FeedRoute", "ExploreRoute").inOrder()
    }

    @Test
    fun `from a Tube that was not opened from the launcher it opens the launcher as a tab`() {
        val nav = controller()
        nav.navigate(TubeHomeRoute) // a search result, a notification

        nav.navigateToLauncher()

        assertThat(nav.isOn(ExploreRoute::class)).isTrue()
        assertThat(nav.stack()).containsExactly("FeedRoute", "ExploreRoute").inOrder()
    }

    @Test
    fun `the bar's Explore item opens the launcher after Tube was left by its Reels slot`() {
        val nav = insideTube()
        nav.navigateToTopLevel(TopLevelDestination.REELS) // Tube's Reels slot
        check(nav.isOn(ReelsRoute::class))

        nav.navigateToTopLevel(TopLevelDestination.EXPLORE) // the shell's bar

        assertThat(nav.isOn(ExploreRoute::class)).isTrue()
        assertThat(nav.isOn(TubeHomeRoute::class)).isFalse()
    }

    @Test
    fun `every other root still comes back the way it was left`() {
        val nav = controller()
        nav.navigateToTopLevel(TopLevelDestination.REELS)
        nav.navigate(TubeYouRoute) // something pushed over the Reels tab
        nav.navigateToTopLevel(TopLevelDestination.HOME)
        check(nav.isOn(FeedRoute::class))

        nav.navigateToTopLevel(TopLevelDestination.REELS)

        assertThat(nav.isOn(TubeYouRoute::class)).isTrue()
    }

    @Test
    fun `only Explore gives up its saved stack`() {
        TopLevelDestination.entries.forEach { root ->
            assertThat(root.restoresItsStack).isEqualTo(root != TopLevelDestination.EXPLORE)
        }
    }
}
