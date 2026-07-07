import 'package:feature_mini_apps/mini_app_detail_screen.dart';
import 'package:feature_mini_apps/mini_app_sandbox_screen.dart';
import 'package:feature_mini_apps/mini_apps_screen.dart';
import 'package:go_router/go_router.dart';

/// Mini-apps (in-app webview apps) route table. Spread into the app router.
List<RouteBase> miniAppsRoutes() => [
  GoRoute(path: '/apps', builder: (_, _) => const MiniAppsScreen()),
  GoRoute(
    path: '/apps/:id',
    builder: (context, state) =>
        MiniAppDetailScreen(appId: state.pathParameters['id']!),
  ),
  GoRoute(
    path: '/apps/sandbox/:id',
    builder: (context, state) =>
        MiniAppSandboxScreen(appId: state.pathParameters['id']!),
  ),
];
