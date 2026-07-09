import 'package:atpost_app/data/models/group_post.dart';
import 'package:atpost_app/data/repositories/group_posts_repository.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Params for fetching posts in a specific group + optional channel filter.
class GroupPostsParams {
  final String groupId;
  final String? channelId;

  const GroupPostsParams({required this.groupId, this.channelId});

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is GroupPostsParams &&
          groupId == other.groupId &&
          channelId == other.channelId;

  @override
  int get hashCode => Object.hash(groupId, channelId);
}

final groupPostsProvider = FutureProvider.autoDispose
    .family<List<GroupPost>, GroupPostsParams>((ref, params) async {
  return ref
      .watch(groupPostsRepositoryProvider)
      .listPosts(params.groupId, channelId: params.channelId);
});

final groupChannelsProvider = FutureProvider.autoDispose
    .family<List<GroupChannel>, String>((ref, groupId) async {
  return ref.watch(groupPostsRepositoryProvider).listChannels(groupId);
});

final pendingPostsProvider = FutureProvider.autoDispose
    .family<List<GroupPost>, String>((ref, groupId) async {
  return ref.watch(groupPostsRepositoryProvider).listPendingPosts(groupId);
});

final joinRequestsProvider = FutureProvider.autoDispose
    .family<List<Map<String, dynamic>>, String>((ref, groupId) async {
  return ref.watch(groupPostsRepositoryProvider).listJoinRequests(groupId);
});
