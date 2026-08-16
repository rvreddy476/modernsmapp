package com.us.android.navigation

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.Text
import androidx.compose.runtime.Composable
import androidx.compose.runtime.getValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.us.android.core.designsystem.component.UsNavigationBar
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.model.SessionState
import com.us.android.feature.auth.login.LoginRoute
import com.us.android.feature.auth.register.RegisterRoute
import com.us.android.feature.auth.verify.VerifyEmailRoute
import com.us.android.feature.profile.navigation.ownProfileScreen
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

// ── Top-level (tab) destinations ───────────────────────────────────────
//
// The Me tab's route is OwnProfileRoute and lives in :feature:profile, because
// the feature owns the screen. The four below have no feature module yet.

@Serializable
data object HomeRoute

@Serializable
data object FriendsRoute

@Serializable
data object ReelsRoute

@Serializable
data object ExploreRoute

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

    // The bar lives OUTSIDE the NavHost so it survives destination changes
    // rather than being recomposed away and back on every navigation.
    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentTab = TopLevelDestination.forDestination(backStackEntry?.destination)

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        containerColor = MaterialTheme.colorScheme.background,
        bottomBar = {
            // Null tab means the current screen is not a tab root — an auth
            // screen, or a pushed profile. No bar there.
            if (currentTab != null) {
                UsNavigationBar(
                    items = TopLevelDestination.entries.map { it.item },
                    selectedIndex = currentTab.ordinal,
                    onSelect = { index ->
                        navController.navigateToTopLevel(TopLevelDestination.entries[index])
                    },
                )
            }
        },
    ) { shellPadding ->
        NavHost(
            navController = navController,
            startDestination = startDestination,
            modifier = Modifier
                .fillMaxSize()
                .padding(shellPadding),
        ) {
            authDestinations(navController)
            tabDestinations(navController)
        }
    }
}

/**
 * Sign-in, registration and email verification.
 *
 * None of these are tab roots, so the bottom bar is absent on all three —
 * [TopLevelDestination.forDestination] returns null for them.
 */
private fun NavGraphBuilder.authDestinations(navController: NavHostController) {
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
}

/**
 * The five tab roots, plus the screens pushed on top of them.
 *
 * Four tabs are placeholders that state why they are empty. Three of the four
 * are blocked on backend work rather than client effort, and the placeholder
 * is where that stays visible.
 */
private fun NavGraphBuilder.tabDestinations(navController: NavHostController) {
    composable<HomeRoute> {
        // The design-system gallery still occupies Home so the token port
        // stays reviewable on a real device. The feed replaces it once a
        // non-empty /v1/feed/home capture proves the item DTO.
        DesignSystemGalleryScreen(
            onOpenOwnProfile = { navController.navigateToTopLevel(TopLevelDestination.ME) },
        )
    }
    composable<FriendsRoute> {
        PlaceholderScreen(
            title = "Friends",
            reason = "The graph endpoints are verified, but followers and following " +
                "return two different shapes for offset and cursor paging. This " +
                "lands with the paging work.",
        )
    }
    composable<ReelsRoute> {
        PlaceholderScreen(
            title = "Reels",
            reason = "Blocked on media delivery: the HLS master playlist exists in " +
                "storage, but /v1/media/:id/serve returns 503 and there is no " +
                "client-usable URL yet.",
        )
    }
    composable<ExploreRoute> {
        PlaceholderScreen(
            title = "Explore",
            reason = "Needs seeded content. Every feed surface currently returns an " +
                "empty page, so the item shape has never been observed.",
        )
    }

    // Registered through the feature's own NavGraphBuilder extensions so
    // `:app` never imports its screens or ViewModels — it supplies only the
    // destinations profile navigates to.
    //
    // Two registrations, one screen: the Me tab is a root with no back
    // control; a pushed profile has one.
    ownProfileScreen(
        onOpenFollowers = {},
        onOpenFollowing = {},
    )
    profileScreen(
        onOpenFollowers = {},
        onOpenFollowing = {},
        onBack = { navController.popBackStack() },
    )
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
