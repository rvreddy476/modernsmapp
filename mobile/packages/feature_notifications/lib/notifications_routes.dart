import 'package:feature_notifications/notifications_screen.dart';
import 'package:go_router/go_router.dart';

/// Notifications inbox route table. Spread into the app router's shell.
List<RouteBase> notificationsRoutes() => [
  GoRoute(
    path: '/notifications',
    builder: (context, state) => const NotificationsScreen(),
  ),
];
