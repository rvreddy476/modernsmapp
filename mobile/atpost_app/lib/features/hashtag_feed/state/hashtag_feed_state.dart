import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/features/hashtag_feed/models/hashtag_model.dart';
import 'package:flutter/foundation.dart';

enum HashtagSort { top, recent }

extension HashtagSortX on HashtagSort {
  String get apiValue => this == HashtagSort.top ? 'top' : 'recent';
  String get label => this == HashtagSort.top ? 'Top' : 'Recent';
}

enum HashtagFeedStatus { initial, loading, loaded, loadingMore, error }

@immutable
class HashtagFeedState {
  const HashtagFeedState({
    this.status = HashtagFeedStatus.initial,
    this.query = '',
    this.selectedHashtag,
    this.sort = HashtagSort.top,
    this.trendingHashtags = const [],
    this.searchSuggestions = const [],
    this.posts = const [],
    this.hasMore = false,
    this.nextCursor,
    this.errorMessage,
    this.isSearching = false,
    this.newPostCount = 0,
  });

  final HashtagFeedStatus status;
  final String query;
  final HashtagModel? selectedHashtag;
  final HashtagSort sort;
  final List<HashtagModel> trendingHashtags;
  final List<HashtagModel> searchSuggestions;
  final List<Post> posts;
  final bool hasMore;
  final String? nextCursor;
  final String? errorMessage;
  final bool isSearching;
  /// Count of new posts received on the SSE stream for the currently
  /// selected hashtag since the last refresh / acknowledgement. Drives
  /// the inline "N new posts" pill at the top of the list; tapping
  /// the pill refetches the first page and resets this counter.
  final int newPostCount;

  HashtagFeedState copyWith({
    HashtagFeedStatus? status,
    String? query,
    HashtagModel? selectedHashtag,
    bool clearSelectedHashtag = false,
    HashtagSort? sort,
    List<HashtagModel>? trendingHashtags,
    List<HashtagModel>? searchSuggestions,
    List<Post>? posts,
    bool? hasMore,
    String? nextCursor,
    bool clearNextCursor = false,
    String? errorMessage,
    bool clearErrorMessage = false,
    bool? isSearching,
    int? newPostCount,
  }) {
    return HashtagFeedState(
      status: status ?? this.status,
      query: query ?? this.query,
      selectedHashtag: clearSelectedHashtag
          ? null
          : (selectedHashtag ?? this.selectedHashtag),
      sort: sort ?? this.sort,
      trendingHashtags: trendingHashtags ?? this.trendingHashtags,
      searchSuggestions: searchSuggestions ?? this.searchSuggestions,
      posts: posts ?? this.posts,
      hasMore: hasMore ?? this.hasMore,
      nextCursor: clearNextCursor ? null : (nextCursor ?? this.nextCursor),
      errorMessage:
          clearErrorMessage ? null : (errorMessage ?? this.errorMessage),
      isSearching: isSearching ?? this.isSearching,
      newPostCount: newPostCount ?? this.newPostCount,
    );
  }
}
