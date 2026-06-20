import 'package:atpost_app/data/models/group.dart';
import 'package:atpost_app/data/models/group_invite.dart';
import 'package:atpost_app/data/models/group_member.dart';
import 'package:atpost_app/data/models/group_post.dart';
import 'package:atpost_app/data/models/group_rule.dart';
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
    bool isMature = false,
    String? coverMediaId,
    String? avatarMediaId,
    String? category,
    String? handle,
  }) async {
    final response = await _api.post(
      '/v1/groups',
      data: {
        'name': name,
        'description': description,
        'privacy': privacy,
        'is_mature': isMature,
        'cover_media_id': ?coverMediaId,
        'avatar_media_id': ?avatarMediaId,
        'category': ?category,
        'handle': ?handle,
      },
    );
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
    await _api.post('/v1/groups/$groupId/invite', data: {'user_id': userId});
  }

  Future<List<GroupMember>> getGroupMembers(
    String groupId, {
    int page = 1,
  }) async {
    final response = await _api.get(
      '/v1/groups/$groupId/members',
      queryParameters: {'page': page},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => GroupMember.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<List<GroupMember>> getBannedMembers(String groupId) async {
    final response = await _api.get('/v1/groups/$groupId/bans');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => GroupMember.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<void> unbanMember(String groupId, String userId) async {
    await _api.delete('/v1/groups/$groupId/bans/$userId');
  }

  Future<void> removeMember(String groupId, String userId) async {
    await _api.delete('/v1/groups/$groupId/members/$userId');
  }

  Future<void> updateMemberRole(
    String groupId,
    String userId,
    String role,
  ) async {
    await _api.patch(
      '/v1/groups/$groupId/members/$userId',
      data: {'role': role},
    );
  }

  Future<List<GroupPost>> getGroupFeed({int page = 1}) async {
    final response = await _api.get(
      '/v1/groups/feed',
      queryParameters: {'page': page},
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

  Future<List<GroupInvite>> getGroupInvites() async {
    final response = await _api.get('/v1/groups/invites/my');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => GroupInvite.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<void> acceptInvite(String inviteId) async {
    await _api.post('/v1/groups/invites/$inviteId/accept');
  }

  Future<void> declineInvite(String inviteId) async {
    await _api.post('/v1/groups/invites/$inviteId/decline');
  }

  Future<List<GroupPost>> getGroupMedia(
    String groupId, {
    String type = 'all',
    int page = 1,
  }) async {
    final response = await _api.get(
      '/v1/groups/$groupId/media',
      queryParameters: {'type': type, 'page': page},
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

  Future<List<GroupRule>> getGroupRules(String groupId) async {
    final response = await _api.get('/v1/groups/$groupId/rules');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => GroupRule.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<GroupRule> createRule(
    String groupId, {
    required String title,
    String description = '',
  }) async {
    final response = await _api.post(
      '/v1/groups/$groupId/rules',
      data: {'title': title, 'description': description},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final inner = data['data'] as Map<String, dynamic>? ?? data;
      return GroupRule.fromJson(inner);
    }
    return GroupRule.fromJson(data as Map<String, dynamic>);
  }

  Future<void> deleteRule(String groupId, String ruleId) async {
    await _api.delete('/v1/groups/$groupId/rules/$ruleId');
  }

  Future<void> deleteGroup(String groupId) async {
    await _api.delete('/v1/groups/$groupId');
  }

  Future<Group> updateGroup(
    String groupId, {
    String? name,
    String? description,
    String? privacy,
    bool? isMature,
    String? coverMediaId,
    String? avatarMediaId,
    String? category,
    String? location,
  }) async {
    final payload = <String, dynamic>{
      'name': ?name,
      'description': ?description,
      'privacy': ?privacy,
      'is_mature': ?isMature,
      'cover_media_id': ?coverMediaId,
      'avatar_media_id': ?avatarMediaId,
      'category': ?category,
      'location': ?location,
    };
    final response =
        await _api.patch('/v1/groups/$groupId', data: payload);
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final inner = data['data'] as Map<String, dynamic>? ?? data;
      return Group.fromJson(inner);
    }
    return Group.fromJson(data as Map<String, dynamic>);
  }
}

final groupsRepositoryProvider = Provider<GroupsRepository>((ref) {
  return GroupsRepository(ref.watch(apiClientProvider));
});
