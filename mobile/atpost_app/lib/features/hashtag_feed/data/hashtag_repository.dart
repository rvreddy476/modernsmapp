import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/features/hashtag_feed/models/hashtag_model.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class HashtagPostsPage {
  const HashtagPostsPage({required this.posts, this.nextCursor});

  final List<Post> posts;
  final String? nextCursor;
}

/// Wraps the three new post-service endpoints:
/// - GET /v1/hashtags/trending      → trending chips (24h SQL fallback)
/// - GET /v1/hashtags/search        → prefix-match suggestions
/// - GET /v1/hashtags/:tag/posts    → posts filtered by hashtag (sort=top|recent)
class HashtagRepository {
  HashtagRepository(this._api, this._users);

  final ApiClient _api;
  final UserRepository _users;

  Future<List<HashtagModel>> getTrending({int limit = 15}) async {
    final response = await _api.get(
      '/v1/hashtags/trending',
      queryParameters: {'limit': limit},
    );
    return _parseHashtagList(response.data);
  }

  Future<List<HashtagModel>> search(String query, {int limit = 10}) async {
    final cleaned = query.trim().replaceAll('#', '');
    if (cleaned.length < 2) return const [];
    try {
      final response = await _api.get(
        '/v1/hashtags/search',
        queryParameters: {'q': cleaned, 'limit': limit},
      );
      return _parseHashtagList(response.data);
    } on DioException catch (e) {
      // 429 means rate-limited — return empty so UI can fall back gracefully.
      if (e.response?.statusCode == 429) return const [];
      rethrow;
    }
  }

  Future<HashtagPostsPage> getPostsForHashtag({
    required String tag,
    required String sort, // 'top' or 'recent'
    String? cursor,
    int limit = 20,
  }) async {
    final cleaned = tag.replaceAll('#', '').trim();
    final response = await _api.get(
      '/v1/hashtags/$cleaned/posts',
      queryParameters: {
        'sort': sort,
        'limit': limit,
        if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
      },
    );

    final raw = response.data['data'];
    final List items;
    if (raw is List) {
      items = raw;
    } else if (raw is Map && raw['items'] is List) {
      items = raw['items'] as List;
    } else {
      items = const [];
    }

    final posts =
        items.map((e) => Post.fromJson(e as Map<String, dynamic>)).toList();

    final hydrated = await _hydrateAuthors(posts);

    final meta = response.data['meta'] as Map<String, dynamic>?;
    final nextCursor = meta?['next_cursor'] as String?;

    return HashtagPostsPage(
      posts: hydrated,
      nextCursor: (nextCursor != null && nextCursor.isNotEmpty)
          ? nextCursor
          : null,
    );
  }

  /// Mirrors FeedRepository._hydrateAuthors so cards render names/avatars
  /// without each post-service call having to JOIN profiles.
  Future<List<Post>> _hydrateAuthors(List<Post> posts) async {
    if (posts.isEmpty) return posts;
    final ids = <String>{
      for (final p in posts)
        if ((p.authorName ?? '').trim().isEmpty && p.authorId.isNotEmpty)
          p.authorId,
    };
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
    } catch (_) {
      return posts;
    }
  }

  List<HashtagModel> _parseHashtagList(dynamic body) {
    final raw = body['data'] ?? body;
    if (raw is! Map) return const [];
    final items = raw['hashtags'];
    if (items is! List) return const [];
    return items
        .whereType<Map>()
        .map((e) => HashtagModel.fromJson(Map<String, dynamic>.from(e)))
        .toList();
  }
}

final hashtagRepositoryProvider = Provider<HashtagRepository>((ref) {
  return HashtagRepository(
    ref.watch(apiClientProvider),
    ref.watch(userRepositoryProvider),
  );
});
