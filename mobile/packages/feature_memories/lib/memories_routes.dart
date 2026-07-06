import 'package:feature_memories/memories/memories_screen.dart';
import 'package:feature_memories/memories/slambook_detail_screen.dart';
import 'package:feature_memories/memories/slambook_share_screen.dart';
import 'package:feature_memories/memories/slambooks_screen.dart';
import 'package:go_router/go_router.dart';

/// Memories (slambooks) route table. The app router spreads this into its
/// shell.
List<RouteBase> memoriesRoutes() => [
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
];
