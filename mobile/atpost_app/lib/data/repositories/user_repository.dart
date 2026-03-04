import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class UserRepository {
  final ApiClient _api;

  UserRepository(this._api);

  /// Get the current authenticated user's profile.
  Future<User> getMe() async {
    final response = await _api.get('${Environment.usersPath}/me');
    return User.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Get a user by ID.
  Future<User> getUser(String userId) async {
    final response = await _api.get('${Environment.usersPath}/$userId');
    return User.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Update current user's profile.
  Future<User> updateProfile(Map<String, dynamic> fields) async {
    final response = await _api.put('${Environment.usersPath}/me', data: fields);
    return User.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Follow a user.
  Future<void> followUser(String userId) async {
    await _api.post('${Environment.graphPath}/follow', data: {
      'followee_id': userId,
    });
  }

  /// Unfollow a user.
  Future<void> unfollowUser(String userId) async {
    await _api.post('${Environment.graphPath}/unfollow', data: {
      'followee_id': userId,
    });
  }

  /// Get followers list.
  Future<List<User>> getFollowers(String userId, {int limit = 20, int offset = 0}) async {
    final response = await _api.get(
      '${Environment.graphPath}/followers/$userId',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => User.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Get following list.
  Future<List<User>> getFollowing(String userId, {int limit = 20, int offset = 0}) async {
    final response = await _api.get(
      '${Environment.graphPath}/following/$userId',
      queryParameters: {'limit': limit, 'offset': offset},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => User.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Search users.
  Future<List<User>> searchUsers(String query, {int limit = 20}) async {
    final response = await _api.get(
      '${Environment.searchPath}/users',
      queryParameters: {'q': query, 'limit': limit},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => User.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Batch fetch up to 100 profiles at once.
  Future<List<User>> getUsersBatch(List<String> userIds) async {
    final response = await _api.post(
      '${Environment.profilesPath}/batch',
      data: {'user_ids': userIds},
    );
    final profiles = (response.data['profiles'] as List<dynamic>?) ?? [];
    return profiles.map((e) => User.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Batch fetch relationship status for multiple target users at once.
  /// Returns map of targetUserId -> relationship data map.
  Future<Map<String, dynamic>> getRelationshipsBatch(
    String viewerId,
    List<String> targetIds,
  ) async {
    final response = await _api.post(
      '${Environment.graphPath}/relationships/batch',
      data: {'viewer_id': viewerId, 'target_ids': targetIds},
    );
    return (response.data['relationships'] as Map<String, dynamic>?) ?? {};
  }

  /// Mute a user (hides their posts from feed).
  Future<void> muteUser(String mutedId) async {
    await _api.post(
      '${Environment.graphPath}/mute',
      data: {'muted_id': mutedId},
    );
  }

  /// Unmute a user.
  Future<void> unmuteUser(String mutedId) async {
    await _api.deleteWithData(
      '${Environment.graphPath}/mute',
      data: {'muted_id': mutedId},
    );
  }

  /// Autocomplete search: returns list of maps with user_id, username, display_name.
  Future<List<Map<String, dynamic>>> searchAutocomplete(
    String query, {
    int limit = 8,
  }) async {
    final response = await _api.get(
      '${Environment.searchPath}/autocomplete',
      queryParameters: {'q': query, 'limit': limit},
    );
    final raw = response.data['data'] ?? response.data;
    return (raw as List<dynamic>)
        .map((e) => e as Map<String, dynamic>)
        .toList();
  }
}

final userRepositoryProvider = Provider<UserRepository>((ref) {
  return UserRepository(ref.watch(apiClientProvider));
});
