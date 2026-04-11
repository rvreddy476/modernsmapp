import 'dart:async';

import 'package:atpost_app/features/channels/channels_list_screen.dart';
import 'package:atpost_app/features/channels/channel_detail_screen.dart';
import 'package:atpost_app/features/channels/create_channel_screen.dart';
import 'package:atpost_app/features/communities/communities_list_screen.dart';
import 'package:atpost_app/features/communities/community_detail_screen.dart';
import 'package:atpost_app/features/communities/community_space_screen.dart';
import 'package:atpost_app/features/communities/create_community_screen.dart';
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
import 'package:atpost_app/features/groups/group_admin_screen.dart';
import 'package:atpost_app/features/groups/group_detail_screen.dart';
import 'package:atpost_app/features/groups/group_post_composer_screen.dart';
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
import 'package:atpost_app/features/calls/call_screen.dart';
import 'package:atpost_app/features/live/live_screen.dart';
import 'package:atpost_app/features/live/broadcast_screen.dart';
import 'package:atpost_app/features/memories/memories_screen.dart';
import 'package:atpost_app/features/memories/slambook_detail_screen.dart';
import 'package:atpost_app/features/memories/slambook_share_screen.dart';
import 'package:atpost_app/features/memories/slambooks_screen.dart';
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
import 'package:atpost_app/features/mini_apps/mini_app_sandbox_screen.dart';
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

class _AuthRouterRefresh extends ChangeNotifier {
  _AuthRouterRefresh(Stream<AuthState> stream) {
    _subscription = stream.listen((_) => notifyListeners());
  }

  late final StreamSubscription<AuthState> _subscription;

  @override
  void dispose() {
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

final appRouterProvider = Provider<GoRouter>((ref) {
  final authService = ref.watch(authServiceProvider);
  final refresh = _AuthRouterRefresh(authService.stateStream);
  ref.onDispose(refresh.dispose);

  return GoRouter(
    initialLocation: '/splash',
    refreshListenable: refresh,
    redirect: (context, state) async {
      final path = state.uri.path;
      await authService.sessionReady;
      final isAuthenticated = authService.isAuthenticated;
      final isPublicRoute = _publicPaths.contains(path);

      if (path == '/splash') return isAuthenticated ? '/' : '/login';
      if (!isAuthenticated && !isPublicRoute) return '/login';
      if (isAuthenticated && isPublicRoute) return '/';
      return null;
    },
    routes: [
      ShellRoute(
        builder: (context, state, child) => _CallRouteObserver(child: child),
        routes: [
          GoRoute(path: '/splash', builder: (context, state) => const _SplashScreen()),
          GoRoute(path: '/', builder: (context, state) => const ShellScaffold()),
          GoRoute(path: '/login', builder: (context, state) => const LoginScreen()),
          GoRoute(path: '/register', builder: (context, state) => const RegisterScreen()),
          GoRoute(path: '/forgot-password', builder: (context, state) => const ForgotPasswordScreen()),
          GoRoute(path: '/verify-otp', builder: (context, state) => OtpVerifyScreen(identifier: state.uri.queryParameters['id'] ?? '', mode: state.uri.queryParameters['mode'] ?? 'login')),
          GoRoute(path: '/chat', builder: (context, state) => const ChatListScreen()),
          GoRoute(path: '/chat/:conversationId', builder: (context, state) => ChatDetailScreen(conversationId: state.pathParameters['conversationId'] ?? 'general')),
          GoRoute(path: '/call', builder: (context, state) => const CallScreen()),
          GoRoute(path: '/posttube', builder: (context, state) => const PosttubeScreen()),
          GoRoute(path: '/reels', builder: (context, state) => const ReelsScreen(fullscreenRoute: true)),
          GoRoute(path: '/create', builder: (context, state) => const CreatePostScreen()),
          GoRoute(path: '/comments/:postId', builder: (context, state) => CommentsScreen(postId: state.pathParameters['postId']!)),
          GoRoute(path: '/shop', builder: (context, state) => const ShopScreen()),
          GoRoute(path: '/memories', builder: (context, state) => const MemoriesScreen()),
          GoRoute(path: '/memories/slambooks', builder: (context, state) => const SlambooksScreen()),
          GoRoute(path: '/memories/slambooks/:slambookId', builder: (context, state) => SlambookDetailScreen(slambookId: state.pathParameters['slambookId']!)),
          GoRoute(path: '/memories/slambooks/share/:token', builder: (context, state) => SlambookShareScreen(shareToken: state.pathParameters['token']!)),
          GoRoute(path: '/live', builder: (context, state) => const LiveScreen()),
          GoRoute(path: '/live/broadcast/:streamId', builder: (context, state) => BroadcastScreen(streamId: state.pathParameters['streamId']!, title: state.uri.queryParameters['title'] ?? 'Live Stream')),
          GoRoute(path: '/profile/:userId', builder: (context, state) => ProfileDetailScreen(userId: state.pathParameters['userId'] ?? '')),
          GoRoute(path: '/notifications', builder: (context, state) => const NotificationsScreen()),
          GoRoute(path: '/followers/:userId', builder: (context, state) => FollowersScreen(userId: state.pathParameters['userId']!)),
          GoRoute(path: '/following/:userId', builder: (context, state) => FollowingScreen(userId: state.pathParameters['userId']!)),
          GoRoute(path: '/friends', builder: (context, state) => const FriendsScreen()),
          GoRoute(path: '/friend-requests', builder: (context, state) => const FriendRequestsScreen()),
          GoRoute(path: '/settings', builder: (context, state) => const SettingsScreen()),
          GoRoute(path: '/settings/profile', builder: (context, state) => const EditProfileScreen()),
          GoRoute(path: '/settings/security', builder: (context, state) => const SecuritySettingsScreen()),
          GoRoute(path: '/settings/notifications', builder: (context, state) => const NotificationSettingsScreen()),
          GoRoute(path: '/settings/privacy', builder: (context, state) => const PrivacySettingsScreen()),
          GoRoute(path: '/settings/wellbeing', builder: (_, _) => const WellbeingSettingsScreen()),
          GoRoute(path: '/settings/verification', builder: (_, _) => const VerificationScreen()),
          GoRoute(path: '/apps', builder: (_, _) => const MiniAppsScreen()),
          GoRoute(path: '/apps/:id', builder: (context, state) => MiniAppDetailScreen(appId: state.pathParameters['id']!)),
          GoRoute(path: '/apps/sandbox/:id', builder: (context, state) => MiniAppSandboxScreen(appId: state.pathParameters['id']!)),
          GoRoute(path: '/stories/create', builder: (context, state) => const CreateStoryScreen()),
          GoRoute(path: '/stories/:userId', builder: (context, state) => StoryViewerScreen(userId: state.pathParameters['userId']!)),
          GoRoute(path: '/bookmarks', builder: (context, state) => const BookmarksScreen()),
          GoRoute(path: '/discover', builder: (context, state) => const DiscoverScreen()),
          GoRoute(path: '/search/results', builder: (context, state) => SearchResultsScreen(query: state.uri.queryParameters['q'] ?? '')),
          GoRoute(path: '/channels', builder: (context, state) => const ChannelsListScreen()),
          GoRoute(path: '/channels/create', builder: (context, state) => const CreateChannelScreen()),
          GoRoute(path: '/channels/:channelId', builder: (context, state) => ChannelDetailScreen(channelId: state.pathParameters['channelId']!)),
          GoRoute(path: '/communities', builder: (context, state) => const CommunitiesListScreen()),
          GoRoute(path: '/communities/create', builder: (context, state) => const CreateCommunityScreen()),
          GoRoute(path: '/communities/:communityId', builder: (context, state) => CommunityDetailScreen(communityId: state.pathParameters['communityId']!)),
          GoRoute(path: '/communities/:communityId/spaces/:spaceId', builder: (context, state) => CommunitySpaceScreen(communityId: state.pathParameters['communityId']!, spaceId: state.pathParameters['spaceId']!)),
          GoRoute(path: '/groups', builder: (context, state) => const GroupsListScreen()),
          GoRoute(path: '/groups/create', builder: (context, state) => const CreateGroupScreen()),
          GoRoute(path: '/groups/:groupId', builder: (context, state) => GroupDetailScreen(groupId: state.pathParameters['groupId']!)),
          GoRoute(path: '/groups/:groupId/post', builder: (context, state) => GroupPostComposerScreen(groupId: state.pathParameters['groupId']!)),
          GoRoute(path: '/groups/:groupId/admin', builder: (context, state) => GroupAdminScreen(groupId: state.pathParameters['groupId']!)),
          GoRoute(path: '/monetization', builder: (context, state) => const MonetizationDashboardScreen()),
          GoRoute(path: '/monetization/tiers', builder: (context, state) => const SubscriptionTiersScreen()),
          GoRoute(path: '/monetization/payouts', builder: (context, state) => const PayoutsScreen()),
          GoRoute(path: '/monetization/analytics', builder: (context, state) => const CreatorAnalyticsScreen()),
          GoRoute(path: '/orders', builder: (context, state) => const OrdersScreen()),
          GoRoute(path: '/orders/:orderId', builder: (context, state) => OrderDetailScreen(orderId: state.pathParameters['orderId']!)),
          GoRoute(path: '/flicks/editor', builder: (_, _) => const FlicksEditorScreen()),
          GoRoute(path: '/flicks/caption', builder: (_, _) => const FlicksCaptionScreen()),
          GoRoute(path: '/upload/progress', builder: (context, state) { final extra = state.extra as Map<String, dynamic>? ?? {}; return UploadProgressScreen(videoPath: extra['videoPath'] as String? ?? '', caption: extra['caption'] as String? ?? '', hashtags: List<String>.from(extra['hashtags'] as List? ?? []), visibility: extra['visibility'] as String? ?? 'public'); }),
          GoRoute(path: '/posttube/upload', builder: (_, _) => const PosttubeUploadScreen()),
        ],
      ),
    ],
  );
});
