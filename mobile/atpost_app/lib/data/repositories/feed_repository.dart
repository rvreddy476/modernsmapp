import 'package:atpost_app/core/cache/cache_keys.dart';
import 'package:atpost_app/core/cache/cache_manager.dart';
import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class FeedRepository {
  final ApiClient _api;
  final CacheManager? _cache;
  static const _tag = 'FeedRepository';

  FeedRepository(this._api, [this._cache]);

  /// Parse feed response — handles both {"data": [...]} and {"data": {"items": [...]}}
  List<Post> _parsePosts(dynamic responseData) {
    final raw = responseData['data'];
    final List<dynamic> items;
    if (raw is List) {
      items = raw;
    } else if (raw is Map) {
      items = (raw['items'] as List<dynamic>?) ?? [];
    } else {
      items = [];
    }
    return items.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList();
  }

  String? _parseNextCursor(dynamic responseData) {
    final rootMeta = responseData['meta'];
    if (rootMeta is Map<String, dynamic>) {
      final cursor = rootMeta['next_cursor'];
      if (cursor is String && cursor.isNotEmpty) return cursor;
    }

    final data = responseData['data'];
    if (data is Map<String, dynamic>) {
      final nestedMeta = data['meta'];
      if (nestedMeta is Map<String, dynamic>) {
        final cursor = nestedMeta['next_cursor'];
        if (cursor is String && cursor.isNotEmpty) return cursor;
      }
      final cursor = data['next_cursor'];
      if (cursor is String && cursor.isNotEmpty) return cursor;
    }

    return null;
  }

  /// Serialize a Post to a cacheable JSON map.
  Map<String, dynamic> _postToJson(Post p) => {
        'id': p.id,
        'author_id': p.authorId,
        'author_name': p.authorName,
        'author_avatar': p.authorAvatar,
        'content': p.content,
        'content_type': p.contentType,
        'visibility': p.visibility,
        'tags': p.tags,
        'media_ids': p.mediaIds,
        'like_count': p.likeCount,
        'comment_count': p.commentCount,
        'share_count': p.shareCount,
        'duration_seconds': p.durationSeconds,
        'is_liked': p.isLiked,
        'is_bookmarked': p.isBookmarked,
        'created_at': p.createdAt.toIso8601String(),
        'feeling': p.feeling,
        'activity': p.activity,
        'activity_detail': p.activityDetail,
        'location_name': p.locationName,
      };

  /// Fetch home feed with pagination.
  Future<List<Post>> getHomeFeed({
    int limit = 20,
    String feedMode = 'ranked',
    String? cursor,
  }) async {
    final page = await getHomeFeedPage(limit: limit, feedMode: feedMode, cursor: cursor);
    return page.items;
  }

  /// Fetch home feed with pagination metadata (cursor support).
  Future<FeedPage> getHomeFeedPage({
    int limit = 20,
    String feedMode = 'ranked',
    String? cursor,
  }) async {
    final params = <String, dynamic>{
      'limit': limit,
      'feed_mode': feedMode,
      'exclude_self': false,
    };
    if (cursor != null) params['cursor'] = cursor;

    try {
      final response = await _api.get(
        '${Environment.feedPath}/home',
        queryParameters: params,
      );
      final posts = _parsePosts(response.data);
      final nextCursor = _parseNextCursor(response.data);

      // Cache first page only
      if (cursor == null && _cache != null) {
        final cacheKey = CacheKeys.feedKey(feedMode);
        await _cache.putList(
          CacheKeys.feedBox,
          cacheKey,
          posts.map(_postToJson).toList(),
          ttl: CacheKeys.feedTtl,
        );
      }

      return FeedPage(items: posts, nextCursor: nextCursor);
    } catch (e) {
      // On failure, try cache for first page
      if (cursor == null && _cache != null) {
        final cacheKey = CacheKeys.feedKey(feedMode);
        final cached = await _cache.getList(CacheKeys.feedBox, cacheKey);
        if (cached != null && cached.isNotEmpty) {
          AppLogger.info('Serving cached feed for $feedMode', tag: _tag);
          return FeedPage(
            items: cached.map((j) => Post.fromJson(j)).toList(),
          );
        }
      }
      rethrow;
    }
  }

  /// Fetch reel feed.
  Future<List<Post>> getReelFeed({int limit = 20, String? cursor}) async {
    final page = await getReelFeedPage(limit: limit, cursor: cursor);
    return page.items;
  }

  /// Fetch reel feed with pagination metadata.
  Future<FeedPage> getReelFeedPage({int limit = 20, String? cursor}) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get(
      '${Environment.feedPath}/reels',
      queryParameters: params,
    );
    return FeedPage(
      items: _parsePosts(response.data),
      nextCursor: _parseNextCursor(response.data),
    );
  }

  /// Fetch video (PostTube) feed.
  Future<List<Post>> getVideoFeed({int limit = 20, String? cursor}) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get(
      '${Environment.feedPath}/watch',
      queryParameters: params,
    );
    return _parsePosts(response.data);
  }
}

class FeedPage {
  const FeedPage({required this.items, this.nextCursor});

  final List<Post> items;
  final String? nextCursor;
}

final feedRepositoryProvider = Provider<FeedRepository>((ref) {
  return FeedRepository(ref.watch(apiClientProvider));
});
