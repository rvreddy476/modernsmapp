class LiveStream {
  final String id;
  final String hostId;
  final String title;
  final String description;
  final String? thumbnailUrl;
  final String status;
  final String visibility;
  final int peakViewers;
  final int totalViewers;
  final int likeCount;
  final DateTime? startedAt;
  final DateTime createdAt;

  const LiveStream({
    required this.id,
    required this.hostId,
    required this.title,
    required this.description,
    this.thumbnailUrl,
    required this.status,
    required this.visibility,
    required this.peakViewers,
    required this.totalViewers,
    required this.likeCount,
    this.startedAt,
    required this.createdAt,
  });

  factory LiveStream.fromJson(Map<String, dynamic> json) {
    return LiveStream(
      id: json['id'] as String? ?? '',
      hostId: json['host_id'] as String? ?? '',
      title: json['title'] as String? ?? '',
      description: json['description'] as String? ?? '',
      thumbnailUrl: json['thumbnail_url'] as String?,
      status: json['status'] as String? ?? 'idle',
      visibility: json['visibility'] as String? ?? 'public',
      peakViewers: json['peak_viewers'] as int? ?? 0,
      totalViewers: json['total_viewers'] as int? ?? 0,
      likeCount: json['like_count'] as int? ?? 0,
      startedAt: json['started_at'] != null
          ? DateTime.parse(json['started_at'] as String)
          : null,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}

class LiveChatMessage {
  final String id;
  final String userId;
  final String message;
  final bool isPinned;
  final DateTime createdAt;

  const LiveChatMessage({
    required this.id,
    required this.userId,
    required this.message,
    required this.isPinned,
    required this.createdAt,
  });

  factory LiveChatMessage.fromJson(Map<String, dynamic> json) {
    return LiveChatMessage(
      id: json['id'] as String? ?? '',
      userId: json['user_id'] as String? ?? '',
      message: json['message'] as String? ?? '',
      isPinned: json['is_pinned'] as bool? ?? false,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
