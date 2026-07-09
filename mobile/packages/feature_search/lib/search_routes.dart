import 'package:feature_search/search_results_screen.dart';
import 'package:feature_search/video_search_screen.dart';
import 'package:go_router/go_router.dart';

/// Search route table (results + video search). The shell's SearchTab and
/// /search/explore stay app-side (the tab embeds many feature surfaces).
List<RouteBase> searchRoutes() => [
  GoRoute(
    path: '/search/results',
    builder: (context, state) => SearchResultsScreen(
      query: state.uri.queryParameters['q'] ?? '',
    ),
  ),
  GoRoute(
    path: '/search/videos',
    builder: (context, state) => const VideoSearchScreen(),
  ),
];
