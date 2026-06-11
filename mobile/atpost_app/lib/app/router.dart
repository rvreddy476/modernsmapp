import 'dart:async';

import 'package:atpost_app/data/models/commerce.dart';
import 'package:atpost_app/features/channels/channels_list_screen.dart';
import 'package:atpost_app/features/channels/channel_detail_screen.dart';
import 'package:atpost_app/features/channels/create_channel_screen.dart';
import 'package:atpost_app/features/commerce/address_book_screen.dart';
import 'package:atpost_app/features/commerce/affiliate_redirect_screen.dart';
import 'package:atpost_app/features/commerce/address_form_screen.dart';
import 'package:atpost_app/features/commerce/cart_screen.dart';
import 'package:atpost_app/features/seller/seller_bulk_import_screen.dart';
import 'package:atpost_app/features/seller/seller_dashboard_screen.dart';
import 'package:atpost_app/features/seller/seller_earnings_screen.dart';
import 'package:atpost_app/features/seller/seller_orders_screen.dart';
import 'package:atpost_app/features/seller/seller_products_screen.dart';
import 'package:atpost_app/features/seller/seller_returns_screen.dart';
import 'package:atpost_app/features/seller/seller_variants_screen.dart';
import 'package:atpost_app/features/commerce/checkout_screen.dart';
import 'package:atpost_app/features/commerce/commerce_home_screen.dart';
import 'package:atpost_app/features/commerce/rfq/rfq_detail_screen.dart';
import 'package:atpost_app/features/commerce/rfq/rfq_list_screen.dart';
import 'package:atpost_app/features/commerce/rfq/rfq_new_screen.dart';
import 'package:atpost_app/features/commerce/commerce_order_detail_screen.dart';
import 'package:atpost_app/features/commerce/my_orders_screen.dart';
import 'package:atpost_app/features/commerce/my_returns_screen.dart';
import 'package:atpost_app/features/commerce/product_detail_screen.dart';
import 'package:atpost_app/features/commerce/product_reviews_screen.dart';
import 'package:atpost_app/features/commerce/return_detail_screen.dart';
import 'package:atpost_app/features/commerce/return_request_screen.dart';
import 'package:atpost_app/features/commerce/search_screen.dart';
import 'package:atpost_app/features/commerce/wishlist_screen.dart';
import 'package:atpost_app/features/commerce/write_review_screen.dart';
// Communities feature disabled — consolidated into Groups ("MySpace").
// Screens kept on disk; no routes reference them.
// import 'package:atpost_app/features/communities/communities_list_screen.dart';
// import 'package:atpost_app/features/communities/community_detail_screen.dart';
// import 'package:atpost_app/features/communities/community_space_screen.dart';
// import 'package:atpost_app/features/communities/create_community_screen.dart';
import 'package:atpost_app/features/auth/forgot_password_screen.dart';
import 'package:atpost_app/features/billpay/billpay_account_detail_screen.dart';
import 'package:atpost_app/features/billpay/billpay_add_account_screen.dart';
import 'package:atpost_app/features/billpay/billpay_category_screen.dart';
import 'package:atpost_app/features/billpay/billpay_home_screen.dart';
import 'package:atpost_app/features/billpay/billpay_payments_screen.dart';
import 'package:atpost_app/features/billpay/billpay_receipt_screen.dart';
import 'package:atpost_app/features/billpay/billpay_recharge_screen.dart';
import 'package:atpost_app/features/billpay/billpay_reminders_screen.dart';
import 'package:atpost_app/features/billpay/billpay_scheduled_screen.dart';
import 'package:atpost_app/features/create/upload_progress_screen.dart';
import 'package:atpost_app/features/posttube/channel_screen.dart';
import 'package:atpost_app/features/posttube/posttube_upload_screen.dart';
import 'package:atpost_app/features/posttube/subscriptions_screen.dart';
import 'package:atpost_app/features/posttube/trending_screen.dart';
import 'package:atpost_app/features/posttube/watch_history_screen.dart';
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
import 'package:atpost_app/features/figo/figo_home_screen.dart';
import 'package:atpost_app/features/figo/figo_rewards_screen.dart';
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
import 'package:atpost_app/features/orders/order_detail_screen.dart';
import 'package:atpost_app/features/orders/orders_screen.dart';
import 'package:atpost_app/features/search/search_results_screen.dart';
import 'package:atpost_app/features/services/service_slug_router.dart';
import 'package:atpost_app/features/services/services_screen.dart';
import 'package:atpost_app/features/stories/create_story_screen.dart';
import 'package:atpost_app/features/stories/story_viewer_screen.dart';
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
import 'package:atpost_app/features/memories/memories_screen.dart';
import 'package:atpost_app/features/memories/slambook_detail_screen.dart';
import 'package:atpost_app/features/memories/slambook_share_screen.dart';
import 'package:atpost_app/features/memories/slambooks_screen.dart';
import 'package:atpost_app/features/notifications/notifications_screen.dart';
import 'package:atpost_app/features/profile/my_media_screen.dart';
import 'package:atpost_app/features/profile/profile_detail_screen.dart';
import 'package:atpost_app/features/social/followers_screen.dart';
import 'package:atpost_app/features/social/following_screen.dart';
import 'package:atpost_app/features/social/friend_requests_screen.dart';
import 'package:atpost_app/features/social/friends_screen.dart';
import 'package:atpost_app/features/posttube/posttube_screen.dart';
import 'package:atpost_app/features/reels/reels_screen.dart';
import 'package:atpost_app/features/qa/ask_question_screen.dart';
import 'package:atpost_app/features/qa/drafts_screen.dart';
import 'package:atpost_app/features/qa/qa_feed_screen.dart';
import 'package:atpost_app/features/qa/qa_profile_screen.dart';
import 'package:atpost_app/features/qa/qa_search_screen.dart';
import 'package:atpost_app/features/qa/question_detail_screen.dart';
import 'package:atpost_app/features/pulse/match_inbox_screen.dart';
import 'package:atpost_app/features/pulse/pulse_chat_screen.dart';
import 'package:atpost_app/features/pulse/pulse_discover_screen.dart';
import 'package:atpost_app/features/pulse/pulse_gate.dart';
import 'package:atpost_app/features/pulse/pulse_landing_screen.dart';
import 'package:atpost_app/features/pulse/pulse_matches_screen.dart';
import 'package:atpost_app/features/pulse/pulse_onboarding_screen.dart';
import 'package:atpost_app/features/pulse/pulse_profile_screen.dart';
import 'package:atpost_app/features/pulse/onboarding/intent_picker_screen.dart';
import 'package:atpost_app/features/pulse/onboarding/tune_setup_screen.dart';
import 'package:atpost_app/features/pulse/onboarding/echoes_consent_screen.dart';
import 'package:atpost_app/features/pulse/safety/block_list_screen.dart';
import 'package:atpost_app/features/pulse/safety/reports_screen.dart';
import 'package:atpost_app/features/pulse/safety/safety_center_screen.dart';
import 'package:atpost_app/features/pulse/safety/trusted_contact_picker.dart';
import 'package:atpost_app/features/pulse/safety/vouch_inbox_screen.dart';
import 'package:atpost_app/features/pulse/safety/vouch_management_screen.dart';
import 'package:atpost_app/features/pulse/verification/aadhaar_verification_screen.dart';
import 'package:atpost_app/features/pulse/verification/selfie_verification_screen.dart';
import 'package:atpost_app/features/pulse/verification/verification_landing_screen.dart';
import 'package:atpost_app/features/pulse/premium/premium_screen.dart';
import 'package:atpost_app/features/pulse/premium/data_export_screen.dart';
import 'package:atpost_app/features/mini_apps/mini_apps_screen.dart';
import 'package:atpost_app/features/mini_apps/mini_app_detail_screen.dart';
import 'package:atpost_app/features/mini_apps/mini_app_sandbox_screen.dart';
import 'package:atpost_app/features/mopedu/booking_in_progress_screen.dart';
import 'package:atpost_app/features/mopedu/mopedu_gate.dart';
import 'package:atpost_app/features/mopedu/mopedu_home_screen.dart';
import 'package:atpost_app/features/mopedu/safety/my_complaints_screen.dart';
import 'package:atpost_app/features/mopedu/safety/safety_center_screen.dart'
    as mopedu_safety;
import 'package:atpost_app/features/mopedu/safety/shared_ride_viewer_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_dashboard_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_earnings_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_landing_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_onboarding_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_referral_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_rides_breakdown_screen.dart';
import 'package:atpost_app/features/mopedu/partner/partner_subscription_screen.dart';
import 'package:atpost_app/features/mopedu/partner/ride_navigation_screen.dart';
import 'package:atpost_app/features/mopedu/ride_history_screen.dart';
import 'package:atpost_app/features/mopedu/ride_summary_screen.dart';
import 'package:atpost_app/features/mopedu/saved_places_screen.dart';
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
import 'package:atpost_app/features/wallet/wallet_aadhaar_verification_screen.dart';
import 'package:atpost_app/features/wallet/wallet_home_screen.dart';
import 'package:atpost_app/features/wallet/wallet_kyc_screen.dart';
import 'package:atpost_app/features/wallet/wallet_send_screen.dart';
import 'package:atpost_app/features/wallet/wallet_top_up_screen.dart';
import 'package:atpost_app/features/wallet/wallet_transaction_detail_screen.dart';
import 'package:atpost_app/features/wallet/wallet_transactions_screen.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/call_service.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
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
          GoRoute(
            path: '/posttube',
            builder: (context, state) => const PosttubeScreen(),
          ),
          GoRoute(
            path: '/reels',
            builder: (context, state) =>
                const ReelsScreen(fullscreenRoute: true),
          ),
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
            redirect: (_, __) => '/reels/editor',
          ),
          GoRoute(
            path: '/flicks/caption',
            redirect: (_, __) => '/reels/caption',
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
          // Sprint 1 (commerce parity): the new `/v1/commerce/*` surface
          // lives at `/commerce`. The legacy `/shop` route stays in place
          // until shop-service callers are migrated (see COMMERCE_RECON §J).
          GoRoute(
            path: '/commerce',
            builder: (_, _) => const CommerceHomeScreen(),
          ),
          GoRoute(
            path: '/commerce/category/:slug',
            builder: (context, state) => CommerceHomeScreen(
              initialCategorySlug: state.pathParameters['slug'],
            ),
          ),
          GoRoute(
            path: '/commerce/product/:id',
            builder: (context, state) => ProductDetailScreen(
              productId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/commerce/product/:id/reviews',
            builder: (context, state) => ProductReviewsScreen(
              productId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/commerce/cart',
            builder: (_, _) => const CartScreen(),
          ),
          // In-video affiliate redirect. Mirrors the public web URL
          // /v1/commerce/affiliate/:linkId; the screen calls the
          // server endpoint with redirects disabled, captures
          // ?via= into AffiliateAttribution, then routes onward.
          GoRoute(
            path: '/commerce/affiliate/:linkId',
            builder: (context, state) => AffiliateRedirectScreen(
              linkId: state.pathParameters['linkId']!,
            ),
          ),
          GoRoute(
            path: '/commerce/checkout',
            builder: (_, _) => const CheckoutScreen(),
          ),
          GoRoute(
            path: '/commerce/addresses',
            builder: (context, state) => AddressBookScreen(
              pickerMode: state.uri.queryParameters['picker'] == '1',
            ),
          ),
          GoRoute(
            path: '/commerce/addresses/new',
            builder: (context, state) => AddressFormScreen(
              existing: state.extra is Address ? state.extra as Address : null,
            ),
          ),
          GoRoute(
            path: '/commerce/orders',
            builder: (_, _) => const MyOrdersScreen(),
          ),
          GoRoute(
            path: '/commerce/orders/:id',
            builder: (context, state) => CommerceOrderDetailScreen(
              orderId: state.pathParameters['id']!,
              justPlaced: state.uri.queryParameters['placed'] == '1',
            ),
          ),
          GoRoute(
            path: '/commerce/orders/:id/return',
            builder: (context, state) => ReturnRequestScreen(
              orderId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/commerce/returns',
            builder: (_, _) => const MyReturnsScreen(),
          ),
          GoRoute(
            path: '/commerce/returns/:id',
            builder: (context, state) => ReturnDetailScreen(
              returnId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/commerce/products/:id/review',
            builder: (context, state) {
              // The order detail screen passes seller_id +
              // order_item_id + product_title via `extra`. The backend
              // requires them to mark the review as a verified purchase.
              final extra = state.extra is Map
                  ? Map<String, dynamic>.from(state.extra as Map)
                  : <String, dynamic>{};
              return WriteReviewScreen(
                productId: state.pathParameters['id']!,
                sellerId: extra['seller_id']?.toString() ?? '',
                orderItemId: extra['order_item_id']?.toString() ?? '',
                productTitle: extra['product_title']?.toString(),
              );
            },
          ),
          GoRoute(
            path: '/commerce/wishlist',
            builder: (_, _) => const WishlistScreen(),
          ),
          GoRoute(
            path: '/commerce/search',
            builder: (context, state) => SearchScreen(
              initialQuery: state.uri.queryParameters['q'],
            ),
          ),
          // Seller surface — dashboard + product management. The "My
          // orders" tile reuses the existing customer order list
          // (sellers see their orders as a buyer would view them, with
          // the upcoming fulfillment actions following in a later
          // slice).
          GoRoute(
            path: '/seller/dashboard',
            builder: (_, _) => const SellerDashboardScreen(),
          ),
          GoRoute(
            path: '/seller/products',
            builder: (_, _) => const SellerProductsScreen(),
          ),
          GoRoute(
            path: '/seller/products/:id/variants',
            builder: (context, state) => SellerVariantsScreen(
              productId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/seller/orders',
            builder: (_, _) => const SellerOrdersScreen(),
          ),
          GoRoute(
            path: '/seller/returns',
            builder: (_, _) => const SellerReturnsScreen(),
          ),
          GoRoute(
            path: '/seller/earnings',
            builder: (_, _) => const SellerEarningsScreen(),
          ),
          GoRoute(
            path: '/seller/bulk-import',
            builder: (_, _) => const SellerBulkImportScreen(),
          ),
          // Phase F4 mobile — RFQ buyer flow.
          GoRoute(
            path: '/rfq',
            builder: (_, _) => const RFQListScreen(),
          ),
          GoRoute(
            path: '/rfq/new',
            builder: (context, state) {
              final sellerId = state.uri.queryParameters['seller_id'] ?? '';
              final variantId = state.uri.queryParameters['variant_id'] ?? '';
              return RFQNewScreen(
                sellerId: sellerId,
                variantId: variantId,
              );
            },
          ),
          GoRoute(
            path: '/rfq/:id',
            builder: (context, state) => RFQDetailScreen(
              rfqId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/figo',
            builder: (context, state) => const FigoHomeScreen(),
          ),
          GoRoute(
            path: '/figo/rewards',
            builder: (context, state) => const FigoRewardsScreen(),
          ),
          // Phase 2 Sprint 1 — consumer wallet (BC of partner-bank PPI).
          GoRoute(
            path: '/wallet',
            builder: (_, _) => const WalletHomeScreen(),
          ),
          GoRoute(
            path: '/wallet/top-up',
            builder: (_, _) => const WalletTopUpScreen(),
          ),
          GoRoute(
            path: '/wallet/send',
            builder: (context, state) {
              final extra = state.extra is Map
                  ? Map<String, dynamic>.from(state.extra as Map)
                  : null;
              return WalletSendScreen(preset: extra);
            },
          ),
          GoRoute(
            path: '/wallet/transactions',
            builder: (_, _) => const WalletTransactionsScreen(),
          ),
          GoRoute(
            path: '/wallet/transactions/:id',
            builder: (context, state) => WalletTransactionDetailScreen(
              transactionId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/wallet/kyc',
            builder: (_, _) => const WalletKycScreen(),
          ),
          GoRoute(
            path: '/wallet/kyc/aadhaar',
            builder: (_, _) => const WalletAadhaarVerificationScreen(),
          ),
          GoRoute(
            path: '/wallet/kyc/aadhaar/callback',
            builder: (context, state) => WalletAadhaarVerificationScreen(
              incomingCode: state.uri.queryParameters['code'],
              incomingState: state.uri.queryParameters['state'],
            ),
          ),
          // Phase 2 — Bill-pay (BBPS via Setu, decision §D2).
          GoRoute(
            path: '/billpay',
            builder: (_, _) => const BillPayHomeScreen(),
          ),
          GoRoute(
            path: '/billpay/category/:id',
            builder: (context, state) => BillPayCategoryScreen(
              categoryId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/billpay/add-account',
            builder: (context, state) => BillPayAddAccountScreen(
              providerId: state.uri.queryParameters['providerId'] ??
                  state.uri.queryParameters['provider'] ??
                  '',
            ),
          ),
          GoRoute(
            path: '/billpay/account/:id',
            builder: (context, state) => BillPayAccountDetailScreen(
              accountId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/billpay/recharge',
            builder: (_, _) => const BillPayRechargeScreen(),
          ),
          GoRoute(
            path: '/billpay/payments',
            builder: (_, _) => const BillPayPaymentsScreen(),
          ),
          GoRoute(
            path: '/billpay/payments/:id',
            builder: (context, state) => BillPayReceiptScreen(
              paymentId: state.pathParameters['id']!,
            ),
          ),
          GoRoute(
            path: '/billpay/reminders',
            builder: (_, _) => const BillPayRemindersScreen(),
          ),
          GoRoute(
            path: '/billpay/scheduled',
            builder: (_, _) => const BillPayScheduledScreen(),
          ),
          // Sprint 2: /pulse is now the orbital + list hero surface. The
          // legacy landing screen lives at /pulse/landing for any deep links.
          //
          // Sprint 6: every Pulse user-facing surface is wrapped in
          // `PulseGate`, which gates on the master `pulse_enabled_master`
          // flag and the v1 city allow-list (Bengaluru / Bangalore).
          GoRoute(
            path: '/pulse',
            builder: (context, state) =>
                const PulseGate(child: PulseDiscoverScreen()),
          ),
          GoRoute(
            path: '/pulse/landing',
            builder: (context, state) =>
                const PulseGate(child: PulseLandingScreen()),
          ),
          GoRoute(
            path: '/pulse/onboarding',
            builder: (context, state) =>
                const PulseGate(child: PulseOnboardingScreen()),
          ),
          GoRoute(
            path: '/pulse/discover',
            builder: (context, state) =>
                const PulseGate(child: PulseDiscoverScreen()),
          ),
          GoRoute(
            path: '/pulse/matches',
            builder: (context, state) => MatchInboxScreen(
              initialTab: state.uri.queryParameters['tab'],
            ),
          ),
          // Sprint 3: deep-link target for `dating.spark.matched` push —
          // opens the inbox positioned on the right tab. The S1 matches
          // surface stays accessible at /pulse/matches/legacy if anyone
          // deep-linked to it.
          GoRoute(
            path: '/pulse/matches/legacy',
            builder: (context, state) => const PulseMatchesScreen(),
          ),
          GoRoute(
            path: '/pulse/matches/:matchId',
            builder: (context, state) => MatchInboxScreen(
              initialTab: state.uri.queryParameters['tab'],
            ),
          ),
          GoRoute(
            path: '/pulse/profile',
            builder: (context, state) => const PulseProfileScreen(),
          ),
          GoRoute(
            path: '/pulse/chat/:conversationId',
            builder: (context, state) => PulseChatScreen(
              conversationId: state.pathParameters['conversationId']!,
            ),
          ),
          // Sprint 1: Pulse onboarding additions (intent -> tune -> echoes consent).
          GoRoute(
            path: '/pulse/onboarding/intent',
            builder: (context, state) => const IntentPickerScreen(),
          ),
          GoRoute(
            path: '/pulse/onboarding/tune',
            builder: (context, state) => const TuneSetupScreen(),
          ),
          GoRoute(
            path: '/pulse/onboarding/echoes',
            builder: (context, state) => const EchoesConsentScreen(),
          ),
          // Sprint 4: verification ladder.
          GoRoute(
            path: '/pulse/verification',
            builder: (_, _) => const VerificationLandingScreen(),
          ),
          GoRoute(
            path: '/pulse/verification/aadhaar',
            builder: (_, _) => const AadhaarVerificationScreen(),
          ),
          GoRoute(
            path: '/pulse/verification/aadhaar/callback',
            builder: (context, state) => AadhaarVerificationScreen(
              incomingCode: state.uri.queryParameters['code'],
              incomingState: state.uri.queryParameters['state'],
            ),
          ),
          GoRoute(
            path: '/pulse/verification/selfie',
            builder: (_, _) => const SelfieVerificationScreen(),
          ),
          // Sprint 4: safety center + sub-screens.
          GoRoute(
            path: '/pulse/safety',
            builder: (_, _) => const SafetyCenterScreen(),
          ),
          GoRoute(
            path: '/pulse/safety/vouches',
            builder: (_, _) => const VouchManagementScreen(),
          ),
          GoRoute(
            path: '/pulse/safety/vouches/inbox',
            builder: (_, _) => const VouchInboxScreen(),
          ),
          GoRoute(
            path: '/pulse/safety/trusted-contact',
            builder: (_, _) => const TrustedContactPicker(),
          ),
          GoRoute(
            path: '/pulse/safety/blocks',
            builder: (_, _) => const BlockListScreen(),
          ),
          GoRoute(
            path: '/pulse/safety/reports',
            builder: (_, _) => const MyReportsScreen(),
          ),
          // Sprint 5: Premium tier + DPDP data export.
          GoRoute(
            path: '/pulse/premium',
            builder: (_, _) => const PremiumScreen(),
          ),
          GoRoute(
            path: '/pulse/data-export',
            builder: (_, _) => const DataExportScreen(),
          ),
          // Legacy /postmatch/* redirects (30-day deprecation window from
          // Sprint 1 ship). Remove after confirming no inbound deep links.
          GoRoute(
            path: '/postmatch',
            redirect: (_, _) => '/pulse',
          ),
          GoRoute(
            path: '/postmatch/onboarding',
            redirect: (_, _) => '/pulse/onboarding',
          ),
          GoRoute(
            path: '/postmatch/discover',
            redirect: (_, _) => '/pulse/discover',
          ),
          GoRoute(
            path: '/postmatch/matches',
            redirect: (_, _) => '/pulse/matches',
          ),
          GoRoute(
            path: '/postmatch/profile',
            redirect: (_, _) => '/pulse/profile',
          ),
          GoRoute(
            path: '/postmatch/chat/:conversationId',
            redirect: (_, state) =>
                '/pulse/chat/${state.pathParameters['conversationId']}',
          ),
          GoRoute(
            path: '/memories',
            builder: (context, state) => const MemoriesScreen(),
          ),
          GoRoute(
            path: '/memories/slambooks',
            builder: (context, state) => const SlambooksScreen(),
          ),
          GoRoute(
            path: '/memories/slambooks/:slambookId',
            builder: (context, state) => SlambookDetailScreen(
              slambookId: state.pathParameters['slambookId']!,
            ),
          ),
          GoRoute(
            path: '/memories/slambooks/share/:token',
            builder: (context, state) =>
                SlambookShareScreen(shareToken: state.pathParameters['token']!),
          ),
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
          GoRoute(
            path: '/mopedu',
            builder: (_, _) => const MopeduGate(child: MopeduHomeScreen()),
          ),
          GoRoute(
            path: '/mopedu/booking/:id',
            builder: (context, state) => MopeduGate(
              child: BookingInProgressScreen(
                rideId: state.pathParameters['id']!,
              ),
            ),
          ),
          GoRoute(
            path: '/mopedu/rides',
            builder: (_, _) => const MopeduGate(child: RideHistoryScreen()),
          ),
          GoRoute(
            path: '/mopedu/rides/:id',
            builder: (context, state) => MopeduGate(
              child: RideSummaryScreen(rideId: state.pathParameters['id']!),
            ),
          ),
          GoRoute(
            path: '/mopedu/saved-places',
            builder: (_, _) => const MopeduGate(child: SavedPlacesScreen()),
          ),
          // Sprint 3 — Mopedu customer safety surfaces.
          GoRoute(
            path: '/mopedu/safety',
            builder: (_, _) => const MopeduGate(
              child: mopedu_safety.SafetyCenterScreen(),
            ),
          ),
          GoRoute(
            path: '/mopedu/complaints',
            builder: (_, _) => const MopeduGate(child: MyComplaintsScreen()),
          ),
          // Public, no-auth shared-ride viewer reached via deep link.
          // The auth interceptor still attaches a token if present, but
          // the backend ignores it for this endpoint. NOT wrapped in
          // `MopeduGate` because share-link recipients may not be in a
          // launch city — withholding the ride view would defeat the
          // whole point of the safety share flow.
          GoRoute(
            path: '/mopedu/share/:token',
            builder: (context, state) => SharedRideViewerScreen(
              token: state.pathParameters['token']!,
            ),
          ),
          // Sprint 2 — Mopedu partner side. Partner routes are gated too
          // because we are not recruiting partners in any city outside
          // the v1 allow-list. The `MopeduGate` waitlist screen is the
          // right surface for an out-of-city partner who taps "Become a
          // Mopedu Partner".
          GoRoute(
            path: '/mopedu/partner',
            builder: (_, _) => const MopeduGate(
              child: PartnerLandingScreen(),
            ),
          ),
          GoRoute(
            path: '/mopedu/partner/onboarding',
            builder: (_, _) => const MopeduGate(
              child: PartnerOnboardingScreen(),
            ),
          ),
          GoRoute(
            path: '/mopedu/partner/dashboard',
            builder: (_, _) => const MopeduGate(
              child: PartnerDashboardScreen(),
            ),
          ),
          GoRoute(
            path: '/mopedu/partner/earnings',
            builder: (_, _) => const MopeduGate(
              child: PartnerEarningsScreen(),
            ),
          ),
          GoRoute(
            path: '/mopedu/partner/subscription',
            builder: (_, _) => const MopeduGate(
              child: PartnerSubscriptionScreen(),
            ),
          ),
          // Sprint 4 — partner polish.
          GoRoute(
            path: '/mopedu/partner/rides-breakdown',
            builder: (context, state) => MopeduGate(
              child: PartnerRidesBreakdownScreen(
                period: state.uri.queryParameters['period'] ?? 'week',
              ),
            ),
          ),
          GoRoute(
            path: '/mopedu/partner/referral',
            builder: (_, _) => const MopeduGate(
              child: PartnerReferralScreen(),
            ),
          ),
          GoRoute(
            path: '/mopedu/partner/rides/:id',
            builder: (context, state) => MopeduGate(
              child: RideNavigationScreen(
                rideId: state.pathParameters['id']!,
              ),
            ),
          ),
          GoRoute(
            path: '/stories/create',
            builder: (context, state) => const CreateStoryScreen(),
          ),
          GoRoute(
            path: '/stories/:userId',
            builder: (context, state) =>
                StoryViewerScreen(userId: state.pathParameters['userId']!),
          ),
        ],
      ),
    ],
  );
});
