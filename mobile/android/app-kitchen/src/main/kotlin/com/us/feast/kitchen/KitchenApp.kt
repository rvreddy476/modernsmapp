package com.us.feast.kitchen

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
import com.us.android.feature.kitchen.root.KitchenRoot
import com.us.android.feature.kitchen.root.KitchenRootViewModel
import com.us.android.feature.kitchen.root.KitchenSplash
import kotlinx.serialization.Serializable

@Serializable
data object KitchenSignInRoute

@Serializable
data object KitchenRegisterRoute

@Serializable
data class KitchenVerifyEmailRoute(val verificationToken: String, val email: String)

/**
 * The shell, driven by the session: sign-in, registration and email
 * verification are :feature:auth's own screens and ViewModels (the same flow
 * Momentum uses); an authenticated session opens [KitchenRoot].
 *
 * The kitchen's ViewModel is keyed by user, so signing out and in as someone
 * else never shows the previous partner's kitchen for a frame.
 */
@Composable
fun KitchenApp(sessionStateProvider: SessionStateProvider) {
    val session by sessionStateProvider.sessionState.collectAsStateWithLifecycle()
    when (val current = session) {
        SessionState.Unknown -> KitchenSplash()
        is SessionState.Authenticated -> key(current.userId) {
            KitchenRoot(viewModel = hiltViewModel<KitchenRootViewModel>(key = "kitchen-${current.userId}"))
        }
        else -> KitchenSignIn()
    }
}

@Composable
private fun KitchenSignIn() {
    val navController = rememberNavController()
    val backToSignIn: () -> Unit = {
        navController.navigate(KitchenSignInRoute) {
            popUpTo<KitchenSignInRoute> { inclusive = true }
        }
    }
    NavHost(navController = navController, startDestination = KitchenSignInRoute) {
        composable<KitchenSignInRoute> {
            LoginRoute(
                onCreateAccount = { navController.navigate(KitchenRegisterRoute) },
                // An unverified account signs in with a fresh verification
                // token; this is the only way to finish it.
                onNeedsVerification = { token, email -> navController.navigate(KitchenVerifyEmailRoute(token, email)) },
            )
        }
        composable<KitchenRegisterRoute> {
            RegisterRoute(
                onNeedsVerification = { token, email ->
                    navController.navigate(KitchenVerifyEmailRoute(token, email)) {
                        popUpTo<KitchenRegisterRoute> { inclusive = true }
                    }
                },
                onBackToLogin = { navController.popBackStack() },
            )
        }
        composable<KitchenVerifyEmailRoute> { entry ->
            val route = entry.toRoute<KitchenVerifyEmailRoute>()
            VerifyEmailRoute(
                verificationToken = route.verificationToken,
                email = route.email,
                onVerified = backToSignIn,
                onBackToLogin = backToSignIn,
            )
        }
    }
}
