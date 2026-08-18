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
import com.us.android.core.media.PlayerPool
import com.us.android.core.model.SessionState
import com.us.android.feature.auth.login.LoginRoute
import com.us.android.feature.auth.register.RegisterRoute
import com.us.android.feature.auth.verify.VerifyEmailRoute
import com.us.android.feature.feed.navigation.FeedRoute
import com.us.android.feature.feed.navigation.feedScreen
import com.us.android.feature.feed.navigation.reelsScreen
import com.us.android.feature.post.navigation.commentsScreen
import com.us.android.feature.post.navigation.navigateToComments
import com.us.android.feature.post.navigation.navigateToPost
import com.us.android.feature.post.navigation.postScreen
import com.us.android.feature.profile.navigation.editProfileScreen
import com.us.android.feature.profile.navigation.navigateToEditProfile
import com.us.android.feature.profile.navigation.navigateToProfile
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
// Home is FeedRoute and Me is OwnProfileRoute; both live in their feature
// modules, because the feature owns the screen. The rest are declared here
// because no feature module owns them yet.

@Serializable
data object FriendsRoute

@Serializable
data object ExploreRoute

/**
 * The design-system gallery.
 *
 * Not a tab. It used to occupy Home; the feed took that over, and the gallery
 * survives as a pushed screen because reviewing tokens at real density on a
 * real device is still the only way to catch a bad colour or a wrong metric.
 */
@Serializable
data object GalleryRoute

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
    pool: PlayerPool,
    navController: NavHostController = rememberNavController(),
) {
    val startDestination = if (sessionState.isAuthenticated) FeedRoute else LoginRoute

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
            tabDestinations(navController, pool)
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
private fun NavGraphBuilder.tabDestinations(
    navController: NavHostController,
    pool: PlayerPool,
) {
    // The real home feed. It replaced the design-system gallery once the
    // 2026-08-17 capture returned a non-empty page and proved the item shape —
    // before that the feed could only have been built on an invented DTO.
    feedScreen(
        onOpenPost = { postId -> navController.navigateToPost(postId) },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
    )
    composable<GalleryRoute> {
        // The gallery is still reachable from Explore so the design tokens stay
        // reviewable on a real device at real density.
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
    // The reels surface. The pool is supplied by :app so its lifetime belongs
    // to the composition root rather than a composable the pager recomposes —
    // it holds decoder sessions, and reacquiring those mid-scroll is exactly
    // the stutter this surface exists to avoid.
    reelsScreen(
        pool = pool,
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
    )
    composable<ExploreRoute> {
        PlaceholderScreen(
            title = "Explore",
            reason = "Search is not built yet. The design-system gallery lives here " +
                "meanwhile so the tokens stay reviewable on a real device.",
            actionLabel = "Open the design gallery",
            onAction = { navController.navigate(GalleryRoute) },
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
        onEditProfile = { navController.navigateToEditProfile() },
    )
    profileScreen(
        onOpenFollowers = {},
        onOpenFollowing = {},
        onBack = { navController.popBackStack() },
    )
    editProfileScreen(
        onBack = { navController.popBackStack() },
        // Saving pops back to the profile, which reloads and shows the new
        // values. Distinct from onBack only in intent today, but the two are
        // separate callbacks so a later "saved" confirmation has somewhere to
        // live without changing the abandon path.
        onSaved = { navController.popBackStack() },
    )

    // Post detail and its comments. Cross-feature navigation is resolved here:
    // :feature:post hands back an author id and :app decides that opens a
    // profile. Neither feature imports the other, so each stays testable alone.
    postScreen(
        onBack = { navController.popBackStack() },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
        onOpenComments = { postId -> navController.navigateToComments(postId) },
    )
    commentsScreen(onBack = { navController.popBackStack() })
}

/** Host for [UsNavHost] that observes the session and rebuilds on change. */
@Composable
fun UsApp(viewModel: MainViewModel, pool: PlayerPool) {
    val sessionState by viewModel.sessionState.collectAsStateWithLifecycle()
    UsNavHost(sessionState = sessionState, pool = pool)
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
