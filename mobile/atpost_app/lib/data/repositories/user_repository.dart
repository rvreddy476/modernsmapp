import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Production-ready User Repository synchronized with 2026-03-19 OpenAPI spec.
/// Optimized for "Billions of Users" with batching and resilient search.
class UserRepository {
  final ApiClient _api;

  UserRepository(this._api);

  /// Fetch current user profile from the identity-platform profile-service.
  /// This is the table the web app writes avatar / cover uploads to via
  /// PUT /v1/profiles/me/avatar and /v1/profiles/me/cover; reading from the
  /// app-DB users table (/v1/users/me) returns stale, empty media IDs.
  Future<User> getMe() async {
    final response = await _api.get('/v1/profiles/me');
    final data = response.data['data'] as Map<String, dynamic>;
    return User.fromJson(data);
  }

  /// Fetch a user's profile by ID from the profile-service for the same
  /// reason as [getMe].
  Future<User> getUser(String userId) async {
    final response = await _api.get('/v1/profiles/$userId');
    final data = response.data['data'] as Map<String, dynamic>;
    return User.fromJson(data);
  }

  /// Batch profile lookup for rendering large lists efficiently.
  /// Synchronized with POST /v1/profiles/batch
  Future<List<User>> getUsersBatch(List<String> userIds) async {
    if (userIds.isEmpty) return [];
    final response = await _api.post('/v1/profiles/batch', data: {'user_ids': userIds});
    final body = response.data;

    if (body is Map<String, dynamic>) {
      final profiles = body['profiles'];
      if (profiles is List) {
        return profiles
            .whereType<Map>()
            .map((e) => User.fromJson(Map<String, dynamic>.from(e)))
            .toList();
      }

      final data = body['data'];
      if (data is Map<String, dynamic>) {
        return data.values
            .whereType<Map>()
            .map((e) => User.fromJson(Map<String, dynamic>.from(e)))
            .toList();
      }

      return body.values
          .whereType<Map>()
          .map((e) => User.fromJson(Map<String, dynamic>.from(e)))
          .toList();
    }

    return [];
  }

  /// Follow a user using the verified UserIdRequest schema.
  /// Synchronized with POST /v1/graph/follow
  Future<void> followUser(String userId) async {
    await _api.post('/v1/graph/follow', data: {'user_id': userId});
  }

  /// Unfollow a user.
  /// Synchronized with POST /v1/graph/unfollow
  Future<void> unfollowUser(String userId) async {
    await _api.post('/v1/graph/unfollow', data: {'user_id': userId});
  }

  /// Mute a user.
  /// Synchronized with POST /v1/graph/mute
  Future<void> muteUser(String userId) async {
    await _api.post('/v1/graph/mute', data: {'user_id': userId});
  }

  /// Unmute a user.
  /// Synchronized with DELETE /v1/graph/mute
  Future<void> unmuteUser(String userId) async {
    await _api.delete('/v1/graph/mute', data: {'user_id': userId});
  }

  /// Block a user.
  /// Synchronized with POST /v1/graph/block
  Future<void> blockUser(String userId) async {
    await _api.post('/v1/graph/block', data: {'user_id': userId});
  }

  /// Unblock a user.
  /// Synchronized with DELETE /v1/graph/unblock/{userId}
  Future<void> unblockUser(String userId) async {
    await _api.delete('/v1/graph/unblock/$userId');
  }

  /// Search users with pagination support.
  /// Synchronized with GET /v1/search/users
  Future<UserSearchResult> searchUsers(String query, {int limit = 20, String? cursor}) async {
    final params = <String, dynamic>{'q': query, 'limit': limit};
    if (cursor != null) params['cursor'] = cursor;

    final response = await _api.get('/v1/search/users', queryParameters: params);
    final data = response.data['data'] as Map<String, dynamic>;

    final items = (data['items'] as List?)?.map((e) => User.fromJson(e as Map<String, dynamic>)).toList() ?? [];
    final nextCursor = response.data['meta']?['next_cursor'] as String?;

    return UserSearchResult(users: items, nextCursor: nextCursor);
  }

  /// Update profile fields.
  /// Synchronized with PUT /v1/users/me
  Future<User> updateProfile(Map<String, dynamic> fields) async {
    final response = await _api.put('/v1/users/me', data: fields);
    final data = response.data['data'] as Map<String, dynamic>;
    return User.fromJson(data);
  }

  /// Fetch follower IDs for a user.
  /// Graph-service returns a flat list of UUID strings.
  Future<List<String>> getFollowerIds(String userId) async {
    final response = await _api.get('/v1/graph/followers/$userId');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.whereType<String>().toList();
  }

  /// Fetch IDs the given user follows.
  /// Graph-service returns a flat list of UUID strings.
  Future<List<String>> getFollowingIds(String userId) async {
    final response = await _api.get('/v1/graph/following/$userId');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.whereType<String>().toList();
  }

  /// Fetch followers as fully-hydrated User objects (display name + avatar).
  Future<List<User>> getFollowers(String userId) async {
    final ids = await getFollowerIds(userId);
    if (ids.isEmpty) return const [];
    return getUsersBatch(ids);
  }

  /// Fetch followed users as fully-hydrated User objects.
  Future<List<User>> getFollowing(String userId) async {
    final ids = await getFollowingIds(userId);
    if (ids.isEmpty) return const [];
    return getUsersBatch(ids);
  }

  /// Fetch pending friend requests.
  /// Synchronized with GET /v1/graph/friends/pending
  Future<List<Map<String, dynamic>>> getPendingFriendRequests() async {
    final response = await _api.get('/v1/graph/friends/pending');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => e as Map<String, dynamic>).toList();
  }

  /// Autocomplete search for users.
  /// Synchronized with GET /v1/search/autocomplete
  Future<List<Map<String, dynamic>>> searchAutocomplete(String query, {int limit = 10}) async {
    final response = await _api.get('/v1/search/autocomplete', queryParameters: {'q': query, 'limit': limit});
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => e as Map<String, dynamic>).toList();
  }

  /// Accept a friend request.
  /// Synchronized with POST /v1/graph/friends/accept
  Future<void> acceptFriendRequest(String userId) async {
    await _api.post('/v1/graph/friends/accept', data: {'user_id': userId});
  }

  /// Reject or cancel a friend request.
  /// Synchronized with POST /v1/graph/friends/reject
  Future<void> rejectFriendRequest(String userId) async {
    await _api.post('/v1/graph/friends/reject', data: {'user_id': userId});
  }

  /// Request an export of account data.
  /// Synchronized with POST /v1/auth/export
  Future<void> requestDataExport() async {
    await _api.post('/v1/auth/export');
  }
}

class UserSearchResult {
  final List<User> users;
  final String? nextCursor;
  UserSearchResult({required this.users, this.nextCursor});
}

final userRepositoryProvider = Provider<UserRepository>((ref) {
  return UserRepository(ref.watch(apiClientProvider));
});
