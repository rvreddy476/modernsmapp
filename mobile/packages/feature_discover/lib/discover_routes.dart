import 'package:feature_discover/discover_screen.dart';
import 'package:go_router/go_router.dart';

/// Discover route. Spread into the app router's shell. (The Q&A topic /
/// question-tile widgets in this package are also embedded by other
/// surfaces directly.)
List<RouteBase> discoverRoutes() => [
  GoRoute(path: '/discover', builder: (_, _) => const DiscoverScreen()),
];
