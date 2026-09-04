package com.us.android.navigation

import androidx.compose.foundation.layout.Arrangement
import androidx.compose.foundation.layout.Column
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.fillMaxSize
import androidx.compose.foundation.layout.padding
import androidx.compose.material3.MaterialTheme
import androidx.compose.material3.Scaffold
import androidx.compose.runtime.Composable
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
import com.us.android.core.designsystem.component.UsNavigationBar
import com.us.android.core.designsystem.component.UsScaffold
import com.us.android.core.designsystem.component.UsWordmark
import com.us.android.core.designsystem.component.UsWordmarkSize
import com.us.android.core.designsystem.theme.UsTheme
import com.us.android.core.media.PlayerPool
import com.us.android.core.model.NotificationTarget
import com.us.android.core.model.SessionState
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
import com.us.android.feature.chat.navigation.navigateToChatLockSettings
import com.us.android.feature.chat.navigation.navigateToChatRequest
import com.us.android.feature.chat.navigation.navigateToChatThread
import com.us.android.feature.chat.navigation.navigateToGroupCreate
import com.us.android.feature.chat.navigation.navigateToGroupInfo
import com.us.android.feature.feed.navigation.FeedRoute
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
import com.us.android.feature.post.navigation.ComposerRoute
import com.us.android.feature.post.navigation.CreateRoute
import com.us.android.feature.post.navigation.PostRoute
import com.us.android.feature.post.navigation.StudioRoute
import com.us.android.feature.post.navigation.commentsScreen
import com.us.android.feature.post.navigation.composerScreen
import com.us.android.feature.post.navigation.createHubScreen
import com.us.android.feature.post.navigation.navigateToComments
import com.us.android.feature.post.navigation.navigateToCreate
import com.us.android.feature.post.navigation.navigateToPost
import com.us.android.feature.post.navigation.navigateToStudio
import com.us.android.feature.post.navigation.postScreen
import com.us.android.feature.post.navigation.studioScreen
import com.us.android.feature.profile.navigation.DirectMessagesRoute
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
import com.us.android.feature.settings.navigation.OnboardingRoute
import com.us.android.feature.settings.navigation.accountControlScreen
import com.us.android.feature.settings.navigation.contentPreferencesScreen
import com.us.android.feature.settings.navigation.manageAccountScreen
import com.us.android.feature.settings.navigation.modulesSettingsScreen
import com.us.android.feature.settings.navigation.navigateToAccountControl
import com.us.android.feature.settings.navigation.navigateToContentPreferences
import com.us.android.feature.settings.navigation.navigateToManageAccount
import com.us.android.feature.settings.navigation.navigateToModulesSettings
import com.us.android.feature.settings.navigation.navigateToScreenTime
import com.us.android.feature.settings.navigation.onboardingScreen
import com.us.android.feature.settings.navigation.screenTimeScreen
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
 * Reels, "+", Friends, Me — with Reels present only when its module is on.
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
    navController: NavHostController = rememberNavController(),
) {
    val tabs = remember(shellState) {
        (shellState as? ShellState.Ready)?.let { TabResolver.resolve(it.prefs) }.orEmpty()
    }
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

    Scaffold(
        modifier = Modifier.fillMaxSize(),
        containerColor = MaterialTheme.colorScheme.background,
        bottomBar = {
            // Null tab means the current screen is not a top-level root — an
            // auth screen, the splash, the picker, or a pushed profile. No
            // bar there. A root that is not IN the bar — the inbox, Explore —
            // hides it too: those open from the header and leave by Back, and
            // a bar with nothing selected under them would only say "you are
            // nowhere". An empty tab list means the shell is not Ready yet.
            if (currentTab != null && currentTab in tabs) {
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
        },
    ) { shellPadding ->
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
            tabDestinations(navController, pool)
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
        com.us.android.screentime.ScreenTimeGuardHost()
    }
}

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
) {
    // The real home feed. It replaced the design-system gallery once the
    // 2026-08-17 capture returned a non-empty page and proved the item shape —
    // before that the feed could only have been built on an invented DTO.
    feedScreen(
        onOpenPost = { postId -> navController.navigateToPost(postId) },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
        onOpenMessages = { navController.navigateToTopLevel(TopLevelDestination.MESSAGES) },
        onOpenNotifications = { navController.navigateToNotifications() },
        // Search is the Explore tab until a dedicated surface exists; the
        // create action left the header for the bar's centre button.
        onOpenSearch = { navController.navigateToTopLevel(TopLevelDestination.EXPLORE) },
        onOpenHashtag = { tag -> navController.navigateToHashtagPosts(tag) },
    )

    // The Friends tab: the same feed narrowed to mutual follows. A tab root,
    // so no back arrow; its own route so the bar knows which item is lit.
    friendsFeedScreen(
        onOpenPost = { postId -> navController.navigateToPost(postId) },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
    )

    // A trending tag's posts, pushed over Home from the HashTag tab.
    hashtagPostsScreen(
        onBack = { navController.popBackStack() },
        onOpenPost = { postId -> navController.navigateToPost(postId) },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
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
    )

    // Live streaming: the hub (live now + go live), the broadcaster surface
    // and the viewer surface. live-service-v2 + LiveKit behind all three.
    liveScreens(
        onBack = { navController.popBackStack() },
        onGoLive = { navController.navigateToGoLive() },
        onWatch = { streamId -> navController.navigateToLiveWatch(streamId) },
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
        // The gallery is still reachable from Explore so the design tokens stay
        // reviewable on a real device at real density.
        DesignSystemGalleryScreen(
            onOpenOwnProfile = { navController.navigateToTopLevel(TopLevelDestination.ME) },
        )
    }
    // The reels surface. The pool is supplied by :app so its lifetime belongs
    // to the composition root rather than a composable the pager recomposes —
    // it holds decoder sessions, and reacquiring those mid-scroll is exactly
    // the stutter this surface exists to avoid.
    reelsScreen(
        pool = pool,
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
        onOpenPost = { postId -> navController.navigateToPost(postId) },
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

    profileDestinations(navController)

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
}

/** Host for [UsNavHost] that observes the session and rebuilds on change. */
@Composable
fun UsApp(viewModel: MainViewModel, pool: PlayerPool) {
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
