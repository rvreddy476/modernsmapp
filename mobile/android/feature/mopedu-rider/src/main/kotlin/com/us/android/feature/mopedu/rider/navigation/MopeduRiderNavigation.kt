// MatchingDeclarationName: this file is the feature's navigation contract —
// the route types plus the graph extension that uses them.
@file:Suppress("MatchingDeclarationName")

package com.us.android.feature.mopedu.rider.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.compose.composable
import androidx.navigation.compose.navigation
import com.us.android.feature.mopedu.rider.MopeduRiderRoute
import com.us.android.feature.mopedu.rider.history.RideHistoryScreen
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentRequest
import kotlinx.serialization.Serializable

/** Mopedu, as one graph with its own back stack: Back from its root leaves Mopedu. */
@Serializable
data object MopeduRiderGraph

@Serializable
data object MopeduRideRoute

@Serializable
data object MopeduHistoryRoute

/**
 * Registers the customer's ride flow. [onOpenPayment] and [onAbandonPayment]
 * are supplied by `:app`, whose Activity the payment sheet opens onto; this
 * module never names the provider.
 */
fun NavGraphBuilder.mopeduRiderScreens(
    navController: NavController,
    onOpenPayment: (MopeduPaymentRequest) -> Unit,
    onAbandonPayment: (MopeduPaymentRequest) -> Unit,
) {
    navigation<MopeduRiderGraph>(startDestination = MopeduRideRoute) {
        composable<MopeduRideRoute> {
            MopeduRiderRoute(
                onNavigateBack = { navController.popBackStack<MopeduRiderGraph>(inclusive = true) },
                onOpenHistory = { navController.navigate(MopeduHistoryRoute) },
                onOpenPayment = onOpenPayment,
                onAbandonPayment = onAbandonPayment,
            )
        }
        composable<MopeduHistoryRoute> {
            RideHistoryScreen(
                onBack = { navController.popBackStack() },
                onOpenPayment = onOpenPayment,
                onAbandonPayment = onAbandonPayment,
            )
        }
    }
}

fun NavController.navigateToMopeduRider() = navigate(MopeduRiderGraph)
