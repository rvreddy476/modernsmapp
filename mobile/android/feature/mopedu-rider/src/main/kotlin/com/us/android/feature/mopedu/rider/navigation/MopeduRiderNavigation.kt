package com.us.android.feature.mopedu.rider.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavOptions
import androidx.navigation.compose.composable
import com.us.android.feature.mopedu.rider.MopeduRiderRoute
import kotlinx.serialization.Serializable

@Serializable
data object MopeduRiderRoute

fun NavController.navigateToMopeduRider(navOptions: NavOptions? = null) {
    navigate(route = MopeduRiderRoute, navOptions = navOptions)
}

fun NavGraphBuilder.mopeduRiderScreen(
    onNavigateBack: () -> Unit,
) {
    composable<MopeduRiderRoute> {
        MopeduRiderRoute(
            onNavigateBack = onNavigateBack,
        )
    }
}
