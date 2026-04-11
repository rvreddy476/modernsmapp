import 'package:atpost_app/data/models/community.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class CommunitiesRepository {
  final ApiClient _api;
  CommunitiesRepository(this._api);

  Future<List<Community>> getMyCommunities({int page = 1}) async {
    final response = await _api.get(
      '/v1/communities/my',
      queryParameters: {'page': page},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => Community.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<List<Community>> discoverCommunities({int page = 1}) async {
    final response = await _api.get(
      '/v1/communities/discover',
      queryParameters: {'page': page},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => Community.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<Community> getCommunity(String communityId) async {
    final response = await _api.get('/v1/communities/$communityId');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final payload = data['data'] as Map<String, dynamic>? ?? data;
      return Community.fromJson(payload);
    }
    return Community.fromJson(data as Map<String, dynamic>);
  }

  Future<void> join(String communityId) async {
    await _api.post('/v1/communities/$communityId/join');
  }

  Future<void> leave(String communityId) async {
    await _api.post('/v1/communities/$communityId/leave');
  }

  Future<Community> createCommunity({
    required String name,
    required String handle,
    required String communityType,
    String description = '',
    List<String> topicTags = const [],
  }) async {
    final response = await _api.post(
      '/v1/communities',
      data: {
        'name': name,
        'handle': handle,
        'community_type': communityType,
        'description': description,
        'topic_tags': topicTags,
      },
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final payload = data['data'] as Map<String, dynamic>? ?? data;
      return Community.fromJson(payload);
    }
    return Community.fromJson(data as Map<String, dynamic>);
  }

  Future<List<CommunitySpace>> getSpaces(String communityId) async {
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

  Future<List<User>> getMembers(String communityId, {int page = 1}) async {
    final response = await _api.get(
      '/v1/communities/$communityId/members',
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

final communitiesRepositoryProvider = Provider<CommunitiesRepository>((ref) {
  return CommunitiesRepository(ref.watch(apiClientProvider));
});
