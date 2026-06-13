import 'package:atpost_app/core/cache/cache_keys.dart';
import 'package:atpost_app/core/cache/cache_manager.dart';
import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Production-ready repository for feed-related operations.
/// Synchronized with the 2026-03-19 OpenAPI mobile integration spec.
class FeedRepository {
  final ApiClient _api;
  final CacheManager? _cache;
  final UserRepository? _users;
  static const _tag = 'FeedRepository';

  FeedRepository(this._api, [this._cache, this._users]);

  /// Hydrate posts with author display name + avatar URL.
  /// /v1/feed/home returns only author_id; the web app does the same lookup
  /// via useBatchProfiles. Without this, all posts render as "Anonymous".
  Future<List<Post>> _hydrateAuthors(List<Post> posts) async {
    if (_users == null || posts.isEmpty) return posts;
    final ids = <String>{};
    for (final p in posts) {
      final id = p.authorId.trim();
      if (id.isEmpty) continue;
      // Only fetch if we don't already have a name (post-service hydration may evolve).
      if ((p.authorName ?? '').trim().isEmpty) ids.add(id);
    }
    if (ids.isEmpty) return posts;
    try {
      final users = await _users.getUsersBatch(ids.toList());
      final byId = <String, User>{for (final u in users) u.id: u};
      return posts.map((p) {
        final u = byId[p.authorId];
        if (u == null) return p;
        return p.copyWith(
          authorName: (p.authorName?.trim().isNotEmpty ?? false)
              ? p.authorName
              : u.displayName,
          authorAvatar: (p.authorAvatar?.trim().isNotEmpty ?? false)
              ? p.authorAvatar
              : u.avatarUrl,
        );
      }).toList();
    } catch (e) {
      AppLogger.warn('Author hydration failed: $e', tag: _tag);
      return posts;
    }
  }

  /// Fetch home feed with pagination metadata (cursor support).
  /// Synchronized with /v1/feed/home from OpenAPI spec.
  Future<FeedPage> getHomeFeedPage({
    int limit = 20,
    String feedMode = 'ranked',
    String platform = 'postbook',
    bool excludeSelf = false,
    bool circleOnly = false,
    bool followingOnly = false,
    String? cursor,
  }) async {
    final params = <String, dynamic>{
      'limit': limit,
      'feed_mode': feedMode,
      'platform': platform,
      if (excludeSelf) 'exclude_self': true,
      if (circleOnly) 'circle_only': true,
      if (followingOnly) 'following_only': true,
    };
    if (cursor != null) params['cursor'] = cursor;

    try {
      AppLogger.info('Requesting /v1/feed/home with params=$params', tag: _tag);
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
      final rawPosts = rawData
          .map((e) => Post.fromJson(e as Map<String, dynamic>))
          .toList();

      // Defensive filter: feed-service falls back to returning bare
      // FeedItem rows (just post_id + author_id + created_at) when the
      // hydration call to post-service fails. Those parse here as Posts
      // with empty content + empty media + null poll. Rather than show
      // "Shared a post" placeholders on every row, drop them — the user
      // sees a smaller (but real) feed and the bug surfaces in monitoring
      // (post-service reachability) instead of as silent garbage.
      final hydratable = rawPosts.where((p) {
        final hasBody = p.content.trim().isNotEmpty;
        final hasMedia = p.mediaIds.isNotEmpty;
        final hasPoll = p.poll != null;
        return hasBody || hasMedia || hasPoll;
      }).toList();

      // Hydrate author display name + avatar from /v1/profiles/batch.
      // /v1/feed/home only returns author_id; without this every post shows
      // "Anonymous" with the placeholder avatar.
      final posts = await _hydrateAuthors(hydratable);

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

      AppLogger.info(
        'Loaded ${posts.length} home feed items, nextCursor=$nextCursor',
        tag: _tag,
      );
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
  }) => getHomeFeedPage(
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
      items: rawData
          .map((e) => Post.fromJson(e as Map<String, dynamic>))
          .toList(),
      nextCursor: response.data['meta']?['next_cursor'],
    );
  }

  Future<FeedPage> getVideoFeedPage({
    int limit = 20,
    String? cursor,
    bool followingOnly = false,
  }) async {
    final params = <String, dynamic>{'limit': limit};
    if (cursor != null) params['cursor'] = cursor;
    if (followingOnly) params['following_only'] = true;

    final response = await _api.get('/v1/feed/watch', queryParameters: params);
    final rawData = response.data['data'] as List<dynamic>? ?? [];

    return FeedPage(
      items: rawData
          .map((e) => Post.fromJson(e as Map<String, dynamic>))
          .toList(),
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
  return FeedRepository(
    ref.watch(apiClientProvider),
    null,
    ref.watch(userRepositoryProvider),
  );
});
