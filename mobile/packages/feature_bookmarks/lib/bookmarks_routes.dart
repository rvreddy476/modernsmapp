import 'package:feature_bookmarks/bookmarks_screen.dart';
import 'package:go_router/go_router.dart';

/// Saved-posts (bookmarks) route table. Spread into the app router's shell.
List<RouteBase> bookmarksRoutes() => [
  GoRoute(
    path: '/bookmarks',
    builder: (context, state) => const BookmarksScreen(),
  ),
];
