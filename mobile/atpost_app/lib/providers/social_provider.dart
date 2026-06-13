import 'dart:async';

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/realtime_event.dart';
import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/chat_repository.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:atpost_app/services/auth_service.dart';
import 'package:atpost_app/services/realtime_service.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final followersProvider =
    FutureProvider.autoDispose.family<List<User>, String>((ref, userId) async {
  return ref.watch(userRepositoryProvider).getFollowers(userId);
});

final followingProvider =
    FutureProvider.autoDispose.family<List<User>, String>((ref, userId) async {
  return ref.watch(userRepositoryProvider).getFollowing(userId);
});

/// The current user's connections (formerly "friends").
/// Synchronized with GET /v1/graph/connections/:userId
final friendsProvider = FutureProvider.autoDispose<List<User>>((ref) async {
  final auth = ref.watch(authServiceProvider);
  await auth.sessionReady;
  final userId = auth.userId;
  if (userId == null) return const [];

  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/graph/connections/$userId');
  final items = (response.data['data'] as List<dynamic>?) ?? [];
  // The endpoint returns a flat list of user-ID strings; hydrate to User
  // objects for display. Falls back to raw maps if the API returns objects.
  final ids = items.whereType<String>().toList();
  if (ids.isNotEmpty) {
    return ref.watch(userRepositoryProvider).getUsersBatch(ids);
  }
  return items
      .whereType<Map>()
      .map((e) => User.fromJson(Map<String, dynamic>.from(e)))
      .toList();
});

/// Provider for pending connection requests (both sent and received).
final friendRequestsProvider =
    FutureProvider.autoDispose<List<FriendRequest>>((ref) async {
  final repo = ref.watch(userRepositoryProvider);
  final auth = ref.watch(authServiceProvider);
  await auth.sessionReady;
  final currentUserId = auth.userId;
  final items = await repo.getPendingConnectionRequests();
  final requests = items
      .map((e) => FriendRequest.fromJson(e, currentUserId: currentUserId))
      .toList();

  // graph-service returns only user IDs — hydrate the counterparty's
  // display name so the requests screen shows real people, not UUIDs.
  final ids = <String>{
    for (final r in requests)
      r.direction == 'received' ? r.senderId : r.receiverId,
  }..removeWhere((id) => id.isEmpty);
  if (ids.isEmpty) return requests;

  try {
    final users = await repo.getUsersBatch(ids.toList());
    final nameById = {
      for (final u in users)
        if (u.displayName.trim().isNotEmpty) u.id: u.displayName,
    };
    return [
      for (final r in requests)
        r.direction == 'received'
            ? r.copyWith(senderName: nameById[r.senderId])
            : r.copyWith(receiverName: nameById[r.receiverId]),
    ];
  } catch (_) {
    return requests; // hydration is best-effort
  }
});

/// Connection requests that trust-safety filtered out of the normal
/// inbox. Same shape + name-hydration as [friendRequestsProvider]; every
/// item is treated as a received request.
/// Synchronized with GET /v1/graph/connection-requests/filtered.
final filteredFriendRequestsProvider =
    FutureProvider.autoDispose<List<FriendRequest>>((ref) async {
  final repo = ref.watch(userRepositoryProvider);
  final auth = ref.watch(authServiceProvider);
  await auth.sessionReady;
  final currentUserId = auth.userId;
  final items = await repo.getFilteredConnectionRequests();
  final requests = items
      .map((e) => FriendRequest.fromJson(e, currentUserId: currentUserId))
      .toList();

  // graph-service returns only user IDs — hydrate the sender's display
  // name the same way the normal requests list does.
  final ids = <String>{
    for (final r in requests) r.senderId,
  }..removeWhere((id) => id.isEmpty);
  if (ids.isEmpty) return requests;

  try {
    final users = await repo.getUsersBatch(ids.toList());
    final nameById = {
      for (final u in users)
        if (u.displayName.trim().isNotEmpty) u.id: u.displayName,
    };
    return [
      for (final r in requests) r.copyWith(senderName: nameById[r.senderId]),
    ];
  } catch (_) {
    return requests; // hydration is best-effort
  }
});

/// The current user's settings map (privacy, Trusted Circle toggles,
/// notification prefs, etc.). Synchronized with GET /v1/users/me/settings.
final userSettingsProvider =
    FutureProvider.autoDispose<Map<String, dynamic>>((ref) async {
  final auth = ref.watch(authServiceProvider);
  await auth.sessionReady;
  if (auth.userId == null) return const {};
  return ref.watch(userRepositoryProvider).getUserSettings();
});

/// The viewer's connection status with [otherUserId]:
/// 'none', 'pending_sent', 'pending_received', or 'accepted'.
/// Drives the status-aware Add Friend / Connect button.
final connectionStatusProvider =
    FutureProvider.autoDispose.family<String, String>((ref, otherUserId) async {
  final auth = ref.watch(authServiceProvider);
  await auth.sessionReady;
  final viewerId = auth.userId;
  if (viewerId == null || viewerId == otherUserId) return 'none';
  return ref
      .watch(userRepositoryProvider)
      .getConnectionStatus(viewerId, otherUserId);
});

/// A friend request model used by the providers.
class FriendRequest {
  const FriendRequest({
    required this.id,
    required this.senderId,
    required this.senderName,
    required this.receiverId,
    required this.receiverName,
    required this.createdAt,
    required this.direction,
    this.senderAvatarId,
    this.mutualFriendsCount = 0,
    this.source = '',
  });

  final String id;
  final String senderId;
  final String senderName;
  final String receiverId;
  final String receiverName;
  final String? senderAvatarId;
  final DateTime createdAt;
  final String direction;
  final int mutualFriendsCount;

  /// How this request originated, when the graph-service reports it
  /// (e.g. 'hashtag:travel', 'qr', 'search', 'mutual'). Drives the
  /// origin chip on the requests surface; empty when unknown.
  final String source;

  /// Parses a connection-request object from GET /v1/graph/connection-requests.
  /// The graph-service returns `sender_id`/`receiver_id` but no `direction`,
  /// so direction is inferred from [currentUserId] when the API omits it.
  factory FriendRequest.fromJson(
    Map<String, dynamic> json, {
    String? currentUserId,
  }) {
    final senderId = json['sender_id'] as String? ?? '';
    final receiverId = json['receiver_id'] as String? ?? '';

    String direction = (json['direction'] as String? ?? '').toLowerCase();
    if (direction.isEmpty) {
      // Inferred: a request the current user sent is "sent", otherwise it
      // is an incoming request shown under "received".
      direction = (currentUserId != null && senderId == currentUserId)
          ? 'sent'
          : 'received';
    }

    return FriendRequest(
      id: json['id'] as String? ?? '',
      senderId: senderId,
      senderName: json['sender_name'] as String? ?? senderId,
      receiverId: receiverId,
      receiverName: json['receiver_name'] as String? ?? receiverId,
      senderAvatarId: json['sender_avatar_id'] as String?,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
      direction: direction,
      mutualFriendsCount: (json['mutual_friends_count'] as int?) ?? 0,
      source: (json['source'] ?? json['origin'] ?? '').toString(),
    );
  }

  /// Returns a copy with the given fields overridden (null = keep current).
  FriendRequest copyWith({String? senderName, String? receiverName}) {
    return FriendRequest(
      id: id,
      senderId: senderId,
      senderName: senderName ?? this.senderName,
      receiverId: receiverId,
      receiverName: receiverName ?? this.receiverName,
      createdAt: createdAt,
      direction: direction,
      senderAvatarId: senderAvatarId,
      mutualFriendsCount: mutualFriendsCount,
      source: source,
    );
  }
}

/// Tracks mute state for a specific user, keyed by userId.
final muteStateProvider =
    StateProvider.autoDispose.family<bool, String>((ref, userId) => false);

/// Tracks block state for a specific user, keyed by userId.
final blockStateProvider =
    StateProvider.autoDispose.family<bool, String>((ref, userId) => false);

/// A ranked friend suggestion ("people you may know") from suggestion-service.
class FriendSuggestion {
  const FriendSuggestion({
    required this.userId,
    required this.displayName,
    required this.username,
    this.avatarMediaId,
    this.score = 0,
    this.reasonCodes = const [],
    this.explainText = '',
    this.mutualFriendCount = 0,
  });

  final String userId;
  final String displayName;
  final String username;
  final String? avatarMediaId;
  final double score;
  final List<String> reasonCodes;
  final String explainText;
  final int mutualFriendCount;

  /// Resolved avatar URL via the API gateway, or null when none is set.
  String? get avatarUrl => (avatarMediaId != null && avatarMediaId!.isNotEmpty)
      ? '${Environment.apiBaseUrl}/v1/media/$avatarMediaId/serve'
      : null;

  factory FriendSuggestion.fromJson(Map<String, dynamic> json) {
    double toDouble(dynamic v) {
      if (v is num) return v.toDouble();
      if (v is String) return double.tryParse(v) ?? 0;
      return 0;
    }

    return FriendSuggestion(
      userId: (json['user_id'] ?? json['candidate_user_id'] ?? '').toString(),
      displayName:
          (json['display_name'] ?? json['name'] ?? 'User').toString(),
      username: (json['username'] ?? '').toString(),
      avatarMediaId: json['avatar_media_id']?.toString(),
      score: toDouble(json['score']),
      reasonCodes: (json['reason_codes'] as List<dynamic>?)
              ?.map((e) => e.toString())
              .toList() ??
          const [],
      explainText: (json['explain_text'] ?? '').toString(),
      mutualFriendCount: (json['mutual_friend_count'] as num?)?.toInt() ?? 0,
    );
  }
}

/// Ranked friend suggestions for the current user (suggestion-service).
final friendSuggestionsProvider =
    FutureProvider.autoDispose<List<FriendSuggestion>>((ref) async {
  final auth = ref.watch(authServiceProvider);
  await auth.sessionReady;
  if (auth.userId == null) return const [];
  final raw =
      await ref.watch(userRepositoryProvider).getFriendSuggestions(limit: 20);
  return raw.map(FriendSuggestion.fromJson).toList();
});

/// The current user's close friends ("Trusted Circle"), hydrated to User
/// objects. Synchronized with GET /v1/graph/close-friends.
final closeFriendsProvider =
    FutureProvider.autoDispose<List<User>>((ref) async {
  final auth = ref.watch(authServiceProvider);
  await auth.sessionReady;
  if (auth.userId == null) return const [];
  final repo = ref.watch(userRepositoryProvider);
  final ids = await repo.getCloseFriendIds();
  if (ids.isEmpty) return const [];
  return repo.getUsersBatch(ids);
});

/// Online/offline status for the current user's friends, keyed by userId.
/// Resolves presence for up to 100 friends via the chat repository.
/// Live presence for the current user's friends.
///
/// Emits an initial snapshot from the batch endpoint, then re-emits the
/// instant the WS gateway pushes a `presence_update` for one of them — so a
/// friend's dot flips green in well under a second, no refresh. A 25s poll
/// reconciles anything a push missed (e.g. a briefly-dropped WebSocket).
///
/// Stays an `AsyncValue<Map<String, bool>>` (StreamProvider) so every
/// existing consumer keeps working unchanged.
final friendsPresenceProvider =
    StreamProvider.autoDispose<Map<String, bool>>((ref) async* {
  final friends = await ref.watch(friendsProvider.future);
  if (friends.isEmpty) {
    yield const <String, bool>{};
    return;
  }
  final ids = friends.take(100).map((u) => u.id).toList();
  final watched = ids.toSet();
  final chat = ref.watch(chatRepositoryProvider);

  // Initial snapshot.
  var presence = <String, bool>{};
  try {
    presence = Map<String, bool>.from(await chat.getPresence(ids));
  } catch (_) {
    presence = <String, bool>{};
  }
  yield Map<String, bool>.of(presence);

  // Merge live WS pushes + a periodic poll fallback into one stream.
  final out = StreamController<Map<String, bool>>();

  final realtime = ref.watch(realtimeServiceProvider);
  final sub = realtime.events.listen((event) {
    if (event is PresenceUpdateEvent && watched.contains(event.userId)) {
      presence = {...presence, event.userId: event.isOnline};
      out.add(Map<String, bool>.of(presence));
    }
  });

  final poll = Timer.periodic(const Duration(seconds: 25), (_) async {
    try {
      final fresh = await chat.getPresence(ids);
      presence = {...presence, ...Map<String, bool>.from(fresh)};
      out.add(Map<String, bool>.of(presence));
    } catch (_) {
      // best-effort — the next poll or push recovers it
    }
  });

  ref.onDispose(() {
    sub.cancel();
    poll.cancel();
    out.close();
  });

  yield* out.stream;
});
