import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class FeedDeltaState {
  final int newCount;
  final String? newestAnchor;

  const FeedDeltaState({this.newCount = 0, this.newestAnchor});
}

class FeedDeltaParams {
  final String feedType;
  final String? groupId;
  final String? channelId;
  final String? communityId;
  final String? spaceId;
  final String anchor;

  const FeedDeltaParams({
    required this.feedType,
    required this.anchor,
    this.groupId,
    this.channelId,
    this.communityId,
    this.spaceId,
  });

  @override
  bool operator ==(Object other) =>
      identical(this, other) ||
      other is FeedDeltaParams &&
          feedType == other.feedType &&
          anchor == other.anchor &&
          groupId == other.groupId &&
          channelId == other.channelId &&
          communityId == other.communityId &&
          spaceId == other.spaceId;

  @override
  int get hashCode =>
      Object.hash(feedType, anchor, groupId, channelId, communityId, spaceId);
}

final feedDeltaProvider = FutureProvider.autoDispose
    .family<FeedDeltaState, FeedDeltaParams>((ref, params) async {
  final api = ref.watch(apiClientProvider);
  final queryParams = <String, String>{
    'feed_type': params.feedType,
    'anchor': params.anchor,
  };
  if (params.groupId != null) queryParams['group_id'] = params.groupId!;
  if (params.channelId != null) queryParams['channel_id'] = params.channelId!;
  if (params.communityId != null) {
    queryParams['community_id'] = params.communityId!;
  }
  if (params.spaceId != null) queryParams['space_id'] = params.spaceId!;

  try {
    final response =
        await api.get('/v1/feed/delta', queryParameters: queryParams);
    final data = response.data['data'] as Map<String, dynamic>?;
    return FeedDeltaState(
      newCount: data?['new_count'] as int? ?? 0,
      newestAnchor: data?['newest_anchor'] as String?,
    );
  } catch (_) {
    return const FeedDeltaState();
  }
});
