import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/live_stream.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class LiveRepository {
  final ApiClient _api;

  LiveRepository(this._api);

  /// List currently live streams.
  Future<List<LiveStream>> getLiveStreams({int limit = 20}) async {
    final response = await _api.get(
      '${Environment.livePath}/streams',
      queryParameters: {'limit': limit},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => LiveStream.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Get a single stream.
  Future<LiveStream> getStream(String streamId) async {
    final response = await _api.get('${Environment.livePath}/streams/$streamId');
    return LiveStream.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Create a new stream.
  Future<LiveStream> createStream({required String title, String description = '', String visibility = 'public'}) async {
    final response = await _api.post(
      '${Environment.livePath}/streams',
      data: {'title': title, 'description': description, 'visibility': visibility},
    );
    return LiveStream.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  /// Go live.
  Future<void> goLive(String streamId) async {
    await _api.post('${Environment.livePath}/streams/$streamId/go-live');
  }

  /// End stream.
  Future<void> endStream(String streamId) async {
    await _api.post('${Environment.livePath}/streams/$streamId/end');
  }

  /// Join a stream as a viewer.
  Future<int> joinStream(String streamId) async {
    final response = await _api.post('${Environment.livePath}/streams/$streamId/join');
    return (response.data['data']?['viewer_count'] as int?) ?? 0;
  }

  /// Leave a stream.
  Future<void> leaveStream(String streamId) async {
    await _api.post('${Environment.livePath}/streams/$streamId/leave');
  }

  /// Like a stream.
  Future<void> likeStream(String streamId) async {
    await _api.post('${Environment.livePath}/streams/$streamId/like');
  }

  /// Get chat messages.
  Future<List<LiveChatMessage>> getChatMessages(String streamId, {int limit = 50}) async {
    final response = await _api.get(
      '${Environment.livePath}/streams/$streamId/chat',
      queryParameters: {'limit': limit},
    );
    final items = (response.data['data']?['items'] as List<dynamic>?) ?? [];
    return items.map((e) => LiveChatMessage.fromJson(e as Map<String, dynamic>)).toList();
  }

  /// Send a chat message.
  Future<LiveChatMessage> sendChatMessage(String streamId, String message) async {
    final response = await _api.post(
      '${Environment.livePath}/streams/$streamId/chat',
      data: {'message': message},
    );
    return LiveChatMessage.fromJson(response.data['data'] as Map<String, dynamic>);
  }
}

final liveRepositoryProvider = Provider<LiveRepository>((ref) {
  return LiveRepository(ref.watch(apiClientProvider));
});
