import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

/// Module 1 P0-3 — PostTube channel subscriptions.
///
/// Subscribing is deliberately NOT following: this hits
/// `channel_subscriptions` in user-service, while follow/unfollow stays in
/// graph-service. Subscribing to a channel does not follow the person
/// socially, and following does not subscribe.
class ChannelSubscriptionsRepository {
  ChannelSubscriptionsRepository(this._api);
  final ApiClient _api;

  /// Resolves a user's canonical channel id. Returns null when the user
  /// has no channel (the UI then hides the subscribe control instead of
  /// silently falling back to follow).
  Future<String?> channelIdForUser(String userId) async {
    final res = await _api.get('/v1/users/$userId/channels');
    final data = res.data;
    final list = (data is Map && data['data'] is List)
        ? data['data'] as List
        : (data is List ? data : const []);
    if (list.isEmpty) return null;
    final first = list.first;
    if (first is Map && first['id'] != null) return first['id'].toString();
    return null;
  }

  /// Whether the current user subscribes to [channelId], plus their
  /// per-channel notify preference.
  Future<ChannelSubscriptionState> status(String channelId) async {
    final res = await _api.get('/v1/channels/$channelId/subscription');
    final data = res.data;
    final body = (data is Map && data['data'] is Map)
        ? Map<String, dynamic>.from(data['data'] as Map)
        : (data is Map ? Map<String, dynamic>.from(data) : <String, dynamic>{});
    final subscribed = body['subscribed'] == true || body['user_id'] != null;
    return ChannelSubscriptionState(
      subscribed: subscribed,
      notifyOn: (body['notify_on'] as String?) ?? 'all',
    );
  }

  /// Subscribe. [notifyOn] is the subscriber's own notification choice —
  /// 'all' (default), 'uploads', or 'none'. 'none' keeps the subscription
  /// but opts out of upload notifications entirely.
  Future<void> subscribe(String channelId, {String notifyOn = 'all'}) async {
    await _api.post(
      '/v1/channels/$channelId/subscribe',
      data: {'notify_on': notifyOn},
    );
  }

  Future<void> unsubscribe(String channelId) async {
    await _api.delete('/v1/channels/$channelId/subscribe');
  }

  /// Channels the user subscribes to — backs the PostTube Subscriptions
  /// tab (which must read subscriptions, not `following_only` feeds).
  Future<List<String>> subscribedChannelIds(String userId) async {
    final res = await _api.get('/v1/users/$userId/subscriptions');
    final data = res.data;
    final list = (data is Map && data['data'] is List)
        ? data['data'] as List
        : (data is List ? data : const []);
    return list
        .whereType<Map>()
        .map((e) => e['channel_id']?.toString())
        .whereType<String>()
        .toList();
  }
}

class ChannelSubscriptionState {
  const ChannelSubscriptionState({
    required this.subscribed,
    required this.notifyOn,
  });

  final bool subscribed;
  final String notifyOn;

  static const none = ChannelSubscriptionState(
    subscribed: false,
    notifyOn: 'all',
  );
}

final channelSubscriptionsRepositoryProvider =
    Provider<ChannelSubscriptionsRepository>((ref) {
  return ChannelSubscriptionsRepository(ref.watch(apiClientProvider));
});
