import 'package:feature_profile/my_media_screen.dart';
import 'package:feature_profile/profile_detail_screen.dart';
import 'package:go_router/go_router.dart';

/// Profile route table (another user's profile detail, my-media grid).
/// Spread into the app router's shell.
List<RouteBase> profileRoutes() => [
  GoRoute(
    path: '/profile/:userId',
    builder: (context, state) =>
        ProfileDetailScreen(userId: state.pathParameters['userId']!),
  ),
  GoRoute(
    path: '/profile/media',
    builder: (_, _) => const MyMediaScreen(),
  ),
];
