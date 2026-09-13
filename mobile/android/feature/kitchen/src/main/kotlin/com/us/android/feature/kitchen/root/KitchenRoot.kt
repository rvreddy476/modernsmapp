package com.us.android.feature.kitchen.root

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.compose.runtime.remember
import androidx.compose.runtime.rememberUpdatedState
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.text.font.FontWeight
import androidx.compose.ui.unit.sp
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.designsystem.theme.MomentumWordmarkFontFamily
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.feature.kitchen.compliance.ComplianceScreen
import com.us.android.feature.kitchen.fssai.FssaiScreen
import com.us.android.feature.kitchen.home.KitchenHomeScreen
import com.us.android.feature.kitchen.hours.OperatingHoursScreen
import com.us.android.feature.kitchen.location.LocationScreen
import com.us.android.feature.kitchen.menu.MenuItemEditorScreen
import com.us.android.feature.kitchen.menu.MenuScreen
import com.us.android.feature.kitchen.navigation.ComplianceStepRoute
import com.us.android.feature.kitchen.navigation.FssaiStepRoute
import com.us.android.feature.kitchen.navigation.HoursStepRoute
import com.us.android.feature.kitchen.navigation.KitchenHomeRoute
import com.us.android.feature.kitchen.navigation.LocationStepRoute
import com.us.android.feature.kitchen.navigation.MenuItemRoute
import com.us.android.feature.kitchen.navigation.MenuRoute
import com.us.android.feature.kitchen.navigation.OnboardingRoute
import com.us.android.feature.kitchen.navigation.OrderDetailRoute
import com.us.android.feature.kitchen.navigation.PayoutStepRoute
import com.us.android.feature.kitchen.navigation.stepRoute
import com.us.android.feature.kitchen.onboarding.OnboardingScreen
import com.us.android.feature.kitchen.payout.PayoutScreen
import com.us.android.feature.kitchen.queue.OrderDetailScreen
import com.us.android.feature.kitchen.ui.MessagePane
import com.us.android.feature.kitchen.ui.RestaurantStatusText

/**
 * Feast Kitchen's signed-in entry point. :app-kitchen shows this once a session
 * exists; everything behind it is gated by [KitchenRootViewModel].
 */
@Composable
fun KitchenRoot(viewModel: KitchenRootViewModel = hiltViewModel()) {
    val state by viewModel.state.collectAsStateWithLifecycle()
    when (val current = state) {
        KitchenRootState.Loading -> KitchenSplash()
        KitchenRootState.NotPartner -> UsScaffold { padding ->
            MessagePane(
                title = "This app is for restaurant partners",
                body = "Feast Kitchen is where restaurants take orders and manage their menus. " +
                    "This account doesn't have a restaurant yet. To order food, use the Momentum app.",
                icon = UsIcons.Store,
                primaryLabel = "Check again",
                onPrimary = viewModel::load,
                secondaryLabel = "Sign out",
                onSecondary = viewModel::signOut,
                modifier = Modifier.padding(padding),
            )
        }
        KitchenRootState.SessionExpired -> {
            LaunchedEffect(Unit) { viewModel.signOut() }
            KitchenSplash()
        }
        is KitchenRootState.Unavailable -> UsScaffold { padding ->
            MessagePane(
                title = "Couldn't open your kitchen",
                body = current.message,
                primaryLabel = "Try again",
                onPrimary = viewModel::load,
                secondaryLabel = "Sign out",
                onSecondary = viewModel::signOut,
                modifier = Modifier.padding(padding),
            )
        }
        is KitchenRootState.Ready -> KitchenNavHost(
            ready = current,
            onRestaurantChanged = viewModel::refreshRestaurant,
            onSelectRestaurant = viewModel::select,
            onSignOut = viewModel::signOut,
        )
    }
}

/** The brand mark while the gate decides. Shared with :app-kitchen's pre-session splash. */
@Composable
fun KitchenSplash() {
    UsScaffold { _ ->
        Column(
            modifier = Modifier.fillMaxSize(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            Text(
                text = "Feast",
                fontFamily = MomentumWordmarkFontFamily,
                fontSize = 44.sp,
                color = UsTheme.extended.textPrimary,
            )
            Text(
                text = "KITCHEN",
                style = MaterialTheme.typography.labelLarge,
                fontWeight = FontWeight.SemiBold,
                color = UsTheme.extended.accentSolid,
            )
        }
    }
}

@Composable
private fun KitchenNavHost(
    ready: KitchenRootState.Ready,
    onRestaurantChanged: () -> Unit,
    onSelectRestaurant: (String) -> Unit,
    onSignOut: () -> Unit,
) {
    // The graph is built once per restaurant; destinations read the latest
    // restaurant through this, not through a value captured at build time.
    val latest by rememberUpdatedState(ready)
    key(ready.selected.id) {
        val restaurantId = ready.selected.id
        val navController = rememberNavController()
        // A kitchen still being set up, or sent back for changes, opens on its checklist.
        val start: Any = remember {
            when (ready.selected.status) {
                RestaurantStatusText.DRAFT, RestaurantStatusText.REJECTED -> OnboardingRoute(restaurantId)
                else -> KitchenHomeRoute(restaurantId)
            }
        }
        val back: () -> Unit = { navController.popBackStack() }
        NavHost(navController = navController, startDestination = start) {
            composable<KitchenHomeRoute> {
                KitchenHomeScreen(
                    restaurant = latest.selected,
                    restaurants = latest.restaurants,
                    onOpenOrder = { orderId -> navController.navigate(OrderDetailRoute(restaurantId, orderId)) },
                    onOpenMenuItem = { categoryId, itemId ->
                        navController.navigate(MenuItemRoute(restaurantId, categoryId, itemId))
                    },
                    onOpenOnboarding = { navController.navigate(OnboardingRoute(restaurantId)) },
                    onOpenStep = { editor -> navController.navigate(stepRoute(editor, restaurantId)) },
                    onSelectRestaurant = onSelectRestaurant,
                    onRestaurantChanged = onRestaurantChanged,
                    onSignOut = onSignOut,
                )
            }
            composable<OnboardingRoute> {
                OnboardingScreen(
                    onBack = if (navController.previousBackStackEntry != null) back else null,
                    onOpenStep = { editor -> navController.navigate(stepRoute(editor, restaurantId)) },
                    onRestaurantChanged = onRestaurantChanged,
                    onOpenKitchen = {
                        navController.navigate(KitchenHomeRoute(restaurantId)) {
                            popUpTo<OnboardingRoute> { inclusive = true }
                        }
                    },
                    onSignOut = onSignOut,
                )
            }
            composable<LocationStepRoute> { LocationScreen(onBack = back) }
            composable<HoursStepRoute> { OperatingHoursScreen(onBack = back) }
            composable<ComplianceStepRoute> { ComplianceScreen(onBack = back) }
            composable<FssaiStepRoute> { FssaiScreen(onBack = back) }
            composable<PayoutStepRoute> { PayoutScreen(onBack = back) }
            composable<MenuRoute> {
                MenuScreen(
                    onBack = back,
                    onOpenItem = { categoryId, itemId -> navController.navigate(MenuItemRoute(restaurantId, categoryId, itemId)) },
                )
            }
            composable<MenuItemRoute> { MenuItemEditorScreen(onBack = back) }
            composable<OrderDetailRoute> { OrderDetailScreen(onBack = back) }
        }
    }
}
