import 'package:feature_hashtag/hashtag_screen.dart';
import 'package:go_router/go_router.dart';

/// Hashtag feed route. Spread into the app router's shell.
List<RouteBase> hashtagRoutes() => [
  GoRoute(
    path: '/hashtag/:tag',
    builder: (context, state) =>
        HashtagScreen(tag: state.pathParameters['tag'] ?? ''),
  ),
];
