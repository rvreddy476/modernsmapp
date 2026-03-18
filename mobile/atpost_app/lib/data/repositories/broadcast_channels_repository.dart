import 'package:atpost_app/data/models/broadcast_channel.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class BroadcastChannelsRepository {
  final ApiClient _api;
  BroadcastChannelsRepository(this._api);

  Future<List<BroadcastChannel>> getMyChannels({int page = 1}) async {
    final response = await _api.get(
      '/v1/channels/my',
      queryParameters: {'page': page},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => BroadcastChannel.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<List<BroadcastChannel>> discoverChannels({int page = 1}) async {
    final response = await _api.get(
      '/v1/channels/discover',
      queryParameters: {'page': page},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => BroadcastChannel.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<BroadcastChannel> getChannel(String channelId) async {
    final response = await _api.get('/v1/channels/$channelId');
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final payload = data['data'] as Map<String, dynamic>? ?? data;
      return BroadcastChannel.fromJson(payload);
    }
    return BroadcastChannel.fromJson(data as Map<String, dynamic>);
  }

  Future<void> subscribe(String channelId) async {
    await _api.post('/v1/channels/$channelId/subscribe');
  }

  Future<void> unsubscribe(String channelId) async {
    await _api.post('/v1/channels/$channelId/unsubscribe');
  }

  Future<List<ChannelUpdate>> getUpdates(
    String channelId, {
    int page = 1,
  }) async {
    final response = await _api.get(
      '/v1/channels/$channelId/updates',
      queryParameters: {'page': page},
    );
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final items = (data['data'] as List<dynamic>?) ?? [];
      return items
          .map((e) => ChannelUpdate.fromJson(e as Map<String, dynamic>))
          .toList();
    }
    return [];
  }

  Future<BroadcastChannel> createChannel({
    required String name,
    required String handle,
    required String channelType,
    String description = '',
  }) async {
    final response = await _api.post('/v1/channels', data: {
      'name': name,
      'handle': handle,
      'channel_type': channelType,
      'description': description,
    });
    final data = response.data;
    if (data is Map<String, dynamic>) {
      final payload = data['data'] as Map<String, dynamic>? ?? data;
      return BroadcastChannel.fromJson(payload);
    }
    return BroadcastChannel.fromJson(data as Map<String, dynamic>);
  }
}

final broadcastChannelsRepositoryProvider =
    Provider<BroadcastChannelsRepository>((ref) {
  return BroadcastChannelsRepository(ref.watch(apiClientProvider));
});
