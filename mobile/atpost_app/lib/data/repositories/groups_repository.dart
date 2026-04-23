import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/models/post.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class GroupsRepository {
  final ApiClient _api;
  GroupsRepository(this._api);

  Future<List<Group>> getGroups({
    String? member,
    String? type,
    String? sort,
    int page = 1,
  }) async {
    final params = <String, dynamic>{'page': page};
    if (member != null) params['member'] = member;
    if (type != null) params['type'] = type;
    if (sort != null) params['sort'] = sort;
    final response = await _api.get('/v1/groups', queryParameters: params);
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => Group.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<Group> getGroup(String groupId) async {
    final response = await _api.get('/v1/groups/$groupId');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final payload = data['data'] as Map<String, dynamic>? ?? data;
      return Group.fromJson(payload);
    }
    return Group.fromJson(data as Map<String, dynamic>);
  }

  Future<Group> createGroup({
    required String name,
    required String description,
    required String privacy,
    String? coverMediaId,
  }) async {
    final response = await _api.post('/v1/groups', data: {
      'name': name,
      'description': description,
      'privacy': privacy,
      'cover_media_id': coverMediaId,
    });
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final payload = data['data'] as Map<String, dynamic>? ?? data;
      return Group.fromJson(payload);
    }
    return Group.fromJson(data as Map<String, dynamic>);
  }

  Future<void> joinGroup(String groupId) async {
    await _api.post('/v1/groups/$groupId/join');
  }

  Future<void> leaveGroup(String groupId) async {
    await _api.post('/v1/groups/$groupId/leave');
  }

  Future<void> inviteUser(String groupId, String userId) async {
    await _api.post(
      '/v1/groups/$groupId/invite',
      data: {'user_id': userId},
    );
  }

  Future<List<Post>> getGroupPosts(String groupId, {int page = 1}) async {
    final response = await _api.get(
      '/v1/groups/$groupId/posts',
      queryParameters: {'page': page},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => Post.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<List<User>> getGroupMembers(String groupId, {int page = 1}) async {
    final response = await _api.get(
      '/v1/groups/$groupId/members',
      queryParameters: {'page': page},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => User.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }
}

final groupsRepositoryProvider = Provider<GroupsRepository>((ref) {
  return GroupsRepository(ref.watch(apiClientProvider));
});
