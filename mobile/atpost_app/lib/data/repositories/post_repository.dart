import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Production-ready repository for post-related operations.
/// Synchronized with the 2026-03-19 OpenAPI mobile integration spec.
class PostRepository {
  final ApiClient _api;

  PostRepository(this._api);

  /// Fetch a single post detail.
  Future<Post> getPostDetail(String postId) async {
    final response = await _api.get('/v1/posts/$postId');
    return Post.fromJson(_unwrapObjectEnvelope(response.data));
  }

  /// Create a new post with verified backend fields.
  Future<Post> createPost({
    required String text,
    required String contentType,
    required String visibility,
    List<String>? mediaIds,
    List<String>? tags,
    String? feeling,
    String? activity,
    String? activityDetail,
    String? locationName,
    Map<String, dynamic>? poll,
  }) async {
    final response = await _api.post(
      '/v1/posts',
      data: {
        'text': text,
        'content_type': contentType,
        'visibility': visibility,
        'media_ids': mediaIds,
        'tags': tags,
        'feeling': feeling,
        'activity': activity,
        'activity_detail': activityDetail,
        'location_name': locationName,
        'poll': poll,
      },
    );
    return Post.fromJson(_unwrapObjectEnvelope(response.data));
  }

  /// List comments for a post.
  Future<List<Comment>> getComments(String postId) async {
    final response = await _api.get('/v1/posts/$postId/comments');
    final items = _unwrapListEnvelope(response.data);
    return items
        .whereType<Map>()
        .map((e) => Comment.fromJson(Map<String, dynamic>.from(e)))
        .toList();
  }

  /// Add a comment to a post.
  Future<Comment> addComment(String postId, String text) async {
    final response = await _api.post(
      '/v1/posts/$postId/comments',
      data: {'text': text},
    );
    return Comment.fromJson(_unwrapObjectEnvelope(response.data));
  }

  /// Get AI-assisted caption suggestions (Sparkles).
  /// Synchronized with /v1/ai/caption/suggest from OpenAPI spec.
  Future<List<String>> getAiCaptionSuggestions(
    String text, {
    String? draftId,
  }) async {
    final response = await _api.post(
      '/v1/ai/caption/suggest',
      data: {'draft_id': draftId ?? 'new-draft', 'text': text},
    );
    final data = response.data as Map<String, dynamic>;
    return (data['suggestions'] as List?)?.cast<String>() ?? [];
  }

  /// Generate AI suggestions for a post.
  /// Synchronized with POST /v1/ai/post/suggest
  Future<Map<String, dynamic>> generateAiSuggestions({
    required String text,
    String? context,
  }) async {
    final response = await _api.post(
      '/v1/ai/post/suggest',
      data: {'text': text, 'context': context},
    );
    return response.data['data'] as Map<String, dynamic>;
  }

  /// Bookmark/Reaction toggles and other specific interactions.
  /// (Note: These may use the generic ObjectEnvelope pattern from the spec)
  Future<void> toggleBookmark(String postId) async {
    await _api.post('/v1/posts/$postId/bookmark');
  }

  /// Toggle a reaction (like/emoji) on a post.
  /// Synchronized with POST /v1/posts/{postId}/react
  Future<void> toggleReaction(String postId, {String? emoji}) async {
    await _api.post(
      '/v1/posts/$postId/react',
      data: {'reaction_type': _reactionTypeFor(emoji)},
    );
  }

  Future<void> deletePost(String postId) async {
    await _api.delete('/v1/posts/$postId');
  }

  Future<void> sharePost(String postId) async {
    await _api.post('/v1/posts/$postId/share');
  }

  Future<void> deleteComment(String commentId) async {
    await _api.delete('/v1/comments/$commentId');
  }

  Future<void> toggleCommentLike(String commentId) async {
    await _api.post('/v1/comments/$commentId/like');
  }

  Future<void> submitReport({
    required String targetType,
    required String targetId,
    required String reason,
    String description = '',
  }) async {
    await _api.post(
      '/v1/reports',
      data: {
        'entity_type': targetType,
        'entity_id': targetId,
        'reason': reason,
        'details': description,
      },
    );
  }
}

Map<String, dynamic> _unwrapObjectEnvelope(dynamic body) {
  if (body is Map<String, dynamic>) {
    final data = body['data'];
    if (data is Map) {
      return Map<String, dynamic>.from(data);
    }
    return body;
  }
  return const <String, dynamic>{};
}

List<dynamic> _unwrapListEnvelope(dynamic body) {
  if (body is List) {
    return body;
  }
  if (body is Map<String, dynamic>) {
    final data = body['data'];
    if (data is List) {
      return data;
    }
    if (data is Map<String, dynamic>) {
      final items = data['items'];
      if (items is List) {
        return items;
      }
    }
  }
  return const <dynamic>[];
}

String _reactionTypeFor(String? emoji) {
  const reactionTypes = <String, String>{
    'like': 'like',
    'love': 'love',
    'haha': 'haha',
    'wow': 'wow',
    'sad': 'sad',
    'angry': 'angry',
    '': 'like',
    '\u{1F44D}': 'like',
    '\u2764\uFE0F': 'love',
    '\u{1F525}': 'love',
    '\u{1F602}': 'haha',
    '\u{1F62E}': 'wow',
    '\u{1F622}': 'sad',
    '\u{1F620}': 'angry',
    '\u{1F44E}': 'angry',
    '\u{1F44F}': 'like',
    '\u{1F64C}': 'like',
    '\u{1F4AF}': 'like',
  };

  final normalized = emoji?.trim() ?? '';
  return reactionTypes[normalized] ?? 'like';
}

final postRepositoryProvider = Provider<PostRepository>((ref) {
  return PostRepository(ref.watch(apiClientProvider));
});
