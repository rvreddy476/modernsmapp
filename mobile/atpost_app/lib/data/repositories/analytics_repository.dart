import 'dart:math';

import 'package:atpost_app/core/utils/app_logger.dart';
import 'package:atpost_app/services/api_client.dart';
import 'package:flutter_riverpod/flutter_riverpod.dart';

class AnalyticsRepository {
  final ApiClient _client;
  AnalyticsRepository(this._client);

  static const _tag = 'AnalyticsRepository';

  /// One analytics session per app process. The backend dedups a display
  /// view per (session_id, content_id), so a stable id means a looping
  /// reel feed that recycles the same reels never inflates view counts.
  static final String _sessionId = _uuidV4();

  // Fetch creator analytics for the current user.
  // [period] is one of '7d', '30d', '90d'
  Future<Map<String, dynamic>> getCreatorStats({String period = '7d'}) async {
    final response = await _client.get(
      '/v1/analytics/creator/me',
      queryParameters: {'period': period},
    );
    return Map<String, dynamic>.from(response.data);
  }

  /// Records a finished video view as a `play_end` analytics event.
  ///
  /// `viewer_id` is left blank on purpose — the server attributes the view
  /// to the authenticated X-User-Id the gateway stamps on the event.
  /// Analytics are best-effort: failures are swallowed.
  Future<void> recordVideoView({
    required String contentId,
    required String creatorId,
    required String contentType, // 'reel' | 'long_video'
    required int watchedMs,
    required int durationMs,
    String surface = 'feed',
  }) async {
    // Mirror the web client's >1s floor — anything shorter is a scroll-by.
    if (contentId.isEmpty || watchedMs <= 1000) return;

    final percentViewed =
        durationMs > 0 ? (watchedMs / durationMs * 100).clamp(0.0, 100.0) : 0.0;
    final completed = durationMs > 0 && watchedMs >= durationMs * 0.95;

    try {
      await _client.post(
        '/v1/analytics/events',
        data: {
          'events': [
            {
              'type': 'play_end',
              'timestamp': DateTime.now().toUtc().toIso8601String(),
              'payload': {
                'content_id': contentId,
                'creator_id': creatorId,
                'viewer_id': '',
                'session_id': _sessionId,
                'content_type': contentType,
                'content_duration_ms': durationMs,
                'watched_ms_total': watchedMs,
                'max_continuous_watch_ms': watchedMs,
                'percent_viewed': percentViewed,
                'loop_count': 0,
                'end_reason': completed ? 'ended' : 'swipe_next',
                'surface': surface,
                'country': '',
                'device_id_hash': '',
                'is_autoplay': true,
              },
            },
          ],
        },
      );
    } catch (e) {
      AppLogger.debug('view event dropped: $e', tag: _tag);
    }
  }
}

/// Generates an RFC 4122 v4 UUID without pulling in a package — the
/// backend stores session_id as a UUID, so a well-formed one is required.
String _uuidV4() {
  final r = Random.secure();
  final b = List<int>.generate(16, (_) => r.nextInt(256));
  b[6] = (b[6] & 0x0f) | 0x40; // version 4
  b[8] = (b[8] & 0x3f) | 0x80; // variant 10xx
  String hex(int n) => n.toRadixString(16).padLeft(2, '0');
  final h = b.map(hex).join();
  return '${h.substring(0, 8)}-${h.substring(8, 12)}-${h.substring(12, 16)}'
      '-${h.substring(16, 20)}-${h.substring(20)}';
}

final analyticsRepositoryProvider = Provider<AnalyticsRepository>((ref) {
  return AnalyticsRepository(ref.read(apiClientProvider));
});
