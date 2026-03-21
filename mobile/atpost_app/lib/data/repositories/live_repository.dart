import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/live_stream.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class LiveRepository {
  final ApiClient _api;

  LiveRepository(this._api);

  Future<List<LiveStream>> getLiveStreams({int limit = 20}) async {
    final response = await _api.get(
      '${Environment.livePath}/streams',
      queryParameters: {'limit': limit},
    );
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        const <dynamic>[];
    return items
        .map((item) => LiveStream.fromJson(item as Map<String, dynamic>))
        .toList();
  }

  Future<LiveStream> getStream(String streamId) async {
    final response = await _api.get('${Environment.livePath}/streams/$streamId');
    return LiveStream.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<LiveStream> createStream({
    required String title,
    String description = '',
    String visibility = 'public',
    String? thumbnailUrl,
  }) async {
    final response = await _api.post(
      '${Environment.livePath}/streams',
      data: {
        'title': title,
        'description': description,
        'visibility': visibility,
        if (thumbnailUrl != null && thumbnailUrl.isNotEmpty)
          'thumbnail_url': thumbnailUrl,
      },
    );
    return LiveStream.fromJson(response.data['data'] as Map<String, dynamic>);
  }

  Future<void> goLive(String streamId) async {
    await _api.post('${Environment.livePath}/streams/$streamId/go-live');
  }

  Future<void> endStream(String streamId) async {
    await _api.post('${Environment.livePath}/streams/$streamId/end');
  }

  Future<int> joinStream(String streamId) async {
    final response = await _api.post('${Environment.livePath}/streams/$streamId/join');
    return (response.data['data']?['viewer_count'] as int?) ?? 0;
  }

  Future<void> leaveStream(String streamId) async {
    await _api.post('${Environment.livePath}/streams/$streamId/leave');
  }

  Future<void> likeStream(String streamId) async {
    await _api.post('${Environment.livePath}/streams/$streamId/like');
  }

  Future<int> getViewerCount(String streamId) async {
    final response = await _api.get('${Environment.livePath}/streams/$streamId/viewers');
    return (response.data['data']?['viewer_count'] as int?) ?? 0;
  }

  Future<List<LiveChatMessage>> getChatMessages(
    String streamId, {
    int limit = 50,
    DateTime? before,
  }) async {
    final queryParameters = <String, dynamic>{'limit': limit};
    if (before != null) {
      queryParameters['before'] = before.toUtc().toIso8601String();
    }

    final response = await _api.get(
      '${Environment.livePath}/streams/$streamId/chat',
      queryParameters: queryParameters,
    );
    final items =
        (response.data['data']?['items'] as List<dynamic>?) ??
        const <dynamic>[];
    return items
        .map((item) => LiveChatMessage.fromJson(item as Map<String, dynamic>))
        .toList();
  }

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
