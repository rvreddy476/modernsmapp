import 'package:feature_commerce/commerce_routes.dart';
import 'package:feature_billpay/billpay_routes.dart';
import 'package:feature_wallet/wallet_routes.dart';
import 'dart:async';

import 'package:atpost_app/features/channels/channels_list_screen.dart';
import 'package:atpost_app/features/channels/channel_detail_screen.dart';
import 'package:atpost_app/features/channels/create_channel_screen.dart';
// Communities feature disabled — consolidated into Groups ("MySpace").
// Screens kept on disk; no routes reference them.
// import 'package:atpost_app/features/communities/communities_list_screen.dart';
// import 'package:atpost_app/features/communities/community_detail_screen.dart';
// import 'package:atpost_app/features/communities/community_space_screen.dart';
// import 'package:atpost_app/features/communities/create_community_screen.dart';
import 'package:atpost_app/features/auth/forgot_password_screen.dart';
import 'package:atpost_app/features/create/upload_progress_screen.dart';
import 'package:atpost_app/features/auth/anomaly_stepup_screen.dart';
import 'package:atpost_app/features/auth/login_screen.dart';
import 'package:atpost_app/features/auth/otp_verify_screen.dart';
import 'package:atpost_app/features/auth/register_screen.dart';
import 'package:atpost_app/features/bookmarks/bookmarks_screen.dart';
import 'package:atpost_app/features/comments/comments_screen.dart';
import 'package:atpost_app/features/create/create_post_screen.dart';
import 'package:atpost_app/features/create/reels_caption_screen.dart';
import 'package:atpost_app/features/create/reels_editor_screen.dart';
import 'package:atpost_app/features/discover/discover_screen.dart';
import 'package:feature_figo/figo_routes.dart';
import 'package:atpost_app/features/groups/group_admin_screen.dart';
import 'package:atpost_app/features/hashtag/hashtag_screen.dart';
import 'package:atpost_app/features/groups/group_detail_screen.dart';
import 'package:atpost_app/features/groups/group_post_composer_screen.dart';
import 'package:atpost_app/features/groups/groups_list_screen.dart';
import 'package:atpost_app/features/groups/create_group_screen.dart';
import 'package:atpost_app/features/pages/pages_list_screen.dart';
import 'package:atpost_app/features/pages/page_detail_screen.dart';
import 'package:atpost_app/features/pages/create_page_screen.dart';
import 'package:atpost_app/features/monetization/creator_analytics_screen.dart';
import 'package:atpost_app/features/monetization/monetization_dashboard_screen.dart';
import 'package:atpost_app/features/monetization/payouts_screen.dart';
import 'package:atpost_app/features/monetization/subscription_tiers_screen.dart';
import 'package:atpost_app/features/search/search_results_screen.dart';
import 'package:atpost_app/features/shell/search_tab.dart';
import 'package:atpost_app/features/search/video_search_screen.dart';
import 'package:atpost_app/features/reviewer/reviewer_console_screen.dart';
import 'package:atpost_app/features/reviewer/reviewer_dashboard_screen.dart';
import 'package:atpost_app/features/reviewer/needs_changes_screen.dart';
import 'package:atpost_app/features/services/service_slug_router.dart';
import 'package:atpost_app/features/services/services_screen.dart';
import 'package:atpost_app/features/chat/chat_detail_screen.dart';
import 'package:atpost_app/features/chat/chat_list_screen.dart';
import 'package:atpost_app/features/chat/message_requests_screen.dart';
import 'package:atpost_app/features/calls/call_screen.dart';
import 'package:atpost_app/features/live/live_screen.dart';
import 'package:atpost_app/features/live/broadcast_screen.dart';
// Live streaming v2 (LiveKit / live-service-v2). Routed under /live/v2/*
// so the legacy v1 screens stay reachable during the gateway cutover.
import 'package:atpost_app/features/live/live_list_screen.dart';
import 'package:atpost_app/features/live/live_viewer_screen.dart';
import 'package:atpost_app/features/live/go_live_screen.dart';
import 'package:atpost_app/features/live/live_broadcaster_screen.dart';
import 'package:atpost_app/features/notifications/notifications_screen.dart';
import 'package:atpost_app/features/profile/my_media_screen.dart';
import 'package:atpost_app/features/profile/profile_detail_screen.dart';
import 'package:atpost_app/features/social/followers_screen.dart';
import 'package:atpost_app/features/social/following_screen.dart';
import 'package:atpost_app/features/social/friend_requests_screen.dart';
import 'package:atpost_app/features/social/friends_screen.dart';
import 'package:feature_reels/reels_routes.dart';
import 'package:feature_posttube/posttube_routes.dart';
import 'package:atpost_app/features/qa/ask_question_screen.dart';
import 'package:atpost_app/features/qa/drafts_screen.dart';
import 'package:atpost_app/features/qa/qa_feed_screen.dart';
import 'package:atpost_app/features/qa/qa_profile_screen.dart';
import 'package:atpost_app/features/qa/qa_search_screen.dart';
import 'package:atpost_app/features/qa/question_detail_screen.dart';
import 'package:feature_memories/memories_routes.dart';
import 'package:feature_stories/stories_routes.dart';
import 'package:feature_mopedu/mopedu_routes.dart';
import 'package:feature_pulse/pulse_routes.dart';
import 'package:atpost_app/features/mini_apps/mini_apps_screen.dart';
import 'package:atpost_app/features/mini_apps/mini_app_detail_screen.dart';
import 'package:atpost_app/features/mini_apps/mini_app_sandbox_screen.dart';
import 'package:atpost_app/features/settings/data_saver_screen.dart';
import 'package:atpost_app/features/settings/edit_profile_screen.dart';
import 'package:atpost_app/features/settings/notification_settings_screen.dart';
import 'package:atpost_app/features/settings/privacy_settings_screen.dart';
import 'package:atpost_app/features/settings/security_settings_screen.dart';
import 'package:atpost_app/features/settings/settings_screen.dart';
import 'package:atpost_app/features/settings/verification_screen.dart';
import 'package:atpost_app/features/settings/wellbeing_settings_screen.dart';
import 'package:atpost_app/features/shell/shell_scaffold.dart';
import 'package:atpost_app/features/shop/shop_screen.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/call_service.dart';
import 'package:atpost_core/utils/app_logger.dart';
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Auth routes that don't require login.
const _publicPaths = {
  '/login',
  '/register',
  '/forgot-password',
  '/verify-otp',
  '/auth/step-up', // A13 anomaly gate — user not yet authenticated.
};

/// Public path prefixes — the share token is dynamic so we can't match
/// the exact path. Every recipient of a Mopedu share-ride link is
/// expected to land here with no AtPost session at all.
///
/// `/live/v2/` covers the anonymous viewer surface for live-streaming v2
/// (`/live/v2/:streamId`) so a recipient of a stream link can land on
/// the viewer without being bounced to /login. The form route
/// `/live/v2/new` is also under this prefix but the backend still
/// rejects anonymous create-stream calls, so the broadcaster flow
/// stays gated. The "subscribe to watch" panel handles the
/// paid-stream case for unauthenticated viewers.
const _publicPathPrefixes = <String>['/mopedu/share/', '/live/v2/'];

bool _isPublicPath(String path) {
  if (_publicPaths.contains(path)) return true;
  for (final prefix in _publicPathPrefixes) {
    if (path.startsWith(prefix)) {
      // `/live/v2/` covers anonymous viewing of any live stream but
      // the broadcaster surface (`/live/v2/new`, `/live/v2/:id/broadcast`)
      // must stay behind auth — broadcasting requires a session and the
      // create-stream endpoint rejects anonymous callers anyway. Excluding
      // these here ensures an anonymous user typing /live/v2/new gets the
      // /login redirect instead of an unhelpful 401 from the backend.
      if (prefix == '/live/v2/') {
        if (path == '/live/v2/new' || path.endsWith('/broadcast')) {
          continue;
        }
      }
      return true;
    }
  }
  return false;
}

/// Splash screen shown while restoring session from secure storage.
class _SplashScreen extends StatelessWidget {
  const _SplashScreen();

  @override
  Widget build(BuildContext context) {
    return const Scaffold(body: Center(child: CircularProgressIndicator()));
  }
}

class _AuthRouterRefresh extends ChangeNotifier {
  _AuthRouterRefresh(Stream<AuthState> stream) {
    _subscription = stream.listen((_) => notifyListeners());
  }

  late final StreamSubscription<AuthState> _subscription;

  // Phase W1 — tracks in-flight redirect deadlines so dispose() can
  // cancel them. flutter_test's "no Timer pending" assertion fires
  // when secure-storage reads stall past test teardown; explicit
  // tracking gives us a clean cancel hook.
  final Set<Timer> _redirectDeadlines = <Timer>{};

  /// Awaits `future` with a hard cap of `timeout`. The underlying
  /// Timer is registered with this refresh listener so dispose() can
  /// cancel it even if the future never resolves. Returns when either
  /// the future completes or the deadline fires.
  Future<void> awaitWithDeadline(Future<void> future, Duration timeout) {
    final completer = Completer<void>();
    late final Timer timer;
    void cleanup() {
      _redirectDeadlines.remove(timer);
      timer.cancel();
    }

    timer = Timer(timeout, () {
      if (!completer.isCompleted) {
        cleanup();
        completer.completeError(
          TimeoutException('redirect deadline', timeout),
        );
      }
    });
    _redirectDeadlines.add(timer);

    future.then((_) {
      if (!completer.isCompleted) {
        cleanup();
        completer.complete();
      }
    }).catchError((Object e) {
      if (!completer.isCompleted) {
        cleanup();
        completer.completeError(e);
      }
    });

    return completer.future;
  }

  @override
  void dispose() {
    for (final t in _redirectDeadlines) {
      t.cancel();
    }
    _redirectDeadlines.clear();
    _subscription.cancel();
    super.dispose();
  }
}

/// A listener that triggers the Call UI when an incoming or outgoing call is detected.
class _CallRouteObserver extends ConsumerWidget {
  const _CallRouteObserver({required this.child});
  final Widget child;

  @override
  Widget build(BuildContext context, WidgetRef ref) {
    ref.listen(callProvider, (previous, next) {
      if (next != null && previous == null && next.state != CallState.idle) {
        GoRouter.of(context).push('/call');
      }
    });
    return child;
  }
}

/// Shared RouteObserver. Subscribers (e.g. ReelsScreen) use it via the
/// RouteAware mixin to find out when a fullscreen route gets pushed
/// on top of the shell so they can pause expensive work like video
/// playback.
final routeObserverProvider = Provider<RouteObserver<ModalRoute<void>>>(
  (_) => RouteObserver<ModalRoute<void>>(),
);

final appRouterProvider = Provider<GoRouter>((ref) {
  final authService = ref.watch(authServiceProvider);
  final refresh = _AuthRouterRefresh(authService.stateStream);
  ref.onDispose(refresh.dispose);
  final routeObserver = ref.watch(routeObserverProvider);

  return GoRouter(
    initialLocation: '/splash',
    refreshListenable: refresh,
    observers: [routeObserver],
    redirect: (context, state) async {
      final path = state.uri.path;

      try {
        // Phase W1 — manual deadline via refresh listener so the Timer
        // is cancellable when the test framework disposes us. The
        // previous `.timeout(3s)` leaked a Timer past teardown.
        await refresh.awaitWithDeadline(
          authService.sessionReady,
          const Duration(seconds: 3),
        );
      } catch (_) {
        AppLogger.warn('Router: Session restoration timed out', tag: 'Router');
      }

      final isAuthenticated = authService.isAuthenticated;
      final isPublicRoute = _isPublicPath(path);

      if (path == '/splash') return isAuthenticated ? '/' : '/login';
      if (!isAuthenticated && !isPublicRoute) return '/login';
      // Sprint 3: don't bounce authenticated users away from public-by-
      // design routes (e.g. `/mopedu/share/:token`). Keep the original
      // hop for the auth-flow allow-list only.
      if (isAuthenticated && _publicPaths.contains(path)) return '/';
      return null;
    },
    routes: [
      ShellRoute(
        builder: (context, state, child) => _CallRouteObserver(child: child),
        routes: [
          GoRoute(
            path: '/splash',
            builder: (context, state) => const _SplashScreen(),
          ),
          GoRoute(
            path: '/',
            builder: (context, state) => const ShellScaffold(),
          ),
          // Shell entry points for the four real tabs. The center "Create"
          // tab is a FAB-driven sheet, not a route. These shell routes share
          // a single ShellScaffold; the `initialTab` parameter just hops the
          // tab provider on first build so deep links land on the right
          // surface.
          GoRoute(
            path: '/friends-tab',
            builder: (_, _) =>
                const ShellScaffold(initialTab: ShellTabIndex.friends),
          ),
          GoRoute(
            path: '/reels-tab',
            builder: (_, _) =>
                const ShellScaffold(initialTab: ShellTabIndex.reels),
          ),
          GoRoute(
            path: '/explore',
            builder: (_, _) =>
                const ShellScaffold(initialTab: ShellTabIndex.explore),
          ),
          // Legacy redirects: /search and /me are no longer tabs (search
          // lives in the home top-bar; profile is reached via avatar tap →
          // /profile/{id}). /inbox folded into /notifications.
          GoRoute(path: '/search', redirect: (_, _) => '/'),
          GoRoute(path: '/inbox', redirect: (_, _) => '/notifications'),
          GoRoute(path: '/me', redirect: (_, _) => '/'),
          GoRoute(
            path: '/login',
            builder: (context, state) => const LoginScreen(),
          ),
          GoRoute(
            path: '/register',
            builder: (context, state) => const RegisterScreen(),
          ),
          GoRoute(
            path: '/forgot-password',
            builder: (context, state) => const ForgotPasswordScreen(),
          ),
          GoRoute(
            path: '/verify-otp',
            builder: (context, state) => OtpVerifyScreen(
              identifier: state.uri.queryParameters['id'] ?? '',
              mode: state.uri.queryParameters['mode'] ?? 'login',
            ),
          ),
          // A13 anomaly step-up. Reached when login returns
          // requires_step_up; carries the one-shot pending_token plus
          // the methods the server allows for this account.
          GoRoute(
            path: '/auth/step-up',
            builder: (context, state) => AnomalyStepUpScreen(
              pendingToken: state.uri.queryParameters['token'] ?? '',
              methods: (state.uri.queryParameters['methods'] ?? '')
                  .split(',')
                  .where((s) => s.isNotEmpty)
                  .toList(),
            ),
          ),
          GoRoute(
            path: '/chat',
            builder: (context, state) => const ChatListScreen(),
          ),
          GoRoute(
            path: '/chat/requests',
            builder: (context, state) => const MessageRequestsScreen(),
          ),
          GoRoute(
            path: '/chat/:conversationId',
            builder: (context, state) => ChatDetailScreen(
              conversationId:
                  state.pathParameters['conversationId'] ?? 'general',
            ),
          ),
          GoRoute(
            path: '/call',
            builder: (context, state) => const CallScreen(),
          ),
          // Follow-Only Public Pages
          GoRoute(
            path: '/pages',
            builder: (context, state) => const PagesListScreen(),
          ),
          GoRoute(
            path: '/pages/create',
            builder: (context, state) => const CreatePageScreen(),
          ),
          GoRoute(
            path: '/page/:handle',
            builder: (context, state) => PageDetailScreen(
              handle: state.pathParameters['handle'] ?? '',
            ),
          ),
          ...posttubeRoutes(),
          ...reelsRoutes(),
          GoRoute(
            path: '/reels/editor',
            builder: (context, state) => const ReelsEditorScreen(),
          ),
          GoRoute(
            path: '/reels/caption',
            builder: (context, state) => const ReelsCaptionScreen(),
          ),
          // Brand sweep 2026-04-30: legacy /flicks/* paths redirect to /reels/*
          // for 30 days while clients on older builds finish rolling forward.
          GoRoute(
            path: '/flicks/editor',
            redirect: (_, _) => '/reels/editor',
          ),
          GoRoute(
            path: '/flicks/caption',
            redirect: (_, _) => '/reels/caption',
          ),
          GoRoute(
            path: '/create',
            builder: (context, state) => const CreatePostScreen(),
          ),
          GoRoute(
            path: '/comments/:postId',
            builder: (context, state) =>
                CommentsScreen(postId: state.pathParameters['postId']!),
          ),
          GoRoute(
            path: '/shop',
            builder: (context, state) => const ShopScreen(),
          ),
          // Commerce (buyer / seller / RFQ / legacy orders) — feature owns routes.
          ...commerceRoutes(),
          // FiGo (food) — the feature package owns its route table.
          ...figoRoutes(),
          // Phase 2 Sprint 1 — consumer wallet (BC of partner-bank PPI).
          ...walletRoutes(),
          // Phase 2 — Bill-pay (BBPS via Setu, decision §D2).
          ...billpayRoutes(),
          // Pulse (dating) — the feature package owns its route table.
          ...pulseRoutes(),
          // Memories (slambooks) — the feature package owns its route table.
          ...memoriesRoutes(),
          GoRoute(
            path: '/live',
            builder: (context, state) => const LiveScreen(),
          ),
          GoRoute(
            path: '/live/broadcast/:streamId',
            builder: (context, state) => BroadcastScreen(
              streamId: state.pathParameters['streamId']!,
              title: state.uri.queryParameters['title'] ?? 'Live Stream',
            ),
          ),
          // Live streaming v2 (LiveKit / live-service-v2).
          GoRoute(
            path: '/live/v2',
            builder: (_, _) => const LiveListScreen(),
          ),
          GoRoute(
            path: '/live/v2/new',
            builder: (_, _) => const GoLiveScreen(),
          ),
          GoRoute(
            path: '/live/v2/:streamId',
            builder: (context, state) => LiveViewerScreen(
              streamId: state.pathParameters['streamId']!,
            ),
          ),
          GoRoute(
            path: '/live/v2/:streamId/broadcast',
            builder: (context, state) => LiveBroadcasterScreen(
              streamId: state.pathParameters['streamId']!,
            ),
          ),
          GoRoute(
            path: '/profile/:userId',
            builder: (context, state) => ProfileDetailScreen(
              userId: state.pathParameters['userId'] ?? '',
            ),
          ),
          GoRoute(
            path: '/notifications',
            builder: (context, state) => const NotificationsScreen(),
          ),
          GoRoute(
            path: '/followers/:userId',
            builder: (context, state) =>
                FollowersScreen(userId: state.pathParameters['userId']!),
          ),
          GoRoute(
            path: '/following/:userId',
            builder: (context, state) =>
                FollowingScreen(userId: state.pathParameters['userId']!),
          ),
          GoRoute(
            path: '/friends',
            builder: (context, state) => const FriendsScreen(),
          ),
          GoRoute(
            path: '/friend-requests',
            builder: (context, state) => const FriendRequestsScreen(),
          ),
          GoRoute(
            path: '/settings',
            builder: (context, state) => const SettingsScreen(),
          ),
          GoRoute(
            path: '/settings/profile',
            builder: (context, state) => const EditProfileScreen(),
          ),
          GoRoute(
            path: '/settings/security',
            builder: (context, state) => const SecuritySettingsScreen(),
          ),
          GoRoute(
            path: '/settings/notifications',
            builder: (context, state) => const NotificationSettingsScreen(),
          ),
          GoRoute(
            path: '/settings/privacy',
            builder: (context, state) => const PrivacySettingsScreen(),
          ),
          GoRoute(
            path: '/settings/wellbeing',
            builder: (_, _) => const WellbeingSettingsScreen(),
          ),
          GoRoute(
            path: '/settings/data-saver',
            builder: (_, _) => const DataSaverScreen(),
          ),
          GoRoute(
            path: '/settings/verification',
            builder: (_, _) => const VerificationScreen(),
          ),
          GoRoute(
            path: '/services',
            builder: (_, _) => const ServicesScreen(),
          ),
          GoRoute(
            path: '/services/:slug',
            builder: (context, state) =>
                ServiceSlugRouter(slug: state.pathParameters['slug']!),
          ),
          GoRoute(
            path: '/profile/media',
            builder: (_, _) => const MyMediaScreen(),
          ),
          GoRoute(path: '/apps', builder: (_, _) => const MiniAppsScreen()),
          GoRoute(
            path: '/apps/:id',
            builder: (context, state) =>
                MiniAppDetailScreen(appId: state.pathParameters['id']!),
          ),
          GoRoute(
            path: '/apps/sandbox/:id',
            builder: (context, state) =>
                MiniAppSandboxScreen(appId: state.pathParameters['id']!),
          ),
          // Sprint 1 — Mopedu rider mini-app (customer side).
          //
          // Sprint 5: every Mopedu user-facing surface is wrapped in
          // `MopeduGate`, which gates on the master `mopedu_enabled_master`
          // flag and the v1 city allow-list (Bengaluru / Bangalore).
          // The public shared-ride viewer (`/mopedu/share/:token`) is
          // intentionally NOT gated — recipients of share links may not
          // even have AtPost installed in a launch city.
          // Mopedu (ride-hailing) — the feature package owns its route table.
          ...mopeduRoutes(),
          // Stories — the feature package owns its route table.
          ...storiesRoutes(),
          // ── Restored routes ───────────────────────────────────────────
          // The module-wiring commit (eba4a40) rewrote this file and
          // accidentally dropped the whole pre-existing block below
          // (bookmarks/discover/hashtag/search/channels/groups/
          // monetization/orders/qa/upload/posttube). Restored verbatim
          // from the last commit that had them (e5796fb), minus the
          // four /communities routes — that feature is disabled.
          GoRoute(
            path: '/bookmarks',
            builder: (context, state) => const BookmarksScreen(),
          ),
          GoRoute(
            path: '/discover',
            builder: (context, state) => const DiscoverScreen(),
          ),
          GoRoute(
            path: '/hashtag/:tag',
            builder: (context, state) => HashtagScreen(
              tag: state.pathParameters['tag'] ?? '',
            ),
          ),
          GoRoute(
            path: '/search/results',
            builder: (context, state) => SearchResultsScreen(
              query: state.uri.queryParameters['q'] ?? '',
            ),
          ),
          GoRoute(
            path: '/search/explore',
            builder: (context, state) => const SearchTab(),
          ),
          GoRoute(
            path: '/search/videos',
            builder: (context, state) => const VideoSearchScreen(),
          ),
          GoRoute(
            path: '/reviewer',
            builder: (context, state) => const ReviewerConsoleScreen(),
          ),
          GoRoute(
            path: '/reviewer/dashboard',
            builder: (context, state) => const ReviewerDashboardScreen(),
          ),
          GoRoute(
            path: '/reviewer/feedback/:contentId',
            builder: (context, state) =>
                NeedsChangesScreen(contentId: state.pathParameters['contentId']!),
          ),
          GoRoute(
            path: '/channels',
            builder: (context, state) => const ChannelsListScreen(),
          ),
          GoRoute(
            path: '/channels/create',
            builder: (context, state) => const CreateChannelScreen(),
          ),
          GoRoute(
            path: '/channels/:channelId',
            builder: (context, state) => ChannelDetailScreen(
              channelId: state.pathParameters['channelId']!,
            ),
          ),
          GoRoute(
            path: '/groups',
            builder: (context, state) => const GroupsListScreen(),
          ),
          GoRoute(
            path: '/groups/create',
            builder: (context, state) => const CreateGroupScreen(),
          ),
          GoRoute(
            path: '/groups/:groupId',
            builder: (context, state) =>
                GroupDetailScreen(groupId: state.pathParameters['groupId']!),
          ),
          GoRoute(
            path: '/groups/:groupId/post',
            builder: (context, state) => GroupPostComposerScreen(
              groupId: state.pathParameters['groupId']!,
            ),
          ),
          GoRoute(
            path: '/groups/:groupId/admin',
            builder: (context, state) =>
                GroupAdminScreen(groupId: state.pathParameters['groupId']!),
          ),
          GoRoute(
            path: '/monetization',
            builder: (context, state) => const MonetizationDashboardScreen(),
          ),
          GoRoute(
            path: '/monetization/tiers',
            builder: (context, state) => const SubscriptionTiersScreen(),
          ),
          GoRoute(
            path: '/monetization/payouts',
            builder: (context, state) => const PayoutsScreen(),
          ),
          GoRoute(
            path: '/monetization/analytics',
            builder: (context, state) => const CreatorAnalyticsScreen(),
          ),
          GoRoute(
            path: '/qa',
            builder: (context, state) => const QAFeedScreen(),
          ),
          GoRoute(
            path: '/qa/ask',
            builder: (context, state) => const AskQuestionScreen(),
          ),
          GoRoute(
            path: '/qa/question/:questionId',
            builder: (context, state) => QuestionDetailScreen(
              questionId: state.pathParameters['questionId']!,
            ),
          ),
          GoRoute(
            path: '/qa/search',
            builder: (context, state) => QaSearchScreen(
              initialQuery: state.uri.queryParameters['q'],
              communityId: state.uri.queryParameters['community_id'],
              topicId: state.uri.queryParameters['topic_id'],
            ),
          ),
          GoRoute(
            path: '/qa/drafts',
            builder: (_, _) => const QaDraftsScreen(),
          ),
          GoRoute(
            path: '/qa/profile/:userId',
            builder: (context, state) => QaProfileScreen(
              userId: state.pathParameters['userId']!,
            ),
          ),
          GoRoute(
            path: '/upload/progress',
            builder: (context, state) {
              final extra = state.extra as Map<String, dynamic>? ?? {};
              return UploadProgressScreen(
                videoPath: extra['videoPath'] as String? ?? '',
                caption: extra['caption'] as String? ?? '',
                hashtags: List<String>.from(extra['hashtags'] as List? ?? []),
                visibility: extra['visibility'] as String? ?? 'public',
              );
            },
          ),
        ],
      ),
    ],
  );
});
