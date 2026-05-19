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
  /// Synchronized with DELETE /v1/graph/block
  Future<void> unblockUser(String userId) async {
    await _api.delete('/v1/graph/block', data: {'user_id': userId});
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

  /// Fetch pending connection requests (both sent and received).
  /// Synchronized with GET /v1/graph/connection-requests
  Future<List<Map<String, dynamic>>> getPendingConnectionRequests() async {
    final response = await _api.get('/v1/graph/connection-requests');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => e as Map<String, dynamic>).toList();
  }

  /// Returns the viewer's connection status with another user:
  /// 'none', 'pending_sent', 'pending_received', or 'accepted'.
  /// Synchronized with GET /v1/graph/relationship
  Future<String> getConnectionStatus(String viewerId, String otherId) async {
    final response = await _api.get(
      '/v1/graph/relationship',
      queryParameters: {'user_id': viewerId, 'other_id': otherId},
    );
    final data = response.data['data'] as Map<String, dynamic>?;
    return (data?['connection_status'] as String?) ?? 'none';
  }

  /// Autocomplete search for users.
  /// Synchronized with GET /v1/search/autocomplete
  Future<List<Map<String, dynamic>>> searchAutocomplete(String query, {int limit = 10}) async {
    final response = await _api.get('/v1/search/autocomplete', queryParameters: {'q': query, 'limit': limit});
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.map((e) => e as Map<String, dynamic>).toList();
  }

  /// Send a connection request to another user.
  /// Synchronized with POST /v1/graph/connection-request
  Future<void> sendConnectionRequest(String userId) async {
    await _api.post('/v1/graph/connection-request', data: {'user_id': userId});
  }

  /// Accept a received connection request. [userId] is the sender's ID.
  /// Synchronized with POST /v1/graph/connection-request/accept
  Future<void> acceptConnectionRequest(String userId) async {
    await _api.post(
      '/v1/graph/connection-request/accept',
      data: {'user_id': userId},
    );
  }

  /// Decline a received connection request. [userId] is the sender's ID.
  /// Synchronized with POST /v1/graph/connection-request/decline
  Future<void> declineConnectionRequest(String userId) async {
    await _api.post(
      '/v1/graph/connection-request/decline',
      data: {'user_id': userId},
    );
  }

  /// Cancel/withdraw a connection request the current user sent.
  /// [userId] is the receiver's ID.
  /// Synchronized with POST /v1/graph/connection-request/cancel
  Future<void> cancelConnectionRequest(String userId) async {
    await _api.post(
      '/v1/graph/connection-request/cancel',
      data: {'user_id': userId},
    );
  }

  /// Remove an existing connection.
  /// Synchronized with DELETE /v1/graph/connection
  Future<void> removeConnection(String userId) async {
    await _api.delete('/v1/graph/connection', data: {'user_id': userId});
  }

  /// Request an export of account data.
  /// Synchronized with POST /v1/auth/export
  Future<void> requestDataExport() async {
    await _api.post('/v1/auth/export');
  }

  /// Fetch the current user's close-friends ("Trusted Circle") IDs.
  /// Synchronized with GET /v1/graph/close-friends
  Future<List<String>> getCloseFriendIds() async {
    final response = await _api.get('/v1/graph/close-friends');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.whereType<String>().toList();
  }

  /// Add a user to the current user's close-friends list.
  /// Synchronized with POST /v1/graph/close-friends/{userId}
  Future<void> addCloseFriend(String userId) async {
    await _api.post('/v1/graph/close-friends/$userId');
  }

  /// Remove a user from the current user's close-friends list.
  /// Synchronized with DELETE /v1/graph/close-friends/{userId}
  Future<void> removeCloseFriend(String userId) async {
    await _api.delete('/v1/graph/close-friends/$userId');
  }

  /// Fetch ranked friend suggestions ("people you may know").
  /// Synchronized with GET /v1/suggestions?type=friend
  Future<List<Map<String, dynamic>>> getFriendSuggestions({
    int limit = 20,
  }) async {
    final response = await _api.get(
      '/v1/suggestions',
      queryParameters: {'type': 'friend', 'limit': limit},
    );
    final data = response.data['data'];
    final List raw = data is List
        ? data
        : (data is Map && data['items'] is List)
            ? data['items'] as List
            : const [];
    return raw
        .whereType<Map>()
        .map((e) => Map<String, dynamic>.from(e))
        .toList();
  }

  /// Hide a friend suggestion so it stops being recommended.
  /// Synchronized with POST /v1/suggestions/action
  Future<void> hideSuggestion(String candidateUserId) async {
    await _api.post(
      '/v1/suggestions/action',
      data: {
        'type': 'friend',
        'surface': 'home',
        'candidate_user_id': candidateUserId,
        'action': 'hide',
      },
    );
  }

  /// Fetch the current user's settings map (privacy, Trusted Circle
  /// toggles, etc.). Synchronized with GET /v1/users/me/settings.
  Future<Map<String, dynamic>> getUserSettings() async {
    final response = await _api.get('/v1/users/me/settings');
    final data = response.data['data'];
    return data is Map ? Map<String, dynamic>.from(data) : {};
  }

  /// Partially update the current user's settings — send only the
  /// changed field(s). Synchronized with PUT /v1/users/me/settings.
  Future<void> updateUserSettings(Map<String, dynamic> fields) async {
    await _api.put('/v1/users/me/settings', data: fields);
  }

  /// Fetch connection requests that trust-safety filtered out of the
  /// normal inbox. Same response shape as getPendingConnectionRequests.
  /// Synchronized with GET /v1/graph/connection-requests/filtered
  Future<List<Map<String, dynamic>>> getFilteredConnectionRequests() async {
    final response = await _api.get('/v1/graph/connection-requests/filtered');
    final items = (response.data['data'] as List<dynamic>?) ?? [];
    return items.whereType<Map>().map((e) => Map<String, dynamic>.from(e)).toList();
  }

  /// Move a filtered connection request back into the normal inbox.
  /// [userId] is the sender's ID.
  /// Synchronized with POST /v1/graph/connection-request/unfilter
  Future<void> unfilterConnectionRequest(String userId) async {
    await _api.post(
      '/v1/graph/connection-request/unfilter',
      data: {'user_id': userId},
    );
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
