import 'package:atpost_app/features/bookmarks/bookmarks_screen.dart';
import 'package:atpost_app/features/comments/comments_screen.dart';
import 'package:atpost_app/features/create/create_post_screen.dart';
import 'package:atpost_app/features/create/reels_caption_screen.dart';
import 'package:atpost_app/features/create/reels_editor_screen.dart';
import 'package:atpost_app/features/create/upload_progress_screen.dart';
import 'package:atpost_app/features/discover/discover_screen.dart';
import 'package:atpost_app/features/hashtag/hashtag_screen.dart';
import 'package:atpost_app/features/posttube/posttube_screen.dart';
import 'package:atpost_app/features/posttube/posttube_upload_screen.dart';
import 'package:atpost_app/features/posttube/subscriptions_screen.dart';
import 'package:atpost_app/features/posttube/trending_screen.dart';
import 'package:atpost_app/features/posttube/watch_history_screen.dart';
import 'package:atpost_app/features/posttube/channel_screen.dart';
import 'package:atpost_app/features/reels/reels_screen.dart';
import 'package:atpost_app/features/search/search_results_screen.dart';
import 'package:atpost_app/features/search/video_search_screen.dart';
import 'package:atpost_app/features/shell/search_tab.dart';
import 'package:atpost_app/features/stories/create_story_screen.dart';
import 'package:atpost_app/features/stories/story_viewer_screen.dart';
import 'package:atpost_app/features/live/live_screen.dart';
import 'package:atpost_app/features/live/broadcast_screen.dart';
import 'package:atpost_app/features/live/live_list_screen.dart';
import 'package:atpost_app/features/live/live_viewer_screen.dart';
import 'package:atpost_app/features/live/go_live_screen.dart';
import 'package:atpost_app/features/live/live_broadcaster_screen.dart';
import 'package:atpost_app/features/memories/memories_screen.dart';
import 'package:atpost_app/features/memories/slambook_detail_screen.dart';
import 'package:atpost_app/features/memories/slambook_share_screen.dart';
import 'package:atpost_app/features/memories/slambooks_screen.dart';
import 'package:go_router/go_router.dart';

class ContentRoutes {
  static List<RouteBase> get routes => [
        GoRoute(
          path: '/posttube',
          builder: (context, state) => const PosttubeScreen(),
        ),
        GoRoute(
          path: '/posttube/upload',
          builder: (_, _) => const PosttubeUploadScreen(),
        ),
        GoRoute(
          path: '/posttube/subscriptions',
          builder: (_, _) => const PosttubeSubscriptionsScreen(),
        ),
        GoRoute(
          path: '/posttube/trending',
          builder: (_, _) => const PosttubeTrendingScreen(),
        ),
        GoRoute(
          path: '/posttube/history',
          builder: (_, _) => const WatchHistoryScreen(),
        ),
        GoRoute(
          path: '/posttube/channel/:userId',
          builder: (_, state) => PosttubeChannelScreen(
            userId: state.pathParameters['userId'] ?? '',
          ),
        ),
        GoRoute(
          path: '/reels',
          builder: (context, state) =>
              const ReelsScreen(fullscreenRoute: true),
        ),
        GoRoute(
          path: '/reels/editor',
          builder: (context, state) => const ReelsEditorScreen(),
        ),
        GoRoute(
          path: '/reels/caption',
          builder: (context, state) => const ReelsCaptionScreen(),
        ),
        GoRoute(path: '/flicks/editor', redirect: (_, _) => '/reels/editor'),
        GoRoute(
          path: '/flicks/caption',
          redirect: (_, _) => '/reels/caption',
        ),
        GoRoute(
          path: '/create',
          builder: (context, state) => const CreatePostScreen(),
        ),
        GoRoute(
          path: '/comments/:postId',
          builder: (context, state) =>
              CommentsScreen(postId: state.pathParameters['postId']!),
        ),
        GoRoute(
          path: '/bookmarks',
          builder: (context, state) => const BookmarksScreen(),
        ),
        GoRoute(
          path: '/discover',
          builder: (context, state) => const DiscoverScreen(),
        ),
        GoRoute(
          path: '/hashtag/:tag',
          builder: (context, state) =>
              HashtagScreen(tag: state.pathParameters['tag'] ?? ''),
        ),
        GoRoute(
          path: '/search/results',
          builder: (context, state) => SearchResultsScreen(
            query: state.uri.queryParameters['q'] ?? '',
          ),
        ),
        GoRoute(
          path: '/search/explore',
          builder: (context, state) => const SearchTab(),
        ),
        GoRoute(
          path: '/search/videos',
          builder: (context, state) => const VideoSearchScreen(),
        ),
        GoRoute(
          path: '/stories/create',
          builder: (context, state) => const CreateStoryScreen(),
        ),
        GoRoute(
          path: '/stories/:userId',
          builder: (context, state) =>
              StoryViewerScreen(userId: state.pathParameters['userId']!),
        ),
        GoRoute(
          path: '/live',
          builder: (context, state) => const LiveScreen(),
        ),
        GoRoute(
          path: '/live/broadcast/:streamId',
          builder: (context, state) => BroadcastScreen(
            streamId: state.pathParameters['streamId']!,
            title: state.uri.queryParameters['title'] ?? 'Live Stream',
          ),
        ),
        GoRoute(path: '/live/v2', builder: (_, _) => const LiveListScreen()),
        GoRoute(
          path: '/live/v2/new',
          builder: (_, _) => const GoLiveScreen(),
        ),
        GoRoute(
          path: '/live/v2/:streamId',
          builder: (context, state) =>
              LiveViewerScreen(streamId: state.pathParameters['streamId']!),
        ),
        GoRoute(
          path: '/live/v2/:streamId/broadcast',
          builder: (context, state) => LiveBroadcasterScreen(
            streamId: state.pathParameters['streamId']!,
          ),
        ),
        GoRoute(
          path: '/memories',
          builder: (context, state) => const MemoriesScreen(),
        ),
        GoRoute(
          path: '/memories/slambooks',
          builder: (context, state) => const SlambooksScreen(),
        ),
        GoRoute(
          path: '/memories/slambooks/:slambookId',
          builder: (context, state) => SlambookDetailScreen(
            slambookId: state.pathParameters['slambookId']!,
          ),
        ),
        GoRoute(
          path: '/memories/slambooks/share/:token',
          builder: (context, state) =>
              SlambookShareScreen(shareToken: state.pathParameters['token']!),
        ),
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
}
