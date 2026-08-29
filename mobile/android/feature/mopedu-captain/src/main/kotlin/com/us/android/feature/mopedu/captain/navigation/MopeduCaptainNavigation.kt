package com.us.android.feature.mopedu.captain.navigation

import androidx.navigation.NavController
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavOptions
import androidx.navigation.compose.composable
import com.us.android.feature.mopedu.captain.MopeduCaptainRoute
import kotlinx.serialization.Serializable

@Serializable
data object MopeduCaptainRoute

fun NavController.navigateToMopeduCaptain(navOptions: NavOptions? = null) {
    navigate(route = MopeduCaptainRoute, navOptions = navOptions)
}

fun NavGraphBuilder.mopeduCaptainScreen(
    onNavigateBack: () -> Unit,
) {
    composable<MopeduCaptainRoute> {
        MopeduCaptainRoute(
            onNavigateBack = onNavigateBack,
        )
    }
}
