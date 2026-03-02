import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class PostRepository {
  final ApiClient _api;

  PostRepository(this._api);

  /// Get a single post by ID.
  Future<Post> getPost(String postId) async {
    final response = await _api.get('${Environment.postsPath}/$postId');
    return Post.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Create a new post.
  Future<Post> createPost({
    required String content,
    String contentType = 'post',
    String visibility = 'public',
    List<String>? mediaIds,
    List<String>? tags,
  }) async {
    final response = await _api.post(Environment.postsPath, data: {
      'content': content,
      'content_type': contentType,
      'visibility': visibility,
      'media_ids': ?mediaIds,
      'tags': ?tags,
    });
    return Post.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Toggle like on a post.
  Future<void> toggleLike(String postId) async {
    await _api.post('${Environment.postsPath}/$postId/like');
  }

  /// React to a post.
  Future<void> react(String postId, String reactType) async {
    await _api.post('${Environment.postsPath}/$postId/react', data: {
      'react_type': reactType,
    });
  }

  /// Get comments for a post.
  Future<List<Comment>> getComments(String postId, {int limit = 20}) async {
    final response = await _api.get(
      '${Environment.postsPath}/$postId/comments',
      queryParameters: {'limit': limit},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items
        .map((e) => Comment.fromJson(e as Map<String, dynamic>))
        .toList();
  }

  /// Add a comment to a post.
  Future<Comment> addComment(String postId, String text) async {
    final response = await _api.post(
      '${Environment.postsPath}/$postId/comments',
      data: {'text': text},
    );
    return Comment.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Bookmark a post.
  Future<void> toggleBookmark(String postId) async {
    await _api.post('${Environment.postsPath}/$postId/bookmark');
  }
}

final postRepositoryProvider = Provider<PostRepository>((ref) {
  return PostRepository(ref.watch(apiClientProvider));
});
