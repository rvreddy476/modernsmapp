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
}

final userRepositoryProvider = Provider<UserRepository>((ref) {
  return UserRepository(ref.watch(apiClientProvider));
});
