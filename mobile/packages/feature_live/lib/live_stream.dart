class LiveStream {
  final String id;
  final String hostId;
  final String title;
  final String description;
  final String? thumbnailUrl;
  final String? streamKey;
  final String? ingestUrl;
  final String? ingestProtocol;
  final String? publishUrl;
  final String? publishProtocol;
  final Map<String, String> publishHeaders;
  final List<LiveIceServer> publishIceServers;
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
    this.publishUrl,
    this.publishProtocol,
    this.publishHeaders = const <String, String>{},
    this.publishIceServers = const <LiveIceServer>[],
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
    final publishConfig = _asMap(json['publish']);
    final rawPublishUrl =
        json['publish_url'] ??
        json['whip_url'] ??
        publishConfig?['url'] ??
        publishConfig?['publish_url'] ??
        publishConfig?['whip_url'];
    final rawPublishProtocol =
        json['publish_protocol'] ??
        json['whip_protocol'] ??
        publishConfig?['protocol'] ??
        publishConfig?['publish_protocol'] ??
        publishConfig?['whip_protocol'];
    final publishHeaders =
        _parseStringMap(
          json['publish_headers'] ??
              json['whip_headers'] ??
              publishConfig?['headers'] ??
              publishConfig?['publish_headers'] ??
              publishConfig?['whip_headers'],
        ) ??
        const <String, String>{};
    final publishIceServers =
        _parseIceServers(
          json['publish_ice_servers'] ??
              json['ice_servers'] ??
              publishConfig?['ice_servers'] ??
              publishConfig?['publish_ice_servers'],
        ) ??
        const <LiveIceServer>[];

    return LiveStream(
      id: json['id'] as String? ?? '',
      hostId: json['host_id'] as String? ?? '',
      title: json['title'] as String? ?? '',
      description: json['description'] as String? ?? '',
      thumbnailUrl: json['thumbnail_url'] as String?,
      streamKey: json['stream_key'] as String?,
      ingestUrl: json['ingest_url'] as String?,
      ingestProtocol: json['ingest_protocol'] as String?,
      publishUrl: rawPublishUrl as String?,
      publishProtocol: rawPublishProtocol as String?,
      publishHeaders: publishHeaders,
      publishIceServers: publishIceServers,
      playbackUrl: json['playback_url'] as String?,
      playbackProtocol: json['playback_protocol'] as String?,
      replayUrl: json['replay_url'] as String?,
      status: json['status'] as String? ?? 'idle',
      visibility: json['visibility'] as String? ?? 'public',
      peakViewers: _parseInt(json['peak_viewers']),
      totalViewers: _parseInt(json['total_viewers']),
      likeCount: _parseInt(json['like_count']),
      startedAt: _parseDateTime(json['started_at']),
      endedAt: _parseDateTime(json['ended_at']),
      durationSeconds: _parseInt(json['duration_secs']),
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      updatedAt: _parseDateTime(json['updated_at']),
    );
  }

  bool get isLive => status == 'live';
  bool get isEnded => status == 'ended';
  bool get hasLivePlayback => (playbackUrl ?? '').isNotEmpty;
  bool get hasReplay => (replayUrl ?? '').isNotEmpty;
  bool get hasPublishTarget => (publishUrl ?? '').isNotEmpty;
  bool get canPublishFromMobile =>
      hasPublishTarget &&
      ((publishProtocol ?? 'whip').toLowerCase() == 'whip');
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
    DateTime? startedAt,
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
      publishUrl: publishUrl,
      publishProtocol: publishProtocol,
      publishHeaders: publishHeaders,
      publishIceServers: publishIceServers,
      playbackUrl: playbackUrl,
      playbackProtocol: playbackProtocol,
      replayUrl: replayUrl,
      status: status ?? this.status,
      visibility: visibility,
      peakViewers: peakViewers ?? this.peakViewers,
      totalViewers: totalViewers ?? this.totalViewers,
      likeCount: likeCount ?? this.likeCount,
      startedAt: startedAt ?? this.startedAt,
      endedAt: endedAt ?? this.endedAt,
      durationSeconds: durationSeconds,
      createdAt: createdAt,
      updatedAt: updatedAt ?? this.updatedAt,
    );
  }
}

class LiveIceServer {
  final List<String> urls;
  final String? username;
  final String? credential;

  const LiveIceServer({
    required this.urls,
    this.username,
    this.credential,
  });

  factory LiveIceServer.fromJson(Map<String, dynamic> json) {
    final rawUrls = json['urls'];
    final urls = switch (rawUrls) {
      String value => <String>[value],
      List<dynamic> value => value.whereType<String>().toList(growable: false),
      _ => const <String>[],
    };
    return LiveIceServer(
      urls: urls,
      username: json['username'] as String?,
      credential: json['credential'] as String?,
    );
  }

  Map<String, dynamic> toRtcConfiguration() {
    return <String, dynamic>{
      'urls': urls,
      if ((username ?? '').isNotEmpty) 'username': username,
      if ((credential ?? '').isNotEmpty) 'credential': credential,
    };
  }
}

class LivePublishSession {
  final String answerSdp;
  final String? sessionUrl;

  const LivePublishSession({
    required this.answerSdp,
    this.sessionUrl,
  });
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

Map<String, dynamic>? _asMap(dynamic value) {
  if (value is Map<String, dynamic>) return value;
  if (value is Map) {
    return value.map(
      (key, item) => MapEntry(key.toString(), item),
    );
  }
  return null;
}

Map<String, String>? _parseStringMap(dynamic value) {
  final map = _asMap(value);
  if (map == null) return null;
  return map.map(
    (key, item) => MapEntry(key, item?.toString() ?? ''),
  );
}

List<LiveIceServer>? _parseIceServers(dynamic value) {
  if (value is! List) return null;
  return value
      .map((item) => _asMap(item))
      .whereType<Map<String, dynamic>>()
      .map(LiveIceServer.fromJson)
      .where((server) => server.urls.isNotEmpty)
      .toList(growable: false);
}

int _parseInt(dynamic value) {
  if (value is int) return value;
  if (value is num) return value.toInt();
  return 0;
}
