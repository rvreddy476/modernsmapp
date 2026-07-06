import 'package:feature_figo/figo_home_screen.dart';
import 'package:feature_figo/figo_rewards_screen.dart';
import 'package:go_router/go_router.dart';

/// FiGo's route table — the app router spreads this into its shell.
List<RouteBase> figoRoutes() => [
  GoRoute(
    path: '/figo',
    builder: (context, state) => const FigoHomeScreen(),
  ),
  GoRoute(
    path: '/figo/rewards',
    builder: (context, state) => const FigoRewardsScreen(),
  ),
];
