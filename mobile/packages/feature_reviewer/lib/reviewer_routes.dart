import 'package:feature_reviewer/needs_changes_screen.dart';
import 'package:feature_reviewer/reviewer_console_screen.dart';
import 'package:feature_reviewer/reviewer_dashboard_screen.dart';
import 'package:go_router/go_router.dart';

/// Content-reviewer (moderation) route table. Spread into the app router.
List<RouteBase> reviewerRoutes() => [
  GoRoute(
    path: '/reviewer',
    builder: (context, state) => const ReviewerConsoleScreen(),
  ),
  GoRoute(
    path: '/reviewer/dashboard',
    builder: (context, state) => const ReviewerDashboardScreen(),
  ),
  GoRoute(
    path: '/reviewer/feedback/:contentId',
    builder: (context, state) =>
        NeedsChangesScreen(contentId: state.pathParameters['contentId']!),
  ),
];
