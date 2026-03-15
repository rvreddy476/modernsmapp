/// Data models for the call-service REST API.

class CallSession {
  final String id;
  final String callType;
  final String sourceType;
  final String? sourceId;
  final String initiatorUserId;
  final String? roomId;
  final String state;
  final bool audioOnly;
  final int maxParticipants;
  final DateTime? startedAt;
  final DateTime? answeredAt;
  final DateTime? endedAt;
  final String? endedReason;
  final DateTime createdAt;
  final List<CallParticipant> participants;

  const CallSession({
    required this.id,
    required this.callType,
    required this.sourceType,
    this.sourceId,
    required this.initiatorUserId,
    this.roomId,
    required this.state,
    required this.audioOnly,
    this.maxParticipants = 10,
    this.startedAt,
    this.answeredAt,
    this.endedAt,
    this.endedReason,
    required this.createdAt,
    this.participants = const [],
  });

  factory CallSession.fromJson(Map<String, dynamic> json) {
    return CallSession(
      id: json['id'] as String,
      callType: json['call_type'] as String? ?? 'audio',
      sourceType: json['source_type'] as String? ?? 'direct',
      sourceId: json['source_id'] as String?,
      initiatorUserId: json['initiator_user_id'] as String,
      roomId: json['room_id'] as String?,
      state: json['state'] as String? ?? 'ringing',
      audioOnly: json['audio_only'] as bool? ?? true,
      maxParticipants: json['max_participants'] as int? ?? 10,
      startedAt: _parseDateTime(json['started_at']),
      answeredAt: _parseDateTime(json['answered_at']),
      endedAt: _parseDateTime(json['ended_at']),
      endedReason: json['ended_reason'] as String?,
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      participants: (json['participants'] as List<dynamic>?)
              ?.map((e) => CallParticipant.fromJson(e as Map<String, dynamic>))
              .toList() ??
          [],
    );
  }
}

class CallParticipant {
  final String id;
  final String userId;
  final String role;
  final String joinState;
  final bool audioMuted;
  final bool videoMuted;
  final bool handRaised;
  final bool isScreenSharing;
  final DateTime? joinedAt;
  final DateTime? leftAt;

  const CallParticipant({
    required this.id,
    required this.userId,
    required this.role,
    required this.joinState,
    this.audioMuted = false,
    this.videoMuted = false,
    this.handRaised = false,
    this.isScreenSharing = false,
    this.joinedAt,
    this.leftAt,
  });

  factory CallParticipant.fromJson(Map<String, dynamic> json) {
    return CallParticipant(
      id: json['id'] as String,
      userId: json['user_id'] as String,
      role: json['role'] as String? ?? 'participant',
      joinState: json['join_state'] as String? ?? 'invited',
      audioMuted: json['audio_muted'] as bool? ?? false,
      videoMuted: json['video_muted'] as bool? ?? false,
      handRaised: json['hand_raised'] as bool? ?? false,
      isScreenSharing: json['is_screen_sharing'] as bool? ?? false,
      joinedAt: _parseDateTime(json['joined_at']),
      leftAt: _parseDateTime(json['left_at']),
    );
  }
}

class ICEServer {
  final List<String> urls;
  final String? username;
  final String? credential;

  const ICEServer({required this.urls, this.username, this.credential});

  factory ICEServer.fromJson(Map<String, dynamic> json) {
    return ICEServer(
      urls: (json['urls'] as List<dynamic>).map((e) => e as String).toList(),
      username: json['username'] as String?,
      credential: json['credential'] as String?,
    );
  }
}

class JoinResponse {
  final CallSession call;
  final String token;
  final List<ICEServer> iceServers;
  final String signalingEndpoint;

  const JoinResponse({
    required this.call,
    required this.token,
    required this.iceServers,
    required this.signalingEndpoint,
  });

  factory JoinResponse.fromJson(Map<String, dynamic> json) {
    return JoinResponse(
      call: CallSession.fromJson(json['call'] as Map<String, dynamic>),
      token: json['token'] as String? ?? '',
      iceServers: (json['ice_servers'] as List<dynamic>?)
              ?.map((e) => ICEServer.fromJson(e as Map<String, dynamic>))
              .toList() ??
          [],
      signalingEndpoint: json['signaling_endpoint'] as String? ?? '',
    );
  }
}

class CallHistoryItem {
  final String id;
  final String callType;
  final String sourceType;
  final String initiatorUserId;
  final String state;
  final bool audioOnly;
  final int durationSeconds;
  final String? endedReason;
  final DateTime createdAt;
  final DateTime? endedAt;
  final List<CallParticipant> participants;

  const CallHistoryItem({
    required this.id,
    required this.callType,
    required this.sourceType,
    required this.initiatorUserId,
    required this.state,
    required this.audioOnly,
    this.durationSeconds = 0,
    this.endedReason,
    required this.createdAt,
    this.endedAt,
    this.participants = const [],
  });

  factory CallHistoryItem.fromJson(Map<String, dynamic> json) {
    return CallHistoryItem(
      id: json['id'] as String,
      callType: json['call_type'] as String? ?? 'audio',
      sourceType: json['source_type'] as String? ?? 'direct',
      initiatorUserId: json['initiator_user_id'] as String,
      state: json['state'] as String? ?? 'ended',
      audioOnly: json['audio_only'] as bool? ?? true,
      durationSeconds: json['duration_seconds'] as int? ?? 0,
      endedReason: json['ended_reason'] as String?,
      createdAt: _parseDateTime(json['created_at']) ?? DateTime.now(),
      endedAt: _parseDateTime(json['ended_at']),
      participants: (json['participants'] as List<dynamic>?)
              ?.map((e) => CallParticipant.fromJson(e as Map<String, dynamic>))
              .toList() ??
          [],
    );
  }
}

DateTime? _parseDateTime(dynamic value) {
  if (value == null) return null;
  if (value is String) return DateTime.tryParse(value);
  return null;
}
