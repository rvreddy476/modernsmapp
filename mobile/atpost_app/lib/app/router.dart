import 'package:feature_commerce/commerce_routes.dart';
import 'package:feature_billpay/billpay_routes.dart';
import 'package:feature_wallet/wallet_routes.dart';
import 'dart:async';

// Communities feature disabled — consolidated into Groups ("MySpace").
// Screens kept on disk; no routes reference them.
// import 'package:atpost_app/features/communities/communities_list_screen.dart';
// import 'package:atpost_app/features/communities/community_detail_screen.dart';
// import 'package:atpost_app/features/communities/community_space_screen.dart';
// import 'package:atpost_app/features/communities/create_community_screen.dart';
import 'package:atpost_app/features/auth/forgot_password_screen.dart';
import 'package:atpost_app/features/auth/anomaly_stepup_screen.dart';
import 'package:atpost_app/features/auth/login_screen.dart';
import 'package:atpost_app/features/auth/otp_verify_screen.dart';
import 'package:atpost_app/features/auth/register_screen.dart';
import 'package:feature_bookmarks/bookmarks_routes.dart';
import 'package:feature_channels/channels_routes.dart';
import 'package:feature_pages/pages_routes.dart';
import 'package:feature_shop/shop_routes.dart';
import 'package:feature_mini_apps/mini_apps_routes.dart';
import 'package:feature_create/create_routes.dart';
import 'package:feature_comments/comments_routes.dart';
import 'package:feature_discover/discover_routes.dart';
import 'package:feature_hashtag/hashtag_routes.dart';
import 'package:feature_figo/figo_routes.dart';
import 'package:feature_groups/groups_routes.dart';
import 'package:feature_monetization/monetization_routes.dart';
import 'package:atpost_app/features/shell/search_tab.dart';
import 'package:feature_search/search_routes.dart';
import 'package:feature_reviewer/reviewer_routes.dart';
import 'package:feature_services/services_routes.dart';
import 'package:atpost_app/features/chat/chat_detail_screen.dart';
import 'package:atpost_app/features/chat/chat_list_screen.dart';
import 'package:atpost_app/features/chat/message_requests_screen.dart';
import 'package:atpost_app/features/calls/call_screen.dart';
// Live streaming v2 (LiveKit / live-service-v2). Routed under /live/v2/*
// so the legacy v1 screens stay reachable during the gateway cutover.
import 'package:feature_live/live_routes.dart';
import 'package:feature_notifications/notifications_routes.dart';
import 'package:feature_profile/profile_routes.dart';
import 'package:feature_social/social_routes.dart';
import 'package:feature_reels/reels_routes.dart';
import 'package:feature_posttube/posttube_routes.dart';
import 'package:feature_qa/qa_routes.dart';
import 'package:feature_memories/memories_routes.dart';
import 'package:feature_stories/stories_routes.dart';
import 'package:feature_mopedu/mopedu_routes.dart';
import 'package:feature_pulse/pulse_routes.dart';
import 'package:feature_settings/settings_routes.dart';
import 'package:atpost_app/features/shell/shell_scaffold.dart';
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
          ...pagesRoutes(),
          ...posttubeRoutes(),
          ...reelsRoutes(),
          ...createRoutes(),
          ...commentsRoutes(),
          ...shopRoutes(),
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
          ...liveRoutes(),
          ...profileRoutes(),
          ...notificationsRoutes(),
          ...socialRoutes(),
          ...settingsRoutes(),
          ...servicesRoutes(),
          ...miniAppsRoutes(),
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
          ...bookmarksRoutes(),
          ...discoverRoutes(),
          ...hashtagRoutes(),
          ...searchRoutes(),
          // The unified SearchTab is a shell surface (embeds many feature
          // tabs), so its route stays app-side.
          GoRoute(
            path: '/search/explore',
            builder: (context, state) => const SearchTab(),
          ),
          ...reviewerRoutes(),
          ...channelsRoutes(),
          ...groupsRoutes(),
          ...monetizationRoutes(),
          ...qaRoutes(),
        ],
      ),
    ],
  );
});
