package com.us.android.navigation

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.SessionState
import com.us.android.feature.auth.login.LoginRoute
import com.us.android.feature.auth.register.RegisterRoute
import com.us.android.feature.auth.verify.VerifyEmailRoute
import com.us.android.feature.profile.navigation.navigateToOwnProfile
import com.us.android.feature.profile.navigation.profileScreen
import kotlinx.serialization.Serializable

@Serializable
data object LoginRoute

@Serializable
data object RegisterRoute

/**
 * Email verification.
 *
 * [verificationToken] is the server-issued credential that names the pending
 * account — the verify and resend endpoints take no user id by design, so
 * this token is the only handle on it. Carried as a route argument so the
 * screen survives rotation and process death without a shared holder.
 */
@Serializable
data class VerifyEmailRoute(
    val verificationToken: String,
    val email: String,
)

@Serializable
data object HomeRoute

/**
 * The app's navigation graph.
 *
 * Routing is driven by [SessionState], which resolves synchronously on the
 * first frame — the graph **never awaits** session restore. That is the whole
 * point of the design and what closes finding F5: the Flutter router blocks
 * every navigation on a 3-second `sessionReady` await ([router.dart:128]).
 *
 * Routes are `@Serializable` objects rather than strings, so arguments become
 * compile-checked instead of stringly-typed.
 */
@Composable
fun UsNavHost(
    sessionState: SessionState,
    navController: NavHostController = rememberNavController(),
) {
    val startDestination = if (sessionState.isAuthenticated) HomeRoute else LoginRoute

    NavHost(
        navController = navController,
        startDestination = startDestination,
        modifier = Modifier.fillMaxSize(),
    ) {
        composable<LoginRoute> {
            LoginRoute(
                onCreateAccount = { navController.navigate(RegisterRoute) },
                // The resumption path: signing in with an unverified account
                // returns 403 EMAIL_NOT_VERIFIED carrying a FRESH token, so a
                // user who closed the app mid-signup can still finish. Without
                // this, that account is stranded permanently.
                onNeedsVerification = { token, email ->
                    navController.navigate(VerifyEmailRoute(token, email))
                },
            )
        }
        composable<RegisterRoute> {
            RegisterRoute(
                onNeedsVerification = { token, email ->
                    navController.navigate(VerifyEmailRoute(token, email)) {
                        // Don't leave a filled signup form behind Back — the
                        // account already exists; resubmitting would 409.
                        popUpTo(RegisterRoute) { inclusive = true }
                    }
                },
                onBackToLogin = { navController.popBackStack() },
            )
        }
        composable<VerifyEmailRoute> { entry ->
            val route = entry.toRoute<VerifyEmailRoute>()
            VerifyEmailRoute(
                verificationToken = route.verificationToken,
                email = route.email,
                // Verification issues no session, so a verified user lands on
                // sign-in, not inside the app.
                onVerified = {
                    navController.navigate(LoginRoute) {
                        popUpTo<LoginRoute> { inclusive = true }
                    }
                },
                onBackToLogin = {
                    navController.navigate(LoginRoute) {
                        popUpTo<LoginRoute> { inclusive = true }
                    }
                },
            )
        }
        composable<HomeRoute> {
            // Placeholder until the tab shell lands. The design-system gallery
            // remains reachable here so the tokens stay reviewable on device.
            DesignSystemGalleryScreen(
                onOpenOwnProfile = { navController.navigateToOwnProfile() },
            )
        }

        // The profile vertical slice. Registered through the feature's own
        // NavGraphBuilder extension, so `:app` never imports its screens or
        // ViewModels — it supplies only the destinations profile navigates to.
        profileScreen(
            // Followers and following lists are not built yet. The graph
            // endpoints are verified, but their two pagination DTOs differ
            // (offset returns a bare list, cursor returns {items, next_cursor})
            // and that belongs with the paging work rather than here.
            onOpenFollowers = {},
            onOpenFollowing = {},
        )
    }
}

/** Host for [UsNavHost] that observes the session and rebuilds on change. */
@Composable
fun UsApp(viewModel: MainViewModel) {
    val sessionState by viewModel.sessionState.collectAsStateWithLifecycle()
    UsNavHost(sessionState = sessionState)
}

@Preview(name = "Splash", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun SplashPreview() {
    UsTheme {
        UsScaffold {
            Column(
                modifier = Modifier.fillMaxSize(),
                horizontalAlignment = Alignment.CenterHorizontally,
                verticalArrangement = Arrangement.Center,
            ) {
                Text(
                    text = "US",
                    style = MaterialTheme.typography.headlineMedium,
                    color = UsTheme.extended.textPrimary,
                )
            }
        }
    }
}
