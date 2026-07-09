import 'package:feature_monetization/creator_analytics_screen.dart';
import 'package:feature_monetization/monetization_dashboard_screen.dart';
import 'package:feature_monetization/payouts_screen.dart';
import 'package:feature_monetization/subscription_tiers_screen.dart';
import 'package:go_router/go_router.dart';

/// Creator monetization route table (dashboard, tiers, payouts, analytics).
/// Spread into the app router's shell.
List<RouteBase> monetizationRoutes() => [
  GoRoute(
      path: '/monetization',
      builder: (_, _) => const MonetizationDashboardScreen()),
  GoRoute(
      path: '/monetization/tiers',
      builder: (_, _) => const SubscriptionTiersScreen()),
  GoRoute(
      path: '/monetization/payouts', builder: (_, _) => const PayoutsScreen()),
  GoRoute(
      path: '/monetization/analytics',
      builder: (_, _) => const CreatorAnalyticsScreen()),
];
