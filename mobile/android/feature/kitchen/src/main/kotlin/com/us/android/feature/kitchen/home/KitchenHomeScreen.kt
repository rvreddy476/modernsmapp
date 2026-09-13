package com.us.android.feature.kitchen.home

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableIntStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import com.us.android.core.designsystem.component.UsNavItem
import com.us.android.core.designsystem.component.UsNavigationBar
import com.us.android.core.designsystem.icon.UsIcons
import com.us.android.core.food.network.PartnerRestaurantDto
import com.us.android.feature.kitchen.earnings.EarningsScreen
import com.us.android.feature.kitchen.menu.MenuScreen
import com.us.android.feature.kitchen.onboarding.StepEditor
import com.us.android.feature.kitchen.queue.OrdersScreen
import com.us.android.feature.kitchen.restaurant.RestaurantScreen

/**
 * The trading kitchen: Orders, Menu, Earnings and the Kitchen tab.
 *
 * The tabs share one navigation destination, so their ViewModels share its
 * store — switching to Menu does not stop the order queue (or its alert).
 */
@Composable
fun KitchenHomeScreen(
    restaurant: PartnerRestaurantDto,
    restaurants: List<PartnerRestaurantDto>,
    onOpenOrder: (orderId: String) -> Unit,
    onOpenMenuItem: (categoryId: String, itemId: String?) -> Unit,
    onOpenOnboarding: () -> Unit,
    onOpenStep: (StepEditor) -> Unit,
    onSelectRestaurant: (String) -> Unit,
    onRestaurantChanged: () -> Unit,
    onSignOut: () -> Unit,
) {
    var tab by rememberSaveable { mutableIntStateOf(TAB_ORDERS) }
    val items = remember {
        listOf(
            UsNavItem("Orders", UsIcons.Package),
            UsNavItem("Menu", UsIcons.Utensils),
            UsNavItem("Earnings", UsIcons.CreditCard),
            UsNavItem("Kitchen", UsIcons.Store),
        )
    }
    val bottomBar: @Composable () -> Unit = {
        UsNavigationBar(items = items, selectedIndex = tab, onSelect = { tab = it })
    }
    when (tab) {
        TAB_ORDERS -> OrdersScreen(
            restaurant = restaurant,
            onOpenOrder = onOpenOrder,
            onOpenKitchenTab = { tab = TAB_KITCHEN },
            bottomBar = bottomBar,
        )
        TAB_MENU -> MenuScreen(onBack = null, onOpenItem = onOpenMenuItem, bottomBar = bottomBar)
        TAB_EARNINGS -> EarningsScreen(bottomBar = bottomBar)
        else -> RestaurantScreen(
            restaurants = restaurants,
            onOpenOnboarding = onOpenOnboarding,
            onOpenStep = onOpenStep,
            onSelectRestaurant = onSelectRestaurant,
            onRestaurantChanged = onRestaurantChanged,
            onSignOut = onSignOut,
            bottomBar = bottomBar,
        )
    }
}

private const val TAB_ORDERS = 0
private const val TAB_MENU = 1
private const val TAB_EARNINGS = 2
private const val TAB_KITCHEN = 3
