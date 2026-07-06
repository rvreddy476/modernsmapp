import 'package:feature_stories/stories/create_story_screen.dart';
import 'package:feature_stories/stories/story_viewer_screen.dart';
import 'package:go_router/go_router.dart';

/// Stories route table. The app router spreads this into its shell.
List<RouteBase> storiesRoutes() => [
  GoRoute(
    path: '/stories/create',
    builder: (context, state) => const CreateStoryScreen(),
  ),
  GoRoute(
    path: '/stories/:userId',
    builder: (context, state) =>
        StoryViewerScreen(userId: state.pathParameters['userId']!),
  ),
];
