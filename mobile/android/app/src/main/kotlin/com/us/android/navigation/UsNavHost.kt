package com.us.android.navigation

import androidx.compose.animation.AnimatedVisibility
import androidx.compose.animation.core.tween
import androidx.compose.animation.fadeIn
import androidx.compose.animation.fadeOut
import androidx.compose.animation.slideInVertically
import androidx.compose.animation.slideOutVertically
import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.WindowInsets
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.material3.ScaffoldDefaults
import androidx.compose.runtime.Composable
import androidx.compose.runtime.CompositionLocalProvider
import androidx.compose.runtime.LaunchedEffect
import androidx.compose.runtime.getValue
import androidx.compose.runtime.mutableStateOf
import androidx.compose.runtime.remember
import androidx.compose.runtime.saveable.rememberSaveable
import androidx.compose.runtime.setValue
import androidx.compose.ui.Alignment
import androidx.compose.ui.Modifier
import androidx.compose.ui.tooling.preview.Preview
import androidx.lifecycle.compose.collectAsStateWithLifecycle
import androidx.navigation.NavDestination.Companion.hasRoute
import androidx.navigation.NavGraphBuilder
import androidx.navigation.NavHostController
import androidx.navigation.compose.NavHost
import androidx.navigation.compose.composable
import androidx.navigation.compose.currentBackStackEntryAsState
import androidx.navigation.compose.rememberNavController
import androidx.navigation.toRoute
import com.us.android.core.commerce.payment.PaymentAttempt
import com.us.android.core.designsystem.component.UsNavigationBar
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsWordmark
import com.us.android.core.designsystem.component.UsWordmarkSize
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.PlayerPool
import com.us.android.core.model.NotificationTarget
import com.us.android.core.model.SessionState
import com.us.android.core.ui.ChromeVisibility
import com.us.android.core.ui.LocalChromeVisibility
import com.us.android.explore.ExploreScreen
import com.us.android.explore.LauncherApp
import com.us.android.explore.LauncherTile
import com.us.android.explore.launcherTiles
import com.us.android.feature.auth.login.LoginRoute
import com.us.android.feature.auth.register.RegisterRoute
import com.us.android.feature.auth.verify.VerifyEmailRoute
import com.us.android.feature.call.navigation.CallRoute
import com.us.android.feature.call.navigation.callHistoryScreen
import com.us.android.feature.call.navigation.callScreen
import com.us.android.feature.call.navigation.navigateToCallHistory
import com.us.android.feature.call.navigation.navigateToCallSurface
import com.us.android.feature.call.navigation.navigateToOutgoingCall
import com.us.android.feature.chat.navigation.ChatRequestRoute
import com.us.android.feature.chat.navigation.ChatThreadRoute
import com.us.android.feature.chat.navigation.GroupCreateRoute
import com.us.android.feature.chat.navigation.chatInboxScreen
import com.us.android.feature.chat.navigation.chatLockSettingsScreen
import com.us.android.feature.chat.navigation.chatRequestScreen
import com.us.android.feature.chat.navigation.chatThreadScreen
import com.us.android.feature.chat.navigation.groupCreateScreen
import com.us.android.feature.chat.navigation.groupInfoScreen
import com.us.android.feature.chat.navigation.navigateToChatInbox
import com.us.android.feature.chat.navigation.navigateToChatLockSettings
import com.us.android.feature.chat.navigation.navigateToChatRequest
import com.us.android.feature.chat.navigation.navigateToChatThread
import com.us.android.feature.chat.navigation.navigateToGroupCreate
import com.us.android.feature.chat.navigation.navigateToGroupInfo
import com.us.android.feature.commerce.navigation.commerceScreens
import com.us.android.feature.commerce.navigation.navigateToCommerce
import com.us.android.feature.feed.navigation.FeedRoute
import com.us.android.feature.feed.navigation.FriendsFeedRoute
import com.us.android.feature.feed.navigation.feedScreen
import com.us.android.feature.feed.navigation.friendsFeedScreen
import com.us.android.feature.feed.navigation.hashtagPostsScreen
import com.us.android.feature.feed.navigation.navigateToHashtagPosts
import com.us.android.feature.feed.navigation.reelsScreen
import com.us.android.feature.live.navigation.liveScreens
import com.us.android.feature.live.navigation.navigateToGoLive
import com.us.android.feature.live.navigation.navigateToLiveHub
import com.us.android.feature.live.navigation.navigateToLiveWatch
import com.us.android.feature.notifications.navigation.navigateToNotifications
import com.us.android.feature.notifications.navigation.notificationsScreen
import com.us.android.feature.post.createhub.CreateSheet
import com.us.android.feature.post.createhub.CreateSurface
import com.us.android.feature.post.navigation.ComposerRoute
import com.us.android.feature.post.navigation.CreateRoute
import com.us.android.feature.post.navigation.PostRoute
import com.us.android.feature.post.navigation.StudioRoute
import com.us.android.feature.post.navigation.composerScreen
import com.us.android.feature.post.navigation.createHubScreen
import com.us.android.feature.post.navigation.navigateToCreate
import com.us.android.feature.post.navigation.navigateToPost
import com.us.android.feature.post.navigation.navigateToStudio
import com.us.android.feature.post.navigation.postScreen
import com.us.android.feature.post.navigation.studioScreen
import com.us.android.feature.profile.navigation.DirectMessagesRoute
import com.us.android.feature.profile.navigation.MomentumHeaderDestinations
import com.us.android.feature.profile.navigation.NotificationSettingsRoute
import com.us.android.feature.profile.navigation.PrivacySettingsRoute
import com.us.android.feature.profile.navigation.ProfileDetailsRoute
import com.us.android.feature.profile.navigation.SecuritySettingsRoute
import com.us.android.feature.profile.navigation.SettingsDestinations
import com.us.android.feature.profile.navigation.SettingsSections
import com.us.android.feature.profile.navigation.editProfileScreen
import com.us.android.feature.profile.navigation.followRequestsScreen
import com.us.android.feature.profile.navigation.navigateToEditProfile
import com.us.android.feature.profile.navigation.navigateToFollowRequests
import com.us.android.feature.profile.navigation.navigateToProfile
import com.us.android.feature.profile.navigation.navigateToSettings
import com.us.android.feature.profile.navigation.ownProfileScreen
import com.us.android.feature.profile.navigation.profileScreen
import com.us.android.feature.profile.navigation.settingsScreens
import com.us.android.feature.search.navigation.SearchDestinations
import com.us.android.feature.search.navigation.SearchOrigin
import com.us.android.feature.search.navigation.navigateToSearch
import com.us.android.feature.search.navigation.searchScreen
import com.us.android.feature.settings.navigation.OnboardingRoute
import com.us.android.feature.settings.navigation.accountControlScreen
import com.us.android.feature.settings.navigation.contentPreferencesScreen
import com.us.android.feature.settings.navigation.manageAccountScreen
import com.us.android.feature.settings.navigation.modulesSettingsScreen
import com.us.android.feature.settings.navigation.navigateToAccountControl
import com.us.android.feature.settings.navigation.navigateToContentPreferences
import com.us.android.feature.settings.navigation.navigateToManageAccount
import com.us.android.feature.settings.navigation.navigateToModulesSettings
import com.us.android.feature.settings.navigation.navigateToRecentlyDeleted
import com.us.android.feature.settings.navigation.navigateToScreenTime
import com.us.android.feature.settings.navigation.onboardingScreen
import com.us.android.feature.settings.navigation.recentlyDeletedScreen
import com.us.android.feature.settings.navigation.screenTimeScreen
import com.us.android.feature.tube.navigation.TubeDestinations
import com.us.android.feature.tube.navigation.navigateToTube
import com.us.android.feature.tube.navigation.navigateToTubeChannel
import com.us.android.feature.tube.navigation.navigateToTubeSaved
import com.us.android.feature.tube.navigation.navigateToTubeScheduled
import com.us.android.feature.tube.navigation.navigateToTubeTab
import com.us.android.feature.tube.navigation.navigateToWatch
import com.us.android.feature.tube.navigation.tubeScreens
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

/**
 * The brand mark, shown while the shell is [ShellState.Loading]: signed in,
 * module choices not yet known. It exists so that a signed-in launch never
 * shows Login or Onboarding for a frame while the cache is read — the two
 * screens whose flash would read as "you've been signed out". No bar.
 */
@Serializable
data object SplashRoute

// ── Top-level (tab) destinations ───────────────────────────────────────
//
// Home is FeedRoute and Me is OwnProfileRoute; both live in their feature
// modules, because the feature owns the screen. The rest are declared here
// because no feature module owns them yet.

/**
 * The Explore tab — the mini-app launcher with a search field on top
 * (founder, 2026-09-05). Its field submits to the search page scoped to
 * everything; the headers open the same page scoped to their own surface
 * (see `SearchOrigin` in `:feature:search`).
 */
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
 * Routing is driven by [ShellState], which combines the session (resolved
 * synchronously on the first frame — the graph **never awaits** session
 * restore; that is what closes finding F5, the Flutter router's 3-second
 * `sessionReady` await at [router.dart:128]) with the user's module choices.
 * The four shell states map to four start destinations: sign-in, the splash,
 * the module picker, or the user's home tab.
 *
 * The bottom bar is built from the same choices, in a FIXED order — Home,
 * Reels, "+", Explore, Me — with Reels present only when its module is on.
 * The home module decides which of those opens first, never their order.
 *
 * Routes are `@Serializable` objects rather than strings, so arguments become
 * compile-checked instead of stringly-typed.
 */
@Composable
fun UsNavHost(
    sessionState: SessionState,
    shellState: ShellState,
    pool: PlayerPool,
    pushDestination: com.us.android.push.PushDestination? = null,
    onPushDestinationConsumed: () -> Unit = {},
    callState: com.us.android.core.call.CallState = com.us.android.core.call.CallState.Idle,
    // Supplied by MainActivity, which IS the Activity the PSP SDK needs and
    // which holds the coordinator. Passing it down rather than looking up a
    // context here removes the "what if this is not an Activity" branch
    // entirely, and keeps :app's provider choice in one place.
    onOpenPaymentSheet: (attempt: PaymentAttempt, orderNumber: String) -> Unit = { _, _ -> },
    onAbandonPaymentSheet: (attempt: PaymentAttempt) -> Unit = { _ -> },
    navController: NavHostController = rememberNavController(),
) {
    val tabs = remember(shellState) {
        (shellState as? ShellState.Ready)?.let { TabResolver.resolve(it.prefs) }.orEmpty()
    }
    val launcher = rememberLauncher()
    val startDestination = shellState.startDestination()

    // An incoming ring fronts the call surface (foreground path; background
    // rings arrive via the full-screen CALLS notification whose tap lands in
    // the same place). Guarded so a ring never stacks a second call screen.
    LaunchedEffect(callState, sessionState.isAuthenticated) {
        if (callState is com.us.android.core.call.CallState.Incoming &&
            sessionState.isAuthenticated &&
            navController.currentBackStackEntry?.destination?.hasRoute(CallRoute::class) != true
        ) {
            navController.navigateToCallSurface()
        }
    }

    // A notification tap routes ONLY under an authenticated session: a tap
    // while signed out waits through the login (the destination survives in
    // PushDestinations), and a tap for a session that has ended routes
    // nowhere until someone signs in again. The thread title is left blank —
    // the screen fills it from the loaded conversation.
    LaunchedEffect(pushDestination, sessionState.isAuthenticated) {
        val destination = pushDestination ?: return@LaunchedEffect
        if (!sessionState.isAuthenticated) return@LaunchedEffect
        onPushDestinationConsumed()
        navController.openPushDestination(destination)
    }

    // The bar lives OUTSIDE the NavHost so it survives destination changes
    // rather than being recomposed away and back on every navigation.
    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentTab = TopLevelDestination.forDestination(backStackEntry?.destination)

    // The Create sheet opens OVER the current tab; nothing is pushed until a
    // tile is picked. Saveable so a rotation mid-choice keeps it open.
    var createSheetOpen by rememberSaveable { mutableStateOf(false) }

    // A screen's request about the chrome — today only "hide the bar", made
    // by Reels' full mode. The shell owns the holder and the decision
    // (bottomBarVisible); the screen only asks, through the local it is
    // handed below. Screen-scoped by construction: the request is withdrawn
    // when the screen leaves, so the bar is back on every other tab.
    val chrome = remember { ChromeVisibility() }

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        containerColor = MaterialTheme.colorScheme.background,
        // Reels fills the frame from the very top: the video under the status
        // bar, its translucent header padding itself beneath it. The shell
        // hands that tab no inset at all; every other screen's own scaffold
        // reserves the system bars as before, because the NavHost below
        // consumes only what it was given.
        contentWindowInsets = if (currentTab?.drawsUnderStatusBar == true) {
            WindowInsets(0)
        } else {
            ScaffoldDefaults.contentWindowInsets
        },
        bottomBar = {
            // Null tab means the current screen is not a top-level root — an
            // auth screen, the splash, the picker, or a pushed profile. No
            // bar there. A root that is not IN the bar — the inbox, Explore —
            // hides it too: those open from the header and leave by Back, and
            // a bar with nothing selected under them would only say "you are
            // nowhere". An empty tab list means the shell is not Ready yet.
            //
            // The route decides instantly; only the SCREEN's request animates.
            // A pushed screen replaces the bar with its own frame in one
            // navigation transition, whereas full mode hides the bar under a
            // video that stays put, and there a bar that vanishes reads as a
            // glitch — so it slides down and fades, and slides back in.
            if (routeShowsBottomBar(currentTab, tabs)) {
                AnimatedVisibility(
                    visible = bottomBarVisible(currentTab, tabs, chrome.bottomBarHidden),
                    enter = fadeIn(tween(BAR_ANIM_MILLIS)) + slideInVertically(tween(BAR_ANIM_MILLIS)) { it },
                    exit = fadeOut(tween(BAR_ANIM_MILLIS)) + slideOutVertically(tween(BAR_ANIM_MILLIS)) { it },
                ) {
                    UsNavigationBar(
                        items = tabs.map { it.item },
                        selectedIndex = tabs.indexOf(currentTab),
                        onSelect = { index -> navController.navigateToTopLevel(tabs[index]) },
                        // Momentum's raised centre button is always Create, not a
                        // tab. It opens the Create SHEET over the current screen;
                        // while the sheet is up the "+" reads as "×" and closes it.
                        centerAction = { createSheetOpen = !createSheetOpen },
                        centerActive = createSheetOpen,
                    )
                }
            }
        },
    ) { shellPadding ->
        CompositionLocalProvider(LocalChromeVisibility provides chrome) {
            NavHost(
                navController = navController,
                startDestination = startDestination,
                modifier = Modifier
                    .fillMaxSize()
                    .padding(shellPadding)
                    // Padding does not CONSUME insets. Without this, every
                    // screen's own scaffold and top bar saw the status bar and
                    // the navigation bar again and reserved them a second time:
                    // a band above the wordmark and a band between the last
                    // card and the tab bar (founder's phone, 2026-09-04).
                    .consumeWindowInsets(shellPadding),
            ) {
                authDestinations(navController)
                shellDestinations()
                tabDestinations(navController, pool, launcher, onOpenPaymentSheet, onAbandonPaymentSheet)
            }
        }
    }

    // The Create sheet: six typed tiles and Go Live. A tile pushes the hub
    // opened on that surface; Go Live opens the live hub, which is where the
    // old create rail's LIVE slot went too.
    if (createSheetOpen) {
        CreateSheet(
            onPick = { surface -> navController.navigateToCreate(surface) },
            onOpenLive = { navController.navigateToLiveHub() },
            onDismiss = { createSheetOpen = false },
        )
    }

    // Screen-time nudge, over whatever is on screen. Gated on an authenticated
    // session: the wellbeing endpoint it polls needs a session, and the nudge
    // has no meaning on the sign-in screen.
    if (sessionState.isAuthenticated) {
        com.us.android.screentime.ScreenTimeGuardHost(
            onChangeLimit = { navController.navigateToScreenTime() },
        )
    }
}

/**
 * The Explore launcher's tiles: every app, always, with the ones this build
 * cannot open marked "Soon". The module choices shape the bar and the home
 * page, not this grid — Explore is where the apps are found.
 */
@Composable
private fun rememberLauncher(): List<LauncherTile> = remember { launcherTiles() }

/**
 * The two start destinations between sign-in and the tabs.
 *
 * Neither takes a callback: both are LEFT by a shell-state change (the cache
 * or the network answering; the onboarding save landing), never by a
 * navigation call. That is what keeps them free of Back — there is nothing
 * behind either to go back to.
 */
private fun NavGraphBuilder.shellDestinations() {
    composable<SplashRoute> { SplashScreen() }
    onboardingScreen()
}

@Composable
private fun SplashScreen() {
    UsScaffold {
        Column(
            modifier = Modifier.fillMaxSize(),
            horizontalAlignment = Alignment.CenterHorizontally,
            verticalArrangement = Arrangement.Center,
        ) {
            UsWordmark(size = UsWordmarkSize.Hero)
        }
    }
}

/**
 * The graph's start for a shell state. When Ready, the home module's tab —
 * Reels for a Reels home, Home for everything else — without touching the
 * bar's order; see [TabResolver.startDestination].
 */
private fun ShellState.startDestination(): Any = when (this) {
    ShellState.Unauthenticated -> LoginRoute
    ShellState.Loading -> SplashRoute
    ShellState.NeedsOnboarding -> OnboardingRoute
    is ShellState.Ready -> TabResolver.startDestination(prefs).rootRoute
}

/** Where a notification tap lands, by push type. Unknown types route nowhere. */
private fun NavHostController.openPushDestination(destination: com.us.android.push.PushDestination) {
    when (destination.type) {
        "dm" -> if (destination.entityId.isNotBlank()) {
            navigateToChatThread(destination.entityId, title = "")
        }
        "message_request" -> navigateToTopLevel(TopLevelDestination.MESSAGES)
        // Call pushes: the ring tap attaches to the live call state; a
        // missed-call tap opens the history.
        "incoming_call", "incoming_video_call" -> navigateToCallSurface()
        "missed_call" -> navigateToCallHistory()
        else -> Unit // not a chat push; existing surfaces handle their own
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
@Suppress("LongMethod") // One destination registration per line; splitting hides the graph.
private fun NavGraphBuilder.tabDestinations(
    navController: NavHostController,
    pool: PlayerPool,
    launcher: List<LauncherTile>,
    // Passed through rather than captured: this is a top-level extension, not
    // a lambda inside UsNavHost, so the parameter is not otherwise in scope.
    onOpenPaymentSheet: (attempt: PaymentAttempt, orderNumber: String) -> Unit,
    onAbandonPaymentSheet: (attempt: PaymentAttempt) -> Unit,
) {
    // A tapped feed video opens the Reels TAB (founder, 2026-09-05), which
    // reads the post id the feed left in ReelsEntry and opens on that reel.
    // The same tab switch the bar makes, so Reels keeps its own back stack
    // and the bar lights its item.
    val onOpenReels = { navController.navigateToTopLevel(TopLevelDestination.REELS) }

    // The real home feed. It replaced the design-system gallery once the
    // 2026-08-17 capture returned a non-empty page and proved the item shape —
    // before that the feed could only have been built on an invented DTO.
    // A photo's media opens IN PLACE — a viewer over the feed, not a pushed
    // post — so there is no post callback here; deep links reach post
    // detail through navigateToPost from the notification target.
    feedScreen(
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
        onOpenMessages = { navController.navigateToTopLevel(TopLevelDestination.MESSAGES) },
        onOpenNotifications = { navController.navigateToNotifications() },
        // The header glyph opens search scoped to Home — people and posts
        // (founder, 2026-09-05); the create action left the header for the
        // bar's centre button.
        onOpenSearch = { navController.navigateToSearch(SearchOrigin.HOME) },
        onOpenHashtag = { tag -> navController.navigateToHashtagPosts(tag) },
        onOpenReels = onOpenReels,
    )

    // The Friends feed: the same feed narrowed to mutual follows. A root
    // (so a pushed profile over it is still "inside Friends") but no longer a
    // bar item: the Explore launcher pushes it, and Back returns there.
    friendsFeedScreen(
        onBack = { navController.popBackStack() },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
        onOpenReels = onOpenReels,
    )

    // A trending tag's posts, pushed over Home from the HashTag tab.
    hashtagPostsScreen(
        onBack = { navController.popBackStack() },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
        onOpenReels = onOpenReels,
    )

    // The Create hub — one composer, opened on the surface the Create sheet
    // chose (Text, Photo, Reel, Audio, Poll, Article). On success the created
    // post REPLACES the hub in the back stack: Back from the new post returns
    // to the feed, not to a creator whose content is already published.
    createHubScreen(
        onClose = { navController.popBackStack() },
        onPublished = { postId ->
            navController.navigate(PostRoute(postId)) {
                popUpTo<CreateRoute> { inclusive = true }
            }
        },
        onOpenStudio = { uris -> navController.navigateToStudio(uris) },
        // A long video handed to the worker closes the hub and lands on the
        // viewer's OWN profile (founder, 2026-09-05), whose grid shows the
        // posting video first with its ring — the way a reel lands on Reels.
        onOpenOwnProfile = {
            navController.popBackStack<CreateRoute>(inclusive = true)
            navController.navigateToTopLevel(TopLevelDestination.ME)
        },
    )

    // Live streaming: the hub (live now + go live), the broadcaster surface
    // and the viewer surface. live-service-v2 + LiveKit behind all three.
    liveScreens(
        onBack = { navController.popBackStack() },
        onGoLive = { navController.navigateToGoLive() },
        onWatch = { streamId -> navController.navigateToLiveWatch(streamId) },
    )

    // Commerce — the buyer journey: catalogue → product → cart → address →
    // checkout → payment → orders, and the seller surface behind the
    // catalogue. Entered from the Explore launcher's Shop tile.
    //
    // `onOpenPaymentSheet` is supplied HERE rather than inside the feature
    // because the PSP integration is an app-level concern; `:feature:commerce`
    // must not know that Razorpay is the provider, or swapping one becomes a
    // change to every screen that touches payment.
    //
    // The handoff asks the SERVER to open the intent, hands the returned
    // client session to the PSP SDK, and publishes the outcome onto
    // PaymentHandoff. The checkout screen collects that and polls — because a
    // sheet closing is evidence, never proof.
    commerceScreens(
        navController = navController,
        onOpenPaymentSheet = onOpenPaymentSheet,
        onAbandonPaymentSheet = onAbandonPaymentSheet,
    )

    // The classic composer route stays registered for any older entry point;
    // the hub's Text tab embeds the same screen.
    composerScreen(
        onClose = { navController.popBackStack() },
        onPublished = { postId ->
            navController.navigate(PostRoute(postId)) {
                popUpTo<ComposerRoute> { inclusive = true }
            }
        },
    )

    // The Post Studio — the multi-photo editor. Same success contract as the
    // composer: the published post replaces the studio in the back stack, so
    // Back lands on the feed rather than an editor whose work is already live.
    studioScreen(
        onClose = { navController.popBackStack() },
        onPublished = { postId ->
            navController.navigate(PostRoute(postId)) {
                popUpTo<StudioRoute> { inclusive = true }
            }
        },
    )

    // The notification inbox — Slice D.
    //
    // `:feature:notifications` hands back a resolved TARGET, never a URL, and
    // `:app` maps it to a destination here. That is what stops the inbox from
    // importing `:feature:post` or `:feature:profile`, and it is the same
    // contract the composer uses for `onPublished`.
    //
    // A comment notification opens the POST rather than the comments sheet:
    // the sheet is a modal over the post, so the post is where a deep link has
    // to land for Back to behave. Focusing the specific comment is tracked
    // follow-up work — the id is carried, nothing yet consumes it.
    notificationsScreen(
        onBack = { navController.popBackStack() },
        onOpenTarget = { target -> navController.openNotificationTarget(target) },
        // Preferences are a `:feature:profile` destination. The inbox asks
        // for "settings"; :app decides that means this route.
        onOpenPreferences = { navController.navigate(NotificationSettingsRoute) },
        // The Momentum Follow Requests panel opens the same approval queue
        // a profile's own entry point does.
        onOpenFollowRequests = { navController.navigateToFollowRequests() },
    )

    // Messages. The entry point is the feed's top bar; a profile's Message
    // button is the other way in, and it arrives already holding a
    // conversation id because the SERVER decided which conversation that is.
    //
    // The inbox resolves each row's title and hands it over, so a thread has a
    // name on its first frame. Deriving it inside the thread would need the
    // viewer's own id, which the thread does not have — every direct
    // conversation would open with a blank header until its member list loaded.
    // The inbox is a top-level root (so a pushed thread still counts as
    // "inside Messages") but no longer a bar item: it opens from the header's
    // message glyph and the bar hides under it, so it carries a back arrow
    // the way Notifications does. Back pops to the tab it was opened from.
    chatInboxScreen(
        onBack = { navController.popBackStack() },
        onOpenThread = { conversationId, title, isGroup ->
            navController.navigateToChatThread(conversationId, title, isGroup)
        },
        onOpenRequest = { conversationId, title ->
            navController.navigateToChatRequest(conversationId, title)
        },
        onCreateGroup = { navController.navigateToGroupCreate() },
        onOpenLockSettings = { navController.navigateToChatLockSettings() },
        onOpenCallHistory = { navController.navigateToCallHistory() },
    )
    chatLockSettingsScreen(onBack = { navController.popBackStack() })
    chatThreadScreen(
        onBack = { navController.popBackStack() },
        onOpenGroupInfo = { conversationId ->
            navController.navigateToGroupInfo(conversationId)
        },
        onStartCall = { peerUserId, peerName, video, conversationId ->
            navController.navigateToOutgoingCall(peerUserId, peerName, video, conversationId)
        },
    )
    callScreen(onBack = { navController.popBackStack() })
    callHistoryScreen(onBack = { navController.popBackStack() })
    // A request decision replaces itself: Accept opens the now-real thread,
    // every other decision returns to the inbox.
    chatRequestScreen(
        onBack = { navController.popBackStack() },
        onAccepted = { conversationId, title ->
            navController.navigate(ChatThreadRoute(conversationId, title)) {
                popUpTo<ChatRequestRoute> { inclusive = true }
            }
        },
        onClosed = { navController.popBackStack() },
    )
    groupCreateScreen(
        onBack = { navController.popBackStack() },
        onCreated = { conversationId, title ->
            navController.navigate(ChatThreadRoute(conversationId, title, isGroup = true)) {
                popUpTo<GroupCreateRoute> { inclusive = true }
            }
        },
    )
    groupInfoScreen(
        onBack = { navController.popBackStack() },
        // Leaving a group closes its info AND its thread.
        onLeft = { navController.navigateToTopLevel(TopLevelDestination.MESSAGES) },
    )
    composable<GalleryRoute> {
        // Registered, not linked: the search placeholder that opened it is gone
        // (2026-09-05). The route stays so the tokens can still be reviewed on
        // a device by pushing it from a debug entry when one is wanted.
        DesignSystemGalleryScreen(
            onOpenOwnProfile = { navController.navigateToTopLevel(TopLevelDestination.ME) },
        )
    }
    // The reels surface. The pool is supplied by :app so its lifetime belongs
    // to the composition root rather than a composable the pager recomposes —
    // it holds decoder sessions, and reacquiring those mid-scroll is exactly
    // the stutter this surface exists to avoid.
    //
    // The header floats over the video (founder, 2026-09-05): the hamburger
    // opens the reel's More sheet inside the screen; search opens the search
    // page scoped to Reels — people and reels.
    reelsScreen(
        pool = pool,
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
        onOpenSearch = { navController.navigateToSearch(SearchOrigin.REELS) },
    )
    exploreDestinations(navController, launcher)

    // Tube — the long-video mini-app (redesign, 2026-09-05): home, pushed
    // from the Explore launcher, with You beside it under Tube's own bar
    // (Subscriptions hangs off You), and the watch screen over any of them. Every route is
    // a pushed screen, so the shell's bar is already gone inside Tube.
    // The header's search glyph opens the search page scoped to the video
    // app (channels, people, videos); its More sheet's rows push the
    // scheduled list and the saved videos. Reels is the app's Reels tab
    // (the screen has left the reel id in ReelsEntry, as the Home feed does);
    // "+" is the Create hub opened on Video; a channel bubble opens the
    // channel's page inside Tube.
    tubeScreens(
        TubeDestinations(
            onBack = { navController.popBackStack() },
            onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
            onOpenSearch = { navController.navigateToSearch(SearchOrigin.VIDEO) },
            onOpenVideo = { postId -> navController.navigateToWatch(postId) },
            onOpenNotifications = { navController.navigateToNotifications() },
            onOpenReels = onOpenReels,
            onCreateVideo = { navController.navigateToCreate(CreateSurface.Video) },
            onOpenExplore = { navController.navigateToTopLevel(TopLevelDestination.EXPLORE) },
            onOpenTab = { tab -> navController.navigateToTubeTab(tab) },
            onOpenChannel = { userId -> navController.navigateToTubeChannel(userId) },
            onOpenScheduled = { navController.navigateToTubeScheduled() },
            onOpenSaved = { navController.navigateToTubeSaved() },
        ),
    )

    // Search (founder, 2026-09-05): one page, scoped by the header that
    // opened it. Every row hands back an id and this is where it resolves: a
    // user opens a profile, a post the post, a reel the Reels tab (the page
    // has left the id in ReelsEntry, as the Home feed does), a video the
    // watch screen, a channel its page inside Tube.
    searchScreen(
        SearchDestinations(
            onBack = { navController.popBackStack() },
            onOpenProfile = { userId -> navController.navigateToProfile(userId) },
            onOpenPost = { postId -> navController.navigateToPost(postId) },
            onOpenReels = onOpenReels,
            onOpenVideo = { postId -> navController.navigateToWatch(postId) },
            onOpenChannel = { userId -> navController.navigateToTubeChannel(userId) },
        ),
    )

    profileDestinations(navController)

    // Post detail. Cross-feature navigation is resolved here: :feature:post
    // hands back an author id and :app decides that opens a profile. Neither
    // feature imports the other, so each stays testable alone. Comments are
    // the shared sheet over the post now, not a destination.
    postScreen(
        onBack = { navController.popBackStack() },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
    )
}

/**
 * The Explore tab. Its field submits to the search page scoped to everything.
 *
 * A launcher tile opens a destination in whichever feature owns it, and
 * this is the one place allowed to know all of them: Chat is the inbox,
 * Friends the friends feed, Alerts the notification inbox, Live the live
 * hub, Tube the long-video list. The other four module tiles have no screen
 * yet, so they never reach here — the screen answers a "Soon" tap itself.
 * Each is a plain push, so Back returns to the launcher rather than to Home.
 */
private fun NavGraphBuilder.exploreDestinations(
    navController: NavHostController,
    launcher: List<LauncherTile>,
) {
    composable<ExploreRoute> {
        ExploreScreen(
            tiles = launcher,
            onSearch = { query -> navController.navigateToSearch(SearchOrigin.EXPLORE, query) },
            onOpenApp = { app ->
                when (app) {
                    LauncherApp.CHAT -> navController.navigateToChatInbox()
                    LauncherApp.FRIENDS -> navController.navigate(FriendsFeedRoute)
                    LauncherApp.ALERTS -> navController.navigateToNotifications()
                    LauncherApp.LIVE -> navController.navigateToLiveHub()
                    LauncherApp.TUBE -> navController.navigateToTube()
                    // Shop opens the catalogue; Orders and the seller hub
                    // hang off its top bar, so one tile is the whole way in.
                    LauncherApp.SHOP -> navController.navigateToCommerce()
                    LauncherApp.MATCH, LauncherApp.ASK, LauncherApp.FEAST -> Unit
                }
            },
        )
    }
}

/** Profile, edit-profile and settings destinations registered by the feature. */
private fun NavGraphBuilder.profileDestinations(navController: NavHostController) {
    // Two registrations, one screen: the Me tab is a root with no back
    // control; a pushed profile has one.
    ownProfileScreen(
        onOpenFollowers = {},
        onOpenFollowing = {},
        onEditProfile = { navController.navigateToEditProfile() },
        onOpenSettings = { navController.navigateToSettings() },
        onOpenFollowRequests = { navController.navigateToFollowRequests() },
        // The Me tab wears the same Momentum header as Home, and its search
        // is Home's: people and posts.
        header = MomentumHeaderDestinations(
            onOpenSearch = { navController.navigateToSearch(SearchOrigin.HOME) },
            onOpenMessages = { navController.navigateToTopLevel(TopLevelDestination.MESSAGES) },
            onOpenNotifications = { navController.navigateToNotifications() },
        ),
        onOpenPost = { postId, contentType -> navController.openProfilePost(postId, contentType) },
    )
    profileScreen(
        onOpenFollowers = {},
        onOpenFollowing = {},
        onBack = { navController.popBackStack() },
        // `:feature:profile` resolves the direct conversation through
        // `:core:chat`; :app alone knows which feature renders it.
        onOpenChat = { conversationId, title ->
            navController.navigateToChatThread(conversationId, title)
        },
        onOpenPost = { postId, contentType -> navController.openProfilePost(postId, contentType) },
    )
    editProfileScreen(
        onBack = { navController.popBackStack() },
        onSaved = { navController.popBackStack() },
    )
    // The private-account owner's approval queue. Opening a row is the same
    // destination a Follow-back or any other profile link uses.
    followRequestsScreen(
        onBack = { navController.popBackStack() },
        onOpenProfile = { userId -> navController.navigateToProfile(userId) },
    )
    // Signing-out destination shared by sign-out, deactivation and deletion:
    // all three end the session the same way, so all three land on the same
    // place — the login screen, with the whole tab stack cleared behind it.
    val onSignedOut: () -> Unit = {
        navController.navigate(LoginRoute) {
            popUpTo<FeedRoute> { inclusive = true }
        }
    }
    settingsScreens(
        SettingsDestinations(
            onBack = { navController.popBackStack() },
            onEditProfile = { navController.navigateToEditProfile() },
            onProfileDetails = { navController.navigate(ProfileDetailsRoute) },
            onDirectMessages = { navController.navigate(DirectMessagesRoute) },
            onSignedOut = onSignedOut,
            sections = SettingsSections(
                onManageAccount = { navController.navigateToManageAccount() },
                onPrivacy = { navController.navigate(PrivacySettingsRoute) },
                onNotifications = { navController.navigate(NotificationSettingsRoute) },
                onScreenTime = { navController.navigateToScreenTime() },
                onContentPreferences = { navController.navigateToContentPreferences() },
                onRecentlyDeleted = { navController.navigateToRecentlyDeleted() },
                onSecurity = { navController.navigate(SecuritySettingsRoute) },
                // The module picker is `:feature:settings`; the hub only
                // asks for "modules" and this is where that resolves.
                onModules = { navController.navigateToModulesSettings() },
            ),
        ),
    )
    // The same picker as onboarding, pushed with a back arrow. Saving pops:
    // the shell re-resolves the tabs from the repository's new state, so the
    // hub the user returns to already sits under the bar they just chose.
    modulesSettingsScreen(onBack = { navController.popBackStack() })
    // Manage account, one level under Settings > Account, plus its nested
    // Account control page (deactivate / delete).
    manageAccountScreen(
        onBack = { navController.popBackStack() },
        onAccountControl = { navController.navigateToAccountControl() },
    )
    accountControlScreen(onBack = { navController.popBackStack() }, onSignedOut = onSignedOut)
    screenTimeScreen(onBack = { navController.popBackStack() })
    contentPreferencesScreen(onBack = { navController.popBackStack() })
    // Recently deleted: the 30-day restore window for soft-deleted posts.
    recentlyDeletedScreen(onBack = { navController.popBackStack() })
}

/** The bar's slide-and-fade for a screen's hide request; matched by the reel's own chrome. */
private const val BAR_ANIM_MILLIS = 200

/** Host for [UsNavHost] that observes the session and rebuilds on change. */
@Composable
fun UsApp(
    viewModel: MainViewModel,
    pool: PlayerPool,
    onOpenPaymentSheet: (attempt: PaymentAttempt, orderNumber: String) -> Unit = { _, _ -> },
    onAbandonPaymentSheet: (attempt: PaymentAttempt) -> Unit = { _ -> },
) {
    val sessionState by viewModel.sessionState.collectAsStateWithLifecycle()
    val shellState by viewModel.shellState.collectAsStateWithLifecycle()
    val pushDestination by viewModel.pushDestination.collectAsStateWithLifecycle()
    val callState by viewModel.callState.collectAsStateWithLifecycle()
    UsNavHost(
        sessionState = sessionState,
        shellState = shellState,
        pool = pool,
        pushDestination = pushDestination,
        onPushDestinationConsumed = viewModel::consumePushDestination,
        callState = callState,
        onOpenPaymentSheet = onOpenPaymentSheet,
        onAbandonPaymentSheet = onAbandonPaymentSheet,
    )
}

@Preview(name = "Splash", showBackground = true, backgroundColor = 0xFF000000)
@Composable
private fun SplashPreview() {
    UsTheme { SplashScreen() }
}

/**
 * Maps a notification target to a destination — Slice D.
 *
 * `:feature:notifications` hands back a resolved [NotificationTarget], never a
 * URL, and this is the only place that knows which destination each one means.
 * That is what stops the inbox importing `:feature:post` or `:feature:profile`.
 *
 * A comment notification opens the POST rather than the comments sheet: the
 * sheet is a modal over the post, so the post is where a deep link has to land
 * for Back to behave. Focusing the individual comment is tracked follow-up
 * work — the id is carried on the target, nothing yet consumes it.
 *
 * [NotificationTarget.None] navigates nowhere. It is the vertical this build
 * has no screen for, or a deep link that did not parse; either way, doing
 * nothing is the only honest option.
 */
private fun NavHostController.openNotificationTarget(target: NotificationTarget) {
    when (target) {
        is NotificationTarget.Post -> navigateToPost(target.postId)
        is NotificationTarget.PostComment -> navigateToPost(target.postId)
        is NotificationTarget.Profile -> navigateToProfile(target.userId)
        is NotificationTarget.Conversation -> navigateToChatThread(target.conversationId, title = "")
        is NotificationTarget.MessageRequest -> navigateToChatRequest(target.conversationId, target.title)
        NotificationTarget.None -> Unit
    }
}

/**
 * A tile of a profile's media grid (2026-09-05): a long video plays in
 * Tube's watch screen, the surface built for it; anything else opens as
 * a post.
 */
private fun NavHostController.openProfilePost(postId: String, contentType: String) {
    if (contentType == LONG_VIDEO_CONTENT_TYPE) navigateToWatch(postId) else navigateToPost(postId)
}

private const val LONG_VIDEO_CONTENT_TYPE = "long_video"
