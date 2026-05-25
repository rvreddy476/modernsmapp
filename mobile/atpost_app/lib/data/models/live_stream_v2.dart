// Live streaming v2 — mirrors the Go `LiveStream` row served by
// Architecture/services/live-service-v2. The legacy `live_stream.dart`
// model maps the v1 server (Cloudflare WHIP/playback) and is being kept
// in place until the gateway stops proxying /v1/live/* to the v1 service.
//
// Once the cutover is complete the v1 model and its repository can be
// removed.

// `@immutable` lives in package:meta which isn't a direct dependency,
// so we use the foundation export instead.
import 'package:flutter/foundation.dart' show immutable;

enum LiveStreamStatus { scheduled, live, ended, failed, unknown }

enum LiveStreamVisibility { public, followers, paid, unknown }

LiveStreamStatus _parseStatus(String? raw) {
  switch (raw) {
    case 'scheduled':
      return LiveStreamStatus.scheduled;
    case 'live':
      return LiveStreamStatus.live;
    case 'ended':
      return LiveStreamStatus.ended;
    case 'failed':
      return LiveStreamStatus.failed;
    default:
      return LiveStreamStatus.unknown;
  }
}

LiveStreamVisibility _parseVisibility(String? raw) {
  switch (raw) {
    case 'public':
      return LiveStreamVisibility.public;
    case 'followers':
      return LiveStreamVisibility.followers;
    case 'paid':
      return LiveStreamVisibility.paid;
    default:
      return LiveStreamVisibility.unknown;
  }
}

DateTime? _parseDate(dynamic value) {
  if (value == null) return null;
  if (value is String && value.isNotEmpty) {
    return DateTime.tryParse(value)?.toLocal();
  }
  return null;
}

int _parseInt(dynamic value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  if (value is String) return int.tryParse(value) ?? 0;
  return 0;
}

@immutable
class LiveStreamV2 {
  final String id;
  final String creatorUserId;
  final String livekitRoom;
  final String title;
  final String description;
  final String? coverMediaId;
  final LiveStreamStatus status;
  final LiveStreamVisibility visibility;
  final DateTime? scheduledAt;
  final DateTime? startedAt;
  final DateTime? endedAt;
  final int viewerPeak;
  final String? recordingUrl;
  final int? recordingDurationSeconds;
  final DateTime createdAt;
  final DateTime updatedAt;

  const LiveStreamV2({
    required this.id,
    required this.creatorUserId,
    required this.livekitRoom,
    required this.title,
    required this.description,
    required this.coverMediaId,
    required this.status,
    required this.visibility,
    required this.scheduledAt,
    required this.startedAt,
    required this.endedAt,
    required this.viewerPeak,
    required this.recordingUrl,
    required this.recordingDurationSeconds,
    required this.createdAt,
    required this.updatedAt,
  });

  bool get isLive => status == LiveStreamStatus.live;
  bool get isEnded => status == LiveStreamStatus.ended;
  bool get isScheduled => status == LiveStreamStatus.scheduled;
  bool get hasRecording => (recordingUrl ?? '').isNotEmpty;

  factory LiveStreamV2.fromJson(Map<String, dynamic> json) {
    return LiveStreamV2(
      id: json['id'] as String? ?? '',
      creatorUserId: json['creator_user_id'] as String? ?? '',
      livekitRoom: json['livekit_room'] as String? ?? '',
      title: json['title'] as String? ?? '',
      description: json['description'] as String? ?? '',
      coverMediaId: json['cover_media_id'] as String?,
      status: _parseStatus(json['status'] as String?),
      visibility: _parseVisibility(json['visibility'] as String?),
      scheduledAt: _parseDate(json['scheduled_at']),
      startedAt: _parseDate(json['started_at']),
      endedAt: _parseDate(json['ended_at']),
      viewerPeak: _parseInt(json['viewer_peak']),
      recordingUrl: json['recording_url'] as String?,
      recordingDurationSeconds: json['recording_duration_seconds'] == null
          ? null
          : _parseInt(json['recording_duration_seconds']),
      createdAt: _parseDate(json['created_at']) ?? DateTime.now(),
      updatedAt: _parseDate(json['updated_at']) ?? DateTime.now(),
    );
  }
}

/// Result of `POST /v1/live/streams/:id/start`.
@immutable
class StartLiveStreamResult {
  final String publisherToken;
  final String room;
  final String serverUrl;
  final LiveStreamV2? stream;

  const StartLiveStreamResult({
    required this.publisherToken,
    required this.room,
    required this.serverUrl,
    this.stream,
  });

  factory StartLiveStreamResult.fromJson(Map<String, dynamic> json) {
    return StartLiveStreamResult(
      publisherToken: json['publisher_token'] as String? ?? '',
      room: json['room'] as String? ?? '',
      serverUrl: json['server_url'] as String? ?? '',
      stream: json['stream'] is Map<String, dynamic>
          ? LiveStreamV2.fromJson(json['stream'] as Map<String, dynamic>)
          : null,
    );
  }
}

/// Result of `GET /v1/live/streams/:id/viewer-token`.
@immutable
class ViewerTokenResult {
  final String token;
  final String room;
  final String serverUrl;

  const ViewerTokenResult({
    required this.token,
    required this.room,
    required this.serverUrl,
  });

  factory ViewerTokenResult.fromJson(Map<String, dynamic> json) {
    return ViewerTokenResult(
      token: json['token'] as String? ?? '',
      room: json['room'] as String? ?? '',
      serverUrl: json['server_url'] as String? ?? '',
    );
  }
}

/// One page from the `GET /v1/live/streams` list endpoint.
@immutable
class LiveStreamPage {
  final List<LiveStreamV2> items;
  final String nextCursor;

  const LiveStreamPage({required this.items, required this.nextCursor});
}
