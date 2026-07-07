import 'package:feature_create/create_post_screen.dart';
import 'package:feature_create/reels_caption_screen.dart';
import 'package:feature_create/reels_editor_screen.dart';
import 'package:feature_create/upload_progress_screen.dart';
import 'package:go_router/go_router.dart';

/// Composer / upload route table (post + reels editor/caption, upload
/// progress; legacy /flicks/* redirect to /reels/*). Spread into the app router.
List<RouteBase> createRoutes() => [
  GoRoute(path: '/create', builder: (_, _) => const CreatePostScreen()),
  GoRoute(
      path: '/reels/editor', builder: (_, _) => const ReelsEditorScreen()),
  GoRoute(
      path: '/reels/caption', builder: (_, _) => const ReelsCaptionScreen()),
  GoRoute(path: '/flicks/editor', redirect: (_, _) => '/reels/editor'),
  GoRoute(path: '/flicks/caption', redirect: (_, _) => '/reels/caption'),
  GoRoute(
    path: '/upload/progress',
    builder: (context, state) {
      final extra = state.extra as Map<String, dynamic>? ?? {};
      return UploadProgressScreen(
        videoPath: extra['videoPath'] as String? ?? '',
        caption: extra['caption'] as String? ?? '',
        hashtags: List<String>.from(extra['hashtags'] as List? ?? []),
        visibility: extra['visibility'] as String? ?? 'public',
      );
    },
  ),
];
