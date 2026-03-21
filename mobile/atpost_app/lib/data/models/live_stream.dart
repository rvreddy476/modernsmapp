class LiveStream {
  final String id;
  final String hostId;
  final String title;
  final String description;
  final String? thumbnailUrl;
  final String? streamKey;
  final String? replayUrl;
  final String status;
  final String visibility;
  final int peakViewers;
  final int totalViewers;
  final int likeCount;
  final DateTime? startedAt;
  final DateTime? endedAt;
  final int durationSeconds;
  final DateTime createdAt;
  final DateTime? updatedAt;

  const LiveStream({
    required this.id,
    required this.hostId,
    required this.title,
    required this.description,
    this.thumbnailUrl,
    this.streamKey,
    this.replayUrl,
    required this.status,
    required this.visibility,
    required this.peakViewers,
    required this.totalViewers,
    required this.likeCount,
    this.startedAt,
    this.endedAt,
    this.durationSeconds = 0,
    required this.createdAt,
    this.updatedAt,
  });

  factory LiveStream.fromJson(Map<String, dynamic> json) {
    return LiveStream(
      id: json['id'] as String? ?? '',
      hostId: json['host_id'] as String? ?? '',
      title: json['title'] as String? ?? '',
      description: json['description'] as String? ?? '',
      thumbnailUrl: json['thumbnail_url'] as String?,
      streamKey: json['stream_key'] as String?,
      replayUrl: json['replay_url'] as String?,
      status: json['status'] as String? ?? 'idle',
      visibility: json['visibility'] as String? ?? 'public',
      peakViewers: json['peak_viewers'] as int? ?? 0,
      totalViewers: json['total_viewers'] as int? ?? 0,
      likeCount: json['like_count'] as int? ?? 0,
      startedAt: _parseDateTime(json['started_at']),
      endedAt: _parseDateTime(json['ended_at']),
      durationSeconds: json['duration_secs'] as int? ?? 0,
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      updatedAt: _parseDateTime(json['updated_at']),
    );
  }

  bool get isLive => status == 'live';
}

class LiveChatMessage {
  final String id;
  final String streamId;
  final String userId;
  final String message;
  final bool isPinned;
  final DateTime createdAt;

  const LiveChatMessage({
    required this.id,
    required this.streamId,
    required this.userId,
    required this.message,
    required this.isPinned,
    required this.createdAt,
  });

  factory LiveChatMessage.fromJson(Map<String, dynamic> json) {
    return LiveChatMessage(
      id: json['id'] as String? ?? '',
      streamId: json['stream_id'] as String? ?? '',
      userId: json['user_id'] as String? ?? '',
      message: json['message'] as String? ?? '',
      isPinned: json['is_pinned'] as bool? ?? false,
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
    );
  }
}

DateTime? _parseDateTime(dynamic value) {
  if (value == null) return null;
  if (value is String) return DateTime.tryParse(value)?.toLocal();
  return null;
}
