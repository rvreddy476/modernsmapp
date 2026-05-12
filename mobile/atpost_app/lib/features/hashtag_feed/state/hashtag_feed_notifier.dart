import 'dart:async';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/features/hashtag_feed/data/hashtag_live_stream.dart';
import 'package:atpost_app/features/hashtag_feed/data/hashtag_repository.dart';
import 'package:atpost_app/features/hashtag_feed/models/hashtag_model.dart';
import 'package:atpost_app/features/hashtag_feed/state/hashtag_feed_state.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Owns the #Hashtag tab state. Public methods cover the transitions
/// described in spec §9.3.
class HashtagFeedNotifier extends StateNotifier<HashtagFeedState> {
  HashtagFeedNotifier(this._repo, this._liveStream)
      : super(const HashtagFeedState()) {
    _bootstrap();
  }

  static const _tag = 'HashtagFeed';
  final HashtagRepository _repo;
  final HashtagLiveStream _liveStream;

  Timer? _searchDebounce;
  int _searchSeq = 0;
  // Live SSE subscription for the currently selected hashtag. Cancelled
  // and re-opened whenever the selection changes; null while no tag
  // is selected (initial state / clearSelectedHashtag).
  StreamSubscription<HashtagStreamEvent>? _streamSub;

  Future<void> _bootstrap() async {
    await refresh();
  }

  /// Initial / pull-to-refresh load of the default mixed-trending view.
  Future<void> refresh() async {
    state = state.copyWith(
      status: HashtagFeedStatus.loading,
      clearErrorMessage: true,
    );
    try {
      final trending = await _repo.getTrending();

      // For the default view we surface posts from the top trending tag.
      // If trending is empty (no posts in last 24h yet), we show the
      // empty state — no separate "everything" feed to avoid bleeding into
      // the For-You experience.
      if (trending.isEmpty) {
        state = state.copyWith(
          status: HashtagFeedStatus.loaded,
          trendingHashtags: const [],
          posts: const [],
          hasMore: false,
          clearNextCursor: true,
        );
        return;
      }

      final firstTag = trending.first;
      final page = await _repo.getPostsForHashtag(
        tag: firstTag.normalizedName,
        sort: state.sort.apiValue,
      );

      state = state.copyWith(
        status: HashtagFeedStatus.loaded,
        trendingHashtags: trending,
        selectedHashtag: firstTag,
        posts: page.posts,
        hasMore: page.nextCursor != null,
        nextCursor: page.nextCursor ?? '',
        clearNextCursor: page.nextCursor == null,
      );
    } catch (e, st) {
      AppLogger.error('refresh failed', tag: _tag, error: e, stackTrace: st);
      state = state.copyWith(
        status: HashtagFeedStatus.error,
        errorMessage: e.toString(),
      );
    }
  }

  /// Switch to a specific hashtag (from chip tap, suggestion tap, or in-text
  /// hashtag tap). Always resets sort to top.
  Future<void> selectHashtag(HashtagModel hashtag) async {
    state = state.copyWith(
      selectedHashtag: hashtag,
      query: '',
      sort: HashtagSort.top,
      posts: const [],
      hasMore: false,
      clearNextCursor: true,
      searchSuggestions: const [],
      status: HashtagFeedStatus.loading,
      clearErrorMessage: true,
      newPostCount: 0,
    );
    _subscribeStream(hashtag.normalizedName);
    await _loadPosts(reset: true);
  }

  /// When only a normalized hashtag name is known (e.g. tap inside post text).
  Future<void> selectHashtagByName(String normalized) async {
    final cleaned = normalized.replaceAll('#', '').trim().toLowerCase();
    if (cleaned.isEmpty) return;
    final inTrending = state.trendingHashtags.firstWhere(
      (h) => h.normalizedName == cleaned,
      orElse: () => HashtagModel(
        normalizedName: cleaned,
        displayName: '#$cleaned',
        postCount: 0,
      ),
    );
    await selectHashtag(inTrending);
  }

  /// Clears the selected hashtag and reverts to the default view.
  Future<void> clearSelectedHashtag() async {
    _cancelStream();
    state = state.copyWith(
      clearSelectedHashtag: true,
      query: '',
      sort: HashtagSort.top,
      posts: const [],
      hasMore: false,
      clearNextCursor: true,
      searchSuggestions: const [],
      newPostCount: 0,
    );
    await refresh();
  }

  /// Pull-to-refresh from the inline "N new posts" pill at the top of
  /// the list. Resets the SSE counter and reloads the first page so
  /// the new posts are merged into the visible list.
  Future<void> acknowledgeNewPosts() async {
    if (state.selectedHashtag == null) return;
    state = state.copyWith(newPostCount: 0);
    await _loadPosts(reset: true);
  }

  /// Opens an SSE subscription for [normalized]. Caller is responsible
  /// for clearing newPostCount in the same state transition so the pill
  /// doesn't briefly flash with the previous tag's count.
  void _subscribeStream(String normalized) {
    _cancelStream();
    final cleaned = normalized.replaceAll('#', '').trim().toLowerCase();
    if (cleaned.isEmpty) return;
    _streamSub = _liveStream.subscribe(cleaned).listen(
      (_) {
        // Bail if the user has navigated away to a different tag
        // mid-flight — the subscription will be replaced on the next
        // selectHashtag() call, but events already in transit are
        // discarded here so they don't poison the new counter.
        if (state.selectedHashtag?.normalizedName != cleaned) return;
        state = state.copyWith(newPostCount: state.newPostCount + 1);
      },
      onError: (err, st) {
        AppLogger.warn(
          'hashtag stream subscription error',
          tag: _tag,
          error: err,
          stackTrace: st is StackTrace ? st : null,
        );
      },
      cancelOnError: false,
    );
  }

  void _cancelStream() {
    _streamSub?.cancel();
    _streamSub = null;
  }

  /// Update the sort mode for the current hashtag.
  Future<void> setSort(HashtagSort sort) async {
    if (sort == state.sort) return;
    state = state.copyWith(
      sort: sort,
      posts: const [],
      hasMore: false,
      clearNextCursor: true,
      status: HashtagFeedStatus.loading,
      clearErrorMessage: true,
    );
    if (state.selectedHashtag != null) {
      await _loadPosts(reset: true);
    } else {
      await refresh();
    }
  }

  /// Debounced search input handler.
  void onSearchChanged(String raw) {
    final value = raw.trim();
    state = state.copyWith(query: value);

    _searchDebounce?.cancel();
    if (value.replaceAll('#', '').length < 2) {
      state = state.copyWith(
        searchSuggestions: const [],
        isSearching: false,
      );
      return;
    }

    final seq = ++_searchSeq;
    _searchDebounce = Timer(const Duration(milliseconds: 300), () async {
      state = state.copyWith(isSearching: true);
      try {
        final results = await _repo.search(value);
        if (seq != _searchSeq) return; // a newer query already started
        state = state.copyWith(
          searchSuggestions: results,
          isSearching: false,
        );
      } catch (e, st) {
        AppLogger.warn('search failed', tag: _tag, error: e, stackTrace: st);
        if (seq != _searchSeq) return;
        state = state.copyWith(
          searchSuggestions: const [],
          isSearching: false,
        );
      }
    });
  }

  void clearSearch() {
    _searchDebounce?.cancel();
    state = state.copyWith(
      query: '',
      searchSuggestions: const [],
      isSearching: false,
    );
  }

  /// Append next page of posts. Idempotent if hasMore is false or already
  /// loading more.
  Future<void> loadMore() async {
    if (!state.hasMore) return;
    if (state.status == HashtagFeedStatus.loadingMore) return;
    if (state.selectedHashtag == null) return;
    state = state.copyWith(status: HashtagFeedStatus.loadingMore);
    await _loadPosts(reset: false);
  }

  Future<void> _loadPosts({required bool reset}) async {
    final selected = state.selectedHashtag;
    if (selected == null) return;
    try {
      final page = await _repo.getPostsForHashtag(
        tag: selected.normalizedName,
        sort: state.sort.apiValue,
        cursor: reset ? null : state.nextCursor,
      );
      final posts = reset ? page.posts : [...state.posts, ...page.posts];
      state = state.copyWith(
        status: HashtagFeedStatus.loaded,
        posts: posts,
        hasMore: page.nextCursor != null,
        nextCursor: page.nextCursor ?? '',
        clearNextCursor: page.nextCursor == null,
      );
    } catch (e, st) {
      AppLogger.error('_loadPosts failed', tag: _tag, error: e, stackTrace: st);
      state = state.copyWith(
        status: reset
            ? HashtagFeedStatus.error
            : HashtagFeedStatus.loaded, // keep previous list on load-more fail
        errorMessage: e.toString(),
      );
    }
  }

  @override
  void dispose() {
    _searchDebounce?.cancel();
    _cancelStream();
    super.dispose();
  }
}

final hashtagLiveStreamProvider = Provider<HashtagLiveStream>(
  (_) => HashtagLiveStream(),
);

final hashtagFeedProvider =
    StateNotifierProvider.autoDispose<HashtagFeedNotifier, HashtagFeedState>(
  (ref) => HashtagFeedNotifier(
    ref.watch(hashtagRepositoryProvider),
    ref.watch(hashtagLiveStreamProvider),
  ),
);
