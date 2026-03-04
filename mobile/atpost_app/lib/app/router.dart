import 'package:atpost_app/features/auth/forgot_password_screen.dart';
import 'package:atpost_app/features/auth/login_screen.dart';
import 'package:atpost_app/features/auth/otp_verify_screen.dart';
import 'package:atpost_app/features/auth/register_screen.dart';
import 'package:atpost_app/features/bookmarks/bookmarks_screen.dart';
import 'package:atpost_app/features/discover/discover_screen.dart';
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
import 'package:atpost_app/features/settings/edit_profile_screen.dart';
import 'package:atpost_app/features/settings/notification_settings_screen.dart';
import 'package:atpost_app/features/settings/privacy_settings_screen.dart';
import 'package:atpost_app/features/settings/security_settings_screen.dart';
import 'package:atpost_app/features/settings/settings_screen.dart';
import 'package:atpost_app/features/shell/shell_scaffold.dart';
import 'package:atpost_app/features/shop/shop_screen.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';
import 'package:go_router/go_router.dart';

final appRouterProvider = Provider<GoRouter>((ref) {
  return GoRouter(
    initialLocation: '/',
    routes: [
      GoRoute(
        path: '/',
        builder: (context, state) => const ShellScaffold(),
      ),
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
      GoRoute(
        path: '/posttube',
        builder: (context, state) => const PosttubeScreen(),
      ),
      GoRoute(
        path: '/reels',
        builder: (context, state) => const ReelsScreen(fullscreenRoute: true),
      ),
      GoRoute(
        path: '/shop',
        builder: (context, state) => const ShopScreen(),
      ),
      GoRoute(
        path: '/memories',
        builder: (context, state) => const MemoriesScreen(),
      ),
      GoRoute(
        path: '/live',
        builder: (context, state) => const LiveScreen(),
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
        builder: (context, state) => FollowersScreen(
          userId: state.pathParameters['userId']!,
        ),
      ),
      GoRoute(
        path: '/following/:userId',
        builder: (context, state) => FollowingScreen(
          userId: state.pathParameters['userId']!,
        ),
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
      GoRoute(
        path: '/stories/create',
        builder: (context, state) => const CreateStoryScreen(),
      ),
      GoRoute(
        path: '/stories/:userId',
        builder: (context, state) => StoryViewerScreen(
          userId: state.pathParameters['userId']!,
        ),
      ),
      GoRoute(
        path: '/bookmarks',
        builder: (context, state) => const BookmarksScreen(),
      ),
      GoRoute(
        path: '/discover',
        builder: (context, state) => const DiscoverScreen(),
      ),
    ],
  );
});

