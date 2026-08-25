import 'package:atpost_app/features/shell/shell_scaffold.dart';
import 'package:flutter/material.dart';
import 'package:go_router/go_router.dart';

class ShellRoutes {
  static List<RouteBase> get routes => [
        GoRoute(
          path: '/splash',
          builder: (context, state) => const SplashScreen(),
        ),
        GoRoute(
          path: '/',
          builder: (context, state) => const ShellScaffold(),
        ),
        GoRoute(
          path: '/friends-tab',
          builder: (_, _) =>
              const ShellScaffold(initialTab: ShellTabIndex.friends),
        ),
        GoRoute(
          path: '/reels-tab',
          builder: (_, _) =>
              const ShellScaffold(initialTab: ShellTabIndex.reels),
        ),
        GoRoute(
          path: '/explore',
          builder: (_, _) =>
              const ShellScaffold(initialTab: ShellTabIndex.explore),
        ),
        GoRoute(path: '/search', redirect: (_, _) => '/'),
        GoRoute(path: '/inbox', redirect: (_, _) => '/notifications'),
        GoRoute(path: '/me', redirect: (_, _) => '/'),
      ];
}

class SplashScreen extends StatelessWidget {
  const SplashScreen({super.key});

  @override
  Widget build(BuildContext context) {
    return const Scaffold(body: Center(child: CircularProgressIndicator()));
  }
}
