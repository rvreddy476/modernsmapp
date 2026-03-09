import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class PostRepository {
  final ApiClient _api;

  PostRepository(this._api);

  /// Toggle a reaction on a post.
  /// Matches the web app's toggleReaction logic.
  Future<void> toggleReaction(String postId, {String emoji = '❤️'}) async {
    await _api.put(
      '${Environment.postsPath}/$postId/reactions',
      data: {'emoji': emoji},
    );
  }

  /// Add a comment to a post.
  Future<void> addComment(String postId, String text) async {
    await _api.post(
      '${Environment.postsPath}/$postId/comments',
      data: {'text': text},
    );
  }

  /// Toggle bookmark status.
  Future<void> toggleBookmark(String postId) async {
    await _api.post('${Environment.postsPath}/$postId/bookmark');
  }

  /// Delete a post.
  Future<void> deletePost(String postId) async {
    await _api.delete('${Environment.postsPath}/$postId');
  }

  /// Create a new post with full metadata.
  Future<void> createPost({
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
    bool noComments = false,
    bool noLikes = false,
    String? postType,
  }) async {
    await _api.post(
      Environment.postsPath,
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
        'no_comments': noComments,
        'no_likes': noLikes,
        'post_type': postType,
      },
    );
  }

  /// Get AI-assisted content suggestions or enhancements.
  /// Mirrors the 'Sparkles' functionality on the web.
  Future<Map<String, dynamic>> generateAiSuggestions({
    required String text,
    String? context,
  }) async {
    final response = await _api.post(
      '${Environment.postsPath}/ai-assist',
      data: {
        'content': text,
        'context': context,
      },
    );
    return response.data['data'] as Map<String, dynamic>? ?? response.data;
  }
}

final postRepositoryProvider = Provider<PostRepository>((ref) {
  return PostRepository(ref.watch(apiClientProvider));
});
