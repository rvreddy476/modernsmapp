package com.us.feast.rider

import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.runtime.key
import androidx.hilt.navigation.compose.hiltViewModel
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.us.android.core.auth.SessionStateProvider
import com.us.android.core.model.SessionState
import com.us.android.feature.auth.login.LoginRoute
import com.us.android.feature.auth.register.RegisterRoute
import com.us.android.feature.auth.verify.VerifyEmailRoute
import com.us.android.feature.rider.root.RiderRoot
import com.us.android.feature.rider.root.RiderRootViewModel
import com.us.android.feature.rider.root.RiderSplash
import kotlinx.serialization.Serializable

@Serializable
data object RiderSignInRoute

@Serializable
data object RiderRegisterRoute

@Serializable
data class RiderVerifyEmailRoute(val verificationToken: String, val email: String)

/**
 * The shell, driven by the session: sign-in, registration and email
 * verification are :feature:auth's own screens (the flow Momentum and Kitchen
 * use); an authenticated session opens [RiderRoot], keyed by user so a sign-out
 * and sign-in as someone else never shows the previous rider for a frame.
 */
@Composable
fun RiderApp(sessionStateProvider: SessionStateProvider) {
    val session by sessionStateProvider.sessionState.collectAsStateWithLifecycle()
    when (val current = session) {
        SessionState.Unknown -> RiderSplash()
        is SessionState.Authenticated -> key(current.userId) {
            RiderRoot(viewModel = hiltViewModel<RiderRootViewModel>(key = "rider-${current.userId}"))
        }
        else -> RiderSignIn()
    }
}

@Composable
private fun RiderSignIn() {
    val navController = rememberNavController()
    val backToSignIn: () -> Unit = {
        navController.navigate(RiderSignInRoute) {
            popUpTo<RiderSignInRoute> { inclusive = true }
        }
    }
    NavHost(navController = navController, startDestination = RiderSignInRoute) {
        composable<RiderSignInRoute> {
            LoginRoute(
                onCreateAccount = { navController.navigate(RiderRegisterRoute) },
                onNeedsVerification = { token, email -> navController.navigate(RiderVerifyEmailRoute(token, email)) },
            )
        }
        composable<RiderRegisterRoute> {
            RegisterRoute(
                onNeedsVerification = { token, email ->
                    navController.navigate(RiderVerifyEmailRoute(token, email)) {
                        popUpTo<RiderRegisterRoute> { inclusive = true }
                    }
                },
                onBackToLogin = { navController.popBackStack() },
            )
        }
        composable<RiderVerifyEmailRoute> { entry ->
            val route = entry.toRoute<RiderVerifyEmailRoute>()
            VerifyEmailRoute(
                verificationToken = route.verificationToken,
                email = route.email,
                onVerified = backToSignIn,
                onBackToLogin = backToSignIn,
            )
        }
    }
}
