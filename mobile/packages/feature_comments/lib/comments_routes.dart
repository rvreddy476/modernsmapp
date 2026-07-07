import 'package:feature_comments/comments_screen.dart';
import 'package:go_router/go_router.dart';

/// Post comments route table. Spread into the app router's shell.
List<RouteBase> commentsRoutes() => [
  GoRoute(
    path: '/comments/:postId',
    builder: (context, state) =>
        CommentsScreen(postId: state.pathParameters['postId']!),
  ),
];
