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
    final data = response.data['data'] as Map<String, dynamic>;
    return Post.fromJson(data);
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
    final data = response.data['data'] as Map<String, dynamic>;
    return Post.fromJson(data);
  }

  /// List comments for a post.
  Future<List<Comment>> getComments(String postId) async {
    final response = await _api.get('/v1/posts/$postId/comments');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => Comment.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Add a comment to a post.
  Future<Comment> addComment(String postId, String text) async {
    final response = await _api.post(
      '/v1/posts/$postId/comments',
      data: {'text': text},
    );
    final data = response.data['data'] as Map<String, dynamic>;
    return Comment.fromJson(data);
  }

  /// Get AI-assisted caption suggestions (Sparkles).
  /// Synchronized with /v1/ai/caption/suggest from OpenAPI spec.
  Future<List<String>> getAiCaptionSuggestions(String text, {String? draftId}) async {
    final response = await _api.post(
      '/v1/ai/caption/suggest',
      data: {
        'draft_id': draftId ?? 'new-draft',
        'text': text,
      },
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
      data: {
        'text': text,
        'context': context,
      },
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
      data: emoji != null ? {'emoji': emoji} : null,
    );
  }

  Future<void> deletePost(String postId) async {
    await _api.delete('/v1/posts/$postId');
  }
}

final postRepositoryProvider = Provider<PostRepository>((ref) {
  return PostRepository(ref.watch(apiClientProvider));
});
