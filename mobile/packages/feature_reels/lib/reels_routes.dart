import 'package:feature_reels/reels_screen.dart';
import 'package:go_router/go_router.dart';

/// Reels (short-form vertical video) route table. The app router spreads
/// this into its shell. The `/reels-tab` entry point (which mounts the
/// shell scaffold on the Reels tab) stays in the app router since it
/// owns the shell.
List<RouteBase> reelsRoutes() => [
  GoRoute(
    path: '/reels',
    builder: (context, state) => const ReelsScreen(fullscreenRoute: true),
  ),
];
