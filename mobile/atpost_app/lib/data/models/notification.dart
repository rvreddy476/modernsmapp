class AppNotification {
  final String id;
  final int bucket;
  final String ts;
  final String type;
  final String actorUserId;
  final String entityType;
  final String entityId;
  final String? deepLink;
  final bool isRead;
  final DateTime createdAt;

  const AppNotification({
    required this.id,
    required this.bucket,
    required this.ts,
    required this.type,
    required this.actorUserId,
    required this.entityType,
    required this.entityId,
    this.deepLink,
    this.isRead = false,
    required this.createdAt,
  });

  factory AppNotification.fromJson(Map<String, dynamic> json) {
    return AppNotification(
      id: json['notification_id'] as String? ?? '',
      bucket: json['bucket'] as int? ?? 0,
      ts: json['ts'] as String? ?? '',
      type: json['type'] as String? ?? '',
      actorUserId: json['actor_user_id'] as String? ?? '',
      entityType: json['entity_type'] as String? ?? '',
      entityId: json['entity_id'] as String? ?? '',
      deepLink: json['deep_link'] as String?,
      isRead: json['is_read'] as bool? ?? false,
      createdAt: json['created_at'] != null
          ? DateTime.parse(json['created_at'] as String)
          : DateTime.now(),
    );
  }
}
