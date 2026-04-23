import 'package:atpost_app/core/cache/cache_keys.dart';
import 'package:atpost_app/core/cache/cache_manager.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Production-ready repository for feed-related operations.
/// Synchronized with the 2026-03-19 OpenAPI mobile integration spec.
class FeedRepository {
  final ApiClient _api;
  final CacheManager? _cache;
  static const _tag = 'FeedRepository';

  FeedRepository(this._api, [this._cache]);

  /// Fetch home feed with pagination metadata (cursor support).
  /// Synchronized with /v1/feed/home from OpenAPI spec.
  Future<FeedPage> getHomeFeedPage({
    int limit = 20,
    String feedMode = 'ranked',
    String platform = 'postbook',
    bool excludeSelf = false,
    bool circleOnly = false,
    String? cursor,
  }) async {
    final params = <String, dynamic>{
      'limit': limit,
      'feed_mode': feedMode,
      'platform': platform,
      if (excludeSelf) 'exclude_self': true,
      if (circleOnly) 'circle_only': true,
    };
    if (cursor != null) params['cursor'] = cursor;

    try {
      final response = await _api.get('/v1/feed/home', queryParameters: params);

      // Parse using the standard Envelope pattern (data, meta)
      final data = response.data['data'];
      final List<dynamic> rawData;
      if (data is List) {
        rawData = data;
      } else if (data is Map && data['items'] is List) {
        rawData = data['items'] as List<dynamic>;
      } else {
        rawData = [];
      }
      final posts = rawData.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList();

      final meta = response.data['meta'] as Map<String, dynamic>?;
      final nextCursor = meta?['next_cursor'] as String?;

      // Cache first page for offline support
      if (cursor == null && _cache != null) {
        final cacheKey = CacheKeys.feedKey(feedMode);
        await _cache.putList(
          CacheKeys.feedBox,
          cacheKey,
          posts.map((p) => p.toJson()).toList(), // Assuming Post has toJson()
          ttl: CacheKeys.feedTtl,
        );
      }

      return FeedPage(items: posts, nextCursor: nextCursor);
    } catch (e) {
      // Offline fallback: Serve cached feed if available
      if (cursor == null && _cache != null) {
        final cacheKey = CacheKeys.feedKey(feedMode);
        final cached = await _cache.getList(CacheKeys.feedBox, cacheKey);
        if (cached != null && cached.isNotEmpty) {
          AppLogger.info('Serving cached feed for $feedMode', tag: _tag);
          return FeedPage(items: cached.map((j) => Post.fromJson(j)).toList());
        }
      }
      rethrow;
    }
  }

  /// Alias for compatibility with older tests.
  Future<FeedPage> getHomeFeed({
    int limit = 20,
    String? cursor,
    String feedMode = 'ranked',
    String platform = 'postbook',
    bool excludeSelf = false,
    bool circleOnly = false,
  }) =>
      getHomeFeedPage(
        limit: limit,
        cursor: cursor,
        feedMode: feedMode,
        platform: platform,
        excludeSelf: excludeSelf,
        circleOnly: circleOnly,
      );

  /// Fetch specialized feeds (Reels, Videos).
  /// (Note: These might use specific paths based on the spec extension)
  Future<FeedPage> getReelFeedPage({int limit = 20, String? cursor}) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    // Reels often map to a specific feed mode or separate endpoint
    final response = await _api.get('/v1/feed/reels', queryParameters: params);
    final rawData = response.data['data'] as List<dynamic>? ?? [];

    return FeedPage(
      items: rawData.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList(),
      nextCursor: response.data['meta']?['next_cursor'],
    );
  }

  Future<FeedPage> getVideoFeedPage({int limit = 20, String? cursor}) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get('/v1/feed/watch', queryParameters: params);
    final rawData = response.data['data'] as List<dynamic>? ?? [];

    return FeedPage(
      items: rawData.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList(),
      nextCursor: response.data['meta']?['next_cursor'],
    );
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
