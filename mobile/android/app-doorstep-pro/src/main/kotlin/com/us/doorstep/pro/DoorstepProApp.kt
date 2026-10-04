package com.us.doorstep.pro

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
import com.us.android.feature.doorsteppro.root.ProRoot
import com.us.android.feature.doorsteppro.root.ProRootViewModel
import com.us.android.feature.doorsteppro.root.ProSplash
import kotlinx.serialization.Serializable

@Serializable
data object ProSignInRoute

@Serializable
data object ProRegisterRoute

@Serializable
data class ProVerifyEmailRoute(val verificationToken: String, val email: String)

/**
 * The shell, driven by the session: sign-in, registration and email
 * verification are :feature:auth's own screens (the flow Momentum and the
 * partner apps use); an authenticated session opens [ProRoot], keyed by user
 * so a sign-out and sign-in as someone else never shows the previous
 * professional for a frame.
 */
@Composable
fun DoorstepProApp(sessionStateProvider: SessionStateProvider) {
    val session by sessionStateProvider.sessionState.collectAsStateWithLifecycle()
    when (val current = session) {
        SessionState.Unknown -> ProSplash()
        is SessionState.Authenticated -> key(current.userId) {
            ProRoot(viewModel = hiltViewModel<ProRootViewModel>(key = "doorstep-pro-${current.userId}"))
        }
        else -> ProSignIn()
    }
}

@Composable
private fun ProSignIn() {
    val navController = rememberNavController()
    val backToSignIn: () -> Unit = {
        navController.navigate(ProSignInRoute) {
            popUpTo<ProSignInRoute> { inclusive = true }
        }
    }
    NavHost(navController = navController, startDestination = ProSignInRoute) {
        composable<ProSignInRoute> {
            LoginRoute(
                onCreateAccount = { navController.navigate(ProRegisterRoute) },
                onNeedsVerification = { token, email -> navController.navigate(ProVerifyEmailRoute(token, email)) },
            )
        }
        composable<ProRegisterRoute> {
            RegisterRoute(
                onNeedsVerification = { token, email ->
                    navController.navigate(ProVerifyEmailRoute(token, email)) {
                        popUpTo<ProRegisterRoute> { inclusive = true }
                    }
                },
                onBackToLogin = { navController.popBackStack() },
            )
        }
        composable<ProVerifyEmailRoute> { entry ->
            val route = entry.toRoute<ProVerifyEmailRoute>()
            VerifyEmailRoute(
                verificationToken = route.verificationToken,
                email = route.email,
                onVerified = backToSignIn,
                onBackToLogin = backToSignIn,
            )
        }
    }
}
