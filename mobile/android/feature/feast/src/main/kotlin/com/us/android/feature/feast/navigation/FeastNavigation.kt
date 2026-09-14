// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph extension that uses them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.feast.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.compose.navigation
import com.us.android.feature.feast.address.AddAddressScreen
import com.us.android.feature.feast.address.AddressesScreen
import com.us.android.feature.feast.cart.CartScreen
import com.us.android.feature.feast.checkout.FeastCheckoutScreen
import com.us.android.feature.feast.checkout.FeastPaymentRequest
import com.us.android.feature.feast.home.FeastHomeScreen
import com.us.android.feature.feast.orders.InvoiceScreen
import com.us.android.feature.feast.orders.OrdersScreen
import com.us.android.feature.feast.restaurant.RestaurantScreen
import com.us.android.feature.feast.tracking.OrderTrackingScreen
import kotlinx.serialization.Serializable

/** Feast, as one graph with its own back stack: Back from its root leaves Feast. */
@Serializable
data object FeastGraph

@Serializable
data object FeastHomeRoute

@Serializable
data class FeastRestaurantRoute(val restaurantId: String)

@Serializable
data object FeastCartRoute

/** [picking] true when opened from checkout to choose where the food goes. */
@Serializable
data class FeastAddressesRoute(val picking: Boolean = false)

@Serializable
data object FeastAddAddressRoute

@Serializable
data object FeastCheckoutRoute

@Serializable
data class FeastTrackingRoute(val orderId: String)

@Serializable
data object FeastOrdersRoute

@Serializable
data class FeastInvoiceRoute(val orderId: String)

/**
 * Registers Feast.
 *
 * [onOpenPayment] and [onAbandonPayment] are supplied by `:app`, whose Activity
 * the payment sheet opens onto; this module never names the provider.
 */
fun NavGraphBuilder.feastScreens(
    navController: NavController,
    onOpenPayment: (FeastPaymentRequest) -> Unit,
    onAbandonPayment: (FeastPaymentRequest) -> Unit,
) {
    navigation<FeastGraph>(startDestination = FeastHomeRoute) {
        composable<FeastHomeRoute> {
            FeastHomeScreen(
                onBack = { navController.popBackStack<FeastGraph>(inclusive = true) },
                onOpenRestaurant = { navController.navigate(FeastRestaurantRoute(it)) },
                onOpenCart = { navController.navigate(FeastCartRoute) },
                onOpenAddresses = { navController.navigate(FeastAddressesRoute(picking = true)) },
                onOpenOrders = { navController.navigate(FeastOrdersRoute) },
                onTrackOrder = { navController.navigate(FeastTrackingRoute(it)) },
            )
        }

        composable<FeastRestaurantRoute> {
            RestaurantScreen(
                onBack = navController::popBackStack,
                onOpenCart = { navController.navigate(FeastCartRoute) },
            )
        }

        composable<FeastCartRoute> {
            CartScreen(
                onBack = navController::popBackStack,
                onBrowse = { navController.popBackStack(FeastHomeRoute, inclusive = false) },
                onCheckout = { navController.navigate(FeastCheckoutRoute) },
            )
        }

        composable<FeastCheckoutRoute> {
            FeastCheckoutScreen(
                onBack = navController::popBackStack,
                onOpenPayment = onOpenPayment,
                onAbandonPayment = onAbandonPayment,
                onChangeAddress = { navController.navigate(FeastAddressesRoute(picking = true)) },
                onTrackOrder = { orderId ->
                    navController.navigate(FeastTrackingRoute(orderId)) {
                        // Back from tracking returns to Feast home, never to a paid checkout.
                        popUpTo(FeastHomeRoute) { inclusive = false }
                    }
                },
                onViewOrders = {
                    navController.navigate(FeastOrdersRoute) { popUpTo(FeastHomeRoute) { inclusive = false } }
                },
                onBrowse = { navController.popBackStack(FeastHomeRoute, inclusive = false) },
            )
        }

        composable<FeastAddressesRoute> {
            AddressesScreen(
                onBack = navController::popBackStack,
                onAdd = { navController.navigate(FeastAddAddressRoute) },
                onPicked = { navController.popBackStack() },
            )
        }

        composable<FeastAddAddressRoute> {
            AddAddressScreen(
                onBack = navController::popBackStack,
                onSaved = { navController.popBackStack() },
            )
        }

        composable<FeastTrackingRoute> {
            OrderTrackingScreen(
                onBack = navController::popBackStack,
                onOpenInvoice = { navController.navigate(FeastInvoiceRoute(it)) },
            )
        }

        composable<FeastOrdersRoute> {
            OrdersScreen(
                onBack = navController::popBackStack,
                onOpenOrder = { navController.navigate(FeastTrackingRoute(it)) },
            )
        }

        composable<FeastInvoiceRoute> {
            InvoiceScreen(onBack = navController::popBackStack)
        }
    }
}

fun NavController.navigateToFeast() = navigate(FeastGraph)
