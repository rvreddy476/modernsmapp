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
import androidx.compose.foundation.layout.WindowInsetsSides
import androidx.compose.foundation.layout.consumeWindowInsets
import androidx.compose.foundation.layout.only
import androidx.compose.foundation.layout.plus
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
import androidx.compose.runtime.rememberCoroutineScope
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
import com.us.android.BuildConfig
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
import com.us.android.feature.auth.navigation.SignInSecurityDestinations
import com.us.android.feature.auth.navigation.navigateToChangePassword
import com.us.android.feature.auth.navigation.navigateToForgotPassword
import com.us.android.feature.auth.navigation.navigateToSignInStep
import com.us.android.feature.auth.navigation.signInSecurityScreens
import com.us.android.feature.auth.register.RegisterRoute
import com.us.android.feature.auth.verify.VerifyEmailRoute
import com.us.android.feature.call.navigation.CallRoute
import com.us.android.feature.call.navigation.callHistoryScreen
import com.us.android.feature.call.navigation.callScreen
import com.us.android.feature.call.navigation.navigateToCallHistory
import com.us.android.feature.call.navigation.navigateToCallSurface
import com.us.android.feature.call.navigation.navigateToOutgoingCall
import com.us.android.feature.chat.navigation.ChatHomeRoute
import com.us.android.feature.chat.navigation.ChatRequestRoute
import com.us.android.feature.chat.navigation.ChatThreadRoute
import com.us.android.feature.chat.navigation.CommunityDestinations
import com.us.android.feature.chat.navigation.CommunityPageRoute
import com.us.android.feature.chat.navigation.GroupCreateRoute
import com.us.android.feature.chat.navigation.JoinByLinkRoute
import com.us.android.feature.chat.navigation.chatHomeScreen
import com.us.android.feature.chat.navigation.chatInboxScreen
import com.us.android.feature.chat.navigation.chatListScreens
import com.us.android.feature.chat.navigation.chatLockSettingsScreen
import com.us.android.feature.chat.navigation.chatRequestScreen
import com.us.android.feature.chat.navigation.chatThreadScreen
import com.us.android.feature.chat.navigation.ChatParityDestinations
import com.us.android.feature.chat.navigation.ThreadParityDestinations
import com.us.android.feature.chat.navigation.chatParityScreens
import com.us.android.feature.chat.navigation.navigateToArchivedChats
import com.us.android.feature.chat.navigation.navigateToChatSettings
import com.us.android.feature.chat.navigation.navigateToNewMessage
import com.us.android.feature.chat.navigation.navigateToSharedReels
import com.us.android.feature.chat.navigation.communityScreens
import com.us.android.feature.chat.navigation.groupCreateScreen
import com.us.android.feature.chat.navigation.groupScreens
import com.us.android.feature.chat.navigation.navigateToChatHome
import com.us.android.feature.chat.navigation.navigateToChatLockSettings
import com.us.android.feature.chat.navigation.HiddenChatsDestinations
import com.us.android.feature.chat.navigation.hiddenChatsScreens
import com.us.android.feature.chat.navigation.navigateToHiddenChats
import com.us.android.feature.chat.navigation.navigateToHiddenChatsSettings
import com.us.android.feature.chat.navigation.navigateToChatRequest
import com.us.android.feature.chat.navigation.navigateToChatRequestsList
import com.us.android.feature.chat.navigation.navigateToChatThread
import com.us.android.feature.chat.navigation.navigateToCommunity
import com.us.android.feature.chat.navigation.navigateToCommunityAdmins
import com.us.android.feature.chat.navigation.navigateToCommunityCreate
import com.us.android.feature.chat.navigation.navigateToCommunityEdit
import com.us.android.feature.chat.navigation.navigateToCommunityPost
import com.us.android.feature.chat.navigation.navigateToGroupAddMembers
import com.us.android.feature.chat.navigation.navigateToGroupCreate
import com.us.android.feature.chat.navigation.navigateToGroupInfo
import com.us.android.feature.chat.navigation.navigateToInvitations
import com.us.android.feature.chat.navigation.navigateToJoinByLink
import com.us.android.feature.chat.ui.home.ChatHomeDestinations
import com.us.android.feature.commerce.navigation.mSellerScreens
import com.us.android.feature.commerce.navigation.mStoreScreens
import com.us.android.feature.commerce.navigation.navigateToMSeller
import com.us.android.feature.commerce.navigation.navigateToMStore
import com.us.android.feature.dating.navigation.datingScreens
import com.us.android.feature.dating.navigation.navigateToDating
import com.us.android.feature.dating.navigation.navigateToDatingMatch
import com.us.android.feature.dating.navigation.navigateToDatingReport
import com.us.android.feature.dating.navigation.navigateToDatingSafety
import com.us.android.feature.dating.navigation.navigateToDatingSparks
import com.us.android.feature.dating.premium.DatingPaymentRequest
import com.us.android.feature.doorstep.navigation.doorstepScreens
import com.us.android.feature.doorstep.navigation.navigateToDoorstep
import com.us.android.feature.doorstep.navigation.navigateToDoorstepBooking
import com.us.android.feature.doorstep.navigation.navigateToDoorstepOutstanding
import com.us.android.feature.doorstep.payment.DoorstepPaymentRequest
import com.us.android.feature.feast.checkout.FeastPaymentRequest
import com.us.android.feature.feast.navigation.feastScreens
import com.us.android.feature.feast.navigation.navigateToFeast
import com.us.android.feature.feed.navigation.FeedRoute
import com.us.android.feature.feed.navigation.FriendsFeedRoute
import com.us.android.feature.feed.navigation.feedScreen
import com.us.android.feature.feed.navigation.friendsFeedScreen
import com.us.android.feature.feed.navigation.followedHashtagScreens
import com.us.android.feature.feed.navigation.hashtagPostsScreen
import com.us.android.feature.feed.navigation.navigateToFollowedHashtagPosts
import com.us.android.feature.feed.navigation.navigateToFollowedHashtags
import com.us.android.feature.feed.navigation.navigateToHashtagPosts
import com.us.android.feature.feed.navigation.navigateToSound
import com.us.android.feature.feed.navigation.navigateToReelsExplore
import com.us.android.feature.feed.navigation.reelsExploreScreen
import com.us.android.feature.feed.navigation.navigateToTrendingSounds
import com.us.android.feature.feed.navigation.reelsScreen
import com.us.android.feature.feed.navigation.soundScreen
import com.us.android.feature.feed.navigation.trendingSoundsScreen
import com.us.android.feature.feed.ui.HomeMenuRow
import com.us.android.feature.live.navigation.GoLiveRoute
import com.us.android.feature.live.navigation.LiveCreatorCentreRoute
import com.us.android.feature.live.navigation.LiveHubRoute
import com.us.android.feature.live.navigation.LiveWatchRoute
import com.us.android.feature.live.navigation.liveScreens
import com.us.android.feature.live.navigation.navigateToGoLive
import com.us.android.feature.live.navigation.navigateToLiveCreatorCentre
import com.us.android.feature.live.navigation.navigateToLiveHub
import com.us.android.feature.live.navigation.navigateToLiveWatch
import com.us.android.feature.memberships.navigation.MembershipsDestinations
import com.us.android.feature.memberships.navigation.membershipScreens
import com.us.android.feature.memberships.navigation.navigateToCreatorMembers
import com.us.android.feature.memberships.navigation.navigateToCreatorMemberships
import com.us.android.feature.memberships.navigation.navigateToMembership
import com.us.android.feature.memberships.navigation.navigateToMyMemberships
import com.us.android.feature.mopedu.rider.navigation.mopeduRiderScreens
import com.us.android.feature.mopedu.rider.navigation.navigateToMopeduRider
import com.us.android.feature.mopedu.rider.payment.MopeduPaymentRequest
import com.us.android.feature.notifications.navigation.navigateToNotifications
import com.us.android.feature.notifications.navigation.notificationsScreen
import com.us.android.feature.post.createhub.CreateScope
import com.us.android.feature.post.createhub.CreateSheet
import com.us.android.feature.post.createhub.CreateSurface
import com.us.android.feature.post.navigation.CaptureRoute
import com.us.android.feature.post.navigation.ComposerRoute
import com.us.android.feature.post.navigation.captureScreen
import com.us.android.feature.post.navigation.navigateToCapture
import com.us.android.feature.post.navigation.CreateRoute
import com.us.android.feature.post.navigation.PostRoute
import com.us.android.feature.post.navigation.StudioRoute
import com.us.android.feature.post.navigation.composerScreen
import com.us.android.feature.post.navigation.createHubScreen
import com.us.android.feature.post.navigation.draftScreens
import com.us.android.feature.post.navigation.navigateToCreate
import com.us.android.feature.post.navigation.navigateToDraft
import com.us.android.feature.post.navigation.navigateToDrafts
import com.us.android.feature.post.navigation.navigateToPost
import com.us.android.feature.post.navigation.navigateToStudio
import com.us.android.feature.post.navigation.postScreen
import com.us.android.feature.post.navigation.studioScreen
import com.us.android.feature.profile.navigation.MomentumHeaderDestinations
import com.us.android.feature.profile.navigation.NotificationSettingsRoute
import com.us.android.feature.profile.navigation.PrivacySettingsRoute
import com.us.android.feature.profile.navigation.ProfileDetailsRoute
import com.us.android.feature.profile.navigation.SecuritySettingsRoute
import com.us.android.feature.profile.navigation.SettingsDestinations
import com.us.android.feature.profile.navigation.SettingsSections
import com.us.android.feature.profile.navigation.connectionsScreen
import com.us.android.feature.profile.navigation.editProfileScreen
import com.us.android.feature.profile.navigation.followListScreen
import com.us.android.feature.profile.navigation.followRequestsScreen
import com.us.android.feature.profile.navigation.navigateToConnectionRequests
import com.us.android.feature.profile.navigation.navigateToConnections
import com.us.android.feature.profile.navigation.navigateToEditProfile
import com.us.android.feature.profile.navigation.navigateToFollowList
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
import com.us.android.feature.tube.navigation.navigateToCreatorHub
import com.us.android.feature.tube.navigation.navigateToCreatorHubUploads
import com.us.android.feature.tube.navigation.navigateToCreditRequests
import com.us.android.feature.tube.navigation.navigateToTube
import com.us.android.feature.tube.navigation.navigateToTubeChannel
import com.us.android.feature.tube.navigation.navigateToTubeCollection
import com.us.android.feature.tube.navigation.navigateToTubeHistory
import com.us.android.feature.tube.navigation.navigateToTubeTopic
import com.us.android.feature.tube.navigation.navigateToTubeTopics
import com.us.android.feature.tube.navigation.navigateToTubeTrending
import com.us.android.feature.tube.navigation.navigateToTubeCollections
import com.us.android.feature.tube.navigation.navigateToTubeOffline
import com.us.android.feature.tube.navigation.navigateToTubeSaved
import com.us.android.feature.tube.navigation.navigateToTubeScheduled
import com.us.android.feature.tube.navigation.navigateToTubeTab
import com.us.android.feature.tube.navigation.navigateToWatch
import com.us.android.feature.tube.navigation.tubeScreens
import com.us.android.push.DatingPushTarget
import com.us.android.push.DoorstepPushTarget
import com.us.android.push.LiveOpen
import com.us.android.push.MEMBERSHIP_TYPES
import com.us.android.push.COPYRIGHT_TYPES
import com.us.android.push.PushDestinations
import com.us.android.push.TYPE_CREDIT_ACCEPTED
import com.us.android.push.TYPE_CREDIT_REQUEST
import com.us.android.push.TYPE_UPLOADED_FLICK
import com.us.android.push.TYPE_UPLOADED_VIDEO
import com.us.android.push.TYPE_WENT_LIVE
import com.us.android.push.liveOpenOf
import com.us.android.push.pushTargetOf
import kotlinx.coroutines.launch
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
    // A reel notification tap (2026-09-12): the shell leaves the post id in
    // ReelsEntry before the tab switch, because a tab root is restored, not
    // pushed, and carries no argument. Supplied by UsApp from MainViewModel.
    onOpenReel: (postId: String) -> Unit = {},
    // A profile or channel web link carries a HANDLE (2026-10-22); the
    // screens take a user id. Supplied by UsApp from MainViewModel; null
    // means the handle is unknown, and the host opens Search with it typed.
    resolveLinkUserId: suspend (com.us.android.push.PushDestination) -> String? = { null },
    callState: com.us.android.core.call.CallState = com.us.android.core.call.CallState.Idle,
    // Supplied by MainActivity, which IS the Activity the PSP SDK needs and
    // which holds the coordinator. Passing it down rather than looking up a
    // context here removes the "what if this is not an Activity" branch
    // entirely, and keeps :app's provider choice in one place.
    onOpenPaymentSheet: (attempt: PaymentAttempt, orderNumber: String) -> Unit = { _, _ -> },
    onAbandonPaymentSheet: (attempt: PaymentAttempt) -> Unit = { _ -> },
    // Feast's sheet, from the same Activity, stamped "feast" by :feature:feast.
    onOpenFeastPayment: (FeastPaymentRequest) -> Unit = { _ -> },
    onAbandonFeastPayment: (FeastPaymentRequest) -> Unit = { _ -> },
    // Dating Premium's sheet, from the same Activity, stamped "dating" by :feature:dating.
    onOpenDatingPayment: (DatingPaymentRequest) -> Unit = { _ -> },
    onAbandonDatingPayment: (DatingPaymentRequest) -> Unit = { _ -> },
    // Mopedu's sheet, from the same Activity, stamped "mopedu" by :feature:mopedu-rider.
    onOpenMopeduPayment: (MopeduPaymentRequest) -> Unit = { _ -> },
    onAbandonMopeduPayment: (MopeduPaymentRequest) -> Unit = { _ -> },
    // Doorstep's sheet, from the same Activity, stamped "doorstep" by :feature:doorstep.
    onOpenDoorstepPayment: (DoorstepPaymentRequest) -> Unit = { _ -> },
    onAbandonDoorstepPayment: (DoorstepPaymentRequest) -> Unit = { _ -> },
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
        navController.openPushDestination(destination, onOpenReel, resolveLinkUserId)
    }

    // The bar lives OUTSIDE the NavHost so it survives destination changes
    // rather than being recomposed away and back on every navigation.
    val backStackEntry by navController.currentBackStackEntryAsState()
    val currentTab = TopLevelDestination.forDestination(backStackEntry?.destination)

    // The Create sheet opens OVER the current tab; nothing is pushed until a
    // tile is picked. Saveable so a rotation mid-choice keeps it open. The
    // SCOPE is the state rather than a boolean: what the plus offers changes
    // with the app it was pressed in (founder, 2026-09-06) and `CreateScope`
    // is the one place that mapping lives. Null means closed.
    var createScope by rememberSaveable { mutableStateOf<CreateScope?>(null) }

    // A screen's request about the chrome — today only "hide the bar", made
    // by Reels' full mode. The shell owns the holder and the decision
    // (bottomBarVisible); the screen only asks, through the local it is
    // handed below. Screen-scoped by construction: the request is withdrawn
    // when the screen leaves, so the bar is back on every other tab.
    val chrome = remember { ChromeVisibility() }

    val liveStage = backStackEntry?.destination.isLiveStage()
    // The Create camera, the photo studio and the post viewer pad both bars themselves; any
    // shell inset showed the light ground as white bands around their dark
    // stage (2026-10-19).
    val fullBleed = backStackEntry?.destination.let {
        it?.hasRoute(CaptureRoute::class) == true || it?.hasRoute(StudioRoute::class) == true ||
            it?.hasRoute(PostRoute::class) == true
    }
    Scaffold(
        modifier = Modifier.fillMaxSize(),
        // On a LIVE screen the shell still reserves the gesture-bar band; it
        // is painted the stage colour the LIVE room uses, not the app's light
        // ground (a white strip under a dark room, 17 Oct emulator walk).
        containerColor = if (liveStage) MaterialTheme.colorScheme.scrim else MaterialTheme.colorScheme.background,
        // Reels fills the frame from the very top: the video under the status
        // bar, its translucent header padding itself beneath it. The shell
        // hands that tab no inset at all; every other screen's own scaffold
        // reserves the system bars as before, because the NavHost below
        // consumes only what it was given.
        contentWindowInsets = when (shellInsetsFor(currentTab, darkStage = liveStage, fullBleed = fullBleed)) {
            ShellInsets.NONE -> WindowInsets(0)
            ShellInsets.NO_TOP -> ScaffoldDefaults.contentWindowInsets
                .only(WindowInsetsSides.Horizontal + WindowInsetsSides.Bottom)
            ShellInsets.ALL -> ScaffoldDefaults.contentWindowInsets
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
                        onSelect = { index ->
                            val target = tabs[index]
                            // The tab already on screen: tell it rather than
                            // navigate to itself (Home refreshes, 2026-10-18).
                            if (target == currentTab) chrome.onTabReselected() else navController.navigateToTopLevel(target)
                        },
                        // Momentum's raised centre button is always Create, not a
                        // tab. It opens the Create SHEET over the current screen;
                        // while the sheet is up the "+" reads as "×" and closes it.
                        // Create is camera first (2026-10-18): the "+" opens the
                        // capture screen, whose More chip holds the Create sheet.
                        centerAction = {
                            if (createScope != null) createScope = null else navController.navigateToCapture()
                        },
                        centerActive = createScope != null,
                        // Home and Reels are full-screen video stages: the bar
                        // goes dark with them (post-screens parity, 2026-10-18).
                        stage = currentTab?.isStage == true,
                    )
                }
            }
        },
    ) { shellPadding ->
        // Tappable @mentions and #hashtags everywhere text is drawn (2026-10-24):
        // a mention resolves its handle the way a profile web link does (Search
        // with the handle when it is unknown); a hashtag opens its posts.
        val linkScope = rememberCoroutineScope()
        val richLinks = remember(navController) {
            com.us.android.core.ui.UsRichTextLinks(
                onMention = { handle ->
                    linkScope.launch {
                        navController.openPushDestination(
                            com.us.android.push.PushDestination(
                                type = com.us.android.push.PushDestinations.TYPE_PROFILE_LINK,
                                entityId = handle,
                                deepLink = "",
                            ),
                            onOpenReel,
                            resolveLinkUserId,
                        )
                    }
                },
                onHashtag = { tag -> navController.navigateToHashtagPosts(tag) },
            )
        }
        CompositionLocalProvider(
            LocalChromeVisibility provides chrome,
            com.us.android.core.ui.LocalUsRichTextLinks provides richLinks,
        ) {
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
                tabDestinations(
                    navController,
                    pool,
                    launcher,
                    onOpenPaymentSheet,
                    onAbandonPaymentSheet,
                    onOpenFeastPayment,
                    onAbandonFeastPayment,
                    onOpenDatingPayment,
                    onAbandonDatingPayment,
                    onOpenMopeduPayment,
                    onAbandonMopeduPayment,
                    onOpenDoorstepPayment,
                    onAbandonDoorstepPayment,
                    exploreShowsBack(tabs),
                    onOpenReel,
                ) {
                    createScope = it
                }
            }
        }
    }

    CreateSheetHost(scope = createScope, navController = navController, onDismiss = { createScope = null })

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
 * The Create sheet, when a "+" has asked for one.
 *
 * A tile pushes the hub opened on that surface; Go Live opens the live hub,
 * which is where the old create rail's LIVE slot went too. WHAT it offers is
 * the SCOPE's, not this function's: the shell's own plus offers everything,
 * Tube's offers a video, a reel and going live (founder, 2026-09-06). A null
 * scope is a closed sheet.
 */
@Composable
private fun CreateSheetHost(scope: CreateScope?, navController: NavHostController, onDismiss: () -> Unit) {
    if (scope == null) return
    CreateSheet(
        onPick = { surface -> navController.navigateToCreate(surface) },
        onOpenLive = { navController.navigateToLiveHub() },
        onDismiss = onDismiss,
        scope = scope,
        onOpenDrafts = { navController.navigateToDrafts() },
    )
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

/**
 * Where a notification tap lands, by push type. Unknown types route nowhere.
 * Suspends only for a profile or channel web link, whose handle is resolved
 * to a user id through [resolveLinkUserId] before the screen is opened.
 */
private suspend fun NavHostController.openPushDestination(
    destination: com.us.android.push.PushDestination,
    onOpenReel: (postId: String) -> Unit,
    resolveLinkUserId: suspend (com.us.android.push.PushDestination) -> String? = { null },
) {
    val customer = com.us.android.core.model.CustomerNotification.resolve(destination.type, destination.entityId, destination.deepLink)
    if (customer != NotificationTarget.None) {
        openNotificationTarget(customer, onOpenReel)
        return
    }
    if (com.us.android.core.model.CustomerNotification.service(destination.type) != null) return
    when (destination.type) {
        "dm" -> if (destination.entityId.isNotBlank()) {
            navigateToChatThread(destination.entityId, title = "")
        }
        // A subscribed channel's upload (2026-09-12): the same target the
        // inbox row would resolve, so the shade and the inbox agree.
        // A followed creator went live (2026-10-02): the live viewer on that
        // stream, resolved the same way.
        // Video credits (2026-10-23): a request opens the requests screen, an acceptance the video.
        TYPE_UPLOADED_VIDEO, TYPE_UPLOADED_FLICK, TYPE_WENT_LIVE, TYPE_CREDIT_REQUEST, TYPE_CREDIT_ACCEPTED ->
            openNotificationTarget(pushTargetOf(destination), onOpenReel)
        // Paid memberships (2026-10-23): the creator's page, or the membership the notice is about.
        in MEMBERSHIP_TYPES -> openNotificationTarget(pushTargetOf(destination), onOpenReel)
        // Copyright title review (2026-10-24): the uploads, or the cleared video.
        in COPYRIGHT_TYPES -> openNotificationTarget(pushTargetOf(destination), onOpenReel)
        "message_request" -> navigateToTopLevel(TopLevelDestination.MESSAGES)
        // Follow and connection requests (2026-10-24): the deep link says where —
        // `/notifications?tab=requests` the approval queue, `/u/{id}` a profile.
        in SOCIAL_REQUEST_TYPES -> openNotificationTarget(pushTargetOf(destination), onOpenReel)
        // An `atpost.app/chat/join/{code}` link, offered by MainActivity as a push
        // destination so it waits through the login like a notification tap.
        CHAT_JOIN_LINK -> if (destination.entityId.isNotBlank()) navigateToJoinByLink(destination.entityId)
        // A post's public permalink, `cleestudio.com/post/{id}` (2026-10-14): the
        // post detail, which reads the post (and its visibility) from the
        // server. The id was already checked to be a UUID by the parser.
        PushDestinations.TYPE_POST_LINK -> if (destination.entityId.isNotBlank()) navigateToPost(destination.entityId)
        // The web's other share routes (2026-10-22), each on the exact item:
        // a reel opens the Reels tab AT that reel (the id parked in ReelsEntry
        // first, like a reel notification); a long video opens Tube's watch
        // screen; a sound its page; a hashtag its posts. The ids were checked
        // by the parser; the screens read everything else from the server.
        PushDestinations.TYPE_REEL_LINK -> if (destination.entityId.isNotBlank()) {
            onOpenReel(destination.entityId)
            navigateToTopLevel(TopLevelDestination.REELS)
        }
        PushDestinations.TYPE_WATCH_LINK -> if (destination.entityId.isNotBlank()) navigateToWatch(destination.entityId)
        PushDestinations.TYPE_SOUND_LINK -> if (destination.entityId.isNotBlank()) navigateToSound(destination.entityId)
        PushDestinations.TYPE_HASHTAG_LINK -> if (destination.entityId.isNotBlank()) navigateToHashtagPosts(destination.entityId)
        // A profile or channel link names a HANDLE; the screens take a user
        // id, so it is resolved first. An unknown handle (or a failed read)
        // lands on Search with the handle typed in — the closest screen that
        // can still find the person or channel — rather than nowhere.
        PushDestinations.TYPE_PROFILE_LINK -> {
            val userId = resolveLinkUserId(destination)
            if (userId != null) navigateToProfile(userId) else navigateToSearch(SearchOrigin.HOME, destination.entityId)
        }
        PushDestinations.TYPE_CHANNEL_LINK -> {
            val userId = resolveLinkUserId(destination)
            if (userId != null) navigateToTubeChannel(userId) else navigateToSearch(SearchOrigin.VIDEO, destination.entityId)
        }
        // Call pushes: the ring tap attaches to the live call state; a
        // missed-call tap opens the history.
        "incoming_call", "incoming_video_call" -> navigateToCallSurface()
        "missed_call" -> navigateToCallHistory()
        // Dating (Wave 3): a spark opens incoming sparks; a match opens the
        // match; a message opens the match and continues into its chat.
        PushDestinations.TYPE_DATING_SPARK,
        PushDestinations.TYPE_DATING_MATCH,
        PushDestinations.TYPE_DATING_MESSAGE,
        PushDestinations.TYPE_DATING_FIRST_MESSAGE,
        PushDestinations.TYPE_DATING_SCAM_ALERT,
        PushDestinations.TYPE_DATING_DATE_CHECKIN,
        -> when (val target = PushDestinations.datingTargetOf(destination)) {
            DatingPushTarget.IncomingSparks -> navigateToDatingSparks()
            DatingPushTarget.Safety -> navigateToDatingSafety()
            is DatingPushTarget.Match ->
                navigateToDatingMatch(target.matchId, openChat = target.openChat, checkIn = target.checkIn)
            null -> Unit
        }
        // Mopedu (2026-09-18): every ride push opens the ride screen, which
        // reads the active ride (or its receipt) from the server.
        in PushDestinations.RIDE_TYPES -> navigateToMopeduRider()
        // Doorstep (2026-10-04): every customer push opens the booking (or the
        // dues screen), which reads the real state from the server.
        in PushDestinations.DOORSTEP_TYPES -> when (val target = PushDestinations.doorstepTargetOf(destination)) {
            is DoorstepPushTarget.Booking -> navigateToDoorstepBooking(target.bookingId)
            DoorstepPushTarget.Outstanding -> navigateToDoorstepOutstanding()
            null -> navigateToDoorstep()
        }
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
    val backToLogin: () -> Unit = {
        navController.navigate(LoginRoute) {
            popUpTo<LoginRoute> { inclusive = true }
        }
    }
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
            // Sign-in security (web parity, founder 21 Oct 2026): the
            // second steps are screens, not messages. A person with
            // two-step verification could not sign in on Android at all
            // before this.
            onForgotPassword = { email -> navController.navigateToForgotPassword(email) },
            onNavigate = { step -> navController.navigateToSignInStep(step) },
        )
    }
    // Two-step code, unusual sign-in, password reset, Google signup
    // completion — declared once in :feature:auth. Success stores a session
    // and the shell flips; every exit is "back to sign in" with the step
    // popped, so a used pending token never sits behind Back.
    signInSecurityScreens(
        SignInSecurityDestinations(
            onBackToLogin = backToLogin,
            // From Settings the session was just cleared: land on Login the
            // way sign-out does, with the whole tab stack behind it gone.
            onPasswordResetDone = { fromSettings ->
                if (fromSettings) {
                    navController.navigate(LoginRoute) {
                        popUpTo<FeedRoute> { inclusive = true }
                    }
                } else {
                    backToLogin()
                }
            },
            onPasswordResetCancelled = { navController.popBackStack() },
        ),
    )
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
    onOpenFeastPayment: (FeastPaymentRequest) -> Unit,
    onAbandonFeastPayment: (FeastPaymentRequest) -> Unit,
    onOpenDatingPayment: (DatingPaymentRequest) -> Unit,
    onAbandonDatingPayment: (DatingPaymentRequest) -> Unit,
    onOpenMopeduPayment: (MopeduPaymentRequest) -> Unit,
    onAbandonMopeduPayment: (MopeduPaymentRequest) -> Unit,
    onOpenDoorstepPayment: (DoorstepPaymentRequest) -> Unit,
    onAbandonDoorstepPayment: (DoorstepPaymentRequest) -> Unit,
    /** Explore wears a back arrow only when it is not a root of the bar ([exploreShowsBack]). */
    exploreHasBack: Boolean,
    /** A reel notification was tapped: the shell parks the id for Reels before the tab switch. */
    onOpenReel: (postId: String) -> Unit,
    /** A mini-app's "+" was pressed: the shell opens the Create sheet in that scope. */
    onOpenCreate: (CreateScope) -> Unit,
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
        onOpenSound = { soundId -> navController.navigateToSound(soundId) },
        // Home's More menu (2026-10-02): screens that already exist. Messages
        // is on the header itself (founder, 2026-10-19: always visible).
        onOpenMenuRow = { row ->
            when (row) {
                HomeMenuRow.FRIENDS -> navController.navigate(FriendsFeedRoute)
                HomeMenuRow.HASHTAGS -> navController.navigateToFollowedHashtagPosts()
                HomeMenuRow.LIVE -> navController.navigateToLiveHub()
                HomeMenuRow.SETTINGS -> navController.navigateToSettings()
            }
        },
    )

    // The Friends feed: the same feed narrowed to mutual follows. A root
    // (so a pushed profile over it is still "inside Friends") but no longer a
    // bar item: the Explore launcher pushes it, and Back returns there.
    friendsFeedScreen(
        onBack = { navController.popBackStack() },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
    )

    // A tag's posts, pushed over Home from a tapped #tag.
    hashtagPostsScreen(
        onBack = { navController.popBackStack() },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
    )
    // Followed hashtags (2026-10-24): the Hashtags feed (Home's More menu) and the
    // list of followed tags (the Me drawer, and the feed's # glyph).
    followedHashtagScreens(
        onBack = { navController.popBackStack() },
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
        onOpenHashtag = { tag -> navController.navigateToHashtagPosts(tag) },
        onOpenFollowedList = { navController.navigateToFollowedHashtags() },
    )

    // The Create hub — one composer, opened on the surface the Create sheet
    // chose (Text, Photo, Reel, Audio, Poll, Article). On success the created
    // post REPLACES the hub in the back stack: Back from the new post returns
    // to the feed, not to a creator whose content is already published.
    // Create, camera first (2026-10-18). Every hand-over replaces the capture
    // screen, so Back from a composer returns to where "+" was pressed.
    captureScreen(
        onClose = { navController.popBackStack() },
        onOpenSurface = { surface, video ->
            navController.navigate(CreateRoute.of(surface, video)) { popUpTo<CaptureRoute> { inclusive = true } }
        },
        onOpenStudio = { uris ->
            navController.navigate(StudioRoute(uris)) { popUpTo<CaptureRoute> { inclusive = true } }
        },
        onOpenLive = { navController.navigateToLiveHub() },
        onOpenDrafts = { navController.navigateToDrafts() },
    )

    createHubScreen(
        onClose = { navController.popBackStack() },
        onPublished = { postId ->
            navController.navigate(PostRoute(postId)) {
                popUpTo<CreateRoute> { inclusive = true }
            }
        },
        onOpenStudio = { uris, alreadyEdited -> navController.navigateToStudio(uris, alreadyEdited) },
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
        // T-30: the creator's own upcoming, past and recorded streams, read-only.
        onCreatorCentre = { navController.navigateToLiveCreatorCentre() },
        // Go live's "not yet" screen (2026-10-02). "Create a post" opens the
        // Create hub on Text; Back from it (or from the new post) returns to
        // the screen, which asks the server again. "Verify email" (and "Verify
        // phone number", should the server still send that row) opens Manage
        // account, the one screen that shows the email, the phone and their
        // state. The emailed-code screen cannot be opened from here: it and
        // resend-verification work only with the verification token that
        // register or a refused sign-in issues, and a signed-in account has none.
        onCreatePost = { navController.navigateToCreate(CreateSurface.Text) },
        onVerifyEmail = { navController.navigateToManageAccount() },
        onVerifyPhone = { navController.navigateToManageAccount() },
    )

    // Commerce — the buyer journey: catalogue → product → cart → address →
    // checkout → payment → orders, and the seller surface behind the
    // catalogue. Entered from the Explore launcher's MStore tile (MSeller
    // has its own tile and graph).
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
    // Two graphs, not one (founder, 2026-09-05): MStore is the buyer app and
    // MSeller the seller app, each with its own back stack, so Back inside one
    // never surfaces halfway up the other. `:app` resolves the two edges
    // neither graph may know: Settings, which lives in `:feature:profile`, and
    // the switch from MStore's profile menu into MSeller.
    mStoreScreens(
        navController = navController,
        onOpenPaymentSheet = onOpenPaymentSheet,
        onAbandonPaymentSheet = onAbandonPaymentSheet,
        onOpenSettings = { navController.navigateToSettings() },
        onOpenSeller = { navController.navigateToMSeller() },
    )
    mSellerScreens(navController = navController)

    // Feast — customer food ordering (A5): restaurants → menu → cart →
    // address → checkout → payment → live tracking, orders and invoices.
    // Entered from the Explore launcher's Feast tile. Like MStore, the sheet
    // opens from the Activity, so the two payment edges are supplied here.
    feastScreens(
        navController = navController,
        onOpenPayment = onOpenFeastPayment,
        onAbandonPayment = onAbandonFeastPayment,
    )

    // Dating (Wave 3): the gate (pilot allowlist → onboarding → selfie) then
    // Pulse, sparks, matches, safety, Premium and privacy. Entered from the
    // Explore launcher's Match tile. A match's chat is created server-side, so
    // Dating only hands :app a conversation id; the Premium sheet opens from
    // the Activity like Feast's.
    datingScreens(
        navController = navController,
        onOpenChat = { conversationId, title -> navController.navigateToChatThread(conversationId, title) },
        onOpenPayment = onOpenDatingPayment,
        onAbandonPayment = onAbandonDatingPayment,
        // Mechanic M9: the match screen's Voice/Video, the same edge as chat's.
        onStartCall = { peerUserId, peerName, video, conversationId ->
            navController.navigateToOutgoingCall(peerUserId, peerName, video, conversationId)
        },
    )

    // Mopedu (2026-09-18): the customer's ride — quote, book, track, OTP, pay,
    // receipt, history with the outstanding-fees sheet. Entered from the
    // Explore launcher's Ride tile. The payment sheet opens from the Activity
    // like Feast's and Dating's, stamped "mopedu".
    mopeduRiderScreens(
        navController = navController,
        onOpenPayment = onOpenMopeduPayment,
        onAbandonPayment = onAbandonMopeduPayment,
    )

    // Doorstep (2026-10-04): home services — catalogue, service options and
    // add-ons, address, slot with the 10-minute hold, checkout, bookings with
    // live status, extras, rating, rework, SOS. Entered from the Explore
    // launcher's Doorstep tile and from its pushes. The payment sheet opens
    // from the Activity like Mopedu's, stamped "doorstep".
    doorstepScreens(
        navController = navController,
        onOpenPayment = onOpenDoorstepPayment,
        onAbandonPayment = onAbandonDoorstepPayment,
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

    // The Post Studio — the multi-photo editor. Post hands the project to the
    // background worker and lands on the viewer's OWN profile, whose grid
    // shows the upload with its ring (founder, 2026-09-06) — the same contract
    // the reel and long-video surfaces have had since 2026-09-05. The studio
    // is popped inclusive, so Back from the profile never returns to an editor
    // whose work has already left it.
    studioScreen(
        onClose = { navController.popBackStack() },
        onOpenOwnProfile = {
            navController.popBackStack<StudioRoute>(inclusive = true)
            navController.navigateToTopLevel(TopLevelDestination.ME)
        },
        // Saved for later (2026-10-18): the studio gives way to the Drafts list.
        onDraftSaved = {
            navController.navigate(com.us.android.feature.post.navigation.DraftsRoute) {
                popUpTo<StudioRoute> { inclusive = true }
            }
        },
    )

    // The saved drafts and the draft editor (2026-10-18). A published draft
    // opens its post, with the list and the editor gone from under it.
    draftScreens(
        onBack = { navController.popBackStack() },
        onOpenDraft = { draftId -> navController.navigateToDraft(draftId) },
        onDraftPublished = { postId ->
            navController.navigate(PostRoute(postId)) {
                popUpTo<com.us.android.feature.post.navigation.DraftsRoute> { inclusive = true }
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
        onOpenTarget = { target -> navController.openNotificationTarget(target, onOpenReel) },
        // Preferences are a `:feature:profile` destination. The inbox asks
        // for "settings"; :app decides that means this route.
        onOpenPreferences = { navController.navigate(NotificationSettingsRoute) },
        // The Momentum Follow Requests panel opens the same approval queue
        // a profile's own entry point does.
        onOpenFollowRequests = { navController.navigateToFollowRequests() },
        // Connections (2026-10-24): the Connection Requests panel opens Connections › Requests.
        onOpenConnectionRequests = { navController.navigateToConnectionRequests() },
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
    // The one chat screen (founder, 2026-09-05): Chats | Groups |
    // Communities | Suggestions under a search pill. The MESSAGES root.
    chatHomeScreen(
        ChatHomeDestinations(
            onBack = { navController.popBackStack() },
            onOpenThread = { conversationId, title, isGroup ->
                navController.navigateToChatThread(conversationId, title, isGroup)
            },
            onOpenRequests = { navController.navigateToChatRequestsList() },
            onOpenInvitations = { navController.navigateToInvitations() },
            onCreateGroup = { navController.navigateToGroupCreate() },
            onCreateCommunity = { navController.navigateToCommunityCreate() },
            onOpenCommunity = { communityId -> navController.navigateToCommunity(communityId) },
            onJoinWithLink = { navController.navigateToJoinByLink() },
            onOpenLockSettings = { navController.navigateToChatLockSettings() },
            onOpenCallHistory = { navController.navigateToCallHistory() },
            // Chat parity (2026-10-18).
            onNewMessage = { navController.navigateToNewMessage() },
            onOpenArchived = { navController.navigateToArchivedChats() },
            // Hidden chats (24 Oct 2026).
            onOpenHiddenChats = { navController.navigateToHiddenChats() },
            onSetUpHiddenChats = { conversationId -> navController.navigateToHiddenChatsSettings(conversationId) },
        ),
    )
    // Hidden chats: the settings page and the list behind the secret key. A
    // chat hidden from inside it (setup first) closes back to Messages.
    hiddenChatsScreens(
        HiddenChatsDestinations(
            onBack = { navController.popBackStack() },
            onOpenThread = { conversationId, title, isGroup ->
                navController.navigateToChatThread(conversationId, title, isGroup)
            },
            onSettingsDone = { hidAChat ->
                if (hidAChat) navController.navigateToTopLevel(TopLevelDestination.MESSAGES) else navController.popBackStack()
            },
            onForgot = { navController.navigateToHiddenChatsSettings() },
        ),
    )
    chatParityScreens(
        ChatParityDestinations(
            onBack = { navController.popBackStack() },
            onOpenThread = { conversationId, title, isGroup ->
                navController.navigateToChatThread(conversationId, title, isGroup)
            },
            onOpenProfile = { userId -> navController.navigateToProfile(userId) },
            onOpenPost = { postId, contentType -> navController.openProfilePost(postId, contentType) },
            onOpenSharedReels = { conversationId, postId -> navController.navigateToSharedReels(conversationId, postId) },
            // Deleting a chat or blocking its person closes its settings AND its thread.
            onChatClosed = { navController.navigateToTopLevel(TopLevelDestination.MESSAGES) },
            onSetUpHiddenChats = { conversationId -> navController.navigateToHiddenChatsSettings(conversationId) },
        ),
    )
    // The 2026-08 inbox stays registered for any older entry point.
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
    chatListScreens(
        onBack = { navController.popBackStack() },
        onOpenRequest = { conversationId, title ->
            navController.navigateToChatRequest(conversationId, title)
        },
    )
    // Communities: a created or edited one lands on its page in place of
    // the form; a page that closed itself (left, deleted) returns to Messages.
    communityScreens(
        CommunityDestinations(
            onBack = { navController.popBackStack() },
            onSaved = { communityId ->
                navController.navigate(CommunityPageRoute(communityId)) {
                    popUpTo<ChatHomeRoute> { inclusive = false }
                }
            },
            onEdit = { communityId -> navController.navigateToCommunityEdit(communityId) },
            onAdmins = { communityId -> navController.navigateToCommunityAdmins(communityId) },
            onPost = { communityId -> navController.navigateToCommunityPost(communityId) },
            onClosed = { navController.navigateToTopLevel(TopLevelDestination.MESSAGES) },
        ),
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
        // Kind messages (Pulse M13): only a Pulse chat ever offers this.
        onReportSender = { senderId, messageId, name ->
            navController.navigateToDatingReport(senderId, messageId, name)
        },
        // A shared post's card opens where the profile grid opens the same post.
        onOpenPost = { postId, contentType -> navController.openProfilePost(postId, contentType) },
        parity = ThreadParityDestinations(
            onOpenChatSettings = { conversationId -> navController.navigateToChatSettings(conversationId) },
            onOpenProfile = { userId -> navController.navigateToProfile(userId) },
            onOpenSound = { soundId -> navController.navigateToSound(soundId) },
            onOpenLive = { streamId -> navController.navigateToLiveWatch(streamId) },
            onOpenSharedReels = { conversationId, postId -> navController.navigateToSharedReels(conversationId, postId) },
        ),
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
    groupScreens(
        onBack = { navController.popBackStack() },
        // Leaving a group closes its info AND its thread.
        onLeft = { navController.navigateToTopLevel(TopLevelDestination.MESSAGES) },
        onAddMembers = { conversationId -> navController.navigateToGroupAddMembers(conversationId) },
        // A join by link opens the group over Messages, the way a created group does.
        onJoined = { conversationId, title ->
            navController.navigate(ChatThreadRoute(conversationId, title, isGroup = true)) {
                popUpTo<JoinByLinkRoute> { inclusive = true }
            }
        },
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
    //
    // Original sounds (2026-09-30): a hashtag chip pushes that tag's posts, a
    // reel's sound line pushes the sound's page, and "Use this sound" opens
    // the reel create flow — the sound itself is already in SoundEntry
    // (:core:media), which the form takes when it starts, because the Create
    // route carries one token and features must not depend on each other.
    val onCreateWithSound = { navController.navigateToCreate(CreateSurface.Reel) }
    reelsScreen(
        pool = pool,
        onOpenAuthor = { authorId -> navController.navigateToProfile(authorId) },
        onOpenHashtag = { tag -> navController.navigateToHashtagPosts(tag) },
        onOpenSound = { soundId -> navController.navigateToSound(soundId) },
        onCreateWithSound = onCreateWithSound,
        // The Offline page lives in Tube's graph; Reels' header More opens it when there is no reel.
        onOpenOffline = { navController.navigateToTubeOffline() },
        // Explore (2026-10-24): the category grid, pushed over Reels.
        onOpenExplore = { navController.navigateToReelsExplore() },
    )
    // A tile opens its reel in the Reels TAB (the page has left the id in ReelsEntry).
    reelsExploreScreen(
        onBack = { navController.popBackStack() },
        onOpenReels = onOpenReels,
    )
    // A sound's page, pushed over Reels. A tile opens its reel in the Reels
    // TAB (the page has left the id in ReelsEntry, as the feeds do).
    soundScreen(
        onBack = { navController.popBackStack() },
        onOpenReels = onOpenReels,
        onCreateWithSound = onCreateWithSound,
        onOpenTrending = { navController.navigateToTrendingSounds() },
    )
    // Trending sounds (2026-10-24), pushed from a sound's page: "Use this
    // sound" is the same door — the sound is already in SoundEntry.
    trendingSoundsScreen(
        onBack = { navController.popBackStack() },
        onCreateWithSound = onCreateWithSound,
    )
    exploreDestinations(navController, launcher, exploreHasBack)

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
            // Tube's "+" (the bar's; the header's was removed 2026-10-02) opens the Create
            // sheet scoped to Tube: video, reel, live (founder, 2026-09-06).
            // It used to jump straight into the video composer, which is why
            // there was nothing to find at the top and no way to start a reel
            // from inside the video app.
            onCreateVideo = { onOpenCreate(CreateScope.Tube) },
            // The launcher itself, not "the Explore tab as it was left": the
            // tab switch restored Tube on top of it. See navigateToLauncher.
            onOpenExplore = { navController.navigateToLauncher() },
            onOpenTab = { tab -> navController.navigateToTubeTab(tab) },
            onOpenChannel = { userId -> navController.navigateToTubeChannel(userId) },
            onOpenScheduled = { navController.navigateToTubeScheduled() },
            onOpenSaved = { navController.navigateToTubeSaved() },
            onOpenCollections = { navController.navigateToTubeCollections() },
            onOpenCollection = { id -> navController.navigateToTubeCollection(id) },
            onOpenOffline = { navController.navigateToTubeOffline() },
            // T-31: the read-only Creator Hub. A comment on an ordinary post
            // opens post detail; reels and long videos go through
            // onOpenReels / onOpenVideo like every other Tube row.
            onOpenCreatorHub = { navController.navigateToCreatorHub() },
            onOpenPost = { postId -> navController.navigateToPost(postId) },
            // The hub's Sounds tab (library sounds, 2026-10-15): the same
            // door as Reels' "Use this sound" — the sound is in SoundEntry.
            onCreateWithSound = onCreateWithSound,
            // The You page's and the More sheet's History (web parity, 2026-10-22).
            onOpenHistory = { navController.navigateToTubeHistory() },
            // A credited name on the watch page (video credits, 2026-10-23): search, scoped to video.
            onSearchVideos = { query -> navController.navigateToSearch(SearchOrigin.VIDEO, query) },
            // Discovery (2026-10-24): Trending, Topics and one topic's page, inside Tube.
            onOpenTrending = { navController.navigateToTubeTrending() },
            onOpenTopics = { navController.navigateToTubeTopics() },
            onOpenTopic = { slug, label -> navController.navigateToTubeTopic(slug, label) },
        ),
    )

    // Search (founder, 2026-09-05; T-16): one page, its tabs set by the
    // header that opened it. Every row hands back an id and this is where it
    // resolves: a user opens a profile, a post the post, a reel the Reels tab
    // (the page has left the id in ReelsEntry, as the Home feed does), a
    // video the watch screen, a channel its page inside Tube, a tag its posts
    // and a sound its page.
    searchScreen(
        SearchDestinations(
            onBack = { navController.popBackStack() },
            onOpenProfile = { userId -> navController.navigateToProfile(userId) },
            onOpenPost = { postId -> navController.navigateToPost(postId) },
            onOpenReels = onOpenReels,
            onOpenVideo = { postId -> navController.navigateToWatch(postId) },
            onOpenChannel = { userId -> navController.navigateToTubeChannel(userId) },
            onOpenHashtag = { tag -> navController.navigateToHashtagPosts(tag) },
            onOpenSound = { soundId -> navController.navigateToSound(soundId) },
        ),
    )

    // The Me tab's drawer "Upload" opens the same Create sheet as the bar's centre button.
    profileDestinations(navController, onOpenUpload = { onOpenCreate(CreateScope.App) })

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
    hasBack: Boolean,
) {
    composable<ExploreRoute> {
        ExploreScreen(
            tiles = launcher,
            // A bar root has nowhere "back" to go; opened from a header it does.
            onBack = if (hasBack) ({ navController.popBackStack() }) else null,
            onSearch = { query -> navController.navigateToSearch(SearchOrigin.EXPLORE, query) },
            onOpenApp = { app ->
                when (app) {
                    LauncherApp.CHAT -> navController.navigateToChatHome()
                    LauncherApp.FRIENDS -> navController.navigate(FriendsFeedRoute)
                    LauncherApp.ALERTS -> navController.navigateToNotifications()
                    LauncherApp.LIVE -> navController.navigateToLiveHub()
                    LauncherApp.TUBE -> navController.navigateToTube()
                    // Two commerce tiles, two graphs: MStore is the buyer app
                    // and MSeller the seller app. Neither hides behind the
                    // other's header any more, and one person can open both.
                    LauncherApp.MSTORE -> navController.navigateToMStore()
                    LauncherApp.MSELLER -> navController.navigateToMSeller()
                    LauncherApp.FEAST -> navController.navigateToFeast()
                    LauncherApp.MATCH -> navController.navigateToDating()
                    LauncherApp.RIDE -> navController.navigateToMopeduRider()
                    LauncherApp.DOORSTEP -> navController.navigateToDoorstep()
                    LauncherApp.ASK -> Unit
                }
            },
        )
    }
}

/** Profile, edit-profile and settings destinations registered by the feature. */
private fun NavGraphBuilder.profileDestinations(navController: NavHostController, onOpenUpload: () -> Unit) {
    // Two registrations, one screen: the Me tab is a root with no back
    // control; a pushed profile has one.
    ownProfileScreen(
        onOpenFollowers = { navController.navigateToFollowList(it) },
        onOpenFollowing = { navController.navigateToFollowList(it) },
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
        onOpenConnections = { navController.navigateToConnections(it) },
        onOpenProfile = { userId -> navController.navigateToProfile(userId) },
        // The end of the publish journey: the upload the viewer was watching
        // on this grid is live, so carry them to where it landed with it
        // first (founder, 2026-09-06). The post id travelled ahead through
        // FeedEntry / ReelsEntry / TubeEntry — a tab root is restored, not
        // pushed, and Tube's home is a `data object` route, so neither
        // navigation can carry an argument.
        //
        // All three kinds move now. A long video used to stay put; the
        // founder asked for it to land on Tube's main page with that video
        // there, and Tube is a PUSH from this tab rather than a tab of its
        // own — so Back from it returns to the profile the viewer posted from.
        onPublished = { contentType ->
            when (contentType) {
                POST_CONTENT_TYPE -> navController.navigateToTopLevel(TopLevelDestination.HOME)
                FLICK_CONTENT_TYPE -> navController.navigateToTopLevel(TopLevelDestination.REELS)
                LONG_VIDEO_CONTENT_TYPE -> navController.navigateToTube()
                else -> Unit
            }
        },
        // T-31: the Me tab's drawer opens the read-only Creator Hub,
        // which lives in Tube's graph (pushed, so Back returns to Me).
        onOpenCreatorHub = { navController.navigateToCreatorHub() },
        // The drawer's Offline videos: the Offline page Reels and Tube open.
        onOpenOffline = { navController.navigateToTubeOffline() },
        // Video credits (2026-10-23): the drawer's Credit requests, in Tube's graph.
        onOpenCreditRequests = { navController.navigateToCreditRequests() },
        // Followed hashtags (2026-10-24): the drawer's row, the list in the feed's graph.
        onOpenFollowedHashtags = { navController.navigateToFollowedHashtags() },
        // Paid memberships (2026-10-23): the drawer's two membership rows.
        onOpenMyMemberships = { navController.navigateToMyMemberships() },
        onOpenMemberships = { navController.navigateToCreatorMemberships() },
        onOpenUpload = onOpenUpload,
    )
    // Paid memberships (2026-10-23): the creator's page, the member's list and
    // one membership. Joining happens in place, from the Join sheet the locks open.
    membershipScreens(
        MembershipsDestinations(
            onBack = { navController.popBackStack() },
            onOpenCreator = { creatorId -> navController.navigateToProfile(creatorId) },
            onOpenMembership = { subscriptionId -> navController.navigateToMembership(subscriptionId) },
            onOpenMembers = { navController.navigateToCreatorMembers() },
        ),
    )
    profileScreen(
        onOpenFollowers = { navController.navigateToFollowList(it) },
        onOpenFollowing = { navController.navigateToFollowList(it) },
        onBack = { navController.popBackStack() },
        // `:feature:profile` resolves the direct conversation through
        // `:core:chat`; :app alone knows which feature renders it.
        onOpenChat = { conversationId, title ->
            navController.navigateToChatThread(conversationId, title)
        },
        onOpenPost = { postId, contentType -> navController.openProfilePost(postId, contentType) },
        onOpenConnections = { navController.navigateToConnections(it) },
        onOpenProfile = { userId -> navController.navigateToProfile(userId) },
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
    // Connections (2026-10-24): a profile's connections, and the viewer's own
    // requests either way. A row opens that person's profile.
    connectionsScreen(
        onBack = { navController.popBackStack() },
        onOpenProfile = { userId -> navController.navigateToProfile(userId) },
    )
    // A profile's Followers / Following, opened from either count. A row
    // opens that person's profile the same way every profile link does.
    followListScreen(
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
            // DM consent: one Messages settings screen; Privacy links to it.
            onMessagesSettings = { navController.navigate(com.us.android.feature.profile.navigation.InboxSettingsRoute) },
            onSignedOut = onSignedOut,
            // Security › Change password is the reset flow with the account's
            // own email locked in (the web's card does the same); setting the
            // password signs out everywhere, this device included.
            onChangePassword = { email -> navController.navigateToChangePassword(email) },
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
                // Data & storage: the Offline page lives in Tube's graph.
                onOfflineVideos = { navController.navigateToTubeOffline() },
                // Chat parity: Settings › Inbox & messaging.
                onInboxMessaging = { navController.navigate(com.us.android.feature.profile.navigation.InboxSettingsRoute) },
                // Web parity (2026-10-22): Privacy › Blocked accounts and Content › Hidden channels.
                onBlockedAccounts = { navController.navigate(com.us.android.feature.profile.navigation.BlockedAccountsRoute) },
                onHiddenChannels = { navController.navigate(com.us.android.feature.profile.navigation.HiddenChannelsRoute) },
                // Account › Request verification (2026-10-24).
                onVerification = { navController.navigate(com.us.android.feature.profile.navigation.VerificationRoute) },
                // Hidden chats (24 Oct 2026): Privacy › Hidden chats is a :feature:chat page.
                onHiddenChats = { navController.navigateToHiddenChatsSettings() },
            ),
            versionName = BuildConfig.VERSION_NAME,
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
    onOpenFeastPayment: (FeastPaymentRequest) -> Unit = { _ -> },
    onAbandonFeastPayment: (FeastPaymentRequest) -> Unit = { _ -> },
    onOpenDatingPayment: (DatingPaymentRequest) -> Unit = { _ -> },
    onAbandonDatingPayment: (DatingPaymentRequest) -> Unit = { _ -> },
    onOpenMopeduPayment: (MopeduPaymentRequest) -> Unit = { _ -> },
    onAbandonMopeduPayment: (MopeduPaymentRequest) -> Unit = { _ -> },
    onOpenDoorstepPayment: (DoorstepPaymentRequest) -> Unit = { _ -> },
    onAbandonDoorstepPayment: (DoorstepPaymentRequest) -> Unit = { _ -> },
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
        onOpenReel = viewModel::openReel,
        resolveLinkUserId = viewModel::resolveLinkUserId,
        callState = callState,
        onOpenPaymentSheet = onOpenPaymentSheet,
        onAbandonPaymentSheet = onAbandonPaymentSheet,
        onOpenFeastPayment = onOpenFeastPayment,
        onAbandonFeastPayment = onAbandonFeastPayment,
        onOpenDatingPayment = onOpenDatingPayment,
        onAbandonDatingPayment = onAbandonDatingPayment,
        onOpenMopeduPayment = onOpenMopeduPayment,
        onAbandonMopeduPayment = onAbandonMopeduPayment,
        onOpenDoorstepPayment = onOpenDoorstepPayment,
        onAbandonDoorstepPayment = onAbandonDoorstepPayment,
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
 * for Back to behave; the comment itself opens the comments sheet on it (2026-10-24).
 *
 * [NotificationTarget.None] navigates nowhere. It is the vertical this build
 * has no screen for, or a deep link that did not parse; either way, doing
 * nothing is the only honest option.
 */
private fun NavHostController.openNotificationTarget(
    target: NotificationTarget,
    onOpenReel: (postId: String) -> Unit,
) {
    when (target) {
        is NotificationTarget.Customer -> when (target.service) {
            com.us.android.core.model.CustomerNotification.Service.HOME -> navigateToDoorstepBooking(target.id)
            com.us.android.core.model.CustomerNotification.Service.FOOD -> navigate(com.us.android.feature.feast.navigation.FeastTrackingRoute(target.id))
            com.us.android.core.model.CustomerNotification.Service.COMMERCE -> navigate(com.us.android.feature.commerce.navigation.OrderDetailRoute(target.id))
            com.us.android.core.model.CustomerNotification.Service.MOPED -> navigate(com.us.android.feature.mopedu.rider.navigation.MopeduBookingRoute(target.id))
        }
        NotificationTarget.CustomerDues -> navigateToDoorstepOutstanding()
        is NotificationTarget.Post -> navigateToPost(target.postId)
        // A comment or mention (2026-10-24): the post, its comments open on that comment.
        is NotificationTarget.PostComment -> navigateToPost(target.postId, target.commentId)
        is NotificationTarget.Profile -> navigateToProfile(target.userId)
        is NotificationTarget.Conversation -> navigateToChatThread(target.conversationId, title = "")
        is NotificationTarget.MessageRequest -> navigateToChatRequest(target.conversationId, target.title)
        // A long video plays on Tube's watch screen, the surface built for it.
        is NotificationTarget.Video -> navigateToWatch(target.postId)
        // A reel opens on the Reels tab AT that reel: the id is parked in
        // ReelsEntry first, the way the Home feed and the profile grid do it,
        // because the tab switch itself carries no argument.
        is NotificationTarget.Reel -> {
            onOpenReel(target.postId)
            navigateToTopLevel(TopLevelDestination.REELS)
        }
        is NotificationTarget.Live -> openLiveStream(target.streamId)
        NotificationTarget.CreditRequests -> navigateToCreditRequests()
        // Paid memberships (2026-10-23).
        is NotificationTarget.Membership -> navigateToMembership(target.subscriptionId)
        NotificationTarget.MyMemberships -> navigateToMyMemberships()
        // The creator's new-member notice (the only one that links here) opens the Members list.
        NotificationTarget.CreatorMemberships -> navigateToCreatorMembers()
        // Copyright title review (2026-10-24): the Creator Hub, on its Videos list.
        NotificationTarget.CreatorHubUploads -> navigateToCreatorHubUploads()
        // Follow requests (2026-10-24): the private account's approval queue.
        NotificationTarget.FollowRequests -> navigateToFollowRequests()
        NotificationTarget.None -> Unit
    }
}

/**
 * Opens the live viewer from a notification (2026-10-02). Whether that is a
 * push, a replace or nothing at all is [liveOpenOf]'s decision, from the
 * stream (if any) the viewer is on right now.
 */
private fun NavHostController.openLiveStream(streamId: String) {
    val watching = currentBackStackEntry
        ?.takeIf { it.destination.hasRoute<LiveWatchRoute>() }
        ?.toRoute<LiveWatchRoute>()
        ?.streamId
    when (liveOpenOf(watching = watching, streamId = streamId)) {
        LiveOpen.STAY -> Unit
        LiveOpen.REPLACE -> navigate(LiveWatchRoute(streamId)) { popUpTo<LiveWatchRoute> { inclusive = true } }
        LiveOpen.PUSH -> navigateToLiveWatch(streamId)
    }
}

/**
 * A tile of a profile's media grid (2026-09-05): a long video plays in
 * Tube's watch screen, the surface built for it; anything else opens as
 * a post.
 */
/** The LIVE screens: dark edge to edge, so the shell leaves the status bar to them ([ShellInsets.NO_TOP]). */
private fun androidx.navigation.NavDestination?.isLiveStage(): Boolean {
    val destination = this ?: return false
    return destination.hasRoute(LiveHubRoute::class) ||
        destination.hasRoute(GoLiveRoute::class) ||
        destination.hasRoute(LiveWatchRoute::class) ||
        destination.hasRoute(LiveCreatorCentreRoute::class)
}

private fun NavHostController.openProfilePost(postId: String, contentType: String) {
    if (contentType == LONG_VIDEO_CONTENT_TYPE) navigateToWatch(postId) else navigateToPost(postId)
}

private const val LONG_VIDEO_CONTENT_TYPE = "long_video"

/** An ordinary photo/text post; it lives on the Home feed. */
private const val POST_CONTENT_TYPE = "post"

/** A reel; it lives on the Reels tab. */
private const val FLICK_CONTENT_TYPE = "flick"

/**
 * The follow- and connection-request pushes (notification-service
 * follow_request_notify.go:29-30 and consumer.go:286-311), each routed by its
 * deep link through [pushTargetOf].
 */
private val SOCIAL_REQUEST_TYPES = setOf("follow_request", "follow_request_accepted", "friend_request", "friend_accepted")

/** The push-destination type MainActivity mints for an `atpost.app/chat/join/{code}` link. */
private const val CHAT_JOIN_LINK = com.us.android.push.PushDestinations.TYPE_CHAT_JOIN
