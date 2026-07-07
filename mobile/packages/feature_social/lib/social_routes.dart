import 'package:feature_social/followers_screen.dart';
import 'package:feature_social/following_screen.dart';
import 'package:feature_social/friend_requests_screen.dart';
import 'package:feature_social/friends_screen.dart';
import 'package:go_router/go_router.dart';

/// Social-graph route table (followers, following, friends, requests).
/// The `/friends-tab` entry (mounts the shell on the Friends tab) stays in
/// the app router since it owns the shell. Spread into the app router.
List<RouteBase> socialRoutes() => [
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
