import 'package:atpost_app/data/models/group_post.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class GroupPostsRepository {
  final ApiClient _api;
  GroupPostsRepository(this._api);

  Future<List<GroupPost>> listPosts(
    String groupId, {
    String? channelId,
    int page = 1,
  }) async {
    final params = <String, dynamic>{'page': page};
    if (channelId != null) params['channel_id'] = channelId;
    final response = await _api.get(
      '/v1/groups/$groupId/posts',
      queryParameters: params,
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => GroupPost.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<GroupPost> createPost(
    String groupId, {
    required String body,
    String? channelId,
    String contentType = 'post',
    String? title,
  }) async {
    final payload = <String, dynamic>{
      'body': body,
      'content_type': contentType,
    };
    if (channelId != null) payload['channel_id'] = channelId;
    if (title != null) payload['title'] = title;
    final response =
        await _api.post('/v1/groups/$groupId/posts', data: payload);
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final inner = data['data'] as Map<String, dynamic>? ?? data;
      return GroupPost.fromJson(inner);
    }
    return GroupPost.fromJson(data as Map<String, dynamic>);
  }

  Future<void> sparkPost(String groupId, String postId) async {
    await _api.post('/v1/groups/$groupId/posts/$postId/spark');
  }

  Future<void> stashPost(String groupId, String postId) async {
    await _api.post('/v1/groups/$groupId/posts/$postId/stash');
  }

  Future<List<GroupChannel>> listChannels(String groupId) async {
    final response = await _api.get('/v1/groups/$groupId/channels');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => GroupChannel.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<GroupChannel> createChannel(
    String groupId, {
    required String name,
    String type = 'discussion',
    String description = '',
  }) async {
    final response = await _api.post('/v1/groups/$groupId/channels', data: {
      'name': name,
      'type': type,
      'description': description,
    });
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final inner = data['data'] as Map<String, dynamic>? ?? data;
      return GroupChannel.fromJson(inner);
    }
    return GroupChannel.fromJson(data as Map<String, dynamic>);
  }

  Future<List<GroupPost>> listPendingPosts(String groupId) async {
    final response = await _api.get('/v1/groups/$groupId/posts/pending');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => GroupPost.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<void> approvePost(String groupId, String postId) async {
    await _api.post('/v1/groups/$groupId/posts/$postId/approve');
  }

  Future<void> rejectPost(String groupId, String postId) async {
    await _api.post('/v1/groups/$groupId/posts/$postId/reject');
  }

  Future<void> banUser(String groupId, String userId) async {
    await _api.post('/v1/groups/$groupId/bans', data: {'user_id': userId});
  }

  Future<List<Map<String, dynamic>>> listJoinRequests(String groupId) async {
    final response = await _api.get('/v1/groups/$groupId/join-requests');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items.cast<Map<String, dynamic>>();
    }
    return [];
  }

  Future<void> approveJoinRequest(String groupId, String userId) async {
    await _api.post('/v1/groups/$groupId/join-requests/$userId/approve');
  }
}

final groupPostsRepositoryProvider = Provider<GroupPostsRepository>((ref) {
  return GroupPostsRepository(ref.watch(apiClientProvider));
});
