import 'package:atpost_app/features/auth/forgot_password_screen.dart';
import 'package:atpost_app/features/create/upload_progress_screen.dart';
import 'package:atpost_app/features/posttube/posttube_upload_screen.dart';
import 'package:atpost_app/features/auth/login_screen.dart';
import 'package:atpost_app/features/auth/otp_verify_screen.dart';
import 'package:atpost_app/features/auth/register_screen.dart';
import 'package:atpost_app/features/bookmarks/bookmarks_screen.dart';
import 'package:atpost_app/features/comments/comments_screen.dart';
import 'package:atpost_app/features/create/create_post_screen.dart';
import 'package:atpost_app/features/create/flicks_caption_screen.dart';
import 'package:atpost_app/features/create/flicks_editor_screen.dart';
import 'package:atpost_app/features/discover/discover_screen.dart';
import 'package:atpost_app/features/groups/group_detail_screen.dart';
import 'package:atpost_app/features/groups/groups_list_screen.dart';
import 'package:atpost_app/features/groups/create_group_screen.dart';
import 'package:atpost_app/features/monetization/creator_analytics_screen.dart';
import 'package:atpost_app/features/monetization/monetization_dashboard_screen.dart';
import 'package:atpost_app/features/monetization/payouts_screen.dart';
import 'package:atpost_app/features/monetization/subscription_tiers_screen.dart';
import 'package:atpost_app/features/orders/order_detail_screen.dart';
import 'package:atpost_app/features/orders/orders_screen.dart';
import 'package:atpost_app/features/search/search_results_screen.dart';
import 'package:atpost_app/features/stories/create_story_screen.dart';
import 'package:atpost_app/features/stories/story_viewer_screen.dart';
import 'package:atpost_app/features/chat/chat_detail_screen.dart';
import 'package:atpost_app/features/chat/chat_list_screen.dart';
import 'package:atpost_app/features/live/live_screen.dart';
import 'package:atpost_app/features/memories/memories_screen.dart';
import 'package:atpost_app/features/notifications/notifications_screen.dart';
import 'package:atpost_app/features/profile/profile_detail_screen.dart';
import 'package:atpost_app/features/social/followers_screen.dart';
import 'package:atpost_app/features/social/following_screen.dart';
import 'package:atpost_app/features/social/friend_requests_screen.dart';
import 'package:atpost_app/features/social/friends_screen.dart';
import 'package:atpost_app/features/posttube/posttube_screen.dart';
import 'package:atpost_app/features/reels/reels_screen.dart';
import 'package:atpost_app/features/mini_apps/mini_apps_screen.dart';
import 'package:atpost_app/features/mini_apps/mini_app_detail_screen.dart';
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
import 'package:flutter/material.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

/// Auth routes that don't require login.
const _publicPaths = {'/login', '/register', '/forgot-password', '/verify-otp'};

/// Splash screen shown while restoring session from secure storage.
class _SplashScreen extends StatelessWidget {
  const _SplashScreen();

  @override
  Widget build(BuildContext context) {
    return const Scaffold(body: Center(child: CircularProgressIndicator()));
  }
}

final appRouterProvider = Provider<GoRouter>((ref) {
  final authService = ref.watch(authServiceProvider);

  return GoRouter(
    initialLocation: '/splash',
    redirect: (context, state) async {
      final path = state.uri.path;

      // Wait for secure-storage session restore to finish
      await authService.sessionReady;

      final isAuthenticated = authService.isAuthenticated;
      final isPublicRoute = _publicPaths.contains(path);

      // Splash is only for the initial load — redirect once session is known
      if (path == '/splash') {
        return isAuthenticated ? '/' : '/login';
      }

      // Not logged in → send to login (unless already on a public route)
      if (!isAuthenticated && !isPublicRoute) return '/login';

      // Logged in but on login/register → send to home
      if (isAuthenticated && isPublicRoute) return '/';

      return null; // no redirect
    },
    routes: [
      // --- Splash (session restore) ---
      GoRoute(
        path: '/splash',
        builder: (context, state) => const _SplashScreen(),
      ),

      // --- Home ---
      GoRoute(path: '/', builder: (context, state) => const ShellScaffold()),

      // --- Auth ---
      GoRoute(path: '/login', builder: (context, state) => const LoginScreen()),
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

      // --- Chat ---
      GoRoute(
        path: '/chat',
        builder: (context, state) => const ChatListScreen(),
      ),
      GoRoute(
        path: '/chat/:conversationId',
        builder: (context, state) => ChatDetailScreen(
          conversationId: state.pathParameters['conversationId'] ?? 'general',
        ),
      ),

      // --- Content ---
      GoRoute(
        path: '/posttube',
        builder: (context, state) => const PosttubeScreen(),
      ),
      GoRoute(
        path: '/reels',
        builder: (context, state) => const ReelsScreen(fullscreenRoute: true),
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

      // --- Shop / Memories / Live ---
      GoRoute(path: '/shop', builder: (context, state) => const ShopScreen()),
      GoRoute(
        path: '/memories',
        builder: (context, state) => const MemoriesScreen(),
      ),
      GoRoute(path: '/live', builder: (context, state) => const LiveScreen()),

      // --- Profile ---
      GoRoute(
        path: '/profile/:userId',
        builder: (context, state) =>
            ProfileDetailScreen(userId: state.pathParameters['userId'] ?? ''),
      ),

      // --- Social ---
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

      // --- Settings ---
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
        path: '/settings/verification',
        builder: (_, _) => const VerificationScreen(),
      ),

      // --- Mini Apps ---
      GoRoute(
        path: '/apps',
        builder: (_, _) => const MiniAppsScreen(),
      ),
      GoRoute(
        path: '/apps/:id',
        builder: (context, state) =>
            MiniAppDetailScreen(appId: state.pathParameters['id']!),
      ),

      // --- Stories ---
      GoRoute(
        path: '/stories/create',
        builder: (context, state) => const CreateStoryScreen(),
      ),
      GoRoute(
        path: '/stories/:userId',
        builder: (context, state) =>
            StoryViewerScreen(userId: state.pathParameters['userId']!),
      ),

      // --- Bookmarks / Discover / Search ---
      GoRoute(
        path: '/bookmarks',
        builder: (context, state) => const BookmarksScreen(),
      ),
      GoRoute(
        path: '/discover',
        builder: (context, state) => const DiscoverScreen(),
      ),
      GoRoute(
        path: '/search/results',
        builder: (context, state) =>
            SearchResultsScreen(query: state.uri.queryParameters['q'] ?? ''),
      ),

      // --- Groups ---
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

      // --- Monetization ---
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

      // --- Orders ---
      GoRoute(
        path: '/orders',
        builder: (context, state) => const OrdersScreen(),
      ),
      GoRoute(
        path: '/orders/:orderId',
        builder: (context, state) =>
            OrderDetailScreen(orderId: state.pathParameters['orderId']!),
      ),

      // --- Flicks Editor ---
      GoRoute(
        path: '/flicks/editor',
        builder: (_, _) => const FlicksEditorScreen(),
      ),
      GoRoute(
        path: '/flicks/caption',
        builder: (_, _) => const FlicksCaptionScreen(),
      ),

      // --- Upload / Creative Studio ---
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
      GoRoute(
        path: '/posttube/upload',
        builder: (_, _) => const PosttubeUploadScreen(),
      ),
    ],
  );
});
