import 'package:feature_posttube/channel_screen.dart';
import 'package:feature_posttube/posttube_screen.dart';
import 'package:feature_posttube/posttube_upload_screen.dart';
import 'package:feature_posttube/subscriptions_screen.dart';
import 'package:feature_posttube/trending_screen.dart';
import 'package:feature_posttube/watch_history_screen.dart';
import 'package:go_router/go_router.dart';

/// PostTube (long-form video) route table. The app router spreads this
/// into its shell.
List<RouteBase> posttubeRoutes() => [
  GoRoute(
    path: '/posttube',
    builder: (context, state) => const PosttubeScreen(),
  ),
  GoRoute(
    path: '/posttube/upload',
    builder: (_, _) => const PosttubeUploadScreen(),
  ),
  GoRoute(
    path: '/posttube/subscriptions',
    builder: (_, _) => const PosttubeSubscriptionsScreen(),
  ),
  GoRoute(
    path: '/posttube/trending',
    builder: (_, _) => const PosttubeTrendingScreen(),
  ),
  GoRoute(
    path: '/posttube/history',
    builder: (_, _) => const WatchHistoryScreen(),
  ),
  GoRoute(
    path: '/posttube/channel/:userId',
    builder: (_, state) => PosttubeChannelScreen(
      userId: state.pathParameters['userId'] ?? '',
    ),
  ),
];
