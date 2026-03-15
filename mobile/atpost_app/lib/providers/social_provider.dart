import 'package:atpost_app/data/models/user.dart';
import 'package:atpost_app/data/repositories/user_repository.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

final followersProvider =
    FutureProvider.autoDispose.family<List<User>, String>((ref, userId) async {
  return ref.watch(userRepositoryProvider).getFollowers(userId);
});

final followingProvider =
    FutureProvider.autoDispose.family<List<User>, String>((ref, userId) async {
  return ref.watch(userRepositoryProvider).getFollowing(userId);
});

final friendsProvider = FutureProvider.autoDispose<List<User>>((ref) async {
  final api = ref.watch(apiClientProvider);
  final response = await api.get('/v1/graph/friends');
  final items = (response.data['data'] as List<dynamic>?) ?? [];
  return items
      .map((e) => User.fromJson(e as Map<String, dynamic>))
      .toList();
});

/// Provider for pending friend requests (both sent and received).
final friendRequestsProvider =
    FutureProvider.autoDispose<List<FriendRequest>>((ref) async {
  final repo = ref.watch(userRepositoryProvider);
  final items = await repo.getPendingFriendRequests();
  return items.map((e) => FriendRequest.fromJson(e)).toList();
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

  factory FriendRequest.fromJson(Map<String, dynamic> json) {
    return FriendRequest(
      id: json['id'] as String? ?? '',
      senderId: json['sender_id'] as String? ?? '',
      senderName:
          json['sender_name'] as String? ?? json['sender_id'] as String? ?? '',
      receiverId: json['receiver_id'] as String? ?? '',
      receiverName:
          json['receiver_name'] as String? ??
          json['receiver_id'] as String? ??
          '',
      senderAvatarId: json['sender_avatar_id'] as String?,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
      direction: (json['direction'] as String? ?? 'received').toLowerCase(),
      mutualFriendsCount: (json['mutual_friends_count'] as int?) ?? 0,
    );
  }
}

/// Tracks mute state for a specific user, keyed by userId.
final muteStateProvider =
    StateProvider.autoDispose.family<bool, String>((ref, userId) => false);

/// Tracks block state for a specific user, keyed by userId.
final blockStateProvider =
    StateProvider.autoDispose.family<bool, String>((ref, userId) => false);
