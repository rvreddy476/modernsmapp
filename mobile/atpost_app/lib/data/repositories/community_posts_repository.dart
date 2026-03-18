import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/models/community_post.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class CommunityPostsRepository {
  final ApiClient _api;
  CommunityPostsRepository(this._api);

  Future<List<CommunityPost>> listSpacePosts(
    String communityId,
    String spaceId, {
    int page = 1,
  }) async {
    final response = await _api.get(
      '/v1/communities/$communityId/spaces/$spaceId/posts',
      queryParameters: {'page': page},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => CommunityPost.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<CommunityPost> createPost(
    String communityId,
    String spaceId, {
    required String body,
    String? title,
    String contentType = 'discussion',
    String? parentPostId,
  }) async {
    final payload = <String, dynamic>{
      'body': body,
      'content_type': contentType,
    };
    if (title != null) payload['title'] = title;
    if (parentPostId != null) payload['parent_post_id'] = parentPostId;
    final response = await _api.post(
      '/v1/communities/$communityId/spaces/$spaceId/posts',
      data: payload,
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final inner = data['data'] as Map<String, dynamic>? ?? data;
      return CommunityPost.fromJson(inner);
    }
    return CommunityPost.fromJson(data as Map<String, dynamic>);
  }

  Future<void> sparkPost(
    String communityId,
    String spaceId,
    String postId,
  ) async {
    await _api.post(
      '/v1/communities/$communityId/spaces/$spaceId/posts/$postId/spark',
    );
  }

  Future<List<CommunitySpace>> listSpaces(String communityId) async {
    final response = await _api.get('/v1/communities/$communityId/spaces');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => CommunitySpace.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<CommunitySpace> createSpace(
    String communityId, {
    required String name,
    required String spaceType,
    String description = '',
  }) async {
    final response = await _api.post(
      '/v1/communities/$communityId/spaces',
      data: {
        'name': name,
        'space_type': spaceType,
        'description': description,
      },
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final inner = data['data'] as Map<String, dynamic>? ?? data;
      return CommunitySpace.fromJson(inner);
    }
    return CommunitySpace.fromJson(data as Map<String, dynamic>);
  }

  Future<List<WikiPage>> listWikiPages(String communityId) async {
    final response = await _api.get('/v1/communities/$communityId/wiki');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => WikiPage.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<WikiPage> createWikiPage(
    String communityId, {
    required String title,
    required String content,
    String? category,
  }) async {
    final payload = <String, dynamic>{
      'title': title,
      'content': content,
    };
    if (category != null) payload['category'] = category;
    final response = await _api.post(
      '/v1/communities/$communityId/wiki',
      data: payload,
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final inner = data['data'] as Map<String, dynamic>? ?? data;
      return WikiPage.fromJson(inner);
    }
    return WikiPage.fromJson(data as Map<String, dynamic>);
  }
}

final communityPostsRepositoryProvider =
    Provider<CommunityPostsRepository>((ref) {
  return CommunityPostsRepository(ref.watch(apiClientProvider));
});
