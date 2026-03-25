class LiveStream {
  final String id;
  final String hostId;
  final String title;
  final String description;
  final String? thumbnailUrl;
  final String? streamKey;
  final String? ingestUrl;
  final String? ingestProtocol;
  final String? playbackUrl;
  final String? playbackProtocol;
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
    this.ingestUrl,
    this.ingestProtocol,
    this.playbackUrl,
    this.playbackProtocol,
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
      ingestUrl: json['ingest_url'] as String?,
      ingestProtocol: json['ingest_protocol'] as String?,
      playbackUrl: json['playback_url'] as String?,
      playbackProtocol: json['playback_protocol'] as String?,
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
  bool get isEnded => status == 'ended';
  bool get hasLivePlayback => (playbackUrl ?? '').isNotEmpty;
  bool get hasReplay => (replayUrl ?? '').isNotEmpty;
  String? get preferredVideoUrl => isLive
      ? hasLivePlayback
            ? playbackUrl
            : hasReplay
            ? replayUrl
            : null
      : hasReplay
      ? replayUrl
      : hasLivePlayback
      ? playbackUrl
      : null;

  LiveStream copyWith({
    String? status,
    int? peakViewers,
    int? totalViewers,
    int? likeCount,
    DateTime? endedAt,
    DateTime? updatedAt,
  }) {
    return LiveStream(
      id: id,
      hostId: hostId,
      title: title,
      description: description,
      thumbnailUrl: thumbnailUrl,
      streamKey: streamKey,
      ingestUrl: ingestUrl,
      ingestProtocol: ingestProtocol,
      playbackUrl: playbackUrl,
      playbackProtocol: playbackProtocol,
      replayUrl: replayUrl,
      status: status ?? this.status,
      visibility: visibility,
      peakViewers: peakViewers ?? this.peakViewers,
      totalViewers: totalViewers ?? this.totalViewers,
      likeCount: likeCount ?? this.likeCount,
      startedAt: startedAt,
      endedAt: endedAt ?? this.endedAt,
      durationSeconds: durationSeconds,
      createdAt: createdAt,
      updatedAt: updatedAt ?? this.updatedAt,
    );
  }
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
      id: json['id'] as String? ?? json['message_id'] as String? ?? '',
      streamId: json['stream_id'] as String? ?? '',
      userId: json['user_id'] as String? ?? '',
      message: json['message'] as String? ?? '',
      isPinned: json['is_pinned'] as bool? ?? false,
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
    );
  }

  LiveChatMessage copyWith({bool? isPinned}) {
    return LiveChatMessage(
      id: id,
      streamId: streamId,
      userId: userId,
      message: message,
      isPinned: isPinned ?? this.isPinned,
      createdAt: createdAt,
    );
  }
}

class LiveMute {
  final String streamId;
  final String userId;
  final String mutedBy;
  final DateTime mutedAt;

  const LiveMute({
    required this.streamId,
    required this.userId,
    required this.mutedBy,
    required this.mutedAt,
  });

  factory LiveMute.fromJson(Map<String, dynamic> json) {
    return LiveMute(
      streamId: json['stream_id'] as String? ?? '',
      userId: json['user_id'] as String? ?? '',
      mutedBy: json['muted_by'] as String? ?? '',
      mutedAt: _parseDateTime(json['muted_at']) ?? DateTime.now(),
    );
  }
}

class LiveWordFilter {
  final String streamId;
  final String word;
  final String addedBy;

  const LiveWordFilter({
    required this.streamId,
    required this.word,
    required this.addedBy,
  });

  factory LiveWordFilter.fromJson(Map<String, dynamic> json) {
    return LiveWordFilter(
      streamId: json['stream_id'] as String? ?? '',
      word: json['word'] as String? ?? '',
      addedBy: json['added_by'] as String? ?? '',
    );
  }
}

DateTime? _parseDateTime(dynamic value) {
  if (value == null) return null;
  if (value is String) return DateTime.tryParse(value)?.toLocal();
  return null;
}
