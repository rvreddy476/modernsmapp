import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// A user-created collection of video posts. Mirrors post-service
/// `postgres.Playlist` (see video_series_handler.go / playlists.go).
class Playlist {
  final String id;
  final String title;
  final String description;
  final String? coverUrl;
  final String visibility;
  final int itemCount;

  const Playlist({
    required this.id,
    required this.title,
    this.description = '',
    this.coverUrl,
    this.visibility = 'public',
    this.itemCount = 0,
  });

  factory Playlist.fromJson(Map<String, dynamic> json) => Playlist(
        id: (json['id'] ?? '').toString(),
        title: (json['title'] ?? '').toString(),
        description: (json['description'] ?? '').toString(),
        coverUrl: json['cover_url']?.toString(),
        visibility: (json['visibility'] ?? 'public').toString(),
        itemCount: (json['item_count'] as num?)?.toInt() ?? 0,
      );
}

/// Repository for playlists / collections — wraps post-service `/v1/playlists`.
class PlaylistRepository {
  final ApiClient _api;
  PlaylistRepository(this._api);

  /// Playlists owned by [creatorId] (use the current user id for "my playlists").
  Future<List<Playlist>> listByCreator(String creatorId) async {
    final response = await _api.get('/v1/creators/$creatorId/playlists');
    return _unwrapList(response.data)
        .whereType<Map<String, dynamic>>()
        .map(Playlist.fromJson)
        .toList(growable: false);
  }

  /// Create a new playlist and return it.
  Future<Playlist> create(String title, {String visibility = 'private'}) async {
    final response = await _api.post(
      '/v1/playlists',
      data: {'title': title, 'visibility': visibility},
    );
    return Playlist.fromJson(_unwrapObject(response.data));
  }

  /// Add a post to a playlist.
  Future<void> addItem(String playlistId, String postId) async {
    await _api.post(
      '/v1/playlists/$playlistId/items',
      data: {'post_id': postId},
    );
  }

  /// Remove a post from a playlist.
  Future<void> removeItem(String playlistId, String postId) async {
    await _api.delete('/v1/playlists/$playlistId/items/$postId');
  }
}

Map<String, dynamic> _unwrapObject(dynamic body) {
  if (body is Map<String, dynamic>) {
    final data = body['data'];
    if (data is Map) return Map<String, dynamic>.from(data);
    return body;
  }
  return const <String, dynamic>{};
}

List<dynamic> _unwrapList(dynamic body) {
  if (body is List) return body;
  if (body is Map<String, dynamic>) {
    final data = body['data'];
    if (data is List) return data;
    if (data is Map<String, dynamic> && data['items'] is List) {
      return data['items'] as List;
    }
  }
  return const <dynamic>[];
}

final playlistRepositoryProvider = Provider<PlaylistRepository>((ref) {
  return PlaylistRepository(ref.watch(apiClientProvider));
});
