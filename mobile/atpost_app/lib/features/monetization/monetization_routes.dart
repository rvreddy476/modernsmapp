import 'package:atpost_app/features/monetization/creator_analytics_screen.dart';
import 'package:atpost_app/features/monetization/monetization_dashboard_screen.dart';
import 'package:atpost_app/features/monetization/payouts_screen.dart';
import 'package:atpost_app/features/monetization/subscription_tiers_screen.dart';
import 'package:atpost_app/features/reviewer/needs_changes_screen.dart';
import 'package:atpost_app/features/reviewer/reviewer_console_screen.dart';
import 'package:atpost_app/features/reviewer/reviewer_dashboard_screen.dart';
import 'package:atpost_app/features/orders/order_detail_screen.dart';
import 'package:atpost_app/features/orders/orders_screen.dart';
import 'package:go_router/go_router.dart';

class MonetizationRoutes {
  static List<RouteBase> get routes => [
        GoRoute(
          path: '/monetization',
          builder: (context, state) => const MonetizationDashboardScreen(),
        ),
        GoRoute(
          path: '/monetization/tiers',
          builder: (context, state) => const SubscriptionTiersScreen(),
        ),
        GoRoute(
          path: '/monetization/payouts',
          builder: (context, state) => const PayoutsScreen(),
        ),
        GoRoute(
          path: '/monetization/analytics',
          builder: (context, state) => const CreatorAnalyticsScreen(),
        ),
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
          builder: (context, state) => NeedsChangesScreen(
            contentId: state.pathParameters['contentId']!,
          ),
        ),
        GoRoute(
          path: '/orders',
          builder: (context, state) => const OrdersScreen(),
        ),
        GoRoute(
          path: '/orders/:orderId',
          builder: (context, state) =>
              OrderDetailScreen(orderId: state.pathParameters['orderId']!),
        ),
      ];
}
