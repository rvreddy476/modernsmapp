import 'dart:async';

import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/repositories/feed_repository.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// State for the paginated feed.
class FeedState {
  final List<Post> posts;
  final String? nextCursor;
  final bool isLoadingMore;
  final bool hasError;

  const FeedState({
    this.posts = const [],
    this.nextCursor,
    this.isLoadingMore = false,
    this.hasError = false,
  });

  FeedState copyWith({
    List<Post>? posts,
    String? nextCursor,
    bool? isLoadingMore,
    bool? hasError,
  }) {
    return FeedState(
      posts: posts ?? this.posts,
      nextCursor: nextCursor ?? this.nextCursor,
      isLoadingMore: isLoadingMore ?? this.isLoadingMore,
      hasError: hasError ?? this.hasError,
    );
  }
}

/// Advanced Home Feed Notifier with pagination and real-time updates.
class HomeFeedNotifier extends StateNotifier<AsyncValue<FeedState>> {
  final FeedRepository _repo;
  final RealtimeService _realtime;
  StreamSubscription? _realtimeSub;
  String _currentFilter = 'For You';

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

  Future<void> fetchFirstPage() async {
    state = const AsyncValue.loading();
    try {
      final posts = await _repo.getHomeFeed(
        feedMode: _filterToMode(_currentFilter),
      );
      // In a real app, we'd get the cursor from the response.
      // Assuming for now the repo might need an update to return FeedPage.
      state = AsyncValue.data(FeedState(posts: posts));
    } catch (e, st) {
      state = AsyncValue.error(e, st);
    }
  }

  Future<void> fetchNextPage() async {
    final currentState = state.value;
    if (currentState == null || currentState.isLoadingMore || currentState.nextCursor == null) return;

    state = AsyncValue.data(currentState.copyWith(isLoadingMore: true));

    try {
      final newPosts = await _repo.getHomeFeed(
        cursor: currentState.nextCursor,
        feedMode: _filterToMode(_currentFilter),
      );

      state = AsyncValue.data(FeedState(
        posts: [...currentState.posts, ...newPosts],
        nextCursor: null, // Update when repo supports cursor return
        isLoadingMore: false,
      ));
    } catch (e) {
      state = AsyncValue.data(currentState.copyWith(isLoadingMore: false, hasError: true));
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

  void _handlePostInteraction(PostInteractionEvent event) {
    _updatePost(event.postId, (post) => Post(
      id: post.id,
      authorId: post.authorId,
      authorName: post.authorName,
      authorAvatar: post.authorAvatar,
      content: post.content,
      contentType: post.contentType,
      tags: post.tags,
      mediaIds: post.mediaIds,
      likeCount: event.likes ?? post.likeCount,
      commentCount: event.comments ?? post.commentCount,
      shareCount: post.shareCount,
      isLiked: post.isLiked,
      isBookmarked: post.isBookmarked,
      createdAt: post.createdAt,
    ));
  }

  void _handlePostLiked(PostLikedEvent event) {
    _updatePost(event.postId, (post) => Post(
      id: post.id,
      authorId: post.authorId,
      authorName: post.authorName,
      authorAvatar: post.authorAvatar,
      content: post.content,
      contentType: post.contentType,
      tags: post.tags,
      mediaIds: post.mediaIds,
      likeCount: event.likeCount ?? (post.likeCount + 1),
      commentCount: post.commentCount,
      shareCount: post.shareCount,
      isLiked: post.isLiked,
      isBookmarked: post.isBookmarked,
      createdAt: post.createdAt,
    ));
  }

  void _handlePostCommented(PostCommentedEvent event) {
    _updatePost(event.postId, (post) => Post(
      id: post.id,
      authorId: post.authorId,
      authorName: post.authorName,
      authorAvatar: post.authorAvatar,
      content: post.content,
      contentType: post.contentType,
      tags: post.tags,
      mediaIds: post.mediaIds,
      likeCount: post.likeCount,
      commentCount: event.commentCount ?? (post.commentCount + 1),
      shareCount: post.shareCount,
      isLiked: post.isLiked,
      isBookmarked: post.isBookmarked,
      createdAt: post.createdAt,
    ));
  }

  /// Helper to find and update a post in the feed by ID.
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

/// Legacy or specific providers.
final reelFeedProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final repo = ref.watch(feedRepositoryProvider);
  return repo.getReelFeed();
});

final videoFeedProvider = FutureProvider.autoDispose<List<Post>>((ref) async {
  final repo = ref.watch(feedRepositoryProvider);
  return repo.getVideoFeed();
});

/// Active feed filter.
final feedFilterProvider = StateProvider<String>((ref) => 'For You');
