// Live streaming v2 repository — talks to live-service-v2 over the
// gateway under /v1/livestream/*. The legacy live_repository.dart at
// /v1/live/* stays in place for v1 OBS/RTMP flows; the gateway has
// separate proxy entries for each.

import 'package:atpost_app/core/config/environment.dart';
import 'package:atpost_app/data/models/live_stream_v2.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:dio/dio.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class LiveStreamsRepository {
  final ApiClient _api;

  LiveStreamsRepository(this._api);

  // Backend envelope: { data, error, meta } — tolerate either form.
  Map<String, dynamic> _unwrap(dynamic body) {
    if (body is Map<String, dynamic>) {
      final d = body['data'];
      if (d is Map<String, dynamic>) return d;
      return body;
    }
    return const <String, dynamic>{};
  }

  Future<LiveStreamPage> listLive({int limit = 20, String? cursor}) async {
    final response = await _api.get(
      '${Environment.liveV2Path}/streams',
      queryParameters: <String, dynamic>{
        'limit': limit,
        if (cursor != null && cursor.isNotEmpty) 'cursor': cursor,
      },
    );
    final body = response.data;
    final raw = body is Map<String, dynamic> ? body : <String, dynamic>{};
    // The handler serialises the array directly into `data` and the
    // cursor into `meta.next_cursor` (api.JSON + Meta wrapper).
    final dataField = raw['data'];
    List<dynamic> rawItems;
    if (dataField is List) {
      rawItems = dataField;
    } else if (dataField is Map &&
        dataField['streams'] is List) {
      rawItems = dataField['streams'] as List<dynamic>;
    } else {
      rawItems = const <dynamic>[];
    }
    final items = rawItems
        .whereType<Map<String, dynamic>>()
        .map(LiveStreamV2.fromJson)
        .toList(growable: false);
    final meta = raw['meta'];
    final nextCursor = (meta is Map<String, dynamic>)
        ? (meta['next_cursor'] as String? ?? '')
        : '';
    return LiveStreamPage(items: items, nextCursor: nextCursor);
  }

  Future<LiveStreamV2> getStream(String streamId) async {
    final response = await _api.get(
      '${Environment.liveV2Path}/streams/$streamId',
    );
    final data = _unwrap(response.data);
    return LiveStreamV2.fromJson(data);
  }

  Future<LiveStreamV2> createStream({
    required String title,
    String description = '',
    String visibility = 'public',
    String? coverMediaId,
    DateTime? scheduledAt,
  }) async {
    final response = await _api.post(
      '${Environment.liveV2Path}/streams',
      data: <String, dynamic>{
        'title': title,
        'description': description,
        'visibility': visibility,
        if (coverMediaId != null) 'cover_media_id': coverMediaId,
        if (scheduledAt != null)
          'scheduled_at': scheduledAt.toUtc().toIso8601String(),
        // The `use_null_aware_elements` lint is an info hint that
        // appears on the keys above — both alternatives (?'key': value)
        // require Dart 3.4+ map-literal null aware keys which aren't yet
        // settled across our toolchain, so we keep the explicit ifs.
      },
    );
    final data = _unwrap(response.data);
    return LiveStreamV2.fromJson(data);
  }

  Future<StartLiveStreamResult> startStream(String streamId) async {
    final response = await _api.post(
      '${Environment.liveV2Path}/streams/$streamId/start',
    );
    final data = _unwrap(response.data);
    return StartLiveStreamResult.fromJson(data);
  }

  Future<void> endStream(String streamId) async {
    await _api.post('${Environment.liveV2Path}/streams/$streamId/end');
  }

  Future<ViewerTokenResult> getViewerToken(String streamId) async {
    final response = await _api.get(
      '${Environment.liveV2Path}/streams/$streamId/viewer-token',
    );
    final data = _unwrap(response.data);
    return ViewerTokenResult.fromJson(data);
  }
}

/// Generic visibility-error helper used by the broadcaster/viewer flows
/// to render a friendly fallback for 401/402/403.
class LiveAccessException implements Exception {
  final int? statusCode;
  final String code;
  final String message;
  const LiveAccessException(this.statusCode, this.code, this.message);

  static LiveAccessException? maybeFrom(Object error) {
    if (error is! DioException) return null;
    final status = error.response?.statusCode;
    if (status == null) return null;
    final body = error.response?.data;
    final errorBlock = (body is Map<String, dynamic>) ? body['error'] : null;
    final code = (errorBlock is Map<String, dynamic>)
        ? (errorBlock['code'] as String? ?? '')
        : '';
    switch (status) {
      case 401:
        return LiveAccessException(
          status,
          code.isEmpty ? 'UNAUTHORIZED' : code,
          'Sign in to watch this stream.',
        );
      case 402:
        return LiveAccessException(
          status,
          code.isEmpty ? 'PAID_REQUIRED' : code,
          'Subscribe to watch this paid stream.',
        );
      case 403:
        return LiveAccessException(
          status,
          code.isEmpty ? 'NOT_FOLLOWER' : code,
          "Only the creator's followers can watch this stream.",
        );
      case 404:
        return LiveAccessException(
          status,
          'NOT_FOUND',
          'This stream no longer exists.',
        );
      default:
        return null;
    }
  }

  @override
  String toString() => 'LiveAccessException($statusCode, $code, $message)';
}

final liveStreamsRepositoryProvider = Provider<LiveStreamsRepository>((ref) {
  return LiveStreamsRepository(ref.watch(apiClientProvider));
});
