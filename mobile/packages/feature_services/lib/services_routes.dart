import 'package:feature_services/service_slug_router.dart';
import 'package:feature_services/services_screen.dart';
import 'package:go_router/go_router.dart';

/// Mini-services directory routes (the services grid + slug router).
/// Spread into the app router's shell.
List<RouteBase> servicesRoutes() => [
  GoRoute(
    path: '/services',
    builder: (_, _) => const ServicesScreen(),
  ),
  GoRoute(
    path: '/services/:slug',
    builder: (context, state) =>
        ServiceSlugRouter(slug: state.pathParameters['slug']!),
  ),
];
