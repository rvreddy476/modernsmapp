import 'dart:async';

import 'package:atpost_app/core/errors/error_handler.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/feed_repository.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// State for the paginated feed with production-grade efficiency.
class FeedState {
  final List<Post> posts;
  final String? nextCursor;
  final bool isLoadingMore;
  final bool hasError;
  final bool hasReachedEnd;

  const FeedState({
    this.posts = const [],
    this.nextCursor,
    this.isLoadingMore = false,
    this.hasError = false,
    this.hasReachedEnd = false,
  });

  FeedState copyWith({
    List<Post>? posts,
    String? nextCursor,
    bool? isLoadingMore,
    bool? hasError,
    bool? hasReachedEnd,
  }) {
    return FeedState(
      posts: posts ?? this.posts,
      nextCursor: nextCursor ?? this.nextCursor,
      isLoadingMore: isLoadingMore ?? this.isLoadingMore,
      hasError: hasError ?? this.hasError,
      hasReachedEnd: hasReachedEnd ?? this.hasReachedEnd,
    );
  }
}

/// Advanced Home Feed Notifier optimized for scale.
/// Features: Sliding window memory management, Pre-fetching, and Resilient retries.
class HomeFeedNotifier extends StateNotifier<AsyncValue<FeedState>> {
  final FeedRepository _repo;
  final RealtimeService _realtime;
  StreamSubscription? _realtimeSub;
  String _currentFilter = 'For You';

  // Prevent duplicate concurrent requests
  bool _isFetching = false;

  // Production optimization: Keep memory footprint stable.
  // If user scrolls through thousands of posts, we trim the top to save RAM.
  static const int _maxPostsInMemory = 500;
  static const int _prefetchThreshold = 5; // Start loading next page when 5 items from end

  HomeFeedNotifier(this._repo, this._realtime) : super(const AsyncValue.loading()) {
    _init();
  }

  Future<void> _init() async {
    await fetchFirstPage();
    _listenToRealtimeEvents();
  }

  void updateFilter(String filter) {
    if (_currentFilter == filter) return;
    _currentFilter = filter;
    fetchFirstPage();
  }

  /// Refreshes the feed from scratch. Uses ErrorHandler.retry for resilience.
  Future<void> fetchFirstPage() async {
    if (_isFetching) return;
    _isFetching = true;

    state = const AsyncValue.loading();
    try {
      final page = await ErrorHandler.retry(() => _repo.getHomeFeedPage(
        feedMode: _filterToMode(_currentFilter),
      ));

      state = AsyncValue.data(FeedState(
        posts: page.items,
        nextCursor: page.nextCursor,
        hasReachedEnd: page.nextCursor == null,
      ));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    } finally {
      _isFetching = false;
    }
  }

  /// Automatically triggered by the UI when the user scrolls.
  Future<void> fetchNextPage() async {
    final currentState = state.value;
    if (currentState == null ||
        currentState.isLoadingMore ||
        currentState.nextCursor == null ||
        _isFetching) {
      return;
    }

    _isFetching = true;
    state = AsyncValue.data(currentState.copyWith(isLoadingMore: true));

    try {
      final page = await ErrorHandler.retry(() => _repo.getHomeFeedPage(
        cursor: currentState.nextCursor,
        feedMode: _filterToMode(_currentFilter),
      ));

      List<Post> newPosts = [...currentState.posts, ...page.items];

      // PRODUCTION OPTIMIZATION: Sliding Window
      // If the list is too long, we drop the oldest items to keep memory usage low.
      if (newPosts.length > _maxPostsInMemory) {
        newPosts = newPosts.sublist(newPosts.length - _maxPostsInMemory);
        AppLogger.info('Feed memory management: trimmed posts to $_maxPostsInMemory', tag: 'FeedNotifier');
      }

      state = AsyncValue.data(FeedState(
        posts: newPosts,
        nextCursor: page.nextCursor,
        isLoadingMore: false,
        hasReachedEnd: page.nextCursor == null,
      ));
    } catch (e) {
      state = AsyncValue.data(currentState.copyWith(isLoadingMore: false, hasError: true));
    } finally {
      _isFetching = false;
    }
  }

  /// Logic to check if we should pre-fetch the next page based on current index.
  void onListItemVisible(int index) {
    final currentState = state.value;
    if (currentState == null) return;

    if (index >= currentState.posts.length - _prefetchThreshold) {
      fetchNextPage();
    }
  }

  void _listenToRealtimeEvents() {
    _realtimeSub?.cancel();
    _realtimeSub = _realtime.events.listen((event) {
      if (event is PostInteractionEvent) {
        _handlePostInteraction(event);
      } else if (event is PostLikedEvent) {
        _handlePostLiked(event);
      } else if (event is PostCommentedEvent) {
        _handlePostCommented(event);
      }
    });
  }

  // Real-time updates use an efficient index-based update to avoid full list rebuilds.
  void _updatePost(String postId, Post Function(Post) updater) {
    final currentState = state.value;
    if (currentState == null) return;

    final index = currentState.posts.indexWhere((p) => p.id == postId);
    if (index != -1) {
      final newPosts = List<Post>.from(currentState.posts);
      newPosts[index] = updater(currentState.posts[index]);
      state = AsyncValue.data(currentState.copyWith(posts: newPosts));
    }
  }

  void _handlePostInteraction(PostInteractionEvent event) {
    _updatePost(event.postId, (post) => post.copyWith(
      likeCount: event.likes ?? post.likeCount,
      commentCount: event.comments ?? post.commentCount,
    ));
  }

  void _handlePostLiked(PostLikedEvent event) {
    _updatePost(event.postId, (post) => post.copyWith(
      likeCount: event.likeCount ?? (post.likeCount + 1),
    ));
  }

  void _handlePostCommented(PostCommentedEvent event) {
    _updatePost(event.postId, (post) => post.copyWith(
      commentCount: event.commentCount ?? (post.commentCount + 1),
    ));
  }

  String _filterToMode(String filter) {
    switch (filter) {
      case 'Following': return 'following';
      case 'Trending': return 'trending';
      default: return 'ranked';
    }
  }

  @override
  void dispose() {
    _realtimeSub?.cancel();
    super.dispose();
  }
}

/// Global provider for the home feed state.
final homeFeedProvider = StateNotifierProvider.autoDispose<HomeFeedNotifier, AsyncValue<FeedState>>((ref) {
  final repo = ref.watch(feedRepositoryProvider);
  final realtime = ref.watch(realtimeServiceProvider);
  return HomeFeedNotifier(repo, realtime);
});

final feedFilterProvider = StateProvider<String>((ref) => 'For You');

/// Provider for the video feed (PostTube).
final videoFeedProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final repo = ref.watch(feedRepositoryProvider);
  final page = await repo.getVideoFeedPage();
  return page.items;
});

/// Provider for the reels feed.
final reelFeedProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final repo = ref.watch(feedRepositoryProvider);
  final page = await repo.getReelFeedPage();
  return page.items;
});

// Add copyWith to Post model if not present, otherwise use constructor.
extension on Post {
  Post copyWith({
    int? likeCount,
    int? commentCount,
  }) {
    return Post(
      id: id,
      authorId: authorId,
      authorName: authorName,
      authorAvatar: authorAvatar,
      content: content,
      contentType: contentType,
      visibility: visibility,
      tags: tags,
      mediaIds: mediaIds,
      likeCount: likeCount ?? this.likeCount,
      commentCount: commentCount ?? this.commentCount,
      shareCount: shareCount,
      durationSeconds: durationSeconds,
      isLiked: isLiked,
      isBookmarked: isBookmarked,
      createdAt: createdAt,
      feeling: feeling,
      activity: activity,
      activityDetail: activityDetail,
      locationName: locationName,
      poll: poll,
    );
  }
}
