import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/features/chat/chat_detail_screen.dart';
import 'package:atpost_app/features/chat/chat_list_screen.dart';
import 'package:atpost_app/features/chat/message_requests_screen.dart';
import 'package:atpost_app/features/calls/call_screen.dart';
import 'package:atpost_app/features/social/followers_screen.dart';
import 'package:atpost_app/features/social/following_screen.dart';
import 'package:atpost_app/features/social/friend_requests_screen.dart';
import 'package:atpost_app/features/social/friends_screen.dart';
import 'package:go_router/go_router.dart';

class SocialRoutes {
  static List<RouteBase> get routes => [
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
          redirect: (context, state) =>
              Environment.callsEnabled ? null : '/chat',
          builder: (context, state) => const CallScreen(),
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
      ];
}
